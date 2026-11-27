package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUntrustCommands(t *testing.T) {
	mac := untrustCommands("darwin", "/h/.tailscale-proxy/ca/ca.pem", "/h", false)
	if len(mac) != 2 || mac[0][1] != "remove-trusted-cert" || !strings.HasSuffix(mac[1][len(mac[1])-1], "login.keychain-db") || mac[1][3] != "tailscale-proxy local CA" {
		t.Errorf("darwin = %v", mac)
	}
	if mac := untrustCommands("darwin", "", "/h", false); len(mac) != 1 || mac[0][1] != "delete-certificate" {
		t.Errorf("darwin without the CA file = %v", mac)
	}
	if win := untrustCommands("windows", "", "", false); win[0][0] != "certutil" || win[0][2] != "-delstore" {
		t.Errorf("windows = %v", win)
	}
	pinSystem(t, 1000, &recRunner{})
	linux := untrustCommands("linux", "", "", false)
	if linux[0][0] != "sudo" || linux[0][1] != "rm" || linux[1][1] != "update-ca-certificates" {
		t.Errorf("linux (non-root) = %v", linux)
	}
	if root := untrustCommands("linux", "", "", true); root[0][0] != "rm" {
		t.Errorf("linux as root = %v", root)
	}
}

func TestUninstallState_keepsConfigUnlessAll(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".tailscale-proxy")
	for _, p := range []string{"ca/ca.pem", "routes/api.json", "bin/tsp", "config.json", "update.json"} {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755)
		_ = os.WriteFile(filepath.Join(dir, p), []byte("x"), 0o644)
	}
	out := captureStdout(t, func() {
		if !uninstallState(dir, false) {
			t.Error("failed")
		}
	})
	left, _ := os.ReadDir(dir)
	if len(left) != 1 || left[0].Name() != "config.json" || !strings.Contains(out, "config.json kept") {
		t.Errorf("left %v, output %q", left, out)
	}
	out = captureStdout(t, func() { uninstallState(dir, true) })
	if _, err := os.Stat(dir); !os.IsNotExist(err) || !strings.Contains(out, "removed") {
		t.Errorf("--all left %s (%v): %q", dir, err, out)
	}
	out = captureStdout(t, func() { uninstallState(dir, true) })
	if !strings.HasPrefix(out, "– ") {
		t.Errorf("a second run = %q, want a skipped step", out)
	}
}

func TestCmdUninstall_needsYesWithoutATerminal(t *testing.T) {
	isolate(t)
	stderr := captureStderr(t, func() {
		if code := cmdUninstall(nil); code != 2 {
			t.Errorf("code = %d, want 2", code)
		}
	})
	if !strings.Contains(stderr, "add --yes") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestCmdUninstall_nothingSetUp(t *testing.T) {
	home, _ := isolate(t)
	pinSystem(t, 501, &recRunner{})
	lookPath = func(string) (string, error) { return "", errString("not found") } // no tailscale, no sudo
	origP := servicePlatform
	servicePlatform = func() (serviceSystem, bool) { return serviceSystem{unitPath: filepath.Join(home, "none")}, true }
	t.Cleanup(func() { servicePlatform = origP })
	dir := filepath.Join(home, ".tailscale-proxy")
	_ = os.MkdirAll(filepath.Join(dir, "routes"), 0o755)
	out := captureStdout(t, func() {
		if code := cmdUninstall([]string{"--yes"}); code != 0 {
			t.Errorf("code = %d", code)
		}
	})
	for _, want := range []string{"– Tailscale Serve/Funnel: tailscale is not installed", "– boot service: not installed", "– local CA: not in the OS trust store", "✓ " + dir + " removed"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestUninstallExposure_onlyTspsEntry(t *testing.T) {
	isolate(t)
	r := &statusRunner{out: "https://node.ts.net (Funnel on)\n|-- / proxy http://127.0.0.1:3000\n"}
	pinSystem(t, 501, r)
	captureStdout(t, func() { uninstallExposure() })
	for _, c := range r.calls {
		if strings.Contains(c, "reset") {
			t.Errorf("reset someone else's entry: %v", r.calls)
		}
	}
	r.out = "https://node.ts.net (Funnel on)\n|-- / proxy http://127.0.0.1:18443\n"
	out := captureStdout(t, func() { uninstallExposure() })
	if !strings.Contains(strings.Join(r.calls, ";"), "tailscale funnel reset") || !strings.Contains(out, "✓ Tailscale Serve/Funnel entry removed") {
		t.Errorf("calls %v, out %q", r.calls, out)
	}
}

// statusRunner answers `tailscale serve status` with out and records every call.
type statusRunner struct {
	out   string
	calls []string
}

func (r *statusRunner) Run(name string, args ...string) (string, string, error) {
	c := strings.Join(append([]string{name}, args...), " ")
	r.calls = append(r.calls, c)
	if c == "tailscale serve status" {
		return r.out, "", nil
	}
	return "", "", nil
}
