// Package asr provides automatic speech recognition.
//
// Build tag control:
//   - Default (no tag): online ASR via DashScope API. cfg.yml's asr.url
//     is required; if missing Init returns an error.
//   - -tags offline: offline ASR via local sherpa-onnx SenseVoiceSmall.
//     cfg.yml's asr section is ignored; models are loaded from models/asr.
package asr

import (
	"github.com/liuyngchng/avatar-desktop-x64/internal/config"
)

// Init creates an ASR Transcriber from the given configuration.
// In the online build it connects to the DashScope API; in the offline
// build it loads the local SenseVoiceSmall model.
func Init(cfg *config.Cfg) (Transcriber, error) {
	return initASR(cfg)
}