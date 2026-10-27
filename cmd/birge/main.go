// Command birge runs the Birge backend, selected by the first argument:
//
//	birge api      the HTTP API (with the /auth/ sign-in gateway)
//	birge worker   outbox relay, event consumers and timers
//	birge migrate  apply the embedded database migrations
//	birge keygen   write local development keys (see -dir)
//
// Configuration comes from the environment; see docs/development.md
// § Configuration and package internal/wiring. keygen needs no environment.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"birge/internal/wiring"
)

func main() {
	os.Exit(run())
}

func run() int {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: birge <api|worker|migrate|keygen>")
		return 2
	}
	if os.Args[1] == "keygen" {
		return runKeygen(os.Args[2:])
	}
	cmd := wiring.Command(os.Args[1])
	switch cmd {
	case wiring.CommandAPI, wiring.CommandWorker, wiring.CommandMigrate:
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\nusage: birge <api|worker|migrate|keygen>\n", os.Args[1])
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("command", string(cmd))

	cfg, err := wiring.Load(os.Getenv, os.ReadFile, cmd)
	if err != nil {
		// Load names variables, never their values.
		logger.Error("invalid configuration", "error", err)
		return 1
	}

	switch cmd {
	case wiring.CommandAPI:
		err = wiring.RunAPI(ctx, logger, cfg)
	case wiring.CommandWorker:
		err = wiring.RunWorker(ctx, logger, cfg)
	case wiring.CommandMigrate:
		err = wiring.Migrate(ctx, logger, cfg.DatabaseURL, cfg.ApplyRoles)
	}
	if err != nil {
		logger.Error("exit", "error", err)
		return 1
	}
	return 0
}
