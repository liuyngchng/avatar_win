//go:build !offline

package asr

import (
	"fmt"

	"github.com/liuyngchng/avatar-desktop-x64/internal/config"
)

// Mode returns a human-readable build mode label: "在线" or "离线".
func Mode() string { return "在线" }

// Desc returns a human-readable description of the ASR backend.
func Desc(cfg *config.Cfg) string {
	if cfg == nil || cfg.ASR.URL == "" {
		return "在线 ASR (未配置)"
	}
	return fmt.Sprintf("在线 ASR: 阿里云 DashScope (model=%s)", cfg.ASR.Model)
}

// initASR creates the online ASR client from cfg.yml.
// The online build requires asr.url to be configured; a missing URL is a
// hard error rather than a silent fallback.
func initASR(cfg *config.Cfg) (Transcriber, error) {
	if cfg.ASR.URL == "" {
		return nil, fmt.Errorf("asr: online build requires cfg.yml asr.url (build with -tags offline for local ASR)")
	}
	return NewClient(cfg.ASR.URL, cfg.ASR.Model, cfg.APIKey, cfg.ASR.Format, cfg.ASR.SampleRate, config.ProxyFunc(cfg.Proxy, cfg.ProxyDisabled)), nil
}
