//go:build !offline

package asr

import (
	"fmt"

	"github.com/liuyngchng/avatar-desktop-x64/internal/config"
)

// initASR creates the online ASR client from cfg.yml.
// The online build requires asr.url to be configured; a missing URL is a
// hard error rather than a silent fallback.
func initASR(cfg *config.Cfg) (Transcriber, error) {
	if cfg.ASR.URL == "" {
		return nil, fmt.Errorf("asr: online build requires cfg.yml asr.url (build with -tags offline for local ASR)")
	}
	return NewClient(cfg.ASR.URL, cfg.ASR.Model, cfg.APIKey, cfg.ASR.Format, cfg.ASR.SampleRate), nil
}