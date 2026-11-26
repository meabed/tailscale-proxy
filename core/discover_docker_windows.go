//go:build windows

package core

// dockerListeners: the Docker API is reached over a Unix socket, not on Windows.
func dockerListeners(portRange) []listener { return nil }

// dockerAvailable: see dockerListeners.
func dockerAvailable() bool { return false }
