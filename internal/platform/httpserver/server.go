// Package httpserver runs the HTTP server with health endpoints and graceful shutdown.
package httpserver

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

const shutdownTimeout = 20 * time.Second

// Handler returns the root handler. Liveness and readiness live outside /v1
// because they are for the orchestrator, not for clients.
func Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

// Run serves until ctx is cancelled, then shuts down gracefully.
func Run(ctx context.Context, logger *slog.Logger, addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", addr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
