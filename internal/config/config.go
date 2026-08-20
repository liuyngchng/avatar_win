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
	URL   string `yaml:"url"`
	Model string `yaml:"model"`
}

// LLMConfig holds the chat model endpoint and model name.
type LLMConfig struct {
	URL   string `yaml:"url"`
	Model string `yaml:"model"`
}

// TTSConfig holds the text-to-speech configuration.
type TTSConfig struct {
	URL   string `yaml:"url"`
	Model string `yaml:"model"`
	Voice string `yaml:"voice"`
}

// Load reads cfg.yml from the same directory as the executable, or from
// the current working directory.
func Load() (*Cfg, error) {
	candidates := []string{
		filepath.Join(exeDir(), "cfg.yml"),
		"cfg.yml",
		filepath.Join("..", "cfg.yml"),
	}

	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var cfg Cfg
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("config: parse %s: %w", p, err)
		}
		return &cfg, nil
	}

	return nil, fmt.Errorf("config: cfg.yml not found in %v", candidates)
}

func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}