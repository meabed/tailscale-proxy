package core

import (
	"testing"
)

func TestInstallMethod(t *testing.T) {
	cases := map[string]string{
		"/opt/homebrew/Cellar/tsp/0.1.0/bin/tsp":                                   "brew",
		"/opt/homebrew/Caskroom/tsp/0.1.0/tsp":                                     "brew",
		"/usr/local/Homebrew/bin/tsp":                                              "brew",
		"/Users/me/.npm/_npx/abc/node_modules/.bin/tsp":                            "npx",
		"/opt/homebrew/lib/node_modules/tailscale-proxy/bin/launcher.js":           "npm",
		"/Users/me/.cache/tailscale-proxy/v1.11.0/tsp":                             "npm",
		`C:\Users\me\AppData\Roaming\npm\node_modules\tailscale-proxy\bin\tsp.exe`: "npm",
		"/Users/me/proj/node_modules/tailscale-proxy-darwin-arm64/bin/tsp":         "npm",
		"/Users/me/bin/tsp":  "standalone",
		"/usr/local/bin/tsp": "standalone",
	}
	for path, want := range cases {
		if got := installMethod(path); got != want {
			t.Errorf("installMethod(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestNormalizeVer(t *testing.T) {
	if normalizeVer("v0.1.0") != normalizeVer("0.1.0") {
		t.Error("v-prefix should be ignored")
	}
	if normalizeVer(" 0.2.0 ") != "0.2.0" {
		t.Error("whitespace should be trimmed")
	}
}

func TestReleaseArchiveName(t *testing.T) {
	if got := releaseArchiveName("darwin", "arm64"); got != "tsp_darwin_arm64.tar.gz" {
		t.Errorf("got %q", got)
	}
	if got := releaseArchiveName("windows", "amd64"); got != "tsp_windows_amd64.zip" {
		t.Errorf("windows should be .zip, got %q", got)
	}
}
