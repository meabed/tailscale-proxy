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
	AutoUpdate       bool   `json:"autoUpdate"`       // the local HTTPS proxy updates itself; the CLI says when a release is out
	LocalRequestLog  string `json:"localRequestLog"`  // the local HTTPS proxy's request log: "errors" (failed, 5xx, slow), "all", "off"
	Discover         bool   `json:"discover"`         // the local HTTPS proxy names every dev server it finds (no tsp run needed)
	LocalDashboard   bool   `json:"localDashboard"`   // https://tsp.localhost: what runs, with a Stop per app
	LocalCatchAll    bool   `json:"localCatchAll"`    // a .localhost name nothing serves gets a page listing what runs (off: no answer)
}

// defaultLocalPort is the local proxy's port. It is never one of Funnel's public
// ports (443, 8443, 10000): Tailscale.app's Serve listens on that port on every
// address, so a local proxy there could not bind it.
const defaultLocalPort = 18443

// defaultConfig returns the built-in defaults.
func defaultConfig() Config {
	return Config{
		Ports: "3000-6000", Bind: "127.0.0.1", Port: defaultLocalPort, Interval: 20,
		HTTPSPort: 443, LogRequests: true, DeregisterCycles: 5, MatchSeparators: true, AutoUpdate: true, LocalRequestLog: "errors", Discover: true,
		LocalDashboard: true,
	}
}

// ConfigPath returns the config file path, ~/.tsp/config.json.
func ConfigPath() (string, error) {
	dir, err := stateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
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

// LoadConfig loads the saved config overlaid on the defaults (the defaults alone
// when there is none). Returns (config, path, existed, error).
func LoadConfig() (Config, string, bool, error) {
	path, err := ConfigPath()
	if err != nil {
		return defaultConfig(), "", false, err
	}
	cfg, existed, err := loadConfigFrom(path)
	return cfg, path, existed, err
}

// SaveConfig writes cfg to the default path and returns that path.
func SaveConfig(cfg Config) (string, error) {
	path, err := ConfigPath()
	if err != nil {
		return "", err
	}
	return path, saveConfigTo(path, cfg)
}
