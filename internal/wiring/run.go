package wiring

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver for goose
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	birgedb "birge/db"
	assistantadapter "birge/internal/domains/assistant/adapter"
	assistantservice "birge/internal/domains/assistant/service"
	chatadapter "birge/internal/domains/chat/adapter"
	chatservice "birge/internal/domains/chat/service"
	circleadapter "birge/internal/domains/circle/adapter"
	circleservice "birge/internal/domains/circle/service"
	directoryadapter "birge/internal/domains/directory/adapter"
	directoryservice "birge/internal/domains/directory/service"
	emergencyadapter "birge/internal/domains/emergency/adapter"
	emergencyservice "birge/internal/domains/emergency/service"
	knowledgeadapter "birge/internal/domains/knowledge/adapter"
	knowledgeservice "birge/internal/domains/knowledge/service"
	membershipadapter "birge/internal/domains/membership/adapter"
	membershipmodel "birge/internal/domains/membership/model"
	membershipservice "birge/internal/domains/membership/service"
	notifyadapter "birge/internal/domains/notify/adapter"
	notifyservice "birge/internal/domains/notify/service"
	"birge/internal/domains/notify/sms/guard"
	privacyadapter "birge/internal/domains/privacy/adapter"
	privacyservice "birge/internal/domains/privacy/service"
	safetyadapter "birge/internal/domains/safety/adapter"
	safetyservice "birge/internal/domains/safety/service"
	sharingadapter "birge/internal/domains/sharing/adapter"
	"birge/internal/domains/sharing/imageproc"
	sharingservice "birge/internal/domains/sharing/service"
	wellbeingadapter "birge/internal/domains/wellbeing/adapter"
	wellbeingservice "birge/internal/domains/wellbeing/service"
	"birge/internal/platform/clock"
	"birge/internal/platform/crypto"
	platformdb "birge/internal/platform/db"
	"birge/internal/platform/outbox"
	"birge/internal/platform/timers"
	"birge/presets"
)

// JetStream stream of all domain events (ADR-0007). The notify consumer and
// the outbox subjects (outbox.Subject) depend on these names.
const (
	StreamName     = "EVENTS"
	StreamSubjects = "birge.events.>"
	// StreamDuplicates is the Nats-Msg-Id deduplication window. The relay
	// republishes an event only when marking it published failed, which it
	// retries within seconds; two minutes covers that with a margin.
	StreamDuplicates = 2 * time.Minute
)

// Worker loop settings.
const (
	relayBatchSize = 100
	relayInterval  = time.Second
	timersInterval = time.Second
)

// rolesHeader bounds how long the grants may wait for and hold locks (ADR-0006).
const rolesHeader = "SET lock_timeout = '5s';\nSET statement_timeout = '60s';\n"

// Migrate applies the embedded migrations with goose and then, when
// ApplyRoles is set, db/roles.sql. Both steps are idempotent: goose records
// applied versions, and grants repeat without effect. A PostgreSQL session
// lock serializes concurrent runs (several migration jobs starting at once).
// After migrations, membership reference tables are seeded from the DEMO
// preset only when they are empty (never overwrites; logs event=DemoPreset).
func Migrate(ctx context.Context, logger *slog.Logger, cfg Config) error {
	sqlDB, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("migrate: open database: %w", err)
	}
	defer func() { _ = sqlDB.Close() }()
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, birgedb.Migrations(), goose.WithSessionLocker(locker))
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	results, err := p.Up(ctx)
	if err != nil {
		return fmt.Errorf("migrate: apply: %w", err)
	}
	for _, r := range results {
		logger.Info("migration applied", "version", r.Source.Version, "duration_ms", r.Duration.Milliseconds())
	}
	version, err := p.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("migrate: read version: %w", err)
	}
	logger.Info("database migrated", "version", version, "applied", len(results))
	if cfg.ApplyRoles {
		conn, err := sqlDB.Conn(ctx)
		if err != nil {
			return fmt.Errorf("migrate: roles: %w", err)
		}
		defer func() { _ = conn.Close() }()
		// One simple-protocol call: PostgreSQL runs the statements as one implicit transaction.
		if _, err := conn.ExecContext(ctx, rolesHeader+birgedb.Roles); err != nil {
			return fmt.Errorf("migrate: apply db/roles.sql: %w", err)
		}
		logger.Info("roles applied")
	}
	if err := seedDemoReference(ctx, logger, cfg.DatabaseURL); err != nil {
		return err
	}
	if err := seedDemoChat(ctx, logger, cfg.DatabaseURL); err != nil {
		return err
	}
	return seedDemoKnowledge(ctx, logger, cfg.DatabaseURL)
}

