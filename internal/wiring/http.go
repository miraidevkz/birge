package wiring

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"birge/contracts/openapi"
	membershipadapter "birge/internal/domains/membership/adapter"
	membershipapi "birge/internal/domains/membership/api"
	"birge/internal/domains/membership/authgw"
	membershipservice "birge/internal/domains/membership/service"
	notifyadapter "birge/internal/domains/notify/adapter"
	notifyapi "birge/internal/domains/notify/api"
	notifyservice "birge/internal/domains/notify/service"
	privacyadapter "birge/internal/domains/privacy/adapter"
	privacyapi "birge/internal/domains/privacy/api"
	privacyservice "birge/internal/domains/privacy/service"
	"birge/internal/platform/apivalidate"
	"birge/internal/platform/auth"
	"birge/internal/platform/clock"
	"birge/internal/platform/crypto"
	"birge/internal/platform/httpapi"
	"birge/internal/platform/meta"
	"birge/internal/platform/ratelimit"
	"birge/internal/platform/session"
)

// AnonymousOperations are exactly the operations declared with "security: []"
// in contracts/openapi/birge.yaml. Everything else under /v1 needs a session.
var AnonymousOperations = []string{
	"GET /v1/meta/client-config",
	"GET /v1/reference/districts",
	"GET /v1/reference/needs",
}

// APIDeps are the resources the API handler is built from.
type APIDeps struct {
	Config   Config
	Pool     *pgxpool.Pool
	Envelope *crypto.Envelope
	Clock    clock.Clock
	Logger   *slog.Logger
	// KratosTransport reaches Kratos for the gateway; optional (tests).
	KratosTransport http.RoundTripper
}

// NewAPIHandler builds the whole HTTP stack of `birge api`, outermost first:
//
//  1. request logging (method, route pattern, status, duration; no bodies,
//     no query strings, no IPs, no headers);
//  2. /auth/: the member sign-in gateway in front of Kratos (it has its own
//     per-IP, per-number and global limits and the proof of work);
//  3. /v1/: per-IP rate limit → session middleware (anonymous operations
//     pass) → request validation (not built yet) → the generated handlers of
//     meta, membership, privacy and notify on one mux;
//  4. /healthz (process alive) and /readyz (database reachable).
func NewAPIHandler(d APIDeps) (http.Handler, error) {
	if d.Pool == nil || d.Envelope == nil || d.Logger == nil {
		return nil, errors.New("wiring: API needs a pool, an envelope and a logger")
	}
	if d.Clock == nil {
		d.Clock = clock.System{}
	}
	cfg := d.Config
	clientIP := ClientIP(cfg.TrustedProxyHeader)

	gw, err := authgw.New(authgw.Config{
		KratosPublicURL: cfg.KratosPublicURL,
		PoWKey:          cfg.PoWKey,
		PoWMaxNumber:    cfg.PoWMaxNumber,
		HashKey:         cfg.HashKey,
		AllowedPrefixes: cfg.SMSPrefixes,
		PerIP:           cfg.AuthPerIP,
		PerNumber:       cfg.AuthPerNumber,
		Global:          cfg.AuthGlobal,
		ClientIP:        clientIP,
		Transport:       d.KratosTransport,
		Clock:           d.Clock,
		Logger:          d.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("wiring: %w", err)
	}
	var kratosClient *http.Client
	if d.KratosTransport != nil {
		kratosClient = &http.Client{Transport: d.KratosTransport}
	}
	sessions, err := session.New(session.Config{
		KratosPublicURL: cfg.KratosPublicURL,
		Client:          kratosClient,
		Anonymous:       AnonymousOperations,
		Clock:           d.Clock,
		Logger:          d.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("wiring: %w", err)
	}
	limiter, err := ratelimit.New(cfg.APIPerIP, ratelimit.Options{Clock: d.Clock})
	if err != nil {
		return nil, fmt.Errorf("wiring: API rate limit: %w", err)
	}

	members := membershipservice.New(&membershipadapter.Postgres{Pool: d.Pool}, DisplayNameEncryptor{Sealer: d.Envelope}, d.Clock)
	privacy := &privacyservice.Service{
		Store:    &privacyadapter.PostgresStore{Pool: d.Pool},
		Shredder: d.Envelope,
		Clock:    d.Clock,
		Logger:   d.Logger,
		Settings: privacyservice.Settings{CurrentVersions: cfg.ConsentVersions, DeletionDueAfter: cfg.DeletionDelay},
	}
	// The API serves the inbox and devices only; delivery (recipients, push,
	// SMS) runs in the worker, so those ports stay unset here.
	notify := &notifyservice.Service{
		Store:  &notifyadapter.PostgresStore{Pool: d.Pool},
		Clock:  d.Clock,
		Logger: d.Logger,
		Config: notifyservice.Config{CriticalSMSAfter: cfg.CriticalSMSAfter, SMSTexts: cfg.SMSTexts},
	}

	api := http.NewServeMux()
	mountAPI(api, cfg, members, privacy, notify, d.Logger)

	// Validation runs after the session (anonymous callers cannot probe the
	// validator) and before the generated handlers, which do not enforce the
	// contract themselves.
	validator, err := apivalidate.New(openapi.Spec, apivalidate.Options{Logger: d.Logger})
	if err != nil {
		return nil, fmt.Errorf("request validator: %w", err)
	}
	v1 := ratelimit.Middleware(limiter, clientIP)(sessions.Wrap(validator.Middleware(api)))

	root := http.NewServeMux()
	root.Handle("/auth/", gw)
	root.Handle("/v1/", v1)
	root.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	root.Handle("GET /readyz", readiness(d.Logger, poolCheck(d.Pool)))
	return logRequests(d.Logger, root, api), nil
}

// mountAPI registers the generated /v1 handlers of every API area on mux,
// with the shared problem+json error handlers.
func mountAPI(mux *http.ServeMux, cfg Config, members *membershipservice.Service,
	privacy *privacyservice.Service, notify *notifyservice.Service, logger *slog.Logger,
) {
	const base = "/v1"
	phones := make([]meta.CrisisPhone, 0, len(cfg.CrisisPhones))
	for _, p := range cfg.CrisisPhones {
		phones = append(phones, meta.CrisisPhone{Number: p.Number, Label: p.Label})
	}
	metaSvc := &meta.Service{Settings: meta.Settings{
		MinVersion: cfg.MinAppVersion, LatestVersion: cfg.LatestAppVersion, CrisisPhones: phones,
	}}
	responseErr := httpapi.ResponseErrorHandler(logger)
	meta.HandlerWithOptions(
		meta.NewStrictHandlerWithOptions(metaSvc, nil, meta.StrictHTTPServerOptions{
			RequestErrorHandlerFunc: httpapi.RequestErrorHandler, ResponseErrorHandlerFunc: responseErr,
		}),
		meta.StdHTTPServerOptions{BaseURL: base, BaseRouter: mux, ErrorHandlerFunc: httpapi.RequestErrorHandler},
	)
	membershipapi.HandlerWithOptions(
		membershipapi.NewStrictHandlerWithOptions(membershipapi.NewServer(members), nil, membershipapi.StrictHTTPServerOptions{
			RequestErrorHandlerFunc: httpapi.RequestErrorHandler, ResponseErrorHandlerFunc: responseErr,
		}),
		membershipapi.StdHTTPServerOptions{BaseURL: base, BaseRouter: mux, ErrorHandlerFunc: httpapi.RequestErrorHandler},
	)
	toMember := memberPrincipal(members, logger)
	privacyapi.HandlerWithOptions(
		privacyapi.NewStrictHandlerWithOptions(&privacyapi.Server{Service: privacy}, nil, privacyapi.StrictHTTPServerOptions{
			RequestErrorHandlerFunc: httpapi.RequestErrorHandler, ResponseErrorHandlerFunc: responseErr,
		}),
		privacyapi.StdHTTPServerOptions{
			BaseURL: base, BaseRouter: mux, ErrorHandlerFunc: httpapi.RequestErrorHandler,
			Middlewares: []privacyapi.MiddlewareFunc{toMember},
		},
	)
	notifyapi.HandlerWithOptions(
		notifyapi.NewStrictHandlerWithOptions(&notifyapi.Server{Service: notify}, nil, notifyapi.StrictHTTPServerOptions{
			RequestErrorHandlerFunc: httpapi.RequestErrorHandler, ResponseErrorHandlerFunc: responseErr,
		}),
		notifyapi.StdHTTPServerOptions{
			BaseURL: base, BaseRouter: mux, ErrorHandlerFunc: httpapi.RequestErrorHandler,
			Middlewares: []notifyapi.MiddlewareFunc{toMember},
		},
	)
}

// memberPrincipal resolves the signed-in member (creating it on first use,
// ADR-0008 addendum) and sets auth.Principal.MemberID for the handlers of
// contexts that key their data by member ID (privacy, notify). The identity
// ID stays untouched. Anonymous and staff requests pass unchanged; the
// handlers refuse a principal without a MemberID.
func memberPrincipal(members *membershipservice.Service, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := auth.FromContext(r.Context())
			if !ok || p.Staff {
				next.ServeHTTP(w, r)
				return
			}
			m, err := members.Me(r.Context())
			if err != nil {
				logger.Error("resolve member", "pattern", r.Pattern, "error", err)
				httpapi.WriteProblem(w, http.StatusInternalServerError, "internal", "Something went wrong.")
				return
			}
			p.MemberID = m.ID
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), p)))
		})
	}
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

