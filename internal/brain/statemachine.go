package brain

import (
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/liuyngchng/avatar-desktop-x64/internal/asr"
	"github.com/liuyngchng/avatar-desktop-x64/internal/audio"
	"github.com/liuyngchng/avatar-desktop-x64/internal/llm"
	"github.com/liuyngchng/avatar-desktop-x64/internal/tts"

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
//
// When the user activates the avatar (via wake word or tap), the state
// machine enters multi-turn mode: after each reply it stays in
// ModeAwaitingUser for conversationIdle (default 3s), and VAD-detected
// speech during that window starts a new turn without requiring the wake
// word again. If the window expires with no speech, the FSM falls back
// to ModeIdle and the wake-word detector resumes.
type StateMachine struct {
	state        State
	stateChanges chan State
	events       chan Event
	visemes      chan VisemeEvent
	ttsClient    tts.Synthesizer
	asrClient    asr.Transcriber
	llmClient    *llm.Client
	audioPlayer  *audio.Player
	recorder     audio.Recorder

	mu         sync.Mutex
	busy       bool
	generation int64         // incremented on each tap; used to detect stale pipelines
	cancel     chan struct{} // closed when the current pipeline should abort

	// inConversation is true while the FSM is in the multi-turn window
	// (after the user activated the avatar and before the idle timeout).
	// Guarded by mu.
	inConversation bool

	// conversationIdle is how long the FSM waits, after the avatar
	// finishes speaking, for the user to start a new turn before falling
	// back to wake-word mode.
	conversationIdle time.Duration

	// wakeWordConfig is the wake word from cfg.yml (default "小然").
	wakeWordConfig string
	// wakeDetector is the background wake-word listener, active while idle.
	// Guarded by mu.
	wakeDetector *wakeWordDetector

	// conversationListener is the multi-turn VAD listener, active while
	// inConversation is true. Guarded by mu.
	conversationListener *conversationListener
}

// NewStateMachine creates a state machine in ModeIdle.
func NewStateMachine(
	ttsClient tts.Synthesizer,
	asrClient asr.Transcriber,
	llmClient *llm.Client,
	audioPlayer *audio.Player,
	recorder audio.Recorder,
	idleAnimationsEnabled bool,
	wakeWord string,
	conversationIdle time.Duration,
) *StateMachine {
	if conversationIdle <= 0 {
		conversationIdle = 3 * time.Second
	}
	sm := &StateMachine{
		state: State{
			Mode:                  ModeIdle,
			Emotion:               EmotionNeutral,
			IdleAnimationsEnabled: idleAnimationsEnabled,
		},
		stateChanges:     make(chan State, 16),
		events:           make(chan Event, 16),
		visemes:          make(chan VisemeEvent, 64),
		ttsClient:        ttsClient,
		asrClient:        asrClient,
		llmClient:        llmClient,
		audioPlayer:      audioPlayer,
		recorder:         recorder,
		cancel:           make(chan struct{}),
		wakeWordConfig:   wakeWord,
		conversationIdle: conversationIdle,
	}
	// Start the wake word detector in the background. It will only activate
	// when the state machine is idle and API clients are initialized.
	sm.startWakeWordDetectorLocked()
	return sm
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

// Reemit re-sends the current state to the renderer. The FSM emits its
// initial state before the webview page has necessarily finished loading,
// so the renderer signals "ready" after load and we re-send here to make
// sure the frontend receives the initial config (e.g. idle animations flag).
func (sm *StateMachine) Reemit() {
	sm.emit()
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
	case "tap", "wake_detected", "speech_detected":
		// If no API config was loaded (nil clients), the avatar can't talk.
		// Log a clear error and return to idle instead of crashing on a
		// nil-pointer dereference deep in the pipeline.
		if sm.asrClient == nil || sm.llmClient == nil || sm.ttsClient == nil {
			slog.Error("statemachine_handleEvent_state:_event="+ev.Type+" → CANNOT TALK: no cfg.yml / API clients not initialized",
				"asr", sm.asrClient != nil, "llm", sm.llmClient != nil, "tts", sm.ttsClient != nil)
			sm.setState(ModeIdle, EmotionNeutral, "")
			sm.emit()
			return
		}

		sm.mu.Lock()

		// Stop both background listeners before the pipeline takes the mic.
		sm.cancelWakeDetectorLocked()
		sm.cancelConversationListenerLocked()

		// Activate multi-turn mode. Any tap or wake word keeps the
		// conversation window open across turns; speech_detected is
		// emitted from inside an already-open window, so this is a no-op
		// for that branch but harmless.
		sm.inConversation = true

		// Increment generation so any running pipeline knows it's stale.
		sm.generation++
		gen := sm.generation

		if sm.busy {
			// Interrupt the current pipeline.
			close(sm.cancel)
			sm.cancel = make(chan struct{})
			slog.Debug("statemachine_handleEvent_state:_interrupting_current_turn", "event", ev.Type, "gen", gen, "prev_gen", gen-1)
		} else {
			slog.Info("statemachine_handleEvent_state:_listening", "event", ev.Type, "gen", gen)
		}

		sm.busy = true
		sm.mu.Unlock()

		sm.setState(ModeListening, EmotionNeutral, "")
		sm.emit()

		// If the event carries pre-existing text (e.g. wake word followed by
		// "今天天气怎么样？"), skip recording + ASR and go straight to LLM.
		preExistingText, _ := ev.Data.(string)

		go sm.pipeline(gen, preExistingText)
	}
}

// setState safely updates the state fields.
func (sm *StateMachine) setState(mode Mode, emotion Emotion, responseText string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.state.Mode = mode
	sm.state.Emotion = emotion
	sm.state.ResponseText = responseText
}

// setStateLocked is like setState but the caller must already hold sm.mu.
func (sm *StateMachine) setStateLocked(mode Mode, emotion Emotion, responseText string) {
	sm.state.Mode = mode
	sm.state.Emotion = emotion
	sm.state.ResponseText = responseText
}

// pipeline runs a full conversation turn:
// record → ASR → LLM (stream) + TTS (overlap) → play + viseme → idle.
//
// If preExistingText is non-empty (e.g. the user said the wake word followed
// by a command), recording and ASR are skipped and the text goes straight to
// the LLM.
//
// LLM and TTS run concurrently: as soon as a complete sentence arrives from
// the streaming LLM, it is sent to TTS while the LLM continues generating
// the next sentence. This cuts the LLM→TTS serial wait time significantly.
//
// gen is the generation number at the time this pipeline was created.
// If a newer generation exists (the user tapped again), this pipeline
// aborts early.
func (sm *StateMachine) pipeline(gen int64, preExistingText string) {
	t0 := time.Now() // ⏱ pipeline start (user trigger)

	// Clear the busy flag only when this pipeline is still the current one.
	defer func() {
		sm.mu.Lock()
		if sm.generation == gen {
			sm.busy = false
			if sm.inConversation {
				// Multi-turn window: stay open for VAD-driven follow-ups.
				// The conversation listener will end the window itself if
				// no speech arrives within conversationIdle.
				if sm.state.Mode != ModeAwaitingUser {
					sm.setStateLocked(ModeAwaitingUser, EmotionNeutral, "")
				}
				sm.startConversationListenerLocked()
			} else {
				// Single-turn: back to wake-word listening.
				if sm.state.Mode == ModeIdle || sm.state.Mode == ModeThinking {
					sm.setStateLocked(ModeIdle, EmotionNeutral, "")
				}
				sm.startWakeWordDetectorLocked()
			}
		}
		sm.mu.Unlock()
		sm.emit()
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

	var userText string
	var err error
	var tRecStart, tRecEnd, tASRStart, tASREnd time.Time
	if preExistingText != "" {
		// Wake word + command path: no recording or ASR needed.
		userText = preExistingText
		slog.Info("statemachine_pipeline_state:_user_said", "text", userText, "gen", gen, "source", "wake_word")
	} else {
		// 1. Record until silence (simple energy-based VAD).
		tRecStart = time.Now() // ⏱ recording start
		samples := sm.recordWithVAD(cancel)
		tRecEnd = time.Now() // ⏱ recording end
		if canceled() {
			slog.Debug("statemachine_pipeline_state:_canceled_after_recording", "gen", gen)
			return
		}
		if len(samples) == 0 {
			slog.Info("statemachine_pipeline_state:_no_speech_detected", "gen", gen)
			sm.setState(ModeIdle, EmotionNeutral, "")
			sm.emit()
			return
		}
		slog.Debug("statemachine_pipeline_⏱_[timing]_recording", "ms", tRecEnd.Sub(tRecStart).Milliseconds(), "elapsed_ms", tRecEnd.Sub(t0).Milliseconds())

		// 2. ASR.
		sm.setState(ModeThinking, EmotionNeutral, "")
		sm.emit()

		tASRStart = time.Now() // ⏱ ASR start
		userText, err = sm.asrClient.Transcribe(samples, recorderSampleRate)
		tASREnd = time.Now() // ⏱ ASR end
		if canceled() {
			slog.Debug("statemachine_pipeline_state:_canceled_after_ASR", "gen", gen)
			return
		}
		if err != nil {
			slog.Error("statemachine_pipeline_state:_ASR_failed", "error", err)
			sm.setState(ModeIdle, EmotionNeutral, "")
			sm.emit()
			return
		}
		userText = trimSpace(userText)
		if userText == "" {
			slog.Info("statemachine_pipeline_state:_ASR_returned_empty_text")
			sm.setState(ModeIdle, EmotionNeutral, "")
			sm.emit()
			return
		}

		sm.mu.Lock()
		sm.state.LastUserText = userText
		sm.mu.Unlock()
		slog.Info("statemachine_pipeline_state:_user_said", "text", userText, "gen", gen)
		slog.Debug("statemachine_pipeline_⏱_[timing]_ASR", "ms", tASREnd.Sub(tASRStart).Milliseconds(), "elapsed_ms", tASREnd.Sub(t0).Milliseconds())
	}

	// Re-emit so the frontend shows the recognized text during thinking.
	sm.emit()

	if canceled() {
		slog.Debug("statemachine_pipeline_state:_canceled_before_LLM", "gen", gen)
		return
	}

	// 3. LLM (streaming) + TTS (overlap).
	// LLM tokens arrive via a channel; we accumulate them into sentences.
	// When a sentence boundary is reached, we immediately send it to TTS
	// while the LLM keeps generating the next sentence.
	// Once all LLM tokens are collected, we wait for the final TTS to finish.
	tLLMStart := time.Now() // ⏱ LLM start
	llmCh := sm.llmClient.ChatStream(userText)

	// Collect all synthesized audio and sentences.
	var allSamples []float32
	var allSentences []string
	tTTSStart := time.Now() // ⏱ TTS overall start (first synthesis)
	tLLMFirstToken := time.Time{}
	tLLMLastToken := time.Time{}
	llmFirstTokenSet := false

	var sb strings.Builder
	ttsDone := make(chan struct{}) // closed when the TTS goroutine is done
	ttsErrs := make(chan error, 1) // buffered so goroutine never blocks on error

	// TTS goroutine: receives sentences, synthesizes them one at a time.
	// This runs concurrently with the LLM stream consumer.
	sentenceCh := make(chan string, 4)
	go func() {
		defer close(ttsDone)
		for sentence := range sentenceCh {
			if canceled() {
				return
			}
			result, err := sm.ttsClient.Synthesize(sentence, 1.0)
			if err != nil {
				slog.Error("statemachine_pipeline_state:_TTS_failed_for_sentence", "sentence", sentence, "error", err)
				ttsErrs <- err
				return
			}
			allSamples = append(allSamples, result.Samples...)
			allSentences = append(allSentences, sentence)
		}
	}()

	// Consume LLM stream, splitting on sentence boundaries (。！？!?).
	// We only split when we have at least minSentenceLen runes and a
	// sentence-ending punctuation, to avoid sending tiny fragments to TTS.
	const minSentenceLen = 4
	llmDone := false
	for !llmDone {
		select {
		case token, ok := <-llmCh:
			if !ok {
				llmDone = true
				if !llmFirstTokenSet {
					tLLMLastToken = time.Now()
				}
				break
			}
			if !llmFirstTokenSet {
				tLLMFirstToken = time.Now()
				llmFirstTokenSet = true
			}
			tLLMLastToken = time.Now()
			sb.WriteString(token)
			// Check whether we have a complete sentence.
			current := sb.String()
			idx := sentenceEndIndex(current)
			if idx > 0 && utf8.RuneCountInString(current[:idx]) >= minSentenceLen {
				sentence := strings.TrimSpace(current[:idx])
				rest := strings.TrimSpace(current[idx:])
				sb.Reset()
				sb.WriteString(rest)
				if sentence != "" {
					sentenceCh <- sentence
				}
			}
		case <-cancel:
			close(sentenceCh)
			<-ttsDone
			slog.Debug("statemachine_pipeline_state:_canceled_during_LLM", "gen", gen)
			return
		}
	}

	// Flush any remaining text as the last sentence.
	// Only send if it has meaningful content (>1 rune, not just whitespace/punct).
	remaining := strings.TrimSpace(sb.String())
	if utf8.RuneCountInString(remaining) >= 2 {
		sentenceCh <- remaining
	} else {
		slog.Debug("statemachine_pipeline_state:_discarding_short_sentence_tail", "text", remaining, "runes", utf8.RuneCountInString(remaining))
	}

	// Close sentenceCh so the TTS goroutine finishes.
	close(sentenceCh)
	<-ttsDone

	// Check for TTS errors.
	select {
	case err := <-ttsErrs:
		slog.Error("statemachine_pipeline_state:_TTS_failed", "error", err)
		sm.setState(ModeIdle, EmotionNeutral, "")
		sm.emit()
		return
	default:
	}

	if canceled() {
		slog.Debug("statemachine_pipeline_state:_canceled_after_TTS", "gen", gen)
		return
	}

	if len(allSamples) == 0 {
		slog.Warn("statemachine_pipeline_state:_TTS_produced_no_audio")
		sm.setState(ModeIdle, EmotionNeutral, "")
		sm.emit()
		return
	}

	// Reconstruct the full reply text for state display.
	replyText := strings.Join(allSentences, "")
	sm.setState(ModeThinking, EmotionHappy, replyText)

	// Record the completed turn in the LLM client's conversation history.
	// This enables multi-turn context for future requests.
	if sm.llmClient != nil {
		sm.llmClient.RecordTurn(userText, replyText)
	}

	// LLM timing: first_token = when we got the first chunk, last_token = when the stream ended.
	// TTS timing: from first sentence sent to TTS until last sentence TTS completed.
	tTTSEnd := time.Now()
	tLLMEnd := tLLMLastToken

	if llmFirstTokenSet {
		slog.Debug("statemachine_pipeline_⏱_[timing]_LLM",
			"first_token_ms", tLLMFirstToken.Sub(tLLMStart).Milliseconds(),
			"stream_done_ms", tLLMEnd.Sub(tLLMStart).Milliseconds(),
			"elapsed_ms", tLLMEnd.Sub(t0).Milliseconds())
	} else {
		slog.Debug("statemachine_pipeline_⏱_[timing]_LLM",
			"stream_done_ms", tLLMEnd.Sub(tLLMStart).Milliseconds(),
			"elapsed_ms", tLLMEnd.Sub(t0).Milliseconds())
	}

	slog.Debug("statemachine_pipeline_⏱_[timing]_TTS",
		"ms", tTTSEnd.Sub(tTTSStart).Milliseconds(),
		"elapsed_ms", tTTSEnd.Sub(t0).Milliseconds())

	// 4. Speak — drive mouth visemes on a fixed rhythm while audio plays.
	if sm.audioPlayer == nil {
		slog.Warn("statemachine_pipeline_state:_audio_player_is_nil,_skipping_playback")
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

	tPlayStart := time.Now() // ⏱ playback start
	player, err := sm.audioPlayer.Play(allSamples)
	if err != nil {
		slog.Error("statemachine_pipeline_state:_audio_play_error", "error", err)
		sm.mu.Lock()
		sm.state.IsSpeaking = false
		sm.mu.Unlock()
		sm.setState(ModeIdle, EmotionNeutral, "")
		sm.emit()
		return
	}

	// Cycle visemes on a fixed rhythm while audio plays. The loop also
	// checks for cancellation so the user can interrupt mid-speech.
	sm.speakWithCancel(player, cancel)
	tPlayEnd := time.Now() // ⏱ playback end

	// Reset viseme to rest.
	select {
	case sm.visemes <- VisemeEvent{Type: "viseme", Viseme: VisemeRest, Weight: 0}:
	default:
	}

	// 6. Back to idle.
	sm.mu.Lock()
	sm.state.IsSpeaking = false
	sm.mu.Unlock()
	sm.setState(ModeIdle, EmotionNeutral, "")
	sm.emit()

	// ⏱ Final timing summary for the entire pipeline.
	// LLM and TTS overlap, so use the later of the two for total.
	overlapEnd := tTTSEnd
	if tLLMEnd.After(tTTSEnd) {
		overlapEnd = tLLMEnd
	}
	slog.Debug("statemachine_pipeline_⏱_[timing]_playback",
		"ms", tPlayEnd.Sub(tPlayStart).Milliseconds(),
		"total_ms", tPlayEnd.Sub(t0).Milliseconds(),
		"rec_pct", float64(tRecEnd.Sub(tRecStart).Milliseconds())/float64(tPlayEnd.Sub(t0).Milliseconds())*100,
		"asr_pct", float64(tASREnd.Sub(tASRStart).Milliseconds())/float64(tPlayEnd.Sub(t0).Milliseconds())*100,
		"llm_tts_overlap_pct", float64(overlapEnd.Sub(tLLMStart).Milliseconds())/float64(tPlayEnd.Sub(t0).Milliseconds())*100,
		"play_pct", float64(tPlayEnd.Sub(tPlayStart).Milliseconds())/float64(tPlayEnd.Sub(t0).Milliseconds())*100,
	)
}

// sentenceEndIndex returns the byte index after the first sentence-ending
// punctuation (。！？!?) in s, or 0 if none found.
func sentenceEndIndex(s string) int {
	runes := []rune(s)
	for i, r := range runes {
		if r == '。' || r == '！' || r == '？' || r == '!' || r == '?' {
			return len(string(runes[:i+1]))
		}
	}
	return 0
}

// speakWithCancel cycles through a fixed viseme sequence while audio plays,
// giving the mouth a natural "talking" look without trying to match specific
// phonemes. If the cancel channel is closed, audio stops immediately.
//
// The cycle is: aa → ih → ou → ee → oh → rest → aa → ...
// Each shape is held for ~120ms then the mouth briefly closes (rest) before
// the next shape. This produces a rhythmic open/close that looks like talking.
func (sm *StateMachine) speakWithCancel(player *oto.Player, cancel <-chan struct{}) {
	// Viseme cycle — loop through these shapes while speaking.
	cycle := []VisemeName{VisemeA, VisemeI, VisemeU, VisemeE, VisemeO}
	cycleIdx := 0
	openMs := 120 // how long each open-mouth shape lasts
	closeMs := 60 // how long the mouth stays closed between shapes

	send := func(v VisemeName, w float64) {
		select {
		case sm.visemes <- VisemeEvent{Type: "viseme", Viseme: v, Weight: w}:
		default:
		}
	}

	// Start with the mouth open immediately.
	send(cycle[cycleIdx], 1.0)
	cycleIdx = (cycleIdx + 1) % len(cycle)
	phaseIsOpen := true
	phaseStart := time.Now()

	for player.IsPlaying() {
		select {
		case <-cancel:
			slog.Debug("statemachine_speakWithCancel_state:_playback_interrupted_by_user")
			player.Pause()
			return
		default:
		}

		if err := player.Err(); err != nil {
			slog.Error("statemachine_speakWithCancel_state:_audio_play_error", "error", err)
			return
		}

		elapsed := time.Since(phaseStart).Milliseconds()

		if phaseIsOpen {
			if elapsed >= int64(openMs) {
				// Mouth was open — now close it.
				send(VisemeRest, 0)
				phaseIsOpen = false
				phaseStart = time.Now()
			}
		} else {
			if elapsed >= int64(closeMs) {
				// Mouth was closed — open with next shape.
				send(cycle[cycleIdx], 1.0)
				cycleIdx = (cycleIdx + 1) % len(cycle)
				phaseIsOpen = true
				phaseStart = time.Now()
			}
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
		slog.Error("statemachine_recordWithVAD_state:_recording_failed", "error", err)
		return nil
	}
	// The recorder is persistent — Start() returns a fresh subscriber
	// channel each time. We don't call Stop() here; the WASAPI session
	// stays alive across recordings.

	const (
		speechThreshold = 0.01                    // RMS above this counts as speech
		silenceDuration = 1200 * time.Millisecond // silence to end the turn
		maxDuration     = 30 * time.Second        // hard safety cap
	)

	var all []float32
	speaking := false
	lastSpeech := time.Now()
	start := time.Now()

	for {
		select {
		case <-cancel:
			slog.Debug("statemachine_recordWithVAD_state:_recording_canceled")
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
					slog.Debug("statemachine_recordWithVAD_state:_speech_started", "rms", rms)
					speaking = true
				}
				lastSpeech = time.Now()
			}

			// Stop when speech started and silence persisted long enough.
			if speaking && time.Since(lastSpeech) >= silenceDuration {
				slog.Debug("statemachine_recordWithVAD_state:_silence_detected,_stopping_recording")
				return all
			}

			// Hard safety cap.
			if time.Since(start) >= maxDuration {
				slog.Debug("statemachine_recordWithVAD_state:_max_recording_duration_reached")
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
