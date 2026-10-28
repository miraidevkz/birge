// Package httpserver runs an HTTP server with graceful shutdown and provides
// the pieces every Birge listener shares: the liveness and readiness
// handlers and the request log. The caller builds its own root mux (the API
// mounts /auth/ and /v1/; the worker only health endpoints and, on a second
// listener, the courier hook) and passes it to Run.
package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"birge/internal/platform/httpapi"
)

// shutdownTimeout bounds the graceful shutdown: in-flight requests get this
// long to finish after the context is cancelled.
const shutdownTimeout = 20 * time.Second

// readinessTimeout bounds all readiness checks of one probe together.
const readinessTimeout = 2 * time.Second

// Run serves handler on addr until ctx is cancelled, then shuts down
// gracefully.
func Run(ctx context.Context, logger *slog.Logger, addr string, handler http.Handler) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		// WriteTimeout is 0 so long-lived WebSocket connections (chat) are not
		// killed by the server; per-write deadlines live in the gateway.
		WriteTimeout:   0,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 16 << 10,
	}
	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", addr)
		errCh <- srv.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		return fmt.Errorf("listen: %w", err)
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

// Liveness answers 204: the process is alive. It checks nothing else, so an
// orchestrator never restarts a replica because a dependency is down.
func Liveness() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
}

// Check is one readiness dependency.
type Check struct {
	// Name identifies the dependency in the log ("database", "nats").
	Name string
	Fn   func(context.Context) error
}

// Readiness answers 204 when every check passes and 503 otherwise. Failures
// are logged with the dependency name; the response does not say which failed.
func Readiness(logger *slog.Logger, checks ...Check) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
		defer cancel()
		for _, c := range checks {
			if err := c.Fn(ctx); err != nil {
				logger.Warn("not ready", "dependency", c.Name, "error", err)
				httpapi.WriteProblem(w, http.StatusServiceUnavailable, "not_ready", "Not ready.")
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// LogRequests logs one line per request with the route pattern instead of the
// path: paths and query strings can carry identifiers typed by the client, and
// patterns keep the log free of them (ADR-0021). No IPs, headers or bodies.
// api resolves the pattern of /v1 routes, which the root mux only knows as
// "/v1/"; it may be nil.
func LogRequests(logger *slog.Logger, root, api *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		root.ServeHTTP(rec, r)
		_, route := root.Handler(r)
		if route == "/v1/" && api != nil {
			if _, p := api.Handler(r); p != "" {
				route = p
			}
		}
		if route == "" {
			route = "unmatched"
		}
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		logger.Info("http request", "method", r.Method, "route", route, "status", status,
			"bytes", rec.bytes, "duration_ms", time.Since(start).Milliseconds())
	})
}

// statusRecorder captures the status code and size for the request log.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

// WriteHeader records the first status code before passing it on.
func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err //nolint:wrapcheck // a ResponseWriter's error is passed through unchanged
}

// Unwrap lets http.ResponseController (used by the reverse proxy) reach the
// underlying writer for Flush.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }
