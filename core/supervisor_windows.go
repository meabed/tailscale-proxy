package core

import (
	"context"
	"fmt"
	"net"
	"os"
)

// Windows has no boot service for the local proxy: `tsp proxy` serves by itself
// (supervisor_unix.go has the supervisor).

type inherited struct {
	ok       bool
	lns      []net.Listener
	port     int
	launcher string
}

func inheritedSockets() (inherited, error) { return inherited{}, nil }

func runWorker(context.Context, inherited) int { return 1 }

func runSupervisor(context.Context, []net.Listener, int) int {
	fmt.Fprintln(os.Stderr, "tsp proxy: --uid is not supported on Windows")
	return 1
}
