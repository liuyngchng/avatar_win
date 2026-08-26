package brain

import (
	"log"
	"strings"
	"time"
)

// wakeWordDetector continuously listens for the wake word in background audio
// and fires wake_detected events when the user says the wake word. It runs in
// its own goroutine and is managed by the state machine.
//
// When the wake word is detected, the detector stops itself and the state
// machine starts a full conversation pipeline. If the user said additional
// words after the wake word (e.g. "小冉，今天天气怎么样？"), those words are
// passed through so the pipeline can skip recording + ASR and go straight to
// the LLM.
type wakeWordDetector struct {
	sm       *StateMachine
	wakeWord string
	done     chan struct{}
}

// startWakeWordDetector begins listening for the wake word. It is a no-op if
// no API clients are available (display-only mode). Caller must hold sm.mu.
func (sm *StateMachine) startWakeWordDetectorLocked() {
	if sm.asrClient == nil || sm.recorder == nil {
		return
	}
	// Already running — don't start a second one.
	if sm.wakeDetector != nil {
		return
	}

	wakeWord := "小冉"
	if sm.wakeWordConfig != "" {
		wakeWord = sm.wakeWordConfig
	}

	d := &wakeWordDetector{
		sm:       sm,
		wakeWord: wakeWord,
		done:     make(chan struct{}),
	}
	sm.wakeDetector = d

	go d.run()
}

// cancelWakeDetector stops the currently running wake word detector, if any.
// Safe to call when no detector is running.
func (sm *StateMachine) cancelWakeDetector() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.cancelWakeDetectorLocked()
}

// cancelWakeDetectorLocked stops the currently running wake word detector.
// Caller must hold sm.mu.
func (sm *StateMachine) cancelWakeDetectorLocked() {
	if sm.wakeDetector != nil {
		sm.wakeDetector.stop()
		sm.wakeDetector = nil
	}
}

func (d *wakeWordDetector) stop() {
	select {
	case <-d.done:
		// already stopped
	default:
		close(d.done)
	}
}

func (d *wakeWordDetector) run() {
	log.Printf("wakeword: listening for %q...", d.wakeWord)

	for {
		select {
		case <-d.done:
			log.Printf("wakeword: stopped")
			return
		default:
		}

		// Listen for speech via VAD.
		samples := d.listenForSpeech()
		if len(samples) == 0 {
			continue
		}

		// Run ASR on the captured audio to check for the wake word.
		text, err := d.sm.asrClient.Transcribe(samples, recorderSampleRate)
		if err != nil {
			log.Printf("wakeword: ASR failed: %v", err)
			continue
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}

		log.Printf("wakeword: heard %q", text)

		// Check if the text contains the wake word.
		if containsWakeWord(text, d.wakeWord) {
			log.Printf("wakeword: WAKE WORD DETECTED in %q", text)

			remainder := extractAfterWakeWord(text, d.wakeWord)

			ev := Event{Type: "wake_detected"}
			if remainder != "" {
				ev.Data = remainder
			}

			// Mark this detector as done before sending the event so the
			// state machine doesn't try to cancel it again.
			d.sm.mu.Lock()
			d.sm.wakeDetector = nil
			d.sm.mu.Unlock()

			select {
			case d.sm.events <- ev:
			default:
				log.Printf("wakeword: event channel full, dropping wake_detected")
			}
			return
		}
	}
}

// listenForSpeech captures audio from the recorder when VAD detects speech.
// It returns the captured samples, or nil if cancelled or no speech was found.
func (d *wakeWordDetector) listenForSpeech() []float32 {
	chunks, err := d.sm.recorder.Start()
	if err != nil {
		log.Printf("wakeword: recorder start failed: %v", err)
		return nil
	}

	const (
		speechThreshold = 0.01
		silenceDuration = 800 * time.Millisecond // shorter silence gap for wake word
		maxDuration     = 5 * time.Second        // wake word + short command
	)

	var all []float32
	speaking := false
	lastSpeech := time.Now()
	start := time.Now()

	for {
		select {
		case <-d.done:
			return all
		case chunk, ok := <-chunks:
			if !ok {
				if !speaking {
					return nil
				}
				return all
			}

			all = append(all, chunk...)

			rms := rmsOf(chunk)
			if rms > speechThreshold {
				if !speaking {
					log.Printf("wakeword: speech detected (rms=%.4f)", rms)
					speaking = true
				}
				lastSpeech = time.Now()
			}

			if speaking && time.Since(lastSpeech) >= silenceDuration {
				log.Printf("wakeword: silence detected, captured %d samples (%.1fs)",
					len(all), float64(len(all))/recorderSampleRate)
				return all
			}

			if time.Since(start) >= maxDuration {
				log.Printf("wakeword: max duration reached (%.1fs)", maxDuration.Seconds())
				return all
			}
		}
	}
}

// containsWakeWord checks whether the wake word appears in the ASR text.
func containsWakeWord(text, wakeWord string) bool {
	return strings.Contains(text, wakeWord)
}

// extractAfterWakeWord returns the text after the wake word, trimmed.
// Common punctuation immediately after the wake word is stripped.
// Example: "小冉，今天天气怎么样？" → "今天天气怎么样？"
func extractAfterWakeWord(text, wakeWord string) string {
	idx := strings.Index(text, wakeWord)
	if idx < 0 {
		return ""
	}
	remainder := text[idx+len(wakeWord):]
	remainder = strings.TrimLeft(remainder, "，,。！？!?、 ")
	return strings.TrimSpace(remainder)
}