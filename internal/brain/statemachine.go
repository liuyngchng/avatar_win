package brain

import (
	"log"
	"math"
	"sync"
	"time"

	"github.com/liuyngchng/avatar-pc/internal/asr"
	"github.com/liuyngchng/avatar-pc/internal/audio"
	"github.com/liuyngchng/avatar-pc/internal/llm"
	"github.com/liuyngchng/avatar-pc/internal/tts"

	"github.com/ebitengine/oto/v3"
)

const recorderSampleRate = 16000

// Event is a message coming from the renderer (user interaction).
type Event struct {
	Type string `json:"type"`
	Data any    `json:"data,omitempty"`
}

// StateMachine orchestrates the digital human's behavior:
// tap → record (VAD) → ASR → LLM → TTS → play + viseme → idle.
//
// Tapping while the avatar is speaking interrupts the current turn and
// starts a new one immediately.
type StateMachine struct {
	state        State
	stateChanges chan State
	events       chan Event
	visemes      chan VisemeEvent
	ttsClient    *tts.Client
	asrClient    *asr.Client
	llmClient    *llm.Client
	audioPlayer  *audio.Player
	recorder     audio.Recorder

	mu         sync.Mutex
	busy       bool
	generation int64        // incremented on each tap; used to detect stale pipelines
	cancel     chan struct{} // closed when the current pipeline should abort
}

// NewStateMachine creates a state machine in ModeIdle.
func NewStateMachine(
	ttsClient *tts.Client,
	asrClient *asr.Client,
	llmClient *llm.Client,
	audioPlayer *audio.Player,
	recorder audio.Recorder,
) *StateMachine {
	return &StateMachine{
		state: State{
			Mode:    ModeIdle,
			Emotion: EmotionNeutral,
		},
		stateChanges: make(chan State, 16),
		events:       make(chan Event, 16),
		visemes:      make(chan VisemeEvent, 64),
		ttsClient:    ttsClient,
		asrClient:    asrClient,
		llmClient:    llmClient,
		audioPlayer:  audioPlayer,
		recorder:     recorder,
		cancel:       make(chan struct{}),
	}
}

// Run starts the FSM loop. It blocks until the channel is closed.
func (sm *StateMachine) Run() {
	sm.emit()
	for ev := range sm.events {
		sm.handleEvent(ev)
	}
}

// StateChanges returns the channel of state updates the main loop
// forwards to the renderer.
func (sm *StateMachine) StateChanges() <-chan State {
	return sm.stateChanges
}

// Visemes returns the channel of viseme events the main loop forwards
// to the renderer for lip-sync.
func (sm *StateMachine) Visemes() <-chan VisemeEvent {
	return sm.visemes
}

// HandleEvent feeds a renderer event into the FSM.
func (sm *StateMachine) HandleEvent(ev Event) {
	sm.events <- ev
}

func (sm *StateMachine) emit() {
	sm.mu.Lock()
	s := sm.state // copy
	sm.mu.Unlock()
	select {
	case sm.stateChanges <- s:
	default:
	}
}

func (sm *StateMachine) handleEvent(ev Event) {
	switch ev.Type {
	case "tap", "wake_detected":
		sm.mu.Lock()

		// Increment generation so any running pipeline knows it's stale.
		sm.generation++
		gen := sm.generation

		if sm.busy {
			// Interrupt the current pipeline.
			close(sm.cancel)
			sm.cancel = make(chan struct{})
			log.Printf("state: event=%s → interrupting current turn (gen=%d → %d)", ev.Type, gen-1, gen)
		} else {
			log.Printf("state: event=%s → listening (gen=%d)", ev.Type, gen)
		}

		sm.busy = true
		sm.mu.Unlock()

		sm.setState(ModeListening, EmotionNeutral, "")
		sm.emit()

		go sm.pipeline(gen)
	}
}

// setState safely updates the state fields.
func (sm *StateMachine) setState(mode Mode, emotion Emotion, responseText string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.state.Mode = mode
	sm.state.Emotion = emotion
	if responseText != "" {
		sm.state.ResponseText = responseText
	}
}

