//go:build offline

package asr

import (
	"fmt"

	"github.com/liuyngchng/avatar-desktop-x64/internal/config"
)

// Mode returns a human-readable build mode label: "在线" or "离线".
func Mode() string { return "离线" }

// Desc returns a human-readable description of the ASR backend.
func Desc(cfg *config.Cfg) string {
	_ = cfg
	return "离线 ASR: sherpa-onnx SenseVoice (本地模型)"
}

// initASR creates the offline ASR engine (SenseVoiceSmall).
// cfg.yml's asr section is ignored; the model is loaded from models/asr
// relative to the executable.
func initASR(cfg *config.Cfg) (Transcriber, error) {
	// cfg is unused — model path is determined by ModelsDir().
	_ = cfg
	return NewOfflineEngine("")
}