// logRequests logs one line per request with the route pattern instead of the
// path: paths and query strings can carry identifiers typed by the client, and
// patterns keep the log free of them (ADR-0021). No IPs, headers or bodies.
// api resolves the pattern of /v1 routes, which the root mux only knows as "/v1/".
func logRequests(logger *slog.Logger, root, api *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		root.ServeHTTP(rec, r)
		_, route := root.Handler(r)
		if route == "/v1/" {
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

// check is one readiness dependency.
type check struct {
	name string
	fn   func(context.Context) error
}

func poolCheck(pool *pgxpool.Pool) check {
	return check{name: "database", fn: func(ctx context.Context) error {
		if err := pool.Ping(ctx); err != nil {
			return fmt.Errorf("ping: %w", err)
		}
		return nil
	}}
}

const readinessTimeout = 2 * time.Second

// readiness answers 204 when every check passes and 503 otherwise. Failures
// are logged with the dependency name; the response does not say which failed.
func readiness(logger *slog.Logger, checks ...check) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
		defer cancel()
		for _, c := range checks {
			if err := c.fn(ctx); err != nil {
				logger.Warn("not ready", "dependency", c.name, "error", err)
				httpapi.WriteProblem(w, http.StatusServiceUnavailable, "not_ready", "Not ready.")
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// shutdownTimeout is the same as in internal/platform/httpserver.
const shutdownTimeout = 20 * time.Second

// serve runs handler on addr until ctx is cancelled, then shuts down
// gracefully. It is internal/platform/httpserver.Run without that package's
// fixed root mux, which cannot mount /auth/ or real readiness checks.
func serve(ctx context.Context, logger *slog.Logger, addr string, handler http.Handler) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
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