// pipeline runs a full conversation turn:
// record → ASR → LLM → TTS → play + viseme → idle.
//
// gen is the generation number at the time this pipeline was created.
// If a newer generation exists (the user tapped again), this pipeline
// aborts early.
func (sm *StateMachine) pipeline(gen int64) {
	// Clear the busy flag only when this pipeline is still the current one.
	defer func() {
		sm.mu.Lock()
		if sm.generation == gen {
			sm.busy = false
		}
		sm.mu.Unlock()
	}()

	// Snapshot the cancel channel for this generation.
	sm.mu.Lock()
	cancel := sm.cancel
	sm.mu.Unlock()

	canceled := func() bool {
		select {
		case <-cancel:
			return true
		default:
			return false
		}
	}

	// 1. Record until silence (simple energy-based VAD).
	samples := sm.recordWithVAD(cancel)
	if canceled() {
		log.Printf("state: canceled after recording (gen=%d)", gen)
		return
	}
	if len(samples) == 0 {
		log.Printf("state: no speech detected (gen=%d)", gen)
		sm.setState(ModeIdle, EmotionNeutral, "")
		sm.emit()
		return
	}

	// 2. ASR.
	sm.setState(ModeThinking, EmotionNeutral, "")
	sm.emit()

	userText, err := sm.asrClient.Transcribe(samples, recorderSampleRate)
	if canceled() {
		log.Printf("state: canceled after ASR (gen=%d)", gen)
		return
	}
	if err != nil {
		log.Printf("state: ASR failed: %v", err)
		sm.setState(ModeIdle, EmotionNeutral, "")
		sm.emit()
		return
	}
	userText = trimSpace(userText)
	if userText == "" {
		log.Printf("state: ASR returned empty text")
		sm.setState(ModeIdle, EmotionNeutral, "")
		sm.emit()
		return
	}

	sm.mu.Lock()
	sm.state.LastUserText = userText
	sm.mu.Unlock()
	log.Printf("state: user said %q (gen=%d)", userText, gen)

	// Re-emit so the frontend shows the recognized text during thinking.
	sm.emit()

	if canceled() {
		log.Printf("state: canceled before LLM (gen=%d)", gen)
		return
	}

	// 3. LLM.
	replyText, err := sm.llmClient.Chat(userText)
	if canceled() {
		log.Printf("state: canceled after LLM (gen=%d)", gen)
		return
	}
	if err != nil {
		log.Printf("state: LLM failed: %v", err)
		sm.setState(ModeIdle, EmotionNeutral, "")
		sm.emit()
		return
	}
	replyText = trimSpace(replyText)
	if replyText == "" {
		log.Printf("state: LLM returned empty reply")
		sm.setState(ModeIdle, EmotionNeutral, "")
		sm.emit()
		return
	}

	sm.setState(ModeThinking, EmotionHappy, replyText)

	if canceled() {
		log.Printf("state: canceled before TTS (gen=%d)", gen)
		return
	}

	// 4. TTS.
	result, err := sm.ttsClient.Synthesize(replyText, 1.0)
	if canceled() {
		log.Printf("state: canceled after TTS (gen=%d)", gen)
		return
	}
	if err != nil {
		log.Printf("state: TTS failed: %v", err)
		sm.setState(ModeIdle, EmotionNeutral, "")
		sm.emit()
		return
	}

	// 5. Generate viseme timeline.
	audioDur := time.Duration(float64(len(result.Samples)) / float64(result.SampleRate) * float64(time.Second))
	timeline := GenerateVisemeTimeline(replyText, audioDur)
	log.Printf("state: viseme timeline: %d entries, total audio %.1fs (gen=%d)", len(timeline), audioDur.Seconds(), gen)

	// 6. Speak.
	if sm.audioPlayer == nil {
		log.Printf("state: audio player is nil, skipping playback")
		sm.mu.Lock()
		sm.state.IsSpeaking = false
		sm.mu.Unlock()
		sm.setState(ModeIdle, EmotionNeutral, "")
		sm.emit()
		return
	}

	sm.mu.Lock()
	sm.state.Mode = ModeSpeaking
	sm.state.IsSpeaking = true
	sm.mu.Unlock()
	sm.emit()

	player, err := sm.audioPlayer.Play(result.Samples)
	if err != nil {
		log.Printf("state: audio play error: %v", err)
		sm.mu.Lock()
		sm.state.IsSpeaking = false
		sm.mu.Unlock()
		sm.setState(ModeIdle, EmotionNeutral, "")
		sm.emit()
		return
	}

	// Drive viseme timeline while audio plays. The loop also checks for
	// cancellation so the user can interrupt the avatar mid-speech.
	sm.speakWithCancel(player, timeline, cancel)

	// Reset viseme to rest.
	select {
	case sm.visemes <- VisemeEvent{Type: "viseme", Viseme: VisemeRest, Weight: 0}:
	default:
	}

	// 7. Back to idle.
	sm.mu.Lock()
	sm.state.IsSpeaking = false
	sm.mu.Unlock()
	sm.setState(ModeIdle, EmotionNeutral, "")
	sm.emit()
}

