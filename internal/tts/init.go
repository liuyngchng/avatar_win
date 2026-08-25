// Package tts provides text-to-speech.
//
// Build tag control:
//   - Default (no tag): online TTS via DashScope API. cfg.yml's tts.url
//     is required; if missing Init returns an error.
//   - -tags offline: offline TTS via local sherpa-onnx Matcha-TTS. cfg.yml's
//     tts section is ignored; models are loaded from models/tts.
package tts

import (
	"github.com/liuyngchng/avatar-pc/internal/config"
)

// Init creates a TTS Synthesizer from the given configuration.
// In the online build it connects to the DashScope API; in the offline
// build it loads the local Matcha-TTS model.
func Init(cfg *config.Cfg) (Synthesizer, error) {
	return initTTS(cfg)
}