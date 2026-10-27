// Command birge runs the Birge backend: the HTTP API, the background worker
// and database migrations, selected by the first argument.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"birge/internal/platform/config"
	"birge/internal/platform/httpserver"
)

func main() {
	os.Exit(run())
}

func run() int {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: birge <api|worker|migrate>")
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load(os.Getenv)
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		return 1
	}

	switch os.Args[1] {
	case "api":
		err = httpserver.Run(ctx, logger, cfg.HTTPAddr)
	case "worker", "migrate":
		err = fmt.Errorf("%s: not implemented yet", os.Args[1])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		logger.Error("exit", "error", err)
		return 1
	}
	return 0
}
