// Package asr provides automatic speech recognition. Speech can be
// transcribed either via the Alibaba Cloud DashScope WebSocket API
// (Client, online) or via a local sherpa-onnx SenseVoiceSmall model
// (Engine, offline). The choice is made at runtime via cfg.yml's
// asr.mode field ("online" or "offline").
package asr

import (
	"fmt"

	"github.com/liuyngchng/avatar-desktop-x64/internal/config"
)

// Mode returns the ASR mode configured in cfg.yml, or "online" if unset.
func Mode(cfg *config.Cfg) string {
	if cfg != nil && cfg.ASR.Mode == "offline" {
		return "离线"
	}
	return "在线"
}

// Desc returns a human-readable description of the ASR backend.
func Desc(cfg *config.Cfg) string {
	if Mode(cfg) == "离线" {
		return "离线 ASR: sherpa-onnx SenseVoice (本地模型)"
	}
	if cfg == nil || cfg.ASR.URL == "" {
		return "在线 ASR (未配置)"
	}
	return fmt.Sprintf("在线 ASR: 阿里云 DashScope (model=%s)", cfg.ASR.Model)
}

// Init creates an ASR Transcriber from the given configuration.
// If cfg.asr.mode == "offline", a local sherpa-onnx SenseVoiceSmall engine
// is loaded. Otherwise the DashScope WebSocket client is created.
func Init(cfg *config.Cfg) (Transcriber, error) {
	if cfg == nil {
		return nil, fmt.Errorf("asr: nil config")
	}

	if cfg.ASR.Mode == "offline" {
		return NewOfflineEngine("")
	}

	if cfg.ASR.URL == "" {
		return nil, fmt.Errorf("asr: online mode requires cfg.yml asr.url (set asr.mode=offline for local ASR)")
	}
	return NewClient(cfg.ASR.URL, cfg.ASR.Model, cfg.APIKey, cfg.ASR.Format, cfg.ASR.SampleRate, config.ProxyFunc(cfg.Proxy, cfg.ProxyDisabled)), nil
}