// speakWithCancel drives viseme timeline while audio plays. If the cancel
// channel is closed, the audio is stopped immediately.
func (sm *StateMachine) speakWithCancel(player *oto.Player, timeline []VisemeTimelineEntry, cancel <-chan struct{}) {
	startTime := time.Now()
	timelineIdx := 0

	for player.IsPlaying() {
		// Check for cancellation.
		select {
		case <-cancel:
			log.Printf("state: playback interrupted by user")
			player.Pause()
			return
		default:
		}

		if err := player.Err(); err != nil {
			log.Printf("state: audio play error: %v", err)
			return
		}

		elapsed := time.Since(startTime).Milliseconds()

		for timelineIdx < len(timeline) && int64(timeline[timelineIdx].StartMs) <= elapsed {
			entry := timeline[timelineIdx]
			ev := VisemeEvent{
				Type:   "viseme",
				Viseme: entry.Viseme,
				Weight: 1.0,
			}
			select {
			case sm.visemes <- ev:
			default:
			}
			timelineIdx++
		}

		time.Sleep(10 * time.Millisecond)
	}
}

// recordWithVAD records audio until the user stops speaking, using a
// simple energy-based voice activity detection: it waits for speech to
// start, then stops after a configurable duration of silence.
//
// If the cancel channel is closed, recording stops immediately.
func (sm *StateMachine) recordWithVAD(cancel <-chan struct{}) []float32 {
	chunks, err := sm.recorder.Start()
	if err != nil {
		log.Printf("state: recording failed: %v", err)
		return nil
	}
	// Never defer Stop() here — if we're cancelled, the next pipeline's
	// Start() call already owns the recorder and our Stop() would kill it.

	const (
		speechThreshold = 0.01                 // RMS above this counts as speech
		silenceDuration = 1500 * time.Millisecond // silence to end the turn
		maxDuration     = 30 * time.Second        // hard safety cap
	)

	var all []float32
	speaking := false
	lastSpeech := time.Now()
	start := time.Now()

	for {
		select {
		case <-cancel:
			log.Printf("state: recording canceled")
			return all
		case chunk, ok := <-chunks:
			if !ok {
				// Channel closed — recorder stopped.
				if !speaking {
					return nil
				}
				return all
			}

			// Accumulate.
			all = append(all, chunk...)

			rms := rmsOf(chunk)
			if rms > speechThreshold {
				if !speaking {
					log.Printf("state: speech started (rms=%.4f)", rms)
					speaking = true
				}
				lastSpeech = time.Now()
			}

			// Stop when speech started and silence persisted long enough.
			if speaking && time.Since(lastSpeech) >= silenceDuration {
				log.Printf("state: silence detected, stopping recording")
				return all
			}

			// Hard safety cap.
			if time.Since(start) >= maxDuration {
				log.Printf("state: max recording duration reached")
				return all
			}
		}
	}
}

// rmsOf computes the root-mean-square of a float32 sample chunk.
func rmsOf(samples []float32) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		sum += float64(s) * float64(s)
	}
	return math.Sqrt(sum / float64(len(samples)))
}

// trimSpace is a small helper to trim surrounding whitespace from a string.
func trimSpace(s string) string {
	start := 0
	end := len(s)
	for start < end {
		c := s[start]
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			break
		}
		start++
	}
	for end > start {
		c := s[end-1]
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			break
		}
		end--
	}
	return s[start:end]
}