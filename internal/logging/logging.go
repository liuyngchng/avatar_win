// Package logging sets up slog with a compact human-friendly handler.
// The handler is reused from the avatar-web project and emits one-line
// records like:
//
//	2006-01-02T15:04:05.000-07:00 [INFO] i/b/statemachine:247 state transition
//
// Each directory component becomes its first letter, and the filename
// has its .go suffix stripped.
//
//	Init(w, level)  — creates the handler, sets slog.Default, returns
//	ParseLevel(s)    — converts a string to slog.Level (case-insensitive)
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// humanHandler implements slog.Handler with a compact single-line format.
type humanHandler struct {
	w         io.Writer
	mu        *sync.Mutex
	min       slog.Level
	addSource bool
}

// defaultHandler holds the handler installed by Init, so SetLevel/GetLevel
// can adjust the global verbosity after startup (e.g. once cfg.yml is read).
var defaultHandler *humanHandler

// moduleRoot is the absolute path of the module root (go.mod directory).
// It is stripped from source file paths so the log shows package-relative paths
// like "i/b/statemachine:247" instead of absolute paths.
var moduleRoot string

func init() {
	// Normalize to forward slashes to match runtime.CallersFrames output,
	// which always uses "/" regardless of the OS.
	moduleRoot = filepath.ToSlash(findModuleRoot())
}

// findModuleRoot walks up from the current directory until it finds go.mod.
func findModuleRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// Init creates a humanHandler writing to w, filtering at or above minLevel,
// sets it as slog's default logger, and returns the handler so the caller can
// use it for error-log wrappers (e.g. http.Server.ErrorLog).
func Init(w io.Writer, minLevel slog.Level) *humanHandler {
	if minLevel == 0 {
		minLevel = slog.LevelInfo
	}
	h := &humanHandler{w: w, mu: &sync.Mutex{}, min: minLevel, addSource: true}
	defaultHandler = h
	slog.SetDefault(slog.New(h))
	return h
}

// SetLevel updates the global log level. Like the previous log-based
// implementation, it's meant to be called once at startup before any
// concurrent logging happens; the level is read unlocked on the hot path.
func SetLevel(l slog.Level) {
	if defaultHandler != nil {
		defaultHandler.min = l
	}
}

// GetLevel returns the currently configured global log level.
func GetLevel() slog.Level {
	if defaultHandler == nil {
		return slog.LevelInfo
	}
	return defaultHandler.min
}

// Enabled reports whether the handler handles records at the given level.
func (h *humanHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.min
}

// Handle formats a record as:
//
//	<time> [<LEVEL>] <file:line> <msg> <key>=<value> ...
func (h *humanHandler) Handle(_ context.Context, r slog.Record) error {
	buf := make([]byte, 0, 128)

	buf = append(buf, r.Time.Format("2006-01-02T15:04:05.000-07:00")...)
	buf = append(buf, " ["...)
	buf = append(buf, r.Level.String()...)
	buf = append(buf, "] "...)

	if h.addSource {
		buf = appendSource(buf)
	}

	buf = append(buf, r.Message...)

	r.Attrs(func(a slog.Attr) bool {
		buf = append(buf, ' ')
		buf = append(buf, a.Key...)
		buf = append(buf, '=')
		buf = appendValue(buf, a.Value)
		return true
	})

	buf = append(buf, '\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.w.Write(buf)
	return err
}

// WithAttrs returns a new handler with the given attributes (no-op).
func (h *humanHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

// WithGroup returns a new handler with the given group (no-op).
func (h *humanHandler) WithGroup(string) slog.Handler { return h }

// Handler returns the underlying slog.Handler (useful for http.Server.ErrorLog).
func (h *humanHandler) Handler() slog.Handler { return h }

// appendValue renders a slog value, quoting only when necessary.
func appendValue(buf []byte, v slog.Value) []byte {
	return appendQuoted(buf, valueString(v))
}

// valueString renders a slog value to its plain string form.
func valueString(v slog.Value) string {
	if v.Kind() == slog.KindAny {
		a := v.Any()
		if a == nil {
			return "<nil>"
		}
		if err, ok := a.(error); ok {
			return err.Error()
		}
		return fmt.Sprint(a)
	}
	return v.String()
}

// appendQuoted appends s to buf, quoting it when it contains characters that
// would make the line ambiguous.
func appendQuoted(buf []byte, s string) []byte {
	if !needsQuoting(s) {
		return append(buf, s...)
	}
	return strconv.AppendQuote(buf, s)
}

// needsQuoting reports whether s must be quoted.
func needsQuoting(s string) bool {
	if s == "" {
		return true
	}
	for _, r := range s {
		if r <= ' ' || r == '=' || r == '"' || r == '\'' || r == '\\' || r == '[' || r == ']' {
			return true
		}
	}
	return false
}

// appendSource walks the call stack to find the caller of slog.Info/Warn/Error
// and appends "file:line " to buf.
func appendSource(buf []byte) []byte {
	var pcs [8]uintptr
	n := runtime.Callers(3, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])

	for {
		frame, more := frames.Next()
		if isLoggingPackage(frame.File) {
			if !more {
				break
			}
			continue
		}
		buf = appendFileLine(buf, frame.File, frame.Line)
		return buf
	}
	return append(buf, "?:? "...)
}

// isLoggingPackage reports whether the file belongs to the logging machinery.
func isLoggingPackage(file string) bool {
	// Match our own logging.go file.
	if len(file) >= 10 && file[len(file)-10:] == "logging.go" {
		return true
	}
	// Match the standard log/slog package.
	for i := 0; i+8 <= len(file); i++ {
		if file[i:i+8] == "log/slog" {
			return true
		}
	}
	return false
}

// appendFileLine appends "<dir-initials>/<basename>:<line> " to buf.
// The module root is stripped first so the path is relative to the project:
// "/home/rd/workspace/avatar_win/internal/brain/statemachine.go" → "i/b/statemachine:247".
func appendFileLine(buf []byte, file string, line int) []byte {
	// Strip the module root prefix so we get a package-relative path.
	if moduleRoot != "" {
		prefix := moduleRoot + "/"
		if strings.HasPrefix(file, prefix) {
			file = file[len(prefix):]
		}
	}
	start := 0
	for i := 0; i < len(file); i++ {
		if file[i] == '/' {
			if i > start {
				buf = append(buf, file[start])
				buf = append(buf, '/')
			}
			start = i + 1
		}
	}
	// Basename (the last segment).
	if start < len(file) {
		basename := file[start:]
		// Strip ".go" suffix.
		if len(basename) > 3 && basename[len(basename)-3:] == ".go" {
			basename = basename[:len(basename)-3]
		}
		buf = append(buf, basename...)
	}
	buf = append(buf, ':')
	buf = strconv.AppendInt(buf, int64(line), 10)
	buf = append(buf, ' ')
	return buf
}

// ParseLevel parses a level name case-insensitively. Unknown or empty values
// return LevelInfo so a typo in cfg.yml never silences the app.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error", "err":
		return slog.LevelError
	}
	return slog.LevelInfo
}