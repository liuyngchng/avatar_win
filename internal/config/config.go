// Package config reads the Avatar PC YAML configuration file.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Cfg holds all API configuration for the avatar services.
type Cfg struct {
	ASR    ASRConfig    `yaml:"asr"`
	LLM    LLMConfig    `yaml:"llm"`
	TTS    TTSConfig    `yaml:"tts"`
	APIKey string       `yaml:"api_key"`
	Avatar AvatarConfig `yaml:"avatar"`
}

// ASRConfig holds the speech recognition configuration.
type ASRConfig struct {
	URL        string `yaml:"url"`
	Model      string `yaml:"model"`
	Format     string `yaml:"format"`
	SampleRate int    `yaml:"sample_rate"`
}

// LLMConfig holds the chat model endpoint and model name.
type LLMConfig struct {
	URL   string `yaml:"url"`
	Model string `yaml:"model"`
	Name  string `yaml:"name"`
}

// TTSConfig holds the text-to-speech configuration.
type TTSConfig struct {
	URL        string `yaml:"url"`
	Model      string `yaml:"model"`
	Voice      string `yaml:"voice"`
	Format     string `yaml:"format"`
	SampleRate int    `yaml:"sample_rate"`
}

// AvatarConfig holds the digital human's behavior configuration.
type AvatarConfig struct {
	// IdleAnimationsEnabled controls whether the avatar randomly plays
	// procedural idle animations (e.g. "Bored", "Cross Jumps") while
	// waiting for interaction. When false, idle only shows breathing and
	// subtle micro-movement.
	//
	// A pointer is used so that an absent key defaults to enabled (true)
	// rather than Go's zero value (false).
	IdleAnimationsEnabled *bool `yaml:"idle_animations_enabled"`
}

// IdleAnimations reports whether idle animations are enabled, defaulting
// to true when the key is absent from cfg.yml.
func (a AvatarConfig) IdleAnimations() bool {
	return a.IdleAnimationsEnabled == nil || *a.IdleAnimationsEnabled
}

// Load reads cfg.yml from the same directory as the executable.
//
// If the file is not found, it returns (nil, nil) — the caller should
// treat this as "no API configuration available" and run with the
// renderer only (avatar visible but cannot talk).
//
// If the file is found but cannot be parsed, an error is returned.
func Load() (*Cfg, error) {
	path := filepath.Join(exeDir(), "cfg.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil // file not found — not an error, just no config
	}

	var cfg Cfg
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return &cfg, nil
}

func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}