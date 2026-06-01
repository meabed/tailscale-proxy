// Command tailscale-proxy-app is a tray-first desktop wrapper around the tsp
// engine. It drives core.Controller in-process — no sidecar — and presents a
// webview panel (served on loopback) from the menu bar: start/stop, switch
// Funnel/Serve, open service URLs, toggle start-at-login, and edit the shared
// config.
package main

import (
	"log"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/meabed/tailscale-proxy/core"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type ui struct {
	app  *application.App
	tray *application.SystemTray
	win  *application.WebviewWindow
	ctl  *core.Controller

	mu    sync.Mutex
	cfg   core.Config
	token string
}

func main() {
	cfg, _, _, err := core.LoadConfig()
	if err != nil {
		log.Printf("config: %v (using defaults)", err)
	}

	app := application.New(application.Options{
		Name:        "Tailscale Proxy",
		Description: "Discover local dev servers and expose them through one Tailscale entry.",
		// Tray-first: no Dock icon on macOS (menu-bar only).
		Mac: application.MacOptions{ActivationPolicy: application.ActivationPolicyAccessory},
	})

	u := &ui{app: app, ctl: core.NewController(), cfg: cfg}
	u.tray = app.SystemTray.New()
	u.tray.SetLabel("tsp")

	panelURL, err := u.startDashboard()
	if err != nil {
		log.Fatalf("dashboard: %v", err)
	}
	u.win = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "panel",
		URL:              panelURL,
		Width:            380,
		Height:           560,
		Frameless:        true,
		DisableResize:    true,
		AlwaysOnTop:      true,
		Hidden:           true,
		BackgroundColour: application.RGBA{Red: 21, Green: 23, Blue: 28, Alpha: 255},
	})
	// Click the menu-bar item to drop the panel down underneath it.
	u.tray.AttachWindow(u.win).WindowDebounce(200 * time.Millisecond)

	// Keep the menu-bar label in sync; the panel polls its own status.
	u.ctl.OnChange(func() { application.InvokeAsync(u.updateLabel) })
	u.updateLabel()

	// Auto-start the proxy on launch (best effort; the panel reflects failures).
	go func() {
		if err := u.ctl.Start(u.opts()); err != nil {
			log.Printf("auto-start: %v", err)
		}
		application.InvokeAsync(u.updateLabel)
	}()

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

func (u *ui) opts() core.Options {
	u.mu.Lock()
	defer u.mu.Unlock()
	return core.OptionsFromConfig(u.cfg)
}

func (u *ui) updateLabel() {
	if u.ctl.Running() {
		u.tray.SetLabel("tsp ●")
	} else {
		u.tray.SetLabel("tsp")
	}
}

func (u *ui) toggle() {
	if err := u.ctl.Toggle(u.opts()); err != nil {
		log.Printf("toggle: %v", err)
	}
	application.InvokeAsync(u.updateLabel)
}

func (u *ui) setPrivate(private bool) {
	u.mu.Lock()
	u.cfg.Private = private
	cfg := u.cfg
	u.mu.Unlock()
	if _, err := core.SaveConfig(cfg); err != nil {
		log.Printf("save config: %v", err)
	}
	// Re-expose under the new mode if we're running.
	if u.ctl.Running() {
		_ = u.ctl.Stop()
		if err := u.ctl.Start(u.opts()); err != nil {
			log.Printf("restart after mode change: %v", err)
		}
	}
	application.InvokeAsync(u.updateLabel)
}

func (u *ui) setAutostart(on bool) {
	var err error
	if on {
		err = enableAutostart()
	} else {
		err = disableAutostart()
	}
	if err != nil {
		log.Printf("autostart: %v", err)
	}
}

func (u *ui) quit() {
	_ = u.ctl.Stop()
	application.InvokeAsync(u.app.Quit)
}

// openExternal opens a URL or file path with the OS default handler.
func openExternal(target string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("open %q: %v", target, err)
	}
}
