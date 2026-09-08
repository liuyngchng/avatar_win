// Package brain contains the state machine and the digital human's
// "mind": mode FSM, emotion mapping, and viseme generation.
//
// It is the Go port of the iOS RobotViewModel / Android robot package,
// minus the camera face-tracking (not needed for a big-screen avatar).
package brain

import (
	"encoding/json"
	"strings"
)

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
	// ModeAwaitingUser: avatar just finished speaking and is in the
	// multi-turn conversation window — waiting for the user to start
	// talking again. If no speech arrives within the configured timeout,
	// the state machine returns to ModeIdle and resumes wake-word
	// listening.
	ModeAwaitingUser
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
	case ModeAwaitingUser:
		return "awaiting_user"
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
	EmotionAngry     Emotion = "angry"
	EmotionSad       Emotion = "sad"
	EmotionSurprised Emotion = "surprised"
	EmotionRelaxed   Emotion = "relaxed"
)

// State is the current state of the digital human, consumed by the UI.
type State struct {
	Mode                   Mode    `json:"mode"`
	Emotion                Emotion `json:"emotion"`
	IsSpeaking             bool    `json:"isSpeaking"`
	LastUserText           string  `json:"lastUserText,omitempty"`
	ResponseText           string  `json:"responseText,omitempty"`
	IdleAnimationsEnabled  bool    `json:"idleAnimationsEnabled"`
}

// EmotionFromString converts a string to an Emotion enum.
func EmotionFromString(s string) Emotion {
	switch strings.ToLower(s) {
	case "happy":
		return EmotionHappy
	case "angry":
		return EmotionAngry
	case "sad":
		return EmotionSad
	case "surprised":
		return EmotionSurprised
	case "relaxed":
		return EmotionRelaxed
	default:
		return EmotionNeutral
	}
}
