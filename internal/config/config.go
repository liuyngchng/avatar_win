// Package config reads the Avatar PC YAML configuration file.
package config

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Cfg holds all API configuration for the avatar services.
type Cfg struct {
	ASR           ASRConfig    `yaml:"asr"`
	LLM           LLMConfig    `yaml:"llm"`
	TTS           TTSConfig    `yaml:"tts"`
	APIKey        string       `yaml:"api_key"`
	Proxy         string       `yaml:"proxy"`
	ProxyDisabled bool         `yaml:"proxy_disabled"`
	WakeWord      string       `yaml:"wake_word"`
	Avatar        AvatarConfig `yaml:"avatar"`
	Log           LogConfig    `yaml:"log"`
}

// LogConfig holds the logging configuration.
type LogConfig struct {
	// Level sets the global log verbosity. One of "debug", "info",
	// "warn", "error". Unknown or empty values are treated as "info".
	Level string `yaml:"level"`
}

// ASRConfig holds the speech recognition configuration.
// Mode is "offline" (local sherpa-onnx SenseVoice) or "online" (DashScope API).
// Default is "online" for backward compatibility.
type ASRConfig struct {
	Mode       string `yaml:"mode"` // "offline" or "online" (default: "online")
	URL        string `yaml:"url"`
	Model      string `yaml:"model"`
	Format     string `yaml:"format"`
	SampleRate int    `yaml:"sample_rate"`
}

// LLMConfig holds the chat model endpoint and model name.
type LLMConfig struct {
	URL       string `yaml:"url"`
	Model     string `yaml:"model"`
	Name      string `yaml:"name"`
	MaxTokens int    `yaml:"max_tokens"`
}

// TTSConfig holds the text-to-speech configuration.
// Mode is "offline" (local sherpa-onnx Matcha-TTS) or "online" (DashScope API).
// Default is "online" for backward compatibility.
type TTSConfig struct {
	Mode       string `yaml:"mode"` // "offline" or "online" (default: "online")
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

	// ConversationIdleMs is the window (in milliseconds) the avatar waits
	// for the user to start speaking again after finishing a reply. While
	// the window is open, the avatar runs in multi-turn mode: VAD-detected
	// speech starts a new turn directly, without requiring the wake word.
	// If the window expires with no speech, the avatar returns to idle and
	// the wake word is required to start a new conversation.
	//
	// 0 or negative means "use the default" (3000ms).
	ConversationIdleMs int `yaml:"conversation_idle_ms"`
}

// IdleAnimations reports whether idle animations are enabled, defaulting
// to true when the key is absent from cfg.yml.
func (a AvatarConfig) IdleAnimations() bool {
	return a.IdleAnimationsEnabled == nil || *a.IdleAnimationsEnabled
}

// ConversationIdle returns the configured conversation idle timeout,
// falling back to 3 seconds when unset or non-positive.
func (a AvatarConfig) ConversationIdle() time.Duration {
	if a.ConversationIdleMs <= 0 {
		return 3 * time.Second
	}
	return time.Duration(a.ConversationIdleMs) * time.Millisecond
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

// ProxyFunc returns an http.Proxy function that respects the following
// priority, suitable for http.Transport and gorilla/websocket Dialer:
//
//  1. proxy_disabled: true — force direct connection, ignore env vars and cfg.
//  2. cfgProxy (cfg.yml proxy field) — if set, always use this proxy.
//  3. Environment variables (HTTPS_PROXY / HTTP_PROXY / NO_PROXY) —
//     standard Go ProxyFromEnvironment.
//  4. Direct connection — no proxy.
func ProxyFunc(cfgProxy string, disabled bool) func(*http.Request) (*url.URL, error) {
	if disabled {
		return func(*http.Request) (*url.URL, error) { return nil, nil }
	}
	if cfgProxy != "" {
		u, err := url.Parse(cfgProxy)
		if err == nil {
			return http.ProxyURL(u)
		}
	}
	return http.ProxyFromEnvironment
}

// ProxyDesc returns a human-readable description of the proxy state.
// Log this at startup so the user knows whether the app is using a proxy.
func ProxyDesc(cfgProxy string, disabled bool) string {
	if disabled {
		return "代理: 已强制关闭 (proxy_disabled=true), 直连"
	}
	if cfgProxy != "" {
		return fmt.Sprintf("代理: %s (cfg.yml proxy)", cfgProxy)
	}
	env := os.Getenv("HTTPS_PROXY")
	if env == "" {
		env = os.Getenv("HTTP_PROXY")
	}
	if env != "" {
		return fmt.Sprintf("代理: %s (环境变量)", env)
	}
	return "代理: 未配置, 直连"
}