// seedDemoReference inserts DEMO cities, districts and needs when every
// membership reference table is empty. Idempotent: a second migrate is a no-op.
func seedDemoReference(ctx context.Context, logger *slog.Logger, databaseURL string) error {
	ref, err := presets.LoadReference()
	if err != nil {
		return fmt.Errorf("migrate: demo reference: %w", err)
	}
	pool, err := platformdb.Open(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("migrate: demo reference: %w", err)
	}
	defer pool.Close()
	seed := membershipmodel.ReferenceSeed{Needs: ref.Needs}
	for _, c := range ref.Cities {
		seed.Cities = append(seed.Cities, membershipmodel.ReferenceCitySeed{Code: c.Code, Districts: c.Districts})
	}
	seeded, err := (&membershipadapter.Postgres{Pool: pool}).SeedReferenceIfEmpty(ctx, seed)
	if err != nil {
		return fmt.Errorf("migrate: demo reference: %w", err)
	}
	if seeded {
		LogDemoPresets(logger, []string{presets.NameReference})
	}
	return nil
}

// seedDemoKnowledge inserts DEMO knowledge topics and articles when the
// knowledge tables are empty. No-op until the knowledge migration is applied
// at merge (schema file exists; migration is generated by the tech lead).
func seedDemoKnowledge(ctx context.Context, logger *slog.Logger, databaseURL string) error {
	k, err := presets.LoadKnowledge()
	if err != nil {
		return fmt.Errorf("migrate: demo knowledge: %w", err)
	}
	pool, err := platformdb.Open(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("migrate: demo knowledge: %w", err)
	}
	defer pool.Close()
	seed := knowledgeadapter.DemoSeed{}
	for _, t := range k.Topics {
		seed.Topics = append(seed.Topics, knowledgeadapter.DemoTopic{
			Kind: t.Kind, Code: t.Code,
			TitleKK: t.Titles["kk"], TitleRU: t.Titles["ru"], TitleEN: t.Titles["en"],
		})
	}
	for _, a := range k.Articles {
		da := knowledgeadapter.DemoArticle{TopicCode: a.TopicCode}
		for _, v := range a.Versions {
			da.Versions = append(da.Versions, knowledgeadapter.DemoVersion{
				Lang: v.Lang, Title: v.Title, Body: v.Body, SourceURL: v.SourceURL,
			})
		}
		seed.Articles = append(seed.Articles, da)
	}
	seeded, err := knowledgeadapter.SeedDemoIfEmpty(ctx, pool, seed, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("migrate: demo knowledge: %w", err)
	}
	if seeded {
		LogDemoPresets(logger, []string{presets.NameKnowledge})
	}
	return nil
}

// NewEnvelope builds the platform envelope for the configured KEK.
func NewEnvelope(pool *pgxpool.Pool, cfg Config, clk clock.Clock) (*crypto.Envelope, error) {
	if cfg.KEK == nil {
		return nil, errors.New("wiring: no KEK configured")
	}
	ring, err := crypto.NewKeyring(cfg.KEK)
	if err != nil {
		return nil, fmt.Errorf("wiring: %w", err)
	}
	env, err := crypto.NewEnvelope(pool, ring, crypto.Options{Clock: clk})
	if err != nil {
		return nil, fmt.Errorf("wiring: %w", err)
	}
	return env, nil
}

