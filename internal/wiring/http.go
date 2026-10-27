package wiring

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"birge/contracts/openapi"
	assistantadapter "birge/internal/domains/assistant/adapter"
	"birge/internal/domains/assistant/adapter/anthropic"
	assistantapi "birge/internal/domains/assistant/api"
	assistantmodel "birge/internal/domains/assistant/model"
	assistantservice "birge/internal/domains/assistant/service"
	chatadapter "birge/internal/domains/chat/adapter"
	chatapi "birge/internal/domains/chat/api"
	"birge/internal/domains/chat/gateway"
	chatmodel "birge/internal/domains/chat/model"
	chatport "birge/internal/domains/chat/port"
	chatservice "birge/internal/domains/chat/service"
	circleadapter "birge/internal/domains/circle/adapter"
	circleapi "birge/internal/domains/circle/api"
	circleservice "birge/internal/domains/circle/service"
	directoryadapter "birge/internal/domains/directory/adapter"
	directoryapi "birge/internal/domains/directory/api"
	directoryservice "birge/internal/domains/directory/service"
	emergencyadapter "birge/internal/domains/emergency/adapter"
	emergencyapi "birge/internal/domains/emergency/api"
	emergencyservice "birge/internal/domains/emergency/service"
	knowledgeadapter "birge/internal/domains/knowledge/adapter"
	knowledgeapi "birge/internal/domains/knowledge/api"
	knowledgeservice "birge/internal/domains/knowledge/service"
	membershipadapter "birge/internal/domains/membership/adapter"
	membershipapi "birge/internal/domains/membership/api"
	"birge/internal/domains/membership/authgw"
	membershipmodel "birge/internal/domains/membership/model"
	membershipservice "birge/internal/domains/membership/service"
	moderationadapter "birge/internal/domains/moderation/adapter"
	moderationapi "birge/internal/domains/moderation/api"
	moderationservice "birge/internal/domains/moderation/service"
	notifyadapter "birge/internal/domains/notify/adapter"
	notifyapi "birge/internal/domains/notify/api"
	notifyservice "birge/internal/domains/notify/service"
	privacyadapter "birge/internal/domains/privacy/adapter"
	privacyapi "birge/internal/domains/privacy/api"
	privacyservice "birge/internal/domains/privacy/service"
	safetyadapter "birge/internal/domains/safety/adapter"
	safetyapi "birge/internal/domains/safety/api"
	safetyservice "birge/internal/domains/safety/service"
	sharingadapter "birge/internal/domains/sharing/adapter"
	sharingapi "birge/internal/domains/sharing/api"
	"birge/internal/domains/sharing/imageproc"
	sharingservice "birge/internal/domains/sharing/service"
	wellbeingadapter "birge/internal/domains/wellbeing/adapter"
	wellbeingapi "birge/internal/domains/wellbeing/api"
	wellbeingservice "birge/internal/domains/wellbeing/service"
	"birge/internal/platform/apivalidate"
	"birge/internal/platform/auth"
	"birge/internal/platform/clock"
	"birge/internal/platform/crypto"
	"birge/internal/platform/httpapi"
	"birge/internal/platform/meta"
	"birge/internal/platform/objectstore"
	"birge/internal/platform/ratelimit"
	"birge/internal/platform/session"
	"birge/internal/platform/staffauth"
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
	// NATS carries chat room fan-out (core, not JetStream). Nil disables
	// live delivery (unit tests that do not exercise the WebSocket).
	NATS *nats.Conn
	// Objects is S3-compatible storage for sharing photos; nil uses Disabled.
	Objects objectstore.Store
	// KratosTransport reaches Kratos for the gateway; optional (tests).
	KratosTransport http.RoundTripper
}

