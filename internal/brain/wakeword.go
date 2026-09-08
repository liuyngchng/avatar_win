package brain

import (
	"log/slog"
	"strings"

	"github.com/liuyngchng/avatar-desktop-x64/internal/kws"
)

// wakeWordDetector continuously listens for the wake word using local KWS
// (Zipformer model) and fires wake_detected events when the wake word is
// detected. It runs in its own goroutine and is managed by the state machine.
//
// When the wake word is detected, the detector stops itself and the state
// machine starts a full conversation pipeline.
type wakeWordDetector struct {
	sm        *StateMachine
	kwsEngine *kws.Engine
	done      chan struct{}
}

// startWakeWordDetector begins listening for the wake word. It is a no-op if
// no KWS engine is available (tap-only mode). Caller must hold sm.mu.
func (sm *StateMachine) startWakeWordDetectorLocked() {
	if sm.kwsEngine == nil || sm.recorder == nil {
		return
	}
	// Already running — don't start a second one.
	if sm.wakeDetector != nil {
		return
	}

	d := &wakeWordDetector{
		sm:        sm,
		kwsEngine: sm.kwsEngine,
		done:      make(chan struct{}),
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

// run is the main loop of the wake word detector. It subscribes to the
// recorder's audio stream and feeds every chunk to the local KWS engine.
// When KWS detects the keyword, a wake_detected event is fired and the
// detector exits.
func (d *wakeWordDetector) run() {
	slog.Info("wakeword_run_wakeword:_kws_listening")

	// Subscribe to the recorder's audio stream. The recorder supports
	// multiple concurrent subscribers, so the pipeline can record
	// simultaneously without conflict.
	chunks, err := d.sm.recorder.Start()
	if err != nil {
		slog.Error("wakeword_run_wakeword:_recorder_start_failed", "error", err)
		return
	}

	for {
		select {
		case <-d.done:
			slog.Info("wakeword_run_wakeword:_stopped")
			return

		case chunk, ok := <-chunks:
			if !ok {
				slog.Info("wakeword_run_wakeword:_recorder_channel_closed")
				return
			}

			keyword := d.kwsEngine.ProcessSamples(chunk)
			if keyword != "" {
				slog.Info("wakeword_run_wakeword:_WAKE_WORD_DETECTED", "keyword", keyword)

				// Mark this detector as done before sending the event so the
				// state machine doesn't try to cancel it again.
				d.sm.mu.Lock()
				d.sm.wakeDetector = nil
				d.sm.mu.Unlock()

				select {
				case d.sm.events <- Event{Type: "wake_detected"}:
				default:
					slog.Warn("wakeword_run_wakeword:_event_channel_full,_dropping_wake_detected")
				}
				return
			}
		}
	}
}

// containsWakeWord checks whether the wake word appears in the ASR text.
// Kept for reference and testing; the KWS-based detector does not need it.
func containsWakeWord(text, wakeWord string) bool {
	return strings.Contains(text, wakeWord)
}

// extractAfterWakeWord returns the command text after the wake word,
// trimmed. Kept for reference and testing; the KWS-based detector does not
// transcribe the utterance, so this path is no longer used at runtime.
//
// A bare name call (no real command) yields "" so the leftover name is
// never sent to the LLM as a meaningless instruction.
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
