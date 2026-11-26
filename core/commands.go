package core

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func cmdConfigure(argv []string) int {
	cfg, _, _, _ := loadConfig()
	fs := flag.NewFlagSet("configure", flag.ContinueOnError)
	fs.StringVar(&cfg.Ports, "ports", cfg.Ports, "port range or single port to scan")
	fs.BoolVar(&cfg.All, "all", cfg.All, "include all listeners")
	fs.StringVar(&cfg.Runtimes, "runtimes", cfg.Runtimes, "comma-separated runtimes")
	fs.BoolVar(&cfg.Private, "private", cfg.Private, "expose privately via Serve")
	fs.StringVar(&cfg.Bind, "bind", cfg.Bind, "proxy listen address")
	fs.IntVar(&cfg.Port, "port", cfg.Port, "local proxy HTTP port")
	fs.IntVar(&cfg.Interval, "interval", cfg.Interval, "re-scan period (seconds)")
	fs.IntVar(&cfg.HTTPSPort, "https-port", cfg.HTTPSPort, "public/tailnet HTTPS port")
	fs.IntVar(&cfg.DeregisterCycles, "deregister-cycles", cfg.DeregisterCycles, "missing scans before removal")
	fs.BoolVar(&cfg.LogRequests, "log-requests", cfg.LogRequests, "log each proxied request")
	fs.BoolVar(&cfg.ForwardHost, "forward-host", cfg.ForwardHost, "forward the public host to apps")
	fs.BoolVar(&cfg.MatchSeparators, "match-separators", cfg.MatchSeparators, "match slugs with '-' and '_' interchangeably")
	fs.BoolVar(&cfg.Docker, "docker", cfg.Docker, "also query Docker API for containers")
	fs.StringVar(&cfg.AcceptDNS, "accept-dns", cfg.AcceptDNS, "set Tailscale MagicDNS (true|false) on start; empty = leave it alone")
	if err := fs.Parse(argv); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if _, err := parsePortRange(cfg.Ports); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if cfg.AcceptDNS != "" && cfg.AcceptDNS != "true" && cfg.AcceptDNS != "false" {
		fmt.Fprintf(os.Stderr, "invalid --accept-dns %q: use true or false\n", cfg.AcceptDNS)
		return 2
	}
	path, err := saveConfig(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "could not save config:", err)
		return 1
	}
	out, _ := json.MarshalIndent(cfg, "", "  ")
	fmt.Printf("Saved %s:\n%s\n\nRun `tsp` to start with this config.\n", path, out)
	return 0
}

func cmdReset(argv []string) int {
	cfg, _, _, _ := loadConfig()
	fs := flag.NewFlagSet("reset", flag.ContinueOnError)
	private := fs.Bool("private", cfg.Private, "reset the Serve entry instead of Funnel")
	_ = fs.Parse(argv)
	mode := modeOf(*private)
	if err := exposeReset(execRunner{}, mode); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("%s reset.\n", mode.label())
	return 0
}

func cmdStatus(argv []string) int {
	mode, dcfg, httpsPort := queryConfig(argv)
	if out, err := exposeStatus(execRunner{}, mode); err != nil {
		fmt.Fprintln(os.Stderr, "status:", err)
	} else {
		fmt.Printf("%s status:\n%s\n", mode.label(), out)
	}
	return printDiscovered(dcfg, mode, httpsPort)
}

func cmdList(argv []string) int {
	mode, dcfg, httpsPort := queryConfig(argv)
	return printDiscovered(dcfg, mode, httpsPort)
}

func cmdDoctor(argv []string) int {
	mode, dcfg, _ := queryConfig(argv)
	if printChecks(runDoctor(execRunner{}, newDiscoverer(execRunner{}), dcfg, mode)) {
		fmt.Println("\nAll checks passed — you're ready to `tsp start`.")
		return 0
	}
	return 1
}

// queryConfig parses the shared discovery/mode flags (seeded from saved config)
// for list/status/doctor, returning the mode, discovery config, and HTTPS port.
func queryConfig(argv []string) (Mode, discoverConfig, int) {
	cfg, _, _, _ := loadConfig()
	fs := flag.NewFlagSet("query", flag.ContinueOnError)
	portsRaw := fs.String("ports", cfg.Ports, "port range or single port")
	all := fs.Bool("all", cfg.All, "include all listeners")
	runtimesRaw := fs.String("runtimes", cfg.Runtimes, "comma-separated runtimes")
	private := fs.Bool("private", cfg.Private, "private (Serve) mode")
	httpsPort := fs.Int("https-port", cfg.HTTPSPort, "public/tailnet HTTPS port")
	docker := fs.Bool("docker", cfg.Docker, "also query Docker API for containers")
	_ = fs.Parse(argv)
	rng, err := parsePortRange(*portsRaw)
	if err != nil {
		rng = PortRange{Lo: 3000, Hi: 5000}
	}
	return modeOf(*private), discoverConfig{rng: rng, all: *all, runtimes: parseRuntimes(*runtimesRaw), docker: *docker}, *httpsPort
}

// cmdRoutes lists the running local names (`tsp run`), as text or --json.
func cmdRoutes(argv []string) int {
	fs := flag.NewFlagSet("routes", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(argv); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	dir, err := stateDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	routes := newLocalRegistry(filepath.Join(dir, "routes")).list()
	if *asJSON {
		if routes == nil {
			routes = []LocalRoute{}
		}
		out, _ := json.MarshalIndent(routes, "", "  ")
		fmt.Println(string(out))
		return 0
	}
	if len(routes) == 0 {
		fmt.Println("No apps are running. `tsp run` in an app's folder serves it at https://<name>.localhost.")
		return 0
	}
	for _, rt := range routes {
		fmt.Printf("%-48s → 127.0.0.1:%-5d  %s\n", rt.URL(), rt.Port, rt.Dir)
	}
	return 0
}