// RunAPI serves the API until ctx is cancelled.
func RunAPI(ctx context.Context, logger *slog.Logger, cfg Config) error {
	pool, err := platformdb.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("api: %w", err)
	}
	defer pool.Close()
	clk := clock.System{}
	env, err := NewEnvelope(pool, cfg, clk)
	if err != nil {
		return err
	}
	nc, err := nats.Connect(cfg.NATSURL, nats.Name("birge-api"), nats.MaxReconnects(-1))
	if err != nil {
		return fmt.Errorf("api: nats connect: %w", err)
	}
	defer nc.Close()
	objects, err := NewObjectStore(cfg, logger)
	if err != nil {
		return err
	}
	h, err := NewAPIHandler(ctx, APIDeps{
		Config: cfg, Pool: pool, Envelope: env, Clock: clk, Logger: logger,
		NATS: nc, Objects: objects,
	})
	if err != nil {
		return err
	}
	return serve(ctx, logger, cfg.HTTPAddr, h)
}

// RunWorker runs the background loops until ctx is cancelled: the outbox
// relay to JetStream, the notify DeliveryRequested consumer, the emergency
// SosShared district-push consumer and the timers worker with the notify,
// privacy, safety, wellbeing and emergency handlers, plus /healthz and
// /readyz (database and NATS) on the HTTP address. When one loop fails, the
// others are stopped and the error is returned, so the orchestrator restarts
// the process instead of running it half-working.
func RunWorker(ctx context.Context, logger *slog.Logger, cfg Config) error {
	if cfg.DeliveryMode != DeliveryModeDev {
		// Load refuses this already; checked again because silently
		// dropping notifications is the failure this guards against.
		return errors.New("worker: no push or SMS provider configured")
	}
	if cfg.Lexicon == nil {
		return errors.New("worker: crisis lexicon is required")
	}
	pool, err := platformdb.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("worker: %w", err)
	}
	defer pool.Close()
	clk := clock.System{}
	env, err := NewEnvelope(pool, cfg, clk)
	if err != nil {
		return err
	}
	nc, err := nats.Connect(cfg.NATSURL, nats.Name("birge-worker"), nats.MaxReconnects(-1))
	if err != nil {
		return fmt.Errorf("worker: nats connect: %w", err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		return fmt.Errorf("worker: jetstream: %w", err)
	}
	if err := EnsureStream(ctx, js, cfg.EventsMaxAge, cfg.EventsMaxBytes); err != nil {
		return err
	}

	identities, err := NewKratosIdentities(cfg.KratosAdminURL, "phone", nil)
	if err != nil {
		return err
	}
	members := membershipservice.New(&membershipadapter.Postgres{Pool: pool}, DisplayNameEncryptor{Sealer: env}, clk)
	safety := &safetyservice.Service{
		Store:     &safetyadapter.PostgresStore{Pool: pool},
		Sealer:    env,
		Directory: &MemberDirectory{Members: members, Phones: identities},
		Lexicon:   cfg.Lexicon,
		Clock:     clk,
		Logger:    logger,
		Settings: safetyservice.Settings{
			BackupAfter:  cfg.EscalationBackupAfter,
			CrisisPhones: safetyCrisisPhones(cfg.CrisisPhones),
			StaffLang:    cfg.StaffSMSLang,
		},
	}
	if cfg.DeliveryMode == DeliveryModeDev && len(cfg.SMSTexts) == 0 {
		logger.Warn("BIRGE_SMS_TEXTS is empty: every SMS will be skipped (no text)")
	}
	logger.Warn("log-only delivery: push and SMS are logged, not sent", "mode", cfg.DeliveryMode)
	circle := &circleservice.Service{
		Store:   &circleadapter.PostgresStore{Pool: pool},
		Members: &CircleMembers{Members: members},
		Blocks:  &EmergencyBlocks{Members: members},
		Clock:   clk,
		Logger:  logger,
	}
	emergency := &emergencyservice.Service{
		Store:         &emergencyadapter.PostgresStore{Pool: pool},
		Sealer:        env,
		Members:       &EmergencyMembers{Members: members},
		Blocks:        &EmergencyBlocks{Members: members},
		Acquaintances: &CircleMet{Circle: circle},
		Clock:         clk,
		Logger:        logger,
		Settings:      emergencyservice.Settings{AutoCloseAfter: cfg.SosAutoCloseAfter},
	}
	notify := &notifyservice.Service{
		Store: &notifyadapter.PostgresStore{Pool: pool},
		Recipients: &Recipients{
			Members: members, Phones: identities, Staff: safety, Emergency: emergency, StaffLang: cfg.StaffSMSLang,
		},
		Push:   LogPushSender{Logger: logger},
		SMS:    LogSMSSender{Logger: logger},
		Guard:  guard.New(cfg.SMSGuard),
		Clock:  clk,
		Logger: logger,
		Config: notifyservice.Config{CriticalSMSAfter: cfg.CriticalSMSAfter, SMSTexts: cfg.SMSTexts},
	}
	privacy := &privacyservice.Service{
		Store:    &privacyadapter.PostgresStore{Pool: pool},
		Shredder: env,
		Clock:    clk,
		Logger:   logger,
		Settings: privacyservice.Settings{CurrentVersions: cfg.ConsentVersions, DeletionDueAfter: cfg.DeletionDelay},
	}
	wellbeing := &wellbeingservice.Service{
		Store:    &wellbeingadapter.PostgresStore{Pool: pool},
		Sealer:   env,
		Crisis:   &CrisisOpener{Safety: safety},
		Clock:    clk,
		Logger:   logger,
		Settings: cfg.Wellbeing,
	}
	knowledge := &knowledgeservice.Service{
		Store:  &knowledgeadapter.PostgresStore{Pool: pool},
		Clock:  clk,
		Logger: logger,
	}
	assistant := &assistantservice.Service{
		Store:  &assistantadapter.PostgresStore{Pool: pool},
		Sealer: env,
		Clock:  clk,
		Logger: logger,
		Settings: assistantservice.Settings{
			DailyLimit: cfg.AssistantDailyLimit, TimeZone: cfg.TimeZone,
			RetentionDays:  assistantservice.DefaultRetentionDays,
			PurgeBatchSize: assistantservice.DefaultPurgeBatchSize,
		},
	}
	objects, err := NewObjectStore(cfg, logger)
	if err != nil {
		return err
	}
	chat := &chatservice.Service{
		Store:   &chatadapter.PostgresStore{Pool: pool},
		Sealer:  env,
		Members: &ChatMembers{Members: members},
		Blocks:  &EmergencyBlocks{Members: members},
		Mentors: &ChatMentors{Members: members},
		Crisis:  &ChatCrisis{Safety: safety},
		Fanout:  &chatadapter.NATSFanout{Conn: nc, Logger: logger},
		Clock:   clk,
		Logger:  logger,
	}
	if err := chat.EnsureRetentionTimer(ctx); err != nil {
		return fmt.Errorf("worker: chat retention: %w", err)
	}
	sharing := &sharingservice.Service{
		Store:   &sharingadapter.PostgresStore{Pool: pool},
		Members: &SharingMembers{Members: members},
		Blocks:  &EmergencyBlocks{Members: members},
		Objects: objects,
		Images:  imageproc.Processor{},
		Clock:   clk,
		Logger:  logger,
	}
	directory := &directoryservice.Service{
		Store:   &directoryadapter.PostgresStore{Pool: pool},
		Members: &DirectoryMembers{Members: members},
		Clock:   clk,
		Logger:  logger,
	}
	handlers, err := mergeHandlers(
		(&notifyadapter.TimerHandlers{Service: notify}).Handlers(),
		(&privacyadapter.TimerHandlers{Service: privacy}).Handlers(),
		(&safetyadapter.TimerHandlers{Service: safety}).Handlers(),
		(&wellbeingadapter.TimerHandlers{Service: wellbeing}).Handlers(),
		(&emergencyadapter.TimerHandlers{Service: emergency}).Handlers(),
		(&assistantadapter.TimerHandlers{Service: assistant}).Handlers(),
		(&knowledgeadapter.TimerHandlers{Service: knowledge}).Handlers(),
		(&circleadapter.TimerHandlers{Service: circle}).Handlers(),
		(&chatadapter.TimerHandlers{Service: chat}).Handlers(),
		(&sharingadapter.TimerHandlers{Service: sharing}).Handlers(),
	)
	if err != nil {
		return err
	}

	relay := &outbox.Relay{
		Pool: pool, Publisher: outbox.JetStreamPublisher{JS: js}, Clock: clk, Logger: logger,
		BatchSize: relayBatchSize, Interval: relayInterval,
	}
	consumer := &notifyadapter.DeliveryConsumer{JS: js, Service: notify, Logger: logger}
	sosShared := &SosSharedConsumer{
		JS: js, Pool: pool, Members: members, Emergency: emergency, Clock: clk, Logger: logger,
	}
	roomOpener := &chatadapter.RoomOpenerConsumer{JS: js, Stream: StreamName, Service: chat, Logger: logger}
	sharingMod := &sharingadapter.ModerationConsumer{JS: js, Service: sharing, Logger: logger}
	directoryMod := &directoryadapter.ModerationConsumer{JS: js, Service: directory, Logger: logger}
	timerWorker := &timers.Worker{Pool: pool, Clock: clk, Logger: logger, Handlers: handlers, Interval: timersInterval}

	health := http.NewServeMux()
	health.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	health.Handle("GET /readyz", readiness(logger, poolCheck(pool), natsCheck(nc)))

	loops := map[string]func(context.Context) error{
		"outbox relay":         relay.Run,
		"notify consumer":      consumer.Run,
		"sos shared":           sosShared.Run,
		"chat room opener":     roomOpener.Run,
		"sharing moderation":   sharingMod.Run,
		"directory moderation": directoryMod.Run,
		"timers":               timerWorker.Run,
		"health endpoints":     func(ctx context.Context) error { return serve(ctx, logger, cfg.HTTPAddr, health) },
	}
	if !cfg.RetrievalEnabled {
		logger.Warn("retrieval disabled: TEI or Qdrant not configured", "event", "RetrievalDisabled")
	} else {
		_, indexer := NewRetrieval(cfg, pool, js, clk, logger)
		if indexer != nil {
			loops["retrieval indexer"] = indexer.Run
		}
	}
	return runAll(ctx, logger, loops)
}

