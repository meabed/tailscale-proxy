package core

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Config is the persisted tsp configuration. Zero values are NOT the defaults —
// always start from defaultConfig() and overlay the file on top.
type Config struct {
	Ports            string `json:"ports"`            // "3000-6000" or single "4000"
	All              bool   `json:"all"`              // include non-web runtimes
	Runtimes         string `json:"runtimes"`         // CSV, "" = all known
	Private          bool   `json:"private"`          // Serve (private) instead of Funnel
	Bind             string `json:"bind"`             // proxy listen address (127.0.0.1 = host only)
	Port             int    `json:"port"`             // local proxy HTTP port
	Interval         int    `json:"interval"`         // re-scan seconds
	HTTPSPort        int    `json:"httpsPort"`        // public/tailnet HTTPS port
	LogRequests      bool   `json:"logRequests"`      // per-request logging
	DeregisterCycles int    `json:"deregisterCycles"` // missing scans before removal
	ForwardHost      bool   `json:"forwardHost"`      // forward the external host to the app
	AcceptDNS        string `json:"acceptDns"`        // "" = leave Tailscale DNS alone; "true"/"false" = set on start
	MatchSeparators  bool   `json:"matchSeparators"`  // match slugs with '-' and '_' interchangeably
	Docker           bool   `json:"docker"`           // also query Docker API for containers
}

// defaultLocalPort is the local proxy's port. It is never one of Funnel's public
// ports (443, 8443, 10000): Tailscale.app's Serve listens on that port on every
// address, so a local proxy there could not bind it.
const defaultLocalPort = 18443

// defaultHTTPSPort is the public Serve/Funnel port: 8443 when this machine
// serves https://*.localhost on 443 (tsp proxy), since a Serve on 443 takes that
// port on macOS and every *.localhost name would hang; else 443.
func defaultHTTPSPort() int {
	if localProxyPresent() {
		return 8443
	}
	return 443
}

// localProxyPresent reports whether the local HTTPS proxy is set up here (its
// CA exists). A variable so tests can pin it.
var localProxyPresent = func() bool {
	dir, err := stateDir()
	if err != nil {
		return false
	}
	certPath, _ := caFiles(dir)
	_, err = os.Stat(certPath)
	return err == nil
}

// defaultConfig returns the built-in defaults.
func defaultConfig() Config {
	return Config{
		Ports: "3000-6000", Bind: "127.0.0.1", Port: defaultLocalPort, Interval: 20,
		HTTPSPort: defaultHTTPSPort(), LogRequests: true, DeregisterCycles: 5, MatchSeparators: true,
	}
}

// configPath returns ~/.tailscale-proxy/config.json.
func configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".tailscale-proxy", "config.json"), nil
}

// stateDir is ~/.tailscale-proxy (beside config.json).
func stateDir() (string, error) {
	p, err := configPath()
	if err != nil {
		return "", err
	}
	return filepath.Dir(p), nil
}

// writeFileAtomic writes data to path through a temp file and rename, so a
// reader never sees a half-written file.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	err = errors.Join(err, tmp.Chmod(perm), tmp.Close())
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}

// loadConfigFrom reads a config file, overlaying it on defaults. Missing file →
// (defaults, false, nil). Returns (config, existed, error).
func loadConfigFrom(path string) (Config, bool, error) {
	cfg := defaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, false, nil
		}
		return cfg, false, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, true, err
	}
	return cfg, true, nil
}

// saveConfigTo writes cfg as indented JSON (atomically), creating the parent dir.
func saveConfigTo(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(data, '\n'), 0o644)
}

// loadConfig loads the config at the default path. Returns (config, path, existed, error).
func loadConfig() (Config, string, bool, error) {
	path, err := configPath()
	if err != nil {
		return defaultConfig(), "", false, err
	}
	cfg, existed, err := loadConfigFrom(path)
	return cfg, path, existed, err
}

// saveConfig writes cfg to the default path and returns it.
func saveConfig(cfg Config) (string, error) {
	path, err := configPath()
	if err != nil {
		return "", err
	}
	return path, saveConfigTo(path, cfg)
}
