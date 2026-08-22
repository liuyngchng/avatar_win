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

// Load reads cfg.yml from the same directory as the executable.
//
// If the file is not found, it returns (nil, nil) — the caller should
// treat this as "no API configuration available" and run with the
// renderer only (avatar visible but cannot talk).
//
// If the file is found but cannot be parsed, an error is returned.
func Load() (*Cfg, error) {
	path := filepath.Join(workDir(), "cfg.yml")
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

func workDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}