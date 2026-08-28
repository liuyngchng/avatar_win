//go:build offline

package tts

import (
	"fmt"

	"github.com/liuyngchng/avatar-desktop-x64/internal/config"
)

// Mode returns a human-readable build mode label: "在线" or "离线".
func Mode() string { return "离线" }

// Desc returns a human-readable description of the TTS backend.
func Desc(cfg *config.Cfg) string {
	_ = cfg
	return "离线 TTS: sherpa-onnx Matcha-TTS + vocos (本地模型)"
}

// initTTS creates the offline TTS engine (Matcha-TTS + vocos).
// cfg.yml's tts section is ignored; the model is loaded from models/tts
// relative to the executable.
func initTTS(cfg *config.Cfg) (Synthesizer, error) {
	_ = cfg
	return NewOfflineEngine("")
}