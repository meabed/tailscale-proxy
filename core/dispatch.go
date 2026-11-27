package core

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Version is injected at build time via
// -ldflags "-X github.com/meabed/tailscale-proxy/core.Version=...".
var Version = "dev"

// Run dispatches the subcommand and returns a process exit code. With no
// subcommand (or a leading flag), it runs `start` with the saved config.
func Run(argv []string) int {
	beforeCommand(argv)
	if len(argv) == 0 {
		return cmdStart(nil)
	}
	switch argv[0] {
	case "start":
		return cmdStart(argv[1:])
	case "list":
		return cmdList(argv[1:])
	case "status":
		return cmdStatus(argv[1:])
	case "reset":
		return cmdReset(argv[1:])
	case "doctor":
		return cmdDoctor(argv[1:])
	case "configure":
		return cmdConfigure(argv[1:])
	case "update":
		return cmdUpdate(argv[1:])
	case "run":
		return cmdRun(argv[1:])
	case "proxy":
		return cmdProxy(argv[1:])
	case "routes":
		return cmdRoutes(argv[1:])
	case "service":
		return cmdService(argv[1:])
	case "trust":
		return cmdTrust(argv[1:])
	case "uninstall":
		return cmdUninstall(argv[1:])
	case "update-check": // hidden: the background half of the CLI's update notice
		return cmdUpdateCheck()
	case "-v", "--version", "version":
		fmt.Println(Version)
		return 0
	case "-h", "--help", "help":
		printHelp()
		return 0
	default:
		// A leading flag means "start with these flags" (start is the default).
		if strings.HasPrefix(argv[0], "-") {
			return cmdStart(argv)
		}
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", argv[0])
		printHelp()
		return 1
	}
}

// beforeCommand runs ahead of every command a person types: it copies this tsp in
// as the local HTTPS proxy when it is newer (the proxy restarts on it, no sudo),
// and in a terminal says once a day when a newer release is out. It never touches
// the network itself: a background `tsp update-check` refreshes update.json.
func beforeCommand(argv []string) {
	cmd := ""
	if len(argv) > 0 {
		cmd = argv[0]
	}
	if Version == "dev" || geteuid() == 0 || cmd == "proxy" || cmd == "update-check" {
		return
	}
	dir, err := stateDir()
	if err != nil || os.MkdirAll(dir, 0o755) != nil {
		return
	}
	self, err := os.Executable()
	if err == nil {
		self, err = filepath.EvalSymlinks(self)
	}
	if err != nil {
		return
	}
	copied, err := syncProxyBin(dir, self)
	quiet := !stderrIsTerminal() || slices.Contains(argv, "--json") || cmd == "-v" || cmd == "--version" || cmd == "version"
	if copied && err == nil && !quiet {
		if s, ok := servicePlatform(); ok && s.selfUpdating() {
			fmt.Fprintf(os.Stderr, "tsp: the local HTTPS proxy moves to tsp %s by itself (no sudo)\n", Version)
		} else if ok && s.installed() {
			fmt.Fprintln(os.Stderr, "tsp: `tsp service install` once (sudo) makes the local HTTPS proxy update itself")
		}
	}
	if quiet || !autoUpdateEnabled() || cmd == "update" {
		return
	}
	if st := loadUpdateState(dir); time.Since(st.CheckedAt) > updateEvery {
		st.CheckedAt = time.Now() // claimed: parallel commands start one check
		saveUpdateState(dir, st)
		spawnUpdateCheck(self)
	}
	if line := updateNotice(dir, self, time.Now()); line != "" {
		fmt.Fprintln(os.Stderr, "tsp: "+line)
	}
}

// spawnUpdateCheck starts `tsp update-check` detached, its output discarded.
func spawnUpdateCheck(self string) {
	cmd := exec.Command(self, "update-check")
	cmd.SysProcAttr = detachSysProcAttr()
	if cmd.Start() == nil {
		_ = cmd.Process.Release()
	}
}

// cmdUpdateCheck refreshes update.json with the latest release (silently).
func cmdUpdateCheck() int {
	dir, err := stateDir()
	if err != nil {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	st := loadUpdateState(dir)
	err = checkLatest(ctx, &st)
	saveUpdateState(dir, st)
	if err != nil {
		return 1
	}
	return 0
}

// stderrIsTerminal is replaced in tests.
var stderrIsTerminal = func() bool {
	fi, err := os.Stderr.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