// NewAPIHandler builds the whole HTTP stack of `birge api`, outermost first:
//
//  1. request logging (method, route pattern, status, duration; no bodies,
//     no query strings, no IPs, no headers);
//  2. /auth/: the member sign-in gateway in front of Kratos (it has its own
//     per-IP, per-number and global limits and the proof of work);
//  3. /v1/: per-IP rate limit → authentication → request validation → the
//     generated handlers of meta, membership, privacy, notify, safety,
//     wellbeing, emergency, assistant, knowledge and circle on one mux.
//     Authentication depends on the route:
//     /v1/staff/ goes through staffauth (OIDC token introspection, MFA
//     required; 503 when no provider is configured), everything else
//     through the member session middleware (anonymous operations pass).
//     The two never stand in for each other: a member session on a staff
//     route is just an unknown token to the IdP (401), and a staff token on
//     a member route is unknown to Kratos (401);
//  4. /healthz (process alive) and /readyz (database reachable).
func NewAPIHandler(ctx context.Context, d APIDeps) (http.Handler, error) {
	if d.Pool == nil || d.Envelope == nil || d.Logger == nil {
		return nil, errors.New("wiring: API needs a pool, an envelope and a logger")
	}
	if d.Config.Lexicon == nil {
		return nil, errors.New("wiring: crisis lexicon is required")
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

	identities, err := NewKratosIdentities(cfg.KratosAdminURL, "phone", kratosClient)
	if err != nil {
		return nil, fmt.Errorf("wiring: %w", err)
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
	safety := &safetyservice.Service{
		Store:     &safetyadapter.PostgresStore{Pool: d.Pool},
		Sealer:    d.Envelope,
		Directory: &MemberDirectory{Members: members, Phones: identities},
		Lexicon:   cfg.Lexicon,
		Clock:     d.Clock,
		Logger:    d.Logger,
		Settings: safetyservice.Settings{
			BackupAfter:  cfg.EscalationBackupAfter,
			CrisisPhones: safetyCrisisPhones(cfg.CrisisPhones),
			StaffLang:    cfg.StaffSMSLang,
		},
	}
	wellbeing := &wellbeingservice.Service{
		Store:    &wellbeingadapter.PostgresStore{Pool: d.Pool},
		Sealer:   d.Envelope,
		Crisis:   &CrisisOpener{Safety: safety},
		Clock:    d.Clock,
		Logger:   d.Logger,
		Settings: cfg.Wellbeing,
	}
	circle := &circleservice.Service{
		Store:   &circleadapter.PostgresStore{Pool: d.Pool},
		Members: &CircleMembers{Members: members},
		Blocks:  &EmergencyBlocks{Members: members},
		Clock:   d.Clock,
		Logger:  d.Logger,
	}
	emergency := &emergencyservice.Service{
		Store:         &emergencyadapter.PostgresStore{Pool: d.Pool},
		Sealer:        d.Envelope,
		Members:       &EmergencyMembers{Members: members},
		Blocks:        &EmergencyBlocks{Members: members},
		Acquaintances: &CircleMet{Circle: circle},
		Clock:         d.Clock,
		Logger:        d.Logger,
		Settings:      emergencyservice.Settings{AutoCloseAfter: cfg.SosAutoCloseAfter},
	}
	knowledge := &knowledgeservice.Service{
		Store:  &knowledgeadapter.PostgresStore{Pool: d.Pool},
		Clock:  d.Clock,
		Logger: d.Logger,
	}
	searcher, _ := NewRetrieval(cfg, d.Pool, nil, d.Clock, d.Logger)
	if !cfg.RetrievalEnabled {
		d.Logger.Warn("retrieval disabled: TEI or Qdrant not configured", "event", "RetrievalDisabled")
	}
	assistantSvc, err := newAssistantService(cfg, d.Pool, d.Envelope, privacy, safety, members, searcher, d.Clock, d.Logger)
	if err != nil {
		return nil, err
	}
	if err := assistantSvc.EnsureRetentionPurge(ctx); err != nil {
		return nil, fmt.Errorf("wiring: assistant retention: %w", err)
	}

	objects := d.Objects
	if objects == nil {
		objects, err = NewObjectStore(cfg, d.Logger)
		if err != nil {
			return nil, err
		}
	}
	var fanout chatport.Fanout = noopFanout{}
	var bus chatport.Bus = noopFanout{}
	if d.NATS != nil {
		n := &chatadapter.NATSFanout{Conn: d.NATS, Logger: d.Logger}
		fanout, bus = n, n
	}
	chatSvc := &chatservice.Service{
		Store:      &chatadapter.PostgresStore{Pool: d.Pool},
		Sealer:     d.Envelope,
		Members:    &ChatMembers{Members: members},
		Blocks:     &EmergencyBlocks{Members: members},
		Mentors:    &ChatMentors{Members: members},
		Crisis:     &ChatCrisis{Safety: safety},
		Fanout:     fanout,
		Suspenders: &ChatAuthorSuspender{Members: members},
		Clock:      d.Clock,
		Logger:     d.Logger,
	}
	sharingSvc := &sharingservice.Service{
		Store:   &sharingadapter.PostgresStore{Pool: d.Pool},
		Members: &SharingMembers{Members: members},
		Blocks:  &EmergencyBlocks{Members: members},
		Objects: objects,
		Images:  imageproc.Processor{},
		Clock:   d.Clock,
		Logger:  d.Logger,
	}
	directorySvc := &directoryservice.Service{
		Store:   &directoryadapter.PostgresStore{Pool: d.Pool},
		Members: &DirectoryMembers{Members: members},
		Clock:   d.Clock,
		Logger:  d.Logger,
	}
	moderationSvc := &moderationservice.Service{
		Store:   &moderationadapter.PostgresStore{Pool: d.Pool},
		Sources: moderationSources(chatSvc, sharingSvc, directorySvc, members),
		Clock:   d.Clock,
		Logger:  d.Logger,
	}

	api := http.NewServeMux()
	mountAPI(api, cfg, members, privacy, notify, safety, wellbeing, emergency, assistantSvc, knowledge, circle, chatSvc, sharingSvc, directorySvc, moderationSvc, d.Logger)

	chatGW := &gateway.Gateway{
		Chat: chatSvc, Auth: &ChatAuthenticator{Sessions: sessions, Members: members},
		Bus: bus, Logger: d.Logger, Options: gateway.Options{OriginPatterns: cfg.ChatWSOrigins},
	}

	// Validation runs after the session (anonymous callers cannot probe the
	// validator) and before the generated handlers, which do not enforce the
	// contract themselves.
	validator, err := apivalidate.New(openapi.Spec, apivalidate.Options{Logger: d.Logger})
	if err != nil {
		return nil, fmt.Errorf("request validator: %w", err)
	}
	validated := validator.Middleware(api)
	staff := staffauth.Unconfigured()
	if cfg.StaffAuth != nil {
		sc := *cfg.StaffAuth
		sc.Clock, sc.Logger = d.Clock, d.Logger
		mw, err := staffauth.New(sc)
		if err != nil {
			return nil, fmt.Errorf("wiring: %w", err)
		}
		staff = mw.Wrap(validated)
	}
	// The split uses a ServeMux like the API mux, so both read the path the
	// same way (cleaning, escaped slashes): a request cannot be routed to a
	// staff handler while being authenticated as a member route.
	// The chat WebSocket authenticates in its first frame (never the header),
	// so it sits outside the session middleware and the OpenAPI validator.
	byAudience := http.NewServeMux()
	byAudience.Handle("GET /v1/chat/ws", chatGW)
	byAudience.Handle("/v1/staff/", staff)
	byAudience.Handle("/v1/", sessions.Wrap(validated))
	v1 := ratelimit.Middleware(limiter, clientIP)(byAudience)

	root := http.NewServeMux()
	root.Handle("/auth/", gw)
	root.Handle("/v1/", v1)
	root.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	root.Handle("GET /readyz", readiness(d.Logger, poolCheck(d.Pool)))
	return logRequests(d.Logger, root, api), nil
}

// noopFanout disables live chat delivery when the API has no NATS connection
// (tests that only exercise HTTP).
type noopFanout struct{}

// Publish implements chatport.Fanout and drops the notice.
func (noopFanout) Publish(context.Context, chatmodel.Notice) error { return nil }

// Subscribe implements chatport.Bus and never delivers notices.
func (noopFanout) Subscribe(_ uuid.UUID, _ func(chatmodel.Notice)) (func(), error) {
	return func() {}, nil
}

// newAssistantService builds the assistant service and its LLM (or leaves LLM
// nil for degraded mode when no API key is configured).
func newAssistantService(
	cfg Config, pool *pgxpool.Pool, env *crypto.Envelope,
	privacy *privacyservice.Service, safety *safetyservice.Service,
	members *membershipservice.Service, searcher retrievalSearcher,
	clk clock.Clock, logger *slog.Logger,
) (*assistantservice.Service, error) {
	phones := make([]assistantmodel.CrisisPhone, 0, len(cfg.CrisisPhones))
	for _, p := range cfg.CrisisPhones {
		phones = append(phones, assistantmodel.CrisisPhone{Number: p.Number, Label: p.Label})
	}
	svc := &assistantservice.Service{
		Store:       &assistantadapter.PostgresStore{Pool: pool},
		Sealer:      env,
		Consent:     privacy,
		Crisis:      LexiconCrisis{Lex: cfg.Lexicon},
		Escalations: &CrisisOpener{Safety: safety},
		Profiles:    &AssistantProfiles{Members: members},
		Retriever:   &KnowledgeRetriever{Inner: searcher},
		Clock:       clk,
		Logger:      logger,
		Settings: assistantservice.Settings{
			DailyLimit:     cfg.AssistantDailyLimit,
			TimeZone:       cfg.TimeZone,
			CrisisPhones:   phones,
			NameDictionary: cfg.AssistantNames,
			Prompt:         cfg.AssistantPrompt,
		},
	}
	if cfg.LLMAPIKey == "" {
		logger.Warn("LLM disabled: no API key; every reply will be degraded", "event", "LLMDisabled")
		return svc, nil
	}
	client, err := anthropic.New(anthropic.Config{
		APIKey:  cfg.LLMAPIKey,
		BaseURL: cfg.LLMBaseURL,
		Model:   cfg.LLMModel,
	})
	if err != nil {
		return nil, fmt.Errorf("wiring: llm: %w", err)
	}
	svc.LLM = client
	return svc, nil
}

// mountAPI registers the generated /v1 handlers of every API area on mux,
// with the shared problem+json error handlers.
func mountAPI(mux *http.ServeMux, cfg Config, members *membershipservice.Service,
	privacy *privacyservice.Service, notify *notifyservice.Service,
	safety *safetyservice.Service, wellbeing *wellbeingservice.Service,
	emergency *emergencyservice.Service, assistant *assistantservice.Service,
	knowledge *knowledgeservice.Service, circle *circleservice.Service,
	chat *chatservice.Service, sharing *sharingservice.Service, directory *directoryservice.Service,
	moderation *moderationservice.Service,
	logger *slog.Logger,
) {
	const base = apiBase
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
	safetyapi.HandlerWithOptions(
		safetyapi.NewStrictHandlerWithOptions(&safetyapi.Server{Service: safety}, nil, safetyapi.StrictHTTPServerOptions{
			RequestErrorHandlerFunc: httpapi.RequestErrorHandler, ResponseErrorHandlerFunc: responseErr,
		}),
		safetyapi.StdHTTPServerOptions{
			BaseURL: base, BaseRouter: mux, ErrorHandlerFunc: httpapi.RequestErrorHandler,
			Middlewares: []safetyapi.MiddlewareFunc{toMember},
		},
	)
	wellbeingapi.HandlerWithOptions(
		wellbeingapi.NewStrictHandlerWithOptions(&wellbeingapi.Server{Service: wellbeing}, nil, wellbeingapi.StrictHTTPServerOptions{
			RequestErrorHandlerFunc: httpapi.RequestErrorHandler, ResponseErrorHandlerFunc: responseErr,
		}),
		wellbeingapi.StdHTTPServerOptions{
			BaseURL: base, BaseRouter: mux, ErrorHandlerFunc: httpapi.RequestErrorHandler,
			Middlewares: []wellbeingapi.MiddlewareFunc{toMember},
		},
	)
	emergencyapi.HandlerWithOptions(
		emergencyapi.NewStrictHandlerWithOptions(&emergencyapi.Server{Service: emergency}, nil, emergencyapi.StrictHTTPServerOptions{
			RequestErrorHandlerFunc: httpapi.RequestErrorHandler, ResponseErrorHandlerFunc: responseErr,
		}),
		emergencyapi.StdHTTPServerOptions{
			BaseURL: base, BaseRouter: mux, ErrorHandlerFunc: httpapi.RequestErrorHandler,
			Middlewares: []emergencyapi.MiddlewareFunc{toMember},
		},
	)
	assistantapi.HandlerWithOptions(
		assistantapi.NewStrictHandlerWithOptions(&assistantapi.Server{Service: assistant}, nil, assistantapi.StrictHTTPServerOptions{
			RequestErrorHandlerFunc: httpapi.RequestErrorHandler, ResponseErrorHandlerFunc: responseErr,
		}),
		assistantapi.StdHTTPServerOptions{
			BaseURL: base, BaseRouter: mux, ErrorHandlerFunc: httpapi.RequestErrorHandler,
			Middlewares: []assistantapi.MiddlewareFunc{toMember},
		},
	)
	knowledgeapi.HandlerWithOptions(
		knowledgeapi.NewStrictHandlerWithOptions(&knowledgeapi.Server{Service: knowledge}, nil, knowledgeapi.StrictHTTPServerOptions{
			RequestErrorHandlerFunc: httpapi.RequestErrorHandler, ResponseErrorHandlerFunc: responseErr,
		}),
		knowledgeapi.StdHTTPServerOptions{
			BaseURL: base, BaseRouter: mux, ErrorHandlerFunc: httpapi.RequestErrorHandler,
			Middlewares: []knowledgeapi.MiddlewareFunc{toMember},
		},
	)
	circleapi.HandlerWithOptions(
		circleapi.NewStrictHandlerWithOptions(&circleapi.Server{Service: circle}, nil, circleapi.StrictHTTPServerOptions{
			RequestErrorHandlerFunc: httpapi.RequestErrorHandler, ResponseErrorHandlerFunc: responseErr,
		}),
		circleapi.StdHTTPServerOptions{
			BaseURL: base, BaseRouter: mux, ErrorHandlerFunc: httpapi.RequestErrorHandler,
			Middlewares: []circleapi.MiddlewareFunc{toMember},
		},
	)
	chatapi.HandlerWithOptions(
		chatapi.NewStrictHandlerWithOptions(&chatapi.Server{Service: chat}, nil, chatapi.StrictHTTPServerOptions{
			RequestErrorHandlerFunc: httpapi.RequestErrorHandler, ResponseErrorHandlerFunc: responseErr,
		}),
		chatapi.StdHTTPServerOptions{
			BaseURL: base, BaseRouter: mux, ErrorHandlerFunc: httpapi.RequestErrorHandler,
			Middlewares: []chatapi.MiddlewareFunc{toMember},
		},
	)
	sharingapi.HandlerWithOptions(
		sharingapi.NewStrictHandlerWithOptions(&sharingapi.Server{Service: sharing}, nil, sharingapi.StrictHTTPServerOptions{
			RequestErrorHandlerFunc: httpapi.RequestErrorHandler, ResponseErrorHandlerFunc: responseErr,
		}),
		sharingapi.StdHTTPServerOptions{
			BaseURL: base, BaseRouter: mux, ErrorHandlerFunc: httpapi.RequestErrorHandler,
			Middlewares: []sharingapi.MiddlewareFunc{toMember},
		},
	)
	directoryapi.HandlerWithOptions(
		directoryapi.NewStrictHandlerWithOptions(&directoryapi.Server{Service: directory}, nil, directoryapi.StrictHTTPServerOptions{
			RequestErrorHandlerFunc: httpapi.RequestErrorHandler, ResponseErrorHandlerFunc: responseErr,
		}),
		directoryapi.StdHTTPServerOptions{
			BaseURL: base, BaseRouter: mux, ErrorHandlerFunc: httpapi.RequestErrorHandler,
			Middlewares: []directoryapi.MiddlewareFunc{toMember},
		},
	)
	moderationapi.HandlerWithOptions(
		moderationapi.NewStrictHandlerWithOptions(&moderationapi.Server{Service: moderation}, nil, moderationapi.StrictHTTPServerOptions{
			RequestErrorHandlerFunc: httpapi.RequestErrorHandler, ResponseErrorHandlerFunc: responseErr,
		}),
		moderationapi.StdHTTPServerOptions{BaseURL: base, BaseRouter: mux, ErrorHandlerFunc: httpapi.RequestErrorHandler},
	)
}

// apiBase is the path prefix of every HTTP route.
const apiBase = "/v1"

// memberPrincipal resolves the signed-in member (creating it on first use,
// ADR-0008 addendum) and sets auth.Principal.MemberID for the handlers of
// contexts that key their data by member ID (privacy, notify, safety,
// wellbeing, emergency). The identity ID stays untouched. Anonymous and staff
// requests pass unchanged; the handlers refuse a principal without a MemberID.
func memberPrincipal(members *membershipservice.Service, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := auth.FromContext(r.Context())
			if !ok || p.Staff {
				next.ServeHTTP(w, r)
				return
			}
			m, err := members.Me(r.Context())
			if errors.Is(err, membershipmodel.ErrAccountDeleted) {
				// A session of a deleted account (possibly replayed after the
				// identity was deleted): refused, and no member is created.
				httpapi.WriteProblem(w, http.StatusUnauthorized, "unauthenticated", "Sign-in required.")
				return
			}
			if err != nil {
				if errors.Is(err, membershipmodel.ErrMemberSuspended) {
					if !crisisExempt(strings.TrimPrefix(r.URL.Path, apiBase)) {
						httpapi.WriteProblem(w, http.StatusForbidden, "member_suspended", "Your account is suspended.")
						return
					}
					id, err := members.CrisisMemberID(r.Context())
					if errors.Is(err, membershipmodel.ErrAccountDeleted) {
						// Deleted meanwhile: a deleted account has no crisis exemption.
						httpapi.WriteProblem(w, http.StatusUnauthorized, "unauthenticated", "Sign-in required.")
						return
					}
					if err != nil {
						logger.Error("resolve suspended member", "pattern", r.Pattern, "error", err)
						httpapi.WriteProblem(w, http.StatusInternalServerError, "internal", "Something went wrong.")
						return
					}
					p.MemberID = id
					next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), p)))
					return
				}
				logger.Error("resolve member", "pattern", r.Pattern, "error", err)
				httpapi.WriteProblem(w, http.StatusInternalServerError, "internal", "Something went wrong.")
				return
			}
			p.MemberID = m.ID
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), p)))
		})
	}
}

// crisisExempt reports whether a suspended member may still call a route
// (path without the API base). Suspension stops her reaching other members;
// it never takes away help for herself (non-negotiable 1) or her rights over
// her own data. Allowed: the help request to staff, raising and closing her
// SOS (it goes to her trusted contacts), reading and sending her emergency
// plan, consents and the deletion request. Sharing an SOS to the community
// and answering responses stay closed.
func crisisExempt(path string) bool {
	switch {
	case path == "/me/help-requests",
		path == "/me/sos",
		strings.HasPrefix(path, "/me/sos/") && strings.HasSuffix(path, "/close"),
		path == "/me/emergency-plan", strings.HasPrefix(path, "/me/emergency-plan/"),
		path == "/me/consents", strings.HasPrefix(path, "/me/consents/"),
		path == "/me/deletion-request":
		return true
	}
	return false
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
