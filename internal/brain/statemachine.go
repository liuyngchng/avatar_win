package brain

import (
	"log"
	"math"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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
// record → ASR → LLM (stream) + TTS (overlap) → play + viseme → idle.
//
// LLM and TTS run concurrently: as soon as a complete sentence arrives from
// the streaming LLM, it is sent to TTS while the LLM continues generating
// the next sentence. This cuts the LLM→TTS serial wait time significantly.
//
// gen is the generation number at the time this pipeline was created.
// If a newer generation exists (the user tapped again), this pipeline
// aborts early.
func (sm *StateMachine) pipeline(gen int64) {
	t0 := time.Now() // ⏱ pipeline start (user trigger)

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
	tRecStart := time.Now() // ⏱ recording start
	samples := sm.recordWithVAD(cancel)
	tRecEnd := time.Now() // ⏱ recording end
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
	log.Printf("⏱ [timing] recording: %dms (total elapsed: %dms)", tRecEnd.Sub(tRecStart).Milliseconds(), tRecEnd.Sub(t0).Milliseconds())

	// 2. ASR.
	sm.setState(ModeThinking, EmotionNeutral, "")
	sm.emit()

	tASRStart := time.Now() // ⏱ ASR start
	userText, err := sm.asrClient.Transcribe(samples, recorderSampleRate)
	tASREnd := time.Now() // ⏱ ASR end
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
	log.Printf("⏱ [timing] ASR: %dms (total elapsed: %dms)", tASREnd.Sub(tASRStart).Milliseconds(), tASREnd.Sub(t0).Milliseconds())

	// Re-emit so the frontend shows the recognized text during thinking.
	sm.emit()

	if canceled() {
		log.Printf("state: canceled before LLM (gen=%d)", gen)
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
				log.Printf("state: TTS failed for sentence %q: %v", sentence, err)
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
			log.Printf("state: canceled during LLM (gen=%d)", gen)
			return
		}
	}

	// Flush any remaining text as the last sentence.
	// Only send if it has meaningful content (>1 rune, not just whitespace/punct).
	remaining := strings.TrimSpace(sb.String())
	if utf8.RuneCountInString(remaining) >= 2 {
		sentenceCh <- remaining
	} else {
		log.Printf("state: discarding short sentence tail %q (%d runes)", remaining, utf8.RuneCountInString(remaining))
	}

	// Close sentenceCh so the TTS goroutine finishes.
	close(sentenceCh)
	<-ttsDone

	// Check for TTS errors.
	select {
	case err := <-ttsErrs:
		log.Printf("state: TTS failed: %v", err)
		sm.setState(ModeIdle, EmotionNeutral, "")
		sm.emit()
		return
	default:
	}

	if canceled() {
		log.Printf("state: canceled after TTS (gen=%d)", gen)
		return
	}

	if len(allSamples) == 0 {
		log.Printf("state: TTS produced no audio")
		sm.setState(ModeIdle, EmotionNeutral, "")
		sm.emit()
		return
	}

	// Reconstruct the full reply text for state display.
	replyText := strings.Join(allSentences, "")
	sm.setState(ModeThinking, EmotionHappy, replyText)

	// LLM timing: first_token = when we got the first chunk, last_token = when the stream ended.
	// TTS timing: from first sentence sent to TTS until last sentence TTS completed.
	tTTSEnd := time.Now()
	tLLMEnd := tLLMLastToken

	if llmFirstTokenSet {
		log.Printf("⏱ [timing] LLM: first_token=%dms, stream_done=%dms (total elapsed: %dms)",
			tLLMFirstToken.Sub(tLLMStart).Milliseconds(),
			tLLMEnd.Sub(tLLMStart).Milliseconds(),
			tLLMEnd.Sub(t0).Milliseconds())
	} else {
		log.Printf("⏱ [timing] LLM: stream_done=%dms (total elapsed: %dms)", tLLMEnd.Sub(tLLMStart).Milliseconds(), tLLMEnd.Sub(t0).Milliseconds())
	}

	log.Printf("⏱ [timing] TTS: %dms (overlap with LLM, total elapsed: %dms)", tTTSEnd.Sub(tTTSStart).Milliseconds(), tTTSEnd.Sub(t0).Milliseconds())

	// 4. Speak — drive mouth visemes on a fixed rhythm while audio plays.
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

	tPlayStart := time.Now() // ⏱ playback start
	player, err := sm.audioPlayer.Play(allSamples)
	if err != nil {
		log.Printf("state: audio play error: %v", err)
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
	log.Printf("⏱ [timing] playback: %dms | TOTAL pipeline: %dms (rec=%.1f%%, asr=%.1f%%, llm+tts overlap=%.1f%%, play=%.1f%%)",
		tPlayEnd.Sub(tPlayStart).Milliseconds(),
		tPlayEnd.Sub(t0).Milliseconds(),
		float64(tRecEnd.Sub(tRecStart).Milliseconds())/float64(tPlayEnd.Sub(t0).Milliseconds())*100,
		float64(tASREnd.Sub(tASRStart).Milliseconds())/float64(tPlayEnd.Sub(t0).Milliseconds())*100,
		float64(overlapEnd.Sub(tLLMStart).Milliseconds())/float64(tPlayEnd.Sub(t0).Milliseconds())*100,
		float64(tPlayEnd.Sub(tPlayStart).Milliseconds())/float64(tPlayEnd.Sub(t0).Milliseconds())*100,
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
	openMs := 120  // how long each open-mouth shape lasts
	closeMs := 60  // how long the mouth stays closed between shapes

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
			log.Printf("state: playback interrupted by user")
			player.Pause()
			return
		default:
		}

		if err := player.Err(); err != nil {
			log.Printf("state: audio play error: %v", err)
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
		log.Printf("state: recording failed: %v", err)
		return nil
	}
	// The recorder is persistent — Start() returns a fresh subscriber
	// channel each time. We don't call Stop() here; the WASAPI session
	// stays alive across recordings.

	const (
		speechThreshold = 0.01                 // RMS above this counts as speech
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