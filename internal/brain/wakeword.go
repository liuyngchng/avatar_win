package brain

import (
	"log/slog"
	"strings"
	"time"
)

// wakeWordDetector continuously listens for the wake word in background audio
// and fires wake_detected events when the user says the wake word. It runs in
// its own goroutine and is managed by the state machine.
//
// When the wake word is detected, the detector stops itself and the state
// machine starts a full conversation pipeline. If the user said additional
// words after the wake word (e.g. "小然，今天天气怎么样？"), those words are
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

	wakeWord := "小然"
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
	slog.Info("wakeword: listening", "wake_word", d.wakeWord)

	for {
		select {
		case <-d.done:
			slog.Info("wakeword: stopped")
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
			slog.Error("wakeword: ASR failed", "error", err)
			continue
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}

		slog.Debug("wakeword: heard", "text", text)

		// Check if the text contains the wake word.
		if containsWakeWord(text, d.wakeWord) {
			slog.Info("wakeword: WAKE WORD DETECTED", "text", text)

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
				slog.Warn("wakeword: event channel full, dropping wake_detected")
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
		slog.Error("wakeword: recorder start failed", "error", err)
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
					slog.Debug("wakeword: speech detected", "rms", rms)
					speaking = true
				}
				lastSpeech = time.Now()
			}

			if speaking && time.Since(lastSpeech) >= silenceDuration {
				slog.Debug("wakeword: silence detected", "samples", len(all), "seconds", float64(len(all))/recorderSampleRate)
				return all
			}

			if time.Since(start) >= maxDuration {
				slog.Debug("wakeword: max duration reached", "seconds", maxDuration.Seconds())
				return all
			}
		}
	}
}

// containsWakeWord checks whether the wake word appears in the ASR text.
func containsWakeWord(text, wakeWord string) bool {
	return strings.Contains(text, wakeWord)
}

// extractAfterWakeWord returns the command text after the wake word,
// trimmed.  A bare name call (no real command) yields "" so the leftover
// name is never sent to the LLM as a meaningless instruction.
//
// Rules, in order:
//  1. Strip leading punctuation/spaces right after the name
//     ("小然，今天..." → "今天...").
//  2. If nothing remains → bare call → "" ("小然").
//  3. If the tail is only vocative particles → bare call → ""
//     ("小然呀" / "小然啊" / "小然嘛"...).
//  4. If the tail repeats the name → bare call → "" ("小然小然。"),
//     unless a real command follows ("小然，小然是谁" stays intact).
//  5. Otherwise return the tail as the command, trailing punctuation and
//     particles preserved ("小然今天天气怎么样？" → "今天天气怎么样？").
func extractAfterWakeWord(text, wakeWord string) string {
	idx := strings.Index(text, wakeWord)
	if idx < 0 {
		return ""
	}
	remainder := text[idx+len(wakeWord):]
	// 1. Strip address glue right after the name.
	remainder = strings.TrimLeft(remainder, "，,。！？!?、 ")
	// 2. Empty tail — bare call.
	if remainder == "" {
		return ""
	}
	// 3. Tail is only vocative particles — bare call.
	if strings.Trim(remainder, "啊呀哇哪呢吗吧哦噢嗯呃诶哈") == "" {
		return ""
	}
	// 4. Tail repeats the name with no command after it.
	if containsWakeWord(remainder, wakeWord) {
		return ""
	}
	// 5. Real command — keep it, including trailing punctuation.
	return strings.TrimSpace(remainder)
}