// Command sextant-core is the device-side binary.
//
// One ELF, two roles (DESIGN D22):
//
//	sextant-core daemon          the privileged long-running process
//	sextant-core ctl <command>   local maintenance (pairing, tokens, secrets)
//	sextant-core version         version and schema
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"sextant-core/internal/config"
	"sextant-core/internal/ctl"
	"sextant-core/internal/engine"
)

// version is stamped by the build script (-X main.version=...).
//
// It must be a plain string variable initialised with a literal: `-X` cannot
// rewrite a variable whose initialiser is another package's value, and the
// failure is silent — the binary just keeps reporting the default.
var version = "3.0.0-dev"

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	switch args[0] {
	case "daemon":
		os.Exit(runDaemon(args[1:]))
	case "ctl":
		os.Exit(ctl.Run(args[1:], version))
	case "version", "-v", "--version":
		fmt.Printf("sextant-core %s (config schema %d, status schema %d)\n",
			version, config.SchemaVersion, 1)
		os.Exit(0)
	case "help", "-h", "--help":
		usage()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Print(`sextant-core — device-side daemon and maintenance CLI

  daemon [--config PATH]    run the privileged daemon (normally started by service.sh)
  ctl <command>             local maintenance; see 'sextant-core ctl help'
  version                   print version and schema

The daemon owns every privileged semantic on this device. Nothing else writes
accessibility settings, background policy or the status file.
`)
}

func runDaemon(args []string) int {
	configPath := engine.NewPaths(config.DefaultDataDir).Config
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--config":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "--config needs a path")
				return 2
			}
			configPath = args[i+1]
			i++
		default:
			fmt.Fprintf(os.Stderr, "unknown daemon flag %q\n", args[i])
			return 2
		}
	}
	cfg, existed, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 1
	}
	if !existed {
		// Not fatal: the module ships a config, so a missing one means the
		// daemon was started by hand. Say so loudly rather than silently using
		// defaults, which is how path bugs hide.
		fmt.Fprintf(os.Stderr, "warning: %s not found; using built-in defaults\n", configPath)
	}
	engine.Version = version
	srv, err := engine.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
		<-sig
		cancel()
	}()
	if err := srv.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "daemon: %v\n", err)
		return 1
	}
	return 0
}
