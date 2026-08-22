// Package logfile configures the standard library logger to write to a
// file next to the executable. This makes it easier to diagnose issues
// when the app is launched by double-clicking (no visible console).
package logfile

import (
	"log"
	"os"
	"path/filepath"
)

// Init redirects the standard logger to write to avatar.log in the
// same directory as the executable. Returns the opened file so the caller
// can close it on exit.
func Init() (*os.File, error) {
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	path := filepath.Join(wd, "avatar.log")

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}

	// Write only to the log file. When built with -H windowsgui there is no
	// console, so os.Stderr is an invalid handle and writing to it fails —
	// and io.MultiWriter stops at the first failing writer, silently
	// discarding all file output too. Writing to the file alone avoids that.
	log.SetOutput(f)
	log.Printf("logfile: logging to %s", path)
	return f, nil
}