// EnsureStream creates or updates the events stream: file storage, the
// deduplication window the outbox relies on, and retention (MaxAge/MaxBytes).
// It is safe on every start.
func EnsureStream(ctx context.Context, js jetstream.JetStream, maxAge time.Duration, maxBytes int64) error {
	_, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:       StreamName,
		Subjects:   []string{StreamSubjects},
		Storage:    jetstream.FileStorage,
		Duplicates: StreamDuplicates,
		MaxAge:     maxAge,
		MaxBytes:   maxBytes,
	})
	if err != nil {
		return fmt.Errorf("worker: ensure stream %s: %w", StreamName, err)
	}
	return nil
}

func natsCheck(nc *nats.Conn) check {
	return check{name: "nats", fn: func(context.Context) error {
		if !nc.IsConnected() {
			return errors.New("not connected")
		}
		return nil
	}}
}

// mergeHandlers joins the timer handlers of several contexts; a timer kind
// claimed twice is a wiring bug.
func mergeHandlers(sets ...map[string]timers.Handler) (map[string]timers.Handler, error) {
	out := map[string]timers.Handler{}
	for _, set := range sets {
		for kind, h := range set {
			if _, dup := out[kind]; dup {
				return nil, fmt.Errorf("wiring: timer kind %q has two handlers", kind)
			}
			out[kind] = h
		}
	}
	return out, nil
}

// runAll runs every loop until ctx is cancelled or one of them fails (or
// returns early), then cancels the rest and waits for all of them.
func runAll(ctx context.Context, logger *slog.Logger, loops map[string]func(context.Context) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	for name, run := range loops {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer cancel()
			err := run(ctx)
			if err == nil && ctx.Err() == nil {
				err = errors.New("stopped unexpectedly")
			}
			if err != nil {
				logger.Error("worker loop stopped", "loop", name, "error", err)
				mu.Lock()
				errs = append(errs, fmt.Errorf("%s: %w", name, err))
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}
