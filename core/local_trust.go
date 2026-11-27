package core

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// `tsp trust` adds the local CA to the OS trust store, so browsers, curl and git
// accept https://*.localhost. Bun and Node started by `tsp run` trust it without
// this (NODE_EXTRA_CA_CERTS).

// trustCommands returns the commands that trust caPath on goos. sudo prefixes the
// Linux steps for a non-root caller; debian picks update-ca-certificates over
// update-ca-trust (Fedora/RHEL).
func trustCommands(goos, caPath, home string, root, debian bool) [][]string {
	switch goos {
	case "darwin":
		// The login keychain: macOS asks the person at the screen once (never over SSH).
		return [][]string{{"security", "add-trusted-cert", "-r", "trustRoot", "-k",
			filepath.Join(home, "Library", "Keychains", "login.keychain-db"), caPath}}
	case "windows":
		return [][]string{{"certutil", "-user", "-f", "-addstore", "Root", caPath}}
	}
	cmds := [][]string{
		{"install", "-m", "0644", caPath, "/usr/local/share/ca-certificates/tailscale-proxy-ca.crt"},
		{"update-ca-certificates"},
	}
	if !debian {
		cmds = [][]string{
			{"install", "-m", "0644", caPath, "/etc/pki/ca-trust/source/anchors/tailscale-proxy-ca.pem"},
			{"update-ca-trust"},
		}
	}
	if !root {
		for i, c := range cmds {
			cmds[i] = append([]string{"sudo"}, c...)
		}
	}
	return cmds
}

func cmdTrust(argv []string) int {
	if len(argv) > 0 {
		fmt.Print("tsp trust — add the local CA (~/.tsp/ca/ca.pem) to the OS trust store\n\nUsage:\n  tsp trust\n")
		if argv[0] == "-h" || argv[0] == "--help" {
			return 0
		}
		return 2
	}
	dir, err := stateDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ca, err := ensureCA(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tsp trust:", err)
		return 1
	}
	return trustCA(ca, false)
}

// trustCA adds ca to the OS trust store unless it is there already. On macOS
// over SSH the keychain cannot ask for approval, so setup (install) leaves it
// as a next step instead of failing.
func trustCA(ca *localCA, setup bool) int {
	if caTrusted(ca) {
		fmt.Println("✓ local CA trusted by the OS:", ca.certPEM)
		return 0
	}
	if runtime.GOOS == "darwin" && os.Getenv("SSH_CONNECTION") != "" {
		fmt.Println("! the local CA is not trusted yet: macOS asks for that in a dialog at the Mac's screen")
		fmt.Println("    → run `tsp trust` in a terminal there (until then browsers warn; Bun and Node under tsp run trust it already)")
		if setup {
			return 0
		}
		return 1
	}
	fmt.Println("→ trusting the local CA, so browsers, curl and git accept https://*.localhost")
	home, _ := os.UserHomeDir()
	_, debianErr := lookPath("update-ca-certificates")
	for _, c := range trustCommands(runtime.GOOS, ca.certPEM, home, geteuid() == 0, debianErr == nil) {
		if err := interactive(c); err != nil {
			fmt.Fprintf(os.Stderr, "tsp trust: %s: %v\n", strings.Join(c, " "), err)
			if runtime.GOOS == "darwin" {
				fmt.Fprintln(os.Stderr, "    → macOS asks in a dialog at the Mac's screen: approve it there, then run `tsp trust` again")
			}
			return 1
		}
	}
	if !caTrusted(ca) {
		fmt.Fprintln(os.Stderr, "tsp trust: the OS did not record the trust (macOS: approve the dialog at the Mac's screen), then run `tsp trust` again")
		return 1
	}
	fmt.Println("✓ local CA trusted: https://*.localhost opens without warnings (restart open browsers)")
	return 0
}
