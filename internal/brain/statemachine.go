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
)

const recorderSampleRate = 16000

// Event is a message coming from the renderer (user interaction).
type Event struct {
	Type string `json:"type"`
	Data any    `json:"data,omitempty"`
}

// StateMachine orchestrates the digital human's behavior:
// tap → record (VAD) → ASR → LLM → TTS → play + viseme → idle.
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

	mu   sync.Mutex
	busy bool
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
		if sm.busy {
			sm.mu.Unlock()
			log.Printf("state: event=%s ignored (busy)", ev.Type)
			return
		}
		sm.busy = true
		sm.mu.Unlock()

		log.Printf("state: event=%s → listening", ev.Type)
		sm.setState(ModeListening, EmotionNeutral, "")
		sm.emit()

		go sm.pipeline()
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
func (sm *StateMachine) pipeline() {
	// Clear the busy flag when this turn completes.
	defer func() {
		sm.mu.Lock()
		sm.busy = false
		sm.mu.Unlock()
	}()

	// 1. Record until silence (simple energy-based VAD).
	samples, err := sm.recordWithVAD()
	if err != nil {
		log.Printf("state: recording failed: %v", err)
		sm.setState(ModeIdle, EmotionNeutral, "")
		sm.emit()
		return
	}
	if len(samples) == 0 {
		log.Printf("state: no speech detected")
		sm.setState(ModeIdle, EmotionNeutral, "")
		sm.emit()
		return
	}

	// 2. ASR.
	sm.setState(ModeThinking, EmotionNeutral, "")
	sm.emit()

	userText, err := sm.asrClient.Transcribe(samples, recorderSampleRate)
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
	log.Printf("state: user said %q", userText)

	// 3. LLM.
	replyText, err := sm.llmClient.Chat(userText)
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

	// 4. TTS.
	result, err := sm.ttsClient.Synthesize(replyText, 1.0)
	if err != nil {
		log.Printf("state: TTS failed: %v", err)
		sm.setState(ModeIdle, EmotionNeutral, "")
		sm.emit()
		return
	}

	// 5. Generate viseme timeline.
	audioDur := time.Duration(float64(len(result.Samples)) / float64(result.SampleRate) * float64(time.Second))
	timeline := GenerateVisemeTimeline(replyText, audioDur)
	log.Printf("state: viseme timeline: %d entries, total audio %.1fs", len(timeline), audioDur.Seconds())

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

	// Drive viseme timeline while audio plays.
	startTime := time.Now()
	timelineIdx := 0
	for player.IsPlaying() {
		if err := player.Err(); err != nil {
			log.Printf("state: audio play error: %v", err)
			break
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

// recordWithVAD records audio until the user stops speaking, using a
// simple energy-based voice activity detection: it waits for speech to
// start, then stops after a configurable duration of silence.
func (sm *StateMachine) recordWithVAD() ([]float32, error) {
	chunks, err := sm.recorder.Start()
	if err != nil {
		return nil, err
	}
	defer sm.recorder.Stop()

	const (
		speechThreshold = 0.01                // RMS above this counts as speech
		silenceDuration = 1500 * time.Millisecond // silence to end the turn
		maxDuration     = 30 * time.Second        // hard safety cap
	)

	var all []float32
	speaking := false
	lastSpeech := time.Now()
	start := time.Now()

	for chunk := range chunks {
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
			break
		}

		// Hard safety cap.
		if time.Since(start) >= maxDuration {
			log.Printf("state: max recording duration reached")
			break
		}
	}

	// Trim leading silence (everything before speech began is noise).
	if !speaking {
		return nil, nil
	}

	return all, nil
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