// Package tts provides text-to-speech. Text can be synthesized either via
// the Alibaba Cloud DashScope Qwen-TTS Realtime WebSocket API (Client,
// online) or via a local sherpa-onnx Matcha-TTS model (Engine, offline).
// The choice is made at runtime via cfg.yml's tts.mode field.
package tts

import (
	"fmt"

	"github.com/liuyngchng/avatar-desktop-x64/internal/config"
)

// Mode returns the TTS mode configured in cfg.yml, or "online" if unset.
func Mode(cfg *config.Cfg) string {
	if cfg != nil && cfg.TTS.Mode == "offline" {
		return "离线"
	}
	return "在线"
}

// Desc returns a human-readable description of the TTS backend.
func Desc(cfg *config.Cfg) string {
	if Mode(cfg) == "离线" {
		return "离线 TTS: sherpa-onnx Matcha-TTS + vocos (本地模型)"
	}
	if cfg == nil || cfg.TTS.URL == "" {
		return "在线 TTS (未配置)"
	}
	return fmt.Sprintf("在线 TTS: 阿里云 DashScope (model=%s, voice=%s)", cfg.TTS.Model, cfg.TTS.Voice)
}

// Init creates a TTS Synthesizer from the given configuration.
// If cfg.tts.mode == "offline", a local sherpa-onnx Matcha-TTS engine
// is loaded. Otherwise the DashScope WebSocket client is created.
func Init(cfg *config.Cfg) (Synthesizer, error) {
	if cfg == nil {
		return nil, fmt.Errorf("tts: nil config")
	}

	if cfg.TTS.Mode == "offline" {
		return NewOfflineEngine("")
	}

	if cfg.TTS.URL == "" {
		return nil, fmt.Errorf("tts: online mode requires cfg.yml tts.url (set tts.mode=offline for local TTS)")
	}
	return NewClient(cfg.TTS.URL, cfg.TTS.Model, cfg.TTS.Voice, cfg.APIKey, cfg.TTS.Format, cfg.TTS.SampleRate, config.ProxyFunc(cfg.Proxy, cfg.ProxyDisabled)), nil
}