// Package logging provides leveled logging on top of the standard
// library log package. The global level is set from cfg.yml (log.level)
// at startup; messages below that level are dropped.
//
// Levels:
//
//	debug — verbose flow details (per-chunk ASR results, speech-onset
//	        events, internal state transitions)
//	info  — key milestones the user can observe (startup, user said
//	        "X", pipeline finished, state changes)
//	warn  — recoverable anomalies (one-shot retries, mismatches)
//	error — failures (API errors, nil clients, fatal init)
//
// The package is intentionally minimal: no structured output, no
// per-subsystem loggers, no caller-site level hints — just enough to
// gate 100+ existing log.Printf call sites by a single config knob.
package logging

import (
	"log"
	"strings"
)

// Level is the severity of a log message.
type Level int

// Level values. Lower numeric value = more verbose. A message is
// emitted when its level is >= the configured global level.
const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

// String returns the canonical lowercase name of the level.
func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "debug"
	case LevelInfo:
		return "info"
	case LevelWarn:
		return "warn"
	case LevelError:
		return "error"
	}
	return "unknown"
}

// ParseLevel parses a level name case-insensitively. Unknown or empty
// values return LevelInfo so a typo in cfg.yml never silences the app.
func ParseLevel(s string) Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return LevelDebug
	case "info":
		return LevelInfo
	case "warn", "warning":
		return LevelWarn
	case "error", "err":
		return LevelError
	}
	return LevelInfo
}

// currentLevel is the global filter. Read and written under no lock —
// it's set once at startup before any logging happens, and read on the
// hot path. A data race here would only briefly show the wrong level.
var currentLevel = LevelInfo

// SetLevel updates the global log level.
func SetLevel(l Level) {
	currentLevel = l
}

// GetLevel returns the currently configured global log level.
func GetLevel() Level {
	return currentLevel
}

// Debugf logs at debug level.
func Debugf(format string, args ...any) {
	if currentLevel <= LevelDebug {
		log.Printf("[DEBUG] "+format, args...)
	}
}

// Infof logs at info level.
func Infof(format string, args ...any) {
	if currentLevel <= LevelInfo {
		log.Printf("[INFO] "+format, args...)
	}
}

// Warnf logs at warn level.
func Warnf(format string, args ...any) {
	if currentLevel <= LevelWarn {
		log.Printf("[WARN] "+format, args...)
	}
}

// Errorf logs at error level.
func Errorf(format string, args ...any) {
	if currentLevel <= LevelError {
		log.Printf("[ERROR] "+format, args...)
	}
}
