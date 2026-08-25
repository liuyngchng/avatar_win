//go:build offline

package tts

import (
	"github.com/liuyngchng/avatar-pc/internal/config"
)

// initTTS creates the offline TTS engine (Matcha-TTS + vocos).
// cfg.yml's tts section is ignored; the model is loaded from models/tts
// relative to the executable.
func initTTS(cfg *config.Cfg) (Synthesizer, error) {
	_ = cfg
	return NewOfflineEngine("")
}