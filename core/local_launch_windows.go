//go:build windows

package core

import (
	"errors"
	"net"
)

// Windows has no boot service for the local proxy (see local_launch_unix.go).

func handOff([]net.Listener, string, int) error {
	return errors.New("no launcher on Windows")
}

func inheritedListeners() ([]net.Listener, int, string, bool, error) {
	return nil, 0, "", false, nil
}
