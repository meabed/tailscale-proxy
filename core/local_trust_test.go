package core

import (
	"strings"
	"testing"
)

func TestTrustCommands(t *testing.T) {
	join := func(c [][]string) string {
		var parts []string
		for _, x := range c {
			parts = append(parts, strings.Join(x, " "))
		}
		return strings.Join(parts, "; ")
	}
	cases := []struct {
		goos         string
		root, debian bool
		want         string
	}{
		{"darwin", false, false, "security add-trusted-cert -r trustRoot -k /h/Library/Keychains/login.keychain-db /ca.pem"},
		{"windows", false, false, "certutil -user -f -addstore Root /ca.pem"},
		{"linux", false, true, "sudo install -m 0644 /ca.pem /usr/local/share/ca-certificates/tailscale-proxy-ca.crt; sudo update-ca-certificates"},
		{"linux", true, true, "install -m 0644 /ca.pem /usr/local/share/ca-certificates/tailscale-proxy-ca.crt; update-ca-certificates"},
		{"linux", false, false, "sudo install -m 0644 /ca.pem /etc/pki/ca-trust/source/anchors/tailscale-proxy-ca.pem; sudo update-ca-trust"},
	}
	for _, c := range cases {
		if got := join(trustCommands(c.goos, "/ca.pem", "/h", c.root, c.debian)); got != c.want {
			t.Errorf("%s root=%v debian=%v:\n got %s\nwant %s", c.goos, c.root, c.debian, got, c.want)
		}
	}
}

func TestCmdTrust_usage(t *testing.T) {
	if code := cmdTrust([]string{"-h"}); code != 0 {
		t.Errorf("-h = %d", code)
	}
	if code := cmdTrust([]string{"extra"}); code != 2 {
		t.Errorf("extra arg = %d, want 2", code)
	}
}

func TestCmdTrust_runsTheOSCommands(t *testing.T) {
	isolate(t)
	ran := pinSystem(t, 501, &recRunner{})
	// The fake commands trust nothing, so tsp reports that the OS did not record it.
	if code := cmdTrust(nil); code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	named := false
	for _, c := range *ran {
		named = named || strings.Contains(strings.Join(c, " "), "ca.pem")
	}
	if !named {
		t.Errorf("no trust command named the CA: %v", *ran)
	}
	interactive = func([]string) error { return errString("denied") }
	if code := cmdTrust(nil); code != 1 {
		t.Errorf("failing command = %d, want 1", code)
	}
}
