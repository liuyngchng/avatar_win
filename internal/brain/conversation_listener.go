package brain

import (
	"log/slog"
	"time"
)

// conversationListener runs while the avatar is in the multi-turn
// conversation window (ModeAwaitingUser). It does pure voice-activity
// detection on the recorder stream — no ASR — and starts a new pipeline
// turn as soon as the user starts speaking. If the configured idle
// timeout passes with no speech, the listener ends the conversation
// window by clearing sm.inConversation and returning to ModeIdle so the
// wake-word detector can take over again.
//
// Like wakeWordDetector, exactly one instance is allowed at a time.
// The owning StateMachine guards the field with sm.mu and removes it
// before sending a speech event, so re-entry is safe.
type conversationListener struct {
	sm   *StateMachine
	done chan struct{}
}

// startConversationListenerLocked begins a conversation listener. No-op
// if there is already one running or if the recorder isn't available.
// Caller must hold sm.mu.
func (sm *StateMachine) startConversationListenerLocked() {
	if sm.recorder == nil {
		return
	}
	if sm.conversationListener != nil {
		return
	}

	l := &conversationListener{
		sm:   sm,
		done: make(chan struct{}),
	}
	sm.conversationListener = l

	go l.run()
}

// cancelConversationListener stops the currently running listener, if
// any. Safe to call when no listener is running.
func (sm *StateMachine) cancelConversationListener() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.cancelConversationListenerLocked()
}

// cancelConversationListenerLocked stops the currently running listener.
// Caller must hold sm.mu.
func (sm *StateMachine) cancelConversationListenerLocked() {
	if sm.conversationListener != nil {
		sm.conversationListener.stop()
		sm.conversationListener = nil
	}
}

func (l *conversationListener) stop() {
	select {
	case <-l.done:
		// already stopped
	default:
		close(l.done)
	}
}

// run is the main loop of the conversation listener. It arms a timer
// for the configured idle window, then watches the recorder stream for
// speech. The first branch to fire wins:
//
//   - timer.C       → idle window expired: end the conversation.
//   - speech stable → clear the listener slot under sm.mu, then enqueue
//                     a speech_detected event so handleEvent runs the
//                     normal pipeline-start path.
//   - l.done        → the listener was cancelled (e.g. user tapped).
func (l *conversationListener) run() {
	idle := l.sm.conversationIdle
	slog.Info("conversation: opened multi-turn window", "idle_ms", idle.Milliseconds())

	idleTimer := time.NewTimer(idle)
	defer idleTimer.Stop()

	chunks, err := l.sm.recorder.Start()
	if err != nil {
		slog.Error("conversation: recorder start failed", "error", err)
		l.cleanup()
		return
	}

	const (
		speechThreshold = 0.01
		// Require a brief stretch of continuous speech before we trigger
		// a new turn — avoids latching onto a single noisy click.
		speechMinDuration = 250 * time.Millisecond
	)

	var speaking bool
	var speechStart time.Time

	for {
		select {
		case <-l.done:
			slog.Debug("conversation: cancelled")
			return

		case <-idleTimer.C:
			slog.Info("conversation: idle window expired, closing")
			l.endConversation()
			return

		case chunk, ok := <-chunks:
			if !ok {
				// Recorder stopped — nothing useful left to do.
				return
			}

			rms := rmsOf(chunk)
			if rms > speechThreshold {
				if !speaking {
					speaking = true
					speechStart = time.Now()
					slog.Debug("conversation: speech started", "rms", rms)
					continue
				}
				if time.Since(speechStart) >= speechMinDuration {
					slog.Info("conversation: speech stable, starting new turn")
					l.triggerNewTurn()
					return
				}
			} else {
				// Below threshold — drop any in-progress speech detection
				// so a brief click or environmental noise doesn't accumulate.
				speaking = false
			}
		}
	}
}

// triggerNewTurn clears the listener slot and enqueues a speech_detected
// event on the state machine so the existing handleEvent path starts a
// new pipeline turn. The state machine is responsible for keeping
// inConversation true across the new turn.
func (l *conversationListener) triggerNewTurn() {
	l.sm.mu.Lock()
	if l.sm.conversationListener == l {
		l.sm.conversationListener = nil
	}
	l.sm.mu.Unlock()

	select {
	case l.sm.events <- Event{Type: "speech_detected"}:
	default:
		slog.Warn("conversation: events channel full, dropping speech_detected")
	}
}

// endConversation clears inConversation, returns the FSM to idle, and
// re-arms the wake word detector. All transitions happen under sm.mu.
func (l *conversationListener) endConversation() {
	l.sm.mu.Lock()
	if l.sm.conversationListener == l {
		l.sm.conversationListener = nil
	}
	if l.sm.state.Mode == ModeAwaitingUser {
		l.sm.setStateLocked(ModeIdle, EmotionNeutral, "")
		l.sm.inConversation = false
		l.sm.startWakeWordDetectorLocked()
	}
	l.sm.mu.Unlock()
	l.sm.emit()
}

// cleanup is used when run() exits without a meaningful terminal action
// (e.g. the recorder failed to start). It clears the listener slot so a
// later call to startConversationListenerLocked can succeed.
func (l *conversationListener) cleanup() {
	l.sm.mu.Lock()
	if l.sm.conversationListener == l {
		l.sm.conversationListener = nil
	}
	l.sm.mu.Unlock()
}