// Package logfile opens the log file next to the executable and returns it.
// This makes it easier to diagnose issues when the app is launched by
// double-clicking (no visible console). The caller wires the returned file
// into the logging handler.
package logfile

import (
	"os"
	"path/filepath"
)

// Init opens avatar.log in the current working directory and returns the
// opened file so the caller can hand it to logging.Init and close it on exit.
func Init() (*os.File, error) {
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	path := filepath.Join(wd, "avatar.log")

	// Write only to the log file. When built with -H windowsgui there is no
	// console, so os.Stderr is an invalid handle and writing to it fails.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return f, nil
}