package core

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// localOptions are what the local HTTPS proxy answers besides the names it
// serves. The running proxy reads them from config.json as they change: a
// `tsp configure --dashboard=false` or `--catch-all` applies within a second.
type localOptions struct {
	Dashboard bool // https://tsp.localhost (and the bare localhost) is the dashboard
	CatchAll  bool // a .localhost name nothing serves gets a page listing what runs; off: no answer, as if nothing listened
}

func optionsOf(cfg Config) localOptions {
	return localOptions{Dashboard: cfg.LocalDashboard, CatchAll: cfg.LocalCatchAll}
}

// liveOptions re-reads config.json when it changes, at most once a second.
type liveOptions struct {
	mu      sync.Mutex
	checked time.Time
	mod     time.Time
	opts    localOptions
}

func newLiveOptions() *liveOptions {
	cfg, _, _, _ := LoadConfig()
	l := &liveOptions{opts: optionsOf(cfg), checked: time.Now()}
	if path, err := ConfigPath(); err == nil {
		if fi, err := os.Stat(path); err == nil {
			l.mod = fi.ModTime()
		}
	}
	return l
}

func (l *liveOptions) get() localOptions {
	l.mu.Lock()
	defer l.mu.Unlock()
	if time.Since(l.checked) < time.Second {
		return l.opts
	}
	l.checked = time.Now()
	path, err := ConfigPath()
	if err != nil {
		return l.opts
	}
	var mod time.Time
	if fi, err := os.Stat(path); err == nil {
		mod = fi.ModTime()
	}
	if !mod.Equal(l.mod) {
		cfg, _, _, _ := LoadConfig()
		l.mod, l.opts = mod, optionsOf(cfg)
	}
	return l.opts
}

// answers reports whether the proxy answers name: a name it serves (a server
// that started a moment ago too: it looks again), the dashboard while it is on,
// and any other .localhost name only with the catch-all on. "" is the bare
// localhost (and a client that sends no name).
func answers(reg *localRegistry, w *localWatcher, o localOptions, name string) bool {
	switch {
	case name == healthName:
		return true
	case o.CatchAll:
		return true
	case name == dashboardName, name == "":
		return o.Dashboard
	}
	if _, ok := lookupWithParents(reg, w, name); ok {
		return true
	}
	w.rescanSoon()
	_, ok := lookupWithParents(reg, w, name)
	return ok
}

// boardNote fills format with the dashboard's address for a CLI message, or is
// "" when the saved config turns the dashboard off.
func boardNote(format string) string {
	if cfg, _, _, _ := LoadConfig(); !cfg.LocalDashboard {
		return ""
	}
	return fmt.Sprintf(format, dashboardURL)
}

// boardURL is the dashboard's address while it is on, for pages and messages.
func (o localOptions) boardURL() string {
	if o.Dashboard {
		return dashboardURL
	}
	return ""
}
