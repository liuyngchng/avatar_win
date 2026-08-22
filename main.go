// Package main is the entry point for the Avatar PC application.
// It creates a window (WebView2 on Windows) and loads the 3D digital
// human rendering page.
//
// Configuration is read from cfg.yml in the same directory as the
// executable. See cfg.yml for the available options.
package main

import (
	"embed"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/liuyngchng/avatar-pc/internal/asr"
	"github.com/liuyngchng/avatar-pc/internal/audio"
	"github.com/liuyngchng/avatar-pc/internal/brain"
	"github.com/liuyngchng/avatar-pc/internal/config"
	"github.com/liuyngchng/avatar-pc/internal/llm"
	"github.com/liuyngchng/avatar-pc/internal/logfile"
	"github.com/liuyngchng/avatar-pc/internal/renderer"
	"github.com/liuyngchng/avatar-pc/internal/tts"
)

//go:embed web
var webAssets embed.FS

// Build metadata, injected via -ldflags "-X main.version=...".
var (
	version   = "dev"
	buildTime = "unknown"
)

func main() {
	// Initialize file logging first so we can see what happens even
	// when launched by double-click (no console).
	logF, err := logfile.Init()
	if err != nil {
		// Still try to continue — log to stderr only.
		log.Printf("logfile: init failed: %v", err)
	} else {
		defer logF.Close()
	}

	log.SetFlags(log.Ltime | log.Lshortfile)
	log.Println("=== Avatar PC starting ===")

	// Step 1: Load configuration from cfg.yml (optional).
	log.Println("main: [1/5] loading config...")
	cfg, err := config.Load()
	if err != nil {
		// cfg.yml exists but is malformed — that's a real error.
		log.Fatalf("main: failed to load config: %v", err)
	}
	if cfg == nil {
		// No cfg.yml found — run in "display only" mode. The avatar renders
		// and the window works, but tapping will log a clear error instead
		// of talking.
		log.Println("main: [1/5] cfg.yml NOT found — running display-only (talking disabled)")
	} else {
		log.Println("main: [1/5] config loaded OK")
	}

	// Step 2: Create the renderer window FIRST — the user should see the
	// VRM avatar as soon as possible, even if audio/network init fails.
	log.Println("main: [2/5] creating renderer window...")
	r, err := renderer.New(webAssets)
	if err != nil {
		log.Fatalf("main: failed to create renderer: %v", err)
	}
	defer r.Close()
	log.Println("main: [2/5] renderer window created OK")

	// Step 3: Initialize online clients (Alibaba Cloud Bailian APIs).
	// These are network-dependent — if cfg is nil (no cfg.yml), we skip
	// them entirely and the avatar will only display, not talk.
	var asrClient *asr.Client
	var llmClient *llm.Client
	var ttsClient *tts.Client

	if cfg != nil {
		log.Println("main: [3/5] initializing API clients...")
		asrClient = asr.NewClient(cfg.ASR.URL, cfg.ASR.Model, cfg.APIKey, cfg.ASR.Format, cfg.ASR.SampleRate)
		defer asrClient.Close()

		llmClient = llm.NewClient(cfg.LLM.URL, cfg.LLM.Model, cfg.APIKey, cfg.LLM.Name)
		defer llmClient.Close()

		ttsClient = tts.NewClient(cfg.TTS.URL, cfg.TTS.Model, cfg.TTS.Voice, cfg.APIKey, cfg.TTS.Format, cfg.TTS.SampleRate)
		defer ttsClient.Close()

		log.Printf("main: [3/5] ASR endpoint=%s (model=%s)", cfg.ASR.URL, cfg.ASR.Model)
		log.Printf("main: [3/5] LLM endpoint=%s (model=%s)", cfg.LLM.URL, cfg.LLM.Model)
		log.Printf("main: [3/5] TTS endpoint=%s (model=%s, voice=%s)", cfg.TTS.URL, cfg.TTS.Model, cfg.TTS.Voice)
	} else {
		log.Println("main: [3/5] skipped — no cfg.yml (talking disabled)")
	}

	// Step 4: Initialize audio player (may block briefly on some systems).
	log.Println("main: [4/5] initializing audio player...")
	var player *audio.Player
	if cfg != nil {
		player, err = audio.NewPlayer(ttsClient.SampleRate())
		if err != nil {
			log.Printf("main: [4/5] audio player init failed (will continue): %v", err)
		} else {
			log.Println("main: [4/5] waiting for audio player ready...")
			player.WaitReady()
			log.Println("main: [4/5] audio player ready OK")
			defer player.Close()
		}
	} else {
		log.Println("main: [4/5] skipped — no cfg.yml (talking disabled)")
	}

	// Initialize audio recorder.
	recorder := audio.NewRecorder()
	log.Println("main: [4/5] audio recorder created OK")
	defer recorder.Stop()

	// Step 5: Determine idle animation flag.
	idleAnims := true // default: enabled
	if cfg != nil {
		idleAnims = cfg.Avatar.IdleAnimations()
	}
	log.Printf("main: idle animations enabled = %v", idleAnims)

	// Step 5: Start the brain (state machine) and event loops.
	log.Println("main: [5/5] starting brain state machine...")
	sm := brain.NewStateMachine(ttsClient, asrClient, llmClient, player, recorder, idleAnims)

	// Start the FSM loop.
	go sm.Run()

	// Handle incoming events from the renderer (user taps, etc.).
	go func() {
		for msg := range r.Events() {
			// "ready" means the webview page has finished loading and is
			// ready to receive state. Re-send the current state so the
			// frontend gets the initial config (e.g. idle animations flag).
			if msg.Type == "ready" {
				sm.Reemit()
				continue
			}
			sm.HandleEvent(msg)
		}
	}()

	// Forward brain state changes to the renderer.
	go func() {
		for state := range sm.StateChanges() {
			r.SendMessage(state)
		}
	}()

	// Forward viseme events to the renderer for lip-sync.
	go func() {
		for vis := range sm.Visemes() {
			r.SendMessage(vis)
		}
	}()

	log.Println("main: all subsystems started, waiting for exit signal...")

	// Wait for SIGINT or SIGTERM.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	log.Printf("main: received signal %v, shutting down...", sig)
}