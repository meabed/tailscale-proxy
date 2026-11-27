package core

import (
	"os"
	"path/filepath"
)

// Everything tsp keeps for a user lives in one folder, ~/.tsp: config.json, the
// local CA (ca/), the running names (routes/), the proxy's own copies of tsp
// (bin/), update.json, the logs (logs/), the admin sockets (run/) and the locks.

const stateDirName = ".tsp"

// stateDir is ~/.tsp.
func stateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, stateDirName), nil
}

// StateDir is stateDir for the desktop app.
func StateDir() (string, error) { return stateDir() }
