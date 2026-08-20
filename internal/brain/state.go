// Package brain contains the state machine and the digital human's
// "mind": mode FSM, emotion mapping, and viseme generation.
//
// It is the Go port of the iOS RobotViewModel / Android robot package,
// minus the camera face-tracking (not needed for a big-screen avatar).
package brain

import "encoding/json"

// Mode is the top-level behavior state (FSM).
type Mode int

const (
	// ModeIdle: waiting for interaction. Eyes wander, occasional blinks.
	ModeIdle Mode = iota
	// ModeListening: user tapped or said wake word — waiting for speech.
	ModeListening
	// ModeSpeaking: TTS active, mouth animates via visemes.
	ModeSpeaking
	// ModeThinking: processing request (waiting on LLM).
	ModeThinking
)

func (m Mode) String() string {
	switch m {
	case ModeIdle:
		return "idle"
	case ModeListening:
		return "listening"
	case ModeSpeaking:
		return "speaking"
	case ModeThinking:
		return "thinking"
	}
	return "unknown"
}

// MarshalJSON encodes Mode as a string (e.g. "idle") so the JS frontend
// can match it in a switch statement.
func (m Mode) MarshalJSON() ([]byte, error) {
	return json.Marshal(m.String())
}

// Emotion drives the digital human's expression.
type Emotion string

const (
	EmotionNeutral   Emotion = "neutral"
	EmotionHappy     Emotion = "happy"
	EmotionCurious   Emotion = "curious"
	EmotionSurprised Emotion = "surprised"
	EmotionShy       Emotion = "shy"
	EmotionSleepy    Emotion = "sleepy"
	EmotionSad       Emotion = "sad"
)

// State is the current state of the digital human, consumed by the UI.
type State struct {
	Mode         Mode    `json:"mode"`
	Emotion      Emotion `json:"emotion"`
	IsSpeaking   bool    `json:"isSpeaking"`
	LastUserText string  `json:"lastUserText,omitempty"`
	ResponseText string  `json:"responseText,omitempty"`
}
