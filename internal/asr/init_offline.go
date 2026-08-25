//go:build offline

package asr

import (
	"github.com/liuyngchng/avatar-pc/internal/config"
)

// initASR creates the offline ASR engine (SenseVoiceSmall).
// cfg.yml's asr section is ignored; the model is loaded from models/asr
// relative to the executable.
func initASR(cfg *config.Cfg) (Transcriber, error) {
	// cfg is unused — model path is determined by ModelsDir().
	_ = cfg
	return NewOfflineEngine("")
}