//go:build !offline

package tts

import (
	"fmt"

	"github.com/liuyngchng/avatar-desktop-x64/internal/config"
)

// initTTS creates the online TTS client from cfg.yml.
// The online build requires tts.url to be configured; a missing URL is a
// hard error rather than a silent fallback.
func initTTS(cfg *config.Cfg) (Synthesizer, error) {
	if cfg.TTS.URL == "" {
		return nil, fmt.Errorf("tts: online build requires cfg.yml tts.url (build with -tags offline for local TTS)")
	}
	return NewClient(cfg.TTS.URL, cfg.TTS.Model, cfg.TTS.Voice, cfg.APIKey, cfg.TTS.Format, cfg.TTS.SampleRate, config.ProxyFunc(cfg.Proxy)), nil
}
