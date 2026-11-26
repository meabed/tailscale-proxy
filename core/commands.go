package core

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func cmdConfigure(argv []string) int {
	cfg, _, _, _ := LoadConfig()
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
	if !validAcceptDNS(cfg.AcceptDNS) {
		fmt.Fprintf(os.Stderr, "invalid --accept-dns %q: use true or false\n", cfg.AcceptDNS)
		return 2
	}
	path, err := SaveConfig(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "could not save config:", err)
		return 1
	}
	out, _ := json.MarshalIndent(cfg, "", "  ")
	fmt.Printf("Saved %s:\n%s\n\nRun `tsp` to start with this config.\n", path, out)
	return 0
}

func cmdReset(argv []string) int {
	cfg, _, _, _ := LoadConfig()
	fs := flag.NewFlagSet("reset", flag.ContinueOnError)
	private := fs.Bool("private", cfg.Private, "reset the Serve entry instead of Funnel")
	if code, stop := parseFlags(fs, argv); stop {
		return code
	}
	mode := modeOf(*private)
	if err := exposeReset(execRunner{}, mode); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("%s reset.\n", mode.label())
	return 0
}

func cmdStatus(argv []string) int {
	mode, dcfg, httpsPort, exit := queryConfig(argv)
	if exit >= 0 {
		return exit
	}
	if out, err := exposeStatus(execRunner{}, mode); err != nil {
		fmt.Fprintln(os.Stderr, "status:", err)
	} else {
		fmt.Printf("%s status:\n%s\n", mode.label(), out)
	}
	return printDiscovered(dcfg, mode, httpsPort)
}

func cmdList(argv []string) int {
	mode, dcfg, httpsPort, exit := queryConfig(argv)
	if exit >= 0 {
		return exit
	}
	return printDiscovered(dcfg, mode, httpsPort)
}

func cmdDoctor(argv []string) int {
	mode, dcfg, _, exit := queryConfig(argv)
	if exit >= 0 {
		return exit
	}
	if printChecks(runDoctor(execRunner{}, newDiscoverer(execRunner{}), dcfg, mode)) {
		fmt.Println("\nAll checks passed — you're ready to `tsp start`.")
		return 0
	}
	return 1
}

// parseFlags parses argv into fs; stop=true means exit now with code (0 after
// -h/--help, 2 after a bad flag, whose error the flag package already printed).
func parseFlags(fs *flag.FlagSet, argv []string) (code int, stop bool) {
	err := fs.Parse(argv)
	if err == nil {
		return 0, false
	}
	if err == flag.ErrHelp {
		return 0, true
	}
	return 2, true
}

// queryConfig parses the shared discovery/mode flags (seeded from saved config)
// for list/status/doctor. exit >= 0: stop with that code (help, a bad flag or
// a bad --ports, which is printed).
func queryConfig(argv []string) (mode Mode, dcfg discoverConfig, httpsPort, exit int) {
	cfg, _, _, _ := LoadConfig()
	fs := flag.NewFlagSet("query", flag.ContinueOnError)
	portsRaw := fs.String("ports", cfg.Ports, "port range or single port")
	all := fs.Bool("all", cfg.All, "include all listeners")
	runtimesRaw := fs.String("runtimes", cfg.Runtimes, "comma-separated runtimes")
	private := fs.Bool("private", cfg.Private, "private (Serve) mode")
	port := fs.Int("https-port", cfg.HTTPSPort, "public/tailnet HTTPS port")
	docker := fs.Bool("docker", cfg.Docker, "also query Docker API for containers")
	if code, stop := parseFlags(fs, argv); stop {
		return 0, discoverConfig{}, 0, code
	}
	rng, err := parsePortRange(*portsRaw)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 0, discoverConfig{}, 0, 2
	}
	return modeOf(*private), discoverConfig{rng: rng, all: *all, runtimes: parseRuntimes(*runtimesRaw), docker: *docker}, *port, -1
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
			routes = []localRoute{}
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
