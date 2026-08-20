// Package logfile configures the standard library logger to write to a
// file next to the executable, in addition to stderr. This makes it easier
// to diagnose issues when the app is launched by double-clicking (no
// visible console).
package logfile

import (
	"io"
	"log"
	"os"
	"path/filepath"
)

// Init redirects the standard logger to also write to avatar.log in the
// same directory as the executable. Returns the opened file so the caller
// can close it on exit.
func Init() (*os.File, error) {
	exe, err := os.Executable()
	if err != nil {
		exe = "."
	}
	dir := filepath.Dir(exe)
	path := filepath.Join(dir, "avatar.log")

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}

	// Write to both stderr (console) and the log file.
	log.SetOutput(io.MultiWriter(os.Stderr, f))
	log.Printf("logfile: logging to %s", path)
	return f, nil
}
