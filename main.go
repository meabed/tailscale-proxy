package main

import (
	"fmt"
	"os"
)

// version is injected at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:]))
}

// run dispatches the subcommand and returns a process exit code.
func run(argv []string) int {
	if len(argv) == 0 {
		printHelp()
		return 1
	}
	switch argv[0] {
	case "-v", "--version", "version":
		fmt.Println(version)
		return 0
	case "-h", "--help", "help":
		printHelp()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", argv[0])
		printHelp()
		return 1
	}
}

// printHelp is a temporary stub; the full version lives in cli.go (Task 10).
func printHelp() {
	fmt.Println("portless-tailscale-proxy (ptp) — see `ptp doctor`")
}
