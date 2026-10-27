// Command birge runs the Birge backend: the HTTP API, the background worker
// and database migrations, selected by the first argument.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"birge/internal/platform/config"
	"birge/internal/platform/httpapi"
	"birge/internal/platform/httpserver"
	"birge/internal/platform/meta"
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
		err = httpserver.Run(ctx, logger, cfg.HTTPAddr, apiHandler(cfg, logger))
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

// apiHandler mounts the generated /v1 handlers of every API area on one mux.
func apiHandler(cfg config.Config, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	phones := make([]meta.CrisisPhone, 0, len(cfg.CrisisPhones))
	for _, p := range cfg.CrisisPhones {
		phones = append(phones, meta.CrisisPhone{Number: p.Number, Label: p.Label})
	}
	metaSvc := &meta.Service{Settings: meta.Settings{
		MinVersion: cfg.MinAppVersion, LatestVersion: cfg.LatestAppVersion, CrisisPhones: phones,
	}}
	meta.HandlerWithOptions(
		meta.NewStrictHandlerWithOptions(metaSvc, nil, meta.StrictHTTPServerOptions{
			RequestErrorHandlerFunc:  httpapi.RequestErrorHandler,
			ResponseErrorHandlerFunc: httpapi.ResponseErrorHandler(logger),
		}),
		meta.StdHTTPServerOptions{BaseURL: "/v1", BaseRouter: mux, ErrorHandlerFunc: httpapi.RequestErrorHandler},
	)
	return mux
}
