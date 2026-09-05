// Package main is the entry point for the Avatar PC application.
// It creates a window (WebView2 on Windows) and loads the 3D digital
// human rendering page.
//
// Configuration is read from cfg.yml in the same directory as the
// executable. See cfg.yml for the available options.
package main

import (
	"embed"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/liuyngchng/avatar-desktop-x64/internal/asr"
	"github.com/liuyngchng/avatar-desktop-x64/internal/audio"
	"github.com/liuyngchng/avatar-desktop-x64/internal/brain"
	"github.com/liuyngchng/avatar-desktop-x64/internal/config"
	"github.com/liuyngchng/avatar-desktop-x64/internal/llm"
	"github.com/liuyngchng/avatar-desktop-x64/internal/logfile"
	"github.com/liuyngchng/avatar-desktop-x64/internal/logging"
	"github.com/liuyngchng/avatar-desktop-x64/internal/renderer"
	"github.com/liuyngchng/avatar-desktop-x64/internal/tts"
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
	// when launched by double-click (no visible console).
	logF, err := logfile.Init()
	if err != nil {
		// Still try to continue — log to stderr only.
		logging.Init(os.Stderr, slog.LevelInfo)
		slog.Error("logfile: init failed", "error", err)
	} else {
		defer logF.Close()
		logging.Init(logF, slog.LevelInfo)
	}

	slog.Info("=== Avatar PC starting ===")

	// Step 1: Load configuration from cfg.yml (optional).
	slog.Info("main: [1/5] loading config...")
	cfg, err := config.Load()
	if err != nil {
		// cfg.yml exists but is malformed — that's a real error.
		slog.Error("main: failed to load config", "error", err)
		os.Exit(1)
	}
	if cfg == nil {
		// No cfg.yml found — run in "display only" mode. The avatar renders
		// and the window works, but tapping will log a clear error instead
		// of talking. logging defaults to Info, so no level setup needed.
		slog.Info("main: [1/5] cfg.yml NOT found — running display-only (talking disabled)")
	} else {
		// Apply the configured log level as early as possible so subsequent
		// init messages honor it. ParseLevel returns Info on unknown input.
		logging.SetLevel(logging.ParseLevel(cfg.Log.Level))
		slog.Info("main: [1/5] config loaded OK", "log.level", cfg.Log.Level)
		slog.Info("main: " + config.ProxyDesc(cfg.Proxy, cfg.ProxyDisabled))
	}

	// Step 2: Create the renderer window FIRST — the user should see the
	// VRM avatar as soon as possible, even if audio/network init fails.
	slog.Info("main: [2/5] creating renderer window...")
	r, err := renderer.New(webAssets)
	if err != nil {
		slog.Error("main: failed to create renderer", "error", err)
		os.Exit(1)
	}
	defer r.Close()
	slog.Info("main: [2/5] renderer window created OK")

	// Step 3: Initialize ASR, LLM, TTS clients.
	//   - ASR and TTS are created by asr.Init/tts.Init, which are selected
	//     at build time: online (DashScope API) by default, or offline
	//     (local sherpa-onnx model) with -tags offline.
	//   - LLM is always online and reads cfg.yml.
	//   - If cfg is nil (no cfg.yml), we skip entirely and the avatar will
	//     only display, not talk.
	var asrClient asr.Transcriber
	var llmClient *llm.Client
	var ttsClient tts.Synthesizer

	if cfg != nil {
		slog.Info("main: [3/5] initializing clients...")

		// ── ASR ────────────────────────────────────
		asrClient, err = asr.Init(cfg)
		if err != nil {
			slog.Error("main: ASR init failed", "error", err)
			os.Exit(1)
		}
		defer asrClient.Close()
		slog.Info("main: [3/5] ASR 模式: " + asr.Mode() + " (" + asr.Desc(cfg) + ")")

		// ── LLM (always online) ───────────────────
		if cfg.LLM.URL == "" {
			slog.Error("main: LLM init failed: cfg.yml llm.url is required")
			os.Exit(1)
		}
		llmClient = llm.NewClient(cfg.LLM.URL, cfg.LLM.Model, cfg.APIKey, cfg.LLM.Name, cfg.LLM.MaxTokens, config.ProxyFunc(cfg.Proxy, cfg.ProxyDisabled))
		defer llmClient.Close()
		slog.Info("main: [3/5] LLM", "endpoint", cfg.LLM.URL, "model", cfg.LLM.Model)

		// ── TTS ────────────────────────────────────
		ttsClient, err = tts.Init(cfg)
		if err != nil {
			slog.Error("main: TTS init failed", "error", err)
			os.Exit(1)
		}
		defer ttsClient.Close()
		slog.Info("main: [3/5] TTS 模式: " + tts.Mode() + " (" + tts.Desc(cfg) + ")")
	} else {
		slog.Info("main: [3/5] skipped — no cfg.yml (talking disabled)")
	}

	// Step 4: Initialize audio player (may block briefly on some systems).
	slog.Info("main: [4/5] initializing audio player...")
	var player *audio.Player
	if cfg != nil {
		player, err = audio.NewPlayer(ttsClient.SampleRate())
		if err != nil {
			slog.Warn("main: [4/5] audio player init failed (will continue)", "error", err)
		} else {
			slog.Info("main: [4/5] waiting for audio player ready...")
			player.WaitReady()
			slog.Info("main: [4/5] audio player ready OK")
			defer player.Close()
		}
	} else {
		slog.Info("main: [4/5] skipped — no cfg.yml (talking disabled)")
	}

	// Initialize audio recorder.
	recorder := audio.NewRecorder()
	slog.Info("main: [4/5] audio recorder created OK")
	defer recorder.Stop()

	// Step 5: Determine idle animation flag and wake word.
	idleAnims := true                       // default: enabled
	wakeWord := ""                          // default: "小然" (applied in brain)
	conversationIdle := 3 * time.Second     // default multi-turn idle window
	if cfg != nil {
		idleAnims = cfg.Avatar.IdleAnimations()
		wakeWord = cfg.WakeWord
		conversationIdle = cfg.Avatar.ConversationIdle()
	}
	slog.Info("main: idle animations", "enabled", idleAnims, "wake_word", wakeWord, "conversation_idle_ms", conversationIdle.Milliseconds())

	// Step 5: Start the brain (state machine) and event loops.
	slog.Info("main: [5/5] starting brain state machine...")
	sm := brain.NewStateMachine(ttsClient, asrClient, llmClient, player, recorder, idleAnims, wakeWord, conversationIdle)

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

	slog.Info("main: all subsystems started, waiting for exit signal...")

	// Print a concise, human-readable startup summary so the user can see at
	// a glance what backend each subsystem is using and whether a proxy is
	// in effect — without having to scroll through the detailed logs above.
	slog.Info("============================================================")
	slog.Info("启动信息汇总 (Startup Summary)")
	if cfg == nil {
		slog.Info("  配置: 未找到 cfg.yml — 仅显示模式 (不说话)")
		slog.Info("  代理: 无")
		slog.Info("  ASR : 未启用")
		slog.Info("  TTS : 未启用")
		slog.Info("  LLM : 未启用")
	} else {
		slog.Info("  代理: " + config.ProxyDesc(cfg.Proxy, cfg.ProxyDisabled))
		slog.Info("  ASR : " + asr.Mode() + " | " + asr.Desc(cfg))
		slog.Info("  TTS : " + tts.Mode() + " | " + tts.Desc(cfg))
		if llmClient != nil {
			slog.Info("  LLM : 在线", "model", cfg.LLM.Model)
		}
	}
	slog.Info("============================================================")

	// Wait for one of:
	//   - User closes the window    → r.Done()
	//   - SIGINT / SIGTERM (Ctrl+C) → sigCh
	// In either case we clean up through deferred calls and exit.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-r.Done():
		slog.Info("main: window closed, shutting down...")
	case sig := <-sigCh:
		slog.Info("main: received signal, shutting down...", "signal", sig)
	}
}