package wiring

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	analyticsmodel "birge/internal/domains/analytics/model"
	membershipadapter "birge/internal/domains/membership/adapter"
	membershipservice "birge/internal/domains/membership/service"
	privacyadapter "birge/internal/domains/privacy/adapter"
	privacyservice "birge/internal/domains/privacy/service"
	safetyadapter "birge/internal/domains/safety/adapter"
	safetyservice "birge/internal/domains/safety/service"
	"birge/internal/platform/clock"
	"birge/internal/platform/outbox"
	"birge/internal/platform/testinfra"
	"birge/internal/platform/timers"
)

// deletionPolicy is the account-deletion rule of every table in the
// database (docs/requirements/mvp.md § 10: "Delete my data deletes all of
// the user's rows (SQL check)"). keep is empty when no row may contain the
// deleted member's ID after deletion; otherwise it says why such a row is
// allowed to stay. memberColumns lists the columns that can hold a member
// ID; the information_schema guard below fails when a table or a
// member-like column is not listed here, so a new context cannot forget
// deletion.
var deletionPolicy = map[string]struct {
	keep          string
	memberColumns []string
}{
	"public.goose_db_version": {"", nil}, // migration bookkeeping

	"platform.outbox":           {"event transport: payloads carry IDs only and published rows are purged", nil},
	"platform.processed_events": {"", nil},
	"platform.timers":           {"done timers keep their subject ID (the shredding timer itself is one)", []string{"subject_id"}},
	"platform.audit_log":        {"append-only audit: <context>.member_deleted entries", []string{"actor_id", "object_id"}},
	"platform.member_keys":      {"shredded key tombstone (ADR-0009)", []string{"member_id"}},
	// Staff accounts hold staff, never members: no row may name a member.
	"platform.staff_accounts": {"", []string{"staff_id"}},

	"membership.cities":                {"", nil},
	"membership.districts":             {"", nil},
	"membership.needs":                 {"", nil},
	"membership.members":               {"tombstone: id and identity_id only, refuses the identity", []string{"identity_id"}},
	"membership.children":              {"", []string{"member_id"}},
	"membership.verification_requests": {"", []string{"member_id"}},
	"membership.blocks":                {"", []string{"blocker", "blocked"}},

	"notify.notifications": {"", []string{"recipient_ref"}},
	"notify.deliveries":    {"", nil},
	"notify.devices":       {"", []string{"member_id"}},

	"privacy.consent_history":   {"", []string{"member_id"}},
	"privacy.deletion_requests": {"the deletion request itself", []string{"member_id"}},

	"safety.escalations": {"shell for staff accountability: IDs, status, times, outcome", []string{"member_id", "taken_by"}},
	"safety.duty_shifts": {"", []string{"staff_id"}},

	"wellbeing.schedules": {"", []string{"member_id"}},
	"wellbeing.checkins":  {"", []string{"member_id"}},
	"wellbeing.practices": {"", nil},

	"emergency.sos_requests":     {"", []string{"member_id"}},
	"emergency.sos_responses":    {"", []string{"responder_id"}},
	"emergency.child_cards":      {"", []string{"member_id"}},
	"emergency.trusted_contacts": {"", []string{"owner_id", "contact_member_id"}},
	"emergency.plan_sends":       {"", []string{"member_id"}},
	"emergency.plan_links":       {"", []string{"owner_id"}},

	"assistant.messages": {"", []string{"member_id"}},

	"knowledge.topics":   {"", nil},
	"knowledge.articles": {"", nil},
	"knowledge.versions": {"", []string{"author_id", "reviewer_id"}},

	"circle.tea_requests":  {"tombstone: requests a kept match references", []string{"member_id"}},
	"circle.matches":       {"tombstone: the other member's match history", []string{"member_a", "member_b"}},
	"circle.acquaintances": {"", []string{"member_a", "member_b"}},

	"chat.rooms":             {"tombstone: direct rooms stay for the other member", []string{"member_low", "member_high"}},
	"chat.room_members":      {"", []string{"member_id"}},
	"chat.room_restrictions": {"", []string{"member_id"}},
	"chat.room_mentors":      {"", []string{"member_id", "assigned_by"}},
	"chat.messages":          {"tombstones (body cleared) and messages under legal hold", []string{"author_id"}},
	"chat.reports":           {"", []string{"reporter_id"}},

	"sharing.listings":         {"", []string{"author_id"}},
	"sharing.photo_uploads":    {"", []string{"member_id"}},
	"sharing.contact_requests": {"", []string{"requester_id"}},

	"directory.specialists": {"tombstone: active cards she suggested (about the specialist, not her)", []string{"added_by"}},
	"directory.reviews":     {"", []string{"author_id"}},

	"moderation.items": {"", []string{"decided_by"}},

	// Analytics keeps no member ID at all: facts name pseudonyms
	// (HMAC-SHA256, bytea), which checkTombstonesAndOthers checks directly.
	"analytics.facts":           {"", nil},
	"analytics.weekly_rollups":  {"", nil},
	"analytics.erased_actors":   {"her pseudonym, so her late events are not counted (ADR-0024)", nil},
	"analytics.erased_subjects": {"pseudonyms of her SOS, escalations and answers, so late facts about them are not counted (ADR-0024)", nil},
}

// memberColumnPattern matches column names that can hold a member (or
// person) ID. A new column of that shape must be listed in deletionPolicy.
const memberColumnPattern = `(member|author|owner|responder|requester|reporter|added_by|blocker|blocked|recipient_ref|identity|actor|subject|staff|taken_by|decided_by|reviewer|assigned_by)`

// appSchemas are the schemas the check covers: every schema that is not PostgreSQL's own.
const appSchemasSQL = `table_schema NOT IN ('pg_catalog', 'information_schema') AND table_schema NOT LIKE 'pg_%'`

type deletedKratos struct {
	mu      sync.Mutex
	deletes []string
}

// fakeKratosWithDelete answers the admin identity lookup and records
// DELETE /admin/identities/{id} (204 the first time, 404 afterwards).
func fakeKratosWithDelete(t *testing.T, identity uuid.UUID) (*httptest.Server, *deletedKratos) {
	t.Helper()
	rec := &deletedKratos{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/identities/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":%q,"traits":{"phone":%q}}`, r.PathValue("id"), testPhone)
	})
	mux.HandleFunc("DELETE /admin/identities/{id}", func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		id := r.PathValue("id")
		if id != identity.String() || slices.Contains(rec.deletes, id) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		rec.deletes = append(rec.deletes, id)
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, rec
}

func (k *deletedKratos) count() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.deletes)
}

// recordingObjects is an object store that records deleted keys.
type recordingObjects struct {
	mu      sync.Mutex
	deleted []string
}

func (o *recordingObjects) PresignPut(context.Context, string, string, int64, time.Duration) (string, error) {
	return "", fmt.Errorf("not used")
}
func (o *recordingObjects) PresignGet(context.Context, string, time.Duration) (string, error) {
	return "", fmt.Errorf("not used")
}
func (o *recordingObjects) Get(context.Context, string) ([]byte, error) {
	return nil, fmt.Errorf("not used")
}
func (o *recordingObjects) Put(context.Context, string, []byte, string) error {
	return fmt.Errorf("not used")
}
func (o *recordingObjects) Delete(_ context.Context, key string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.deleted = append(o.deleted, key)
	return nil
}
func (o *recordingObjects) keys() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := slices.Clone(o.deleted)
	sort.Strings(out)
	return out
}

// deletionSeed holds the IDs of the rows seeded for the deleted member (her)
// and another member (other), so the test can check what went and what stayed.
type deletionSeed struct {
	her, other, district                uuid.UUID
	herTimerSubjects                    []uuid.UUID
	otherNotification, otherMessage     uuid.UUID
	heldMessage, otherListing, otherSos uuid.UUID
	keptSpecialist, otherReview, match  uuid.UUID
	directRoom, herUpload, herListing   uuid.UUID
	analyticsKey, otherAnswerSubject    []byte
}

func seedEveryContext(t *testing.T, ctx context.Context, pool *pgxpool.Pool, her uuid.UUID) deletionSeed {
	t.Helper()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed: %v\n%s", err, sql)
		}
	}
	id := func() uuid.UUID { return uuid.Must(uuid.NewV7()) }
	sealed := []byte{0x01, 0x02, 0x03} // stands in for ciphertext; never opened
	s := deletionSeed{her: her, other: id()}
	if err := pool.QueryRow(ctx, `SELECT id FROM membership.districts ORDER BY code LIMIT 1`).Scan(&s.district); err != nil {
		t.Fatalf("demo district: %v", err)
	}
	staff := id()
	later := time.Now().UTC().Add(time.Hour)

	// membership
	exec(`INSERT INTO membership.members (id, identity_id, district_id, verification) VALUES ($1, $2, $3, 'verified')`, s.other, id(), s.district)
	exec(`UPDATE membership.members SET district_id = $2, last_active_day = '2026-10-27' WHERE id = $1`, her, s.district)
	exec(`INSERT INTO membership.children (member_id, idempotency_key, birth_year, needs) VALUES ($1, $2, 2018, '{}')`, her, id())
	exec(`INSERT INTO membership.verification_requests (member_id) VALUES ($1)`, her)
	exec(`INSERT INTO membership.blocks (blocker, blocked) VALUES ($1, $2), ($2, $1)`, her, s.other)

	// notify: her inbox, a notification to the other member that names her, one that does not
	var herNotification uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO notify.notifications (request_id, recipient_kind, recipient_ref, template, class)
		VALUES ($1, 'member', $2, 'chat.new_message', 'normal') RETURNING id`, id(), her).Scan(&herNotification); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO notify.deliveries (notification_id, channel, status) VALUES ($1, 'push', 'pending')`, herNotification)
	exec(`INSERT INTO notify.notifications (request_id, recipient_kind, recipient_ref, template, params, class)
		VALUES ($1, 'member', $2, 'emergency.plan_sent', jsonb_build_object('owner_id', $3::text), 'normal')`, id(), s.other, her)
	if err := pool.QueryRow(ctx, `INSERT INTO notify.notifications (request_id, recipient_kind, recipient_ref, template, params, class)
		VALUES ($1, 'member', $2, 'chat.new_message', jsonb_build_object('room_id', $3::text), 'normal') RETURNING id`,
		id(), s.other, id()).Scan(&s.otherNotification); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO notify.devices (member_id, device_id, platform, push_token) VALUES ($1, $2, 'android', 'push-token-example')`, her, id())

	// privacy
	exec(`INSERT INTO privacy.consent_history (member_id, type, version, granted) VALUES ($1, 'data_processing', 'demo-1', true)`, her)

	// safety: a closed escalation with a note
	exec(`INSERT INTO safety.escalations (member_id, source, status, district_id, opened_at, taken_by, taken_at, outcome, note, closed_at)
		VALUES ($1, 'menu', 'closed', $2, now(), $3, now(), 'contacted', $4, now())`, her, s.district, staff, sealed)

	// wellbeing
	exec(`INSERT INTO wellbeing.schedules (member_id, weekday, hour) VALUES ($1, 1, 10)`, her)
	exec(`INSERT INTO wellbeing.checkins (id, member_id, questionnaire_version, answers, phq4, pss4, flag, idempotency_key, completed_at)
		VALUES ($1, $2, 'v1', $3, 1, 1, 'none', $4, now())`, id(), her, sealed, id())

	// emergency
	herSos := id()
	s.otherSos = id()
	exec(`INSERT INTO emergency.sos_requests (id, member_id, idempotency_key, type, content, created_at, closes_at)
		VALUES ($1, $2, $3, 'other', $4, now(), $5), ($6, $7, $8, 'other', $4, now(), $5)`,
		herSos, her, id(), sealed, later, s.otherSos, s.other, id())
	exec(`INSERT INTO emergency.sos_responses (sos_id, responder_id, created_at) VALUES ($1, $2, now()), ($3, $4, now())`,
		herSos, s.other, s.otherSos, her)
	exec(`INSERT INTO emergency.child_cards (member_id, content, updated_at) VALUES ($1, $2, now())`, her, sealed)
	herExternal, herLink := id(), id()
	exec(`INSERT INTO emergency.trusted_contacts (id, owner_id, idempotency_key, kind, name, phone, lang, status, created_at, confirmed_at, plan_sent_at)
		VALUES ($1, $2, $3, 'external', $4, $4, 'ru', 'confirmed', now(), now(), now())`, herExternal, her, id(), sealed)
	exec(`INSERT INTO emergency.plan_links (id, token_hash, contact_id, owner_id, sent_at, created_at, expires_at)
		VALUES ($1, decode(repeat('07', 32), 'hex'), $2, $3, now(), now(), now() + interval '24 hours')`, herLink, herExternal, her)
	exec(`INSERT INTO emergency.trusted_contacts (id, owner_id, idempotency_key, kind, contact_member_id, lang, status, created_at)
		VALUES ($1, $2, $3, 'member', $4, 'ru', 'invited', now())`, id(), s.other, id(), her)
	exec(`INSERT INTO emergency.plan_sends (id, member_id, idempotency_key, contact_count, sent_at) VALUES ($1, $2, $3, 1, now())`, id(), her, id())

	// assistant
	exec(`INSERT INTO assistant.messages (id, member_id, role, text_sealed, idempotency_key, created_at)
		VALUES ($1, $2, 'member', $3, $4, now())`, id(), her, sealed, id())

	// circle: an open unmatched request, a matched pair, an acquaintance
	herOpen, herMatched, otherMatched := id(), id(), id()
	exec(`INSERT INTO circle.tea_requests (id, member_id, idempotency_key, format, same_district, similar_age, shared_needs, status, created_at, expires_at)
		VALUES ($1, $2, $3, 'call', false, false, false, 'open', now(), $4),
		       ($5, $2, $6, 'call', false, false, false, 'matched', now(), $4),
		       ($7, $8, $9, 'call', false, false, false, 'matched', now(), $4)`,
		herOpen, her, id(), later, herMatched, id(), otherMatched, s.other, id())
	lo, hi, reqLo, reqHi := her, s.other, herMatched, otherMatched
	if hi.String() < lo.String() {
		lo, hi, reqLo, reqHi = hi, lo, reqHi, reqLo
	}
	s.match = id()
	exec(`INSERT INTO circle.matches (id, member_a, member_b, request_a_id, request_b_id, format, status, created_at)
		VALUES ($1, $2, $3, $4, $5, 'call', 'proposed', now())`, s.match, lo, hi, reqLo, reqHi)
	exec(`INSERT INTO circle.acquaintances (member_a, member_b, since) VALUES ($1, $2, now())`, lo, hi)

	// chat: a direct room with both, a district room with her, a held message
	s.directRoom = id()
	groupRoom := id()
	exec(`INSERT INTO chat.rooms (id, kind, origin, origin_id, member_low, member_high, last_seq, created_at)
		VALUES ($1, 'direct', 'circle.match', $2, $3, $4, 3, now())`, s.directRoom, s.match, lo, hi)
	exec(`INSERT INTO chat.rooms (id, kind, district_id, title_key, last_seq, created_at)
		VALUES ($1, 'district', $2, 'chat.room.district', 1, now())`, groupRoom, id())
	exec(`INSERT INTO chat.room_members (room_id, member_id, joined_at) VALUES ($1, $2, now()), ($1, $3, now()), ($4, $2, now())`,
		s.directRoom, her, s.other, groupRoom)
	herMessage, groupMessage := id(), id()
	s.otherMessage, s.heldMessage = id(), id()
	exec(`INSERT INTO chat.messages (id, room_id, seq, author_id, client_id, body, status, legal_hold, created_at, updated_at) VALUES
		($1, $2, 1, $3, $4, $5, 'visible', false, now(), now()),
		($6, $2, 2, $7, $8, $5, 'visible', false, now(), now()),
		($9, $2, 3, $3, $10, $5, 'hidden', true, now(), now()),
		($11, $12, 1, $3, $13, $5, 'visible', false, now(), now())`,
		herMessage, s.directRoom, her, id(), sealed, s.otherMessage, s.other, id(), s.heldMessage, id(), groupMessage, groupRoom, id())
	exec(`INSERT INTO chat.reports (id, message_id, room_id, reporter_id, reason, created_at) VALUES
		($1, $2, $3, $4, 'spam', now()), ($5, $6, $3, $7, 'spam', now())`,
		id(), s.otherMessage, s.directRoom, her, id(), herMessage, s.other)
	exec(`INSERT INTO chat.room_restrictions (room_id, member_id, muted_until, updated_at) VALUES ($1, $2, $3, now())`, groupRoom, her, later)
	exec(`INSERT INTO chat.room_mentors (room_id, member_id, assigned_by, assigned_at) VALUES ($1, $2, $3, now())`, groupRoom, her, staff)

	// sharing: her listing with a processed photo and a request on it; her request on another listing
	s.herUpload, s.herListing, s.otherListing = id(), id(), id()
	exec(`INSERT INTO sharing.photo_uploads (id, member_id, object_key, content_type, size_bytes, expires_at, status, processed_object_key, used_at, created_at)
		VALUES ($1, $2, $3, 'image/jpeg', 100, $4, 'processed', $5, now(), now())`,
		s.herUpload, her, "sharing/uploads/"+s.herUpload.String(), later, "sharing/listings/"+s.herListing.String()+".jpg")
	exec(`INSERT INTO sharing.listings (id, author_id, idempotency_key, type, category, title, description, district_id,
			photo_upload_id, photo_object_key, status, approved_at, closes_at, created_at)
		VALUES ($1, $2, $3, 'give', 'toys', 'Example item', 'Example description', $4, $5, $6, 'active', now(), $7, now())`,
		s.herListing, her, id(), s.district, s.herUpload, "sharing/listings/"+s.herListing.String()+".jpg", later)
	exec(`INSERT INTO sharing.listings (id, author_id, idempotency_key, type, category, title, description, district_id, status, created_at)
		VALUES ($1, $2, $3, 'need', 'toys', 'Other item', 'Other description', $4, 'pending', now())`, s.otherListing, s.other, id(), s.district)
	exec(`INSERT INTO sharing.contact_requests (id, listing_id, requester_id, status, created_at) VALUES ($1, $2, $3, 'pending', now()), ($4, $5, $6, 'pending', now())`,
		id(), s.herListing, s.other, id(), s.otherListing, her)

	// directory: a pending suggestion (deleted), an active card others reviewed (kept), her review
	pending, otherSpecialist := id(), id()
	s.keptSpecialist, s.otherReview = id(), id()
	herReview := id()
	exec(`INSERT INTO directory.specialists (id, added_by, idempotency_key, name, profession, district_id, status, created_at) VALUES
		($1, $2, $3, 'Example Specialist', 'speech_therapist', $4, 'pending', now()),
		($5, $2, $6, 'Example Kept', 'speech_therapist', $4, 'active', now()),
		($7, $8, $9, 'Example Other', 'speech_therapist', $4, 'active', now())`,
		pending, her, id(), s.district, s.keptSpecialist, id(), otherSpecialist, s.other, id())
	exec(`INSERT INTO directory.reviews (id, specialist_id, author_id, rating, content, status, created_at, updated_at) VALUES
		($1, $2, $3, 5, 'Example review', 'active', now(), now()), ($4, $5, $6, 4, 'Her review', 'pending', now(), now())`,
		s.otherReview, s.keptSpecialist, s.other, herReview, otherSpecialist, her)

	// moderation: an item about her review holds only IDs and codes
	exec(`INSERT INTO moderation.items (id, kind, object_id, created_at) VALUES ($1, 'review', $2, now())`, id(), herReview)

	// analytics: her active day, her SOS and its closing (no actor), her
	// rights answer and its rating (no actor), and the other member's active
	// day, rights answer and rating
	s.analyticsKey = bytes.Repeat([]byte{0x5a}, 32)
	herActor, otherActor := analyticsmodel.Pseudonym(s.analyticsKey, her), analyticsmodel.Pseudonym(s.analyticsKey, s.other)
	herSosSubject := analyticsmodel.SubjectPseudonym(s.analyticsKey, herSos)
	exec(`INSERT INTO analytics.facts (id, event_type, occurred_at, actor) VALUES ($1, 'membership.MemberActiveDay', now(), $2), ($3, 'membership.MemberActiveDay', now(), $4)`,
		id(), herActor, id(), otherActor)
	exec(`INSERT INTO analytics.facts (id, event_type, occurred_at, code, actor, subject) VALUES ($1, 'emergency.SosRaised', now(), 'other', $2, $3)`,
		id(), herActor, herSosSubject)
	exec(`INSERT INTO analytics.facts (id, event_type, occurred_at, subject) VALUES ($1, 'emergency.SosClosed', now(), $2)`, id(), herSosSubject)
	herAnswer, otherAnswer := analyticsmodel.SubjectPseudonym(s.analyticsKey, id()), analyticsmodel.SubjectPseudonym(s.analyticsKey, id())
	s.otherAnswerSubject = otherAnswer
	exec(`INSERT INTO analytics.facts (id, event_type, occurred_at, code, actor, subject) VALUES
		($1, 'assistant.QuestionAnswered', now(), 'rights_knowledge', $2, $3), ($4, 'assistant.QuestionAnswered', now(), 'rights_knowledge', $5, $6)`,
		id(), herActor, herAnswer, id(), otherActor, otherAnswer)
	exec(`INSERT INTO analytics.facts (id, event_type, occurred_at, code, subject) VALUES
		($1, 'assistant.ReplyRated', now(), 'helped', $2), ($3, 'assistant.ReplyRated', now(), 'helped', $4)`,
		id(), herAnswer, id(), otherAnswer)

	// Pending timers of her and her records (far in the future: only cancelling may end them).
	for _, tm := range []struct {
		kind    string
		subject uuid.UUID
	}{
		{"notify.Push", herNotification}, {"wellbeing.Reminder", her}, {"emergency.SosAutoClose", herSos}, {"emergency.PlanLinkPurge", herLink},
		{"circle.RequestExpire", herOpen}, {"circle.AskMet", s.match}, {"chat.PushFanout", herMessage},
		{"sharing.AutoClose", s.herListing},
	} {
		exec(`INSERT INTO platform.timers (kind, subject_id, dedup_key, due_at) VALUES ($1, $2, $3, $4)`,
			tm.kind, tm.subject, tm.subject.String(), later)
		s.herTimerSubjects = append(s.herTimerSubjects, tm.subject)
	}
	return s
}

// TestAccountDeletionEveryContext runs the whole deletion on PostgreSQL and
// JetStream: a member with data in every context requests deletion, the
// relay publishes privacy.DataDeletionRequested, every context's consumer
// erases her rows, the timers delete the Kratos identity and the photo
// objects and shred the key. Then a SQL check over every schema finds her ID
// only where deletionPolicy allows it, and a repeated deletion changes nothing.
func TestAccountDeletionEveryContext(t *testing.T) {
	e := newE2E(t)
	ctx := context.Background()
	logs := &syncBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))

	env := e.env
	env["BIRGE_DELETION_DELAY"] = "2s"
	apiCfg, err := load(env, e.files, CommandAPI)
	if err != nil {
		t.Fatal(err)
	}
	api := e.handler(apiCfg)

	rec := do(t, api, http.MethodPatch, "/v1/me", e2eToken, `{"displayName":"`+e2eDisplayName+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /v1/me: %d %s", rec.Code, rec.Body)
	}
	var me struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil || me.ID == uuid.Nil {
		t.Fatalf("me body %s: %v", rec.Body, err)
	}
	seed := seedEveryContext(t, ctx, e.pool, me.ID)
	if n := countRowsWith(t, ctx, e.pool, "membership.children", me.ID); n != 1 {
		t.Fatalf("seed check: children rows = %d", n)
	}

	if rec := do(t, api, http.MethodPost, "/v1/me/deletion-request", e2eToken, `{"confirm":"DELETE"}`); rec.Code != http.StatusAccepted {
		t.Fatalf("POST /v1/me/deletion-request: %d %s", rec.Code, rec.Body)
	}

	// The worker side: relay, the eleven deletion consumers and the timers
	// (privacy shred, membership identity, sharing objects), as RunWorker wires them.
	kratos, kratosDeletes := fakeKratosWithDelete(t, e.identity)
	identities, err := NewKratosIdentities(kratos.URL, "phone", nil)
	if err != nil {
		t.Fatal(err)
	}
	objects := &recordingObjects{}
	nc, err := nats.Connect(testinfra.NATS(t))
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureStream(ctx, js, defaultEventsMaxAge, defaultEventsMaxBytes); err != nil {
		t.Fatal(err)
	}
	clk := clock.System{}
	deleters := NewDeleters(e.pool, objects, identities, seed.analyticsKey, clk, logger)
	consumers := deleters.Consumers(js, e.pool, logger)
	if len(consumers) != 11 {
		t.Fatalf("consumers = %d, want 11", len(consumers))
	}
	privacy := &privacyservice.Service{
		Store: &privacyadapter.PostgresStore{Pool: e.pool}, Shredder: e.envelope, Clock: clk, Logger: logger,
		Settings: privacyservice.Settings{CurrentVersions: apiCfg.ConsentVersions, DeletionDueAfter: apiCfg.DeletionDelay},
	}
	handlers, err := mergeHandlers(
		(&privacyadapter.TimerHandlers{Service: privacy}).Handlers(),
		deleters.TimerHandlers(),
	)
	if err != nil {
		t.Fatal(err)
	}
	loops := map[string]func(context.Context) error{
		"relay": (&outbox.Relay{Pool: e.pool, Publisher: outbox.JetStreamPublisher{JS: js}, Clock: clk, Logger: logger,
			BatchSize: relayBatchSize, Interval: 100 * time.Millisecond}).Run,
		"timers": (&timers.Worker{Pool: e.pool, Clock: clk, Logger: logger, Handlers: handlers, Interval: 100 * time.Millisecond}).Run,
	}
	for _, c := range consumers {
		loops[c.Durable()] = c.Run
	}
	wctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- runAll(wctx, logger, loops) }()

	var eventID uuid.UUID
	waitFor(t, done, 40*time.Second, func() bool {
		var processed, completed int
		_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM platform.processed_events WHERE consumer LIKE '%.Deletion'`).Scan(&processed)
		_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM privacy.deletion_requests WHERE member_id = $1 AND completed_at IS NOT NULL`, me.ID).Scan(&completed)
		var pendingTimers int
		_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM platform.timers
			WHERE kind IN ('membership.DeleteIdentity', 'sharing.DeleteObjects', 'privacy.DeletionDue') AND done_at IS NULL`).Scan(&pendingTimers)
		return processed == len(consumers) && completed == 1 && pendingTimers == 0 && kratosDeletes.count() == 1
	})
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("worker loops: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("worker loops did not stop")
	}
	if err := e.pool.QueryRow(ctx, `SELECT DISTINCT event_id FROM platform.processed_events WHERE consumer LIKE '%.Deletion'`).Scan(&eventID); err != nil {
		t.Fatal(err)
	}

	checkInformationSchemaCovered(t, ctx, e.pool)
	checkNoRowNamesMember(t, ctx, e.pool, me.ID)
	checkTombstonesAndOthers(t, ctx, e.pool, seed, me.ID, e.identity)

	wantKeys := []string{"sharing/listings/" + seed.herListing.String() + ".jpg", "sharing/uploads/" + seed.herUpload.String()}
	if got := objects.keys(); !slices.Equal(got, wantKeys) {
		t.Errorf("deleted objects = %v, want %v", got, wantKeys)
	}

	// The session of the deleted account is refused and never makes a new member.
	if rec := do(t, api, http.MethodGet, "/v1/me", e2eToken, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /v1/me after deletion: %d %s, want 401", rec.Code, rec.Body)
	}
	if rec := do(t, api, http.MethodGet, "/v1/me/inbox", e2eToken, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /v1/me/inbox after deletion: %d %s, want 401", rec.Code, rec.Body)
	}
	var identityRows int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM membership.members WHERE identity_id = $1`, e.identity).Scan(&identityRows); err != nil || identityRows != 1 {
		t.Errorf("members of the identity = %d, %v; want the tombstone only", identityRows, err)
	}

	// Idempotency: redelivering the same event and running every DeleteMember
	// and the shred timer again change no row anywhere.
	before := databaseSnapshot(t, ctx, e.pool)
	body, err := json.Marshal(outbox.Envelope{ID: eventID, Type: "privacy.DataDeletionRequested", Version: 1,
		OccurredAt: time.Now().UTC(), Data: json.RawMessage(fmt.Sprintf(`{"member_id":%q,"due_at":%q}`, me.ID, time.Now().UTC().Format(time.RFC3339)))})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range consumers {
		if err := c.HandleMessage(ctx, body); err != nil {
			t.Fatalf("redelivery to %s: %v", c.Processed(), err)
		}
		if err := c.Deleter.DeleteMember(ctx, me.ID); err != nil {
			t.Fatalf("repeated DeleteMember in %s: %v", c.Context, err)
		}
	}
	if err := privacy.HandleDeletionDue(ctx, me.ID); err != nil {
		t.Fatal(err)
	}
	after := databaseSnapshot(t, ctx, e.pool)
	for table := range deletionPolicy {
		if before[table] != after[table] {
			t.Errorf("repeated deletion changed %s", table)
		}
	}

	// A late chat crisis retry (chat.CrisisEscalation keeps running) still
	// opens an escalation, but copies no district: she is gone from membership.
	members := membershipservice.New(&membershipadapter.Postgres{Pool: e.pool}, DisplayNameEncryptor{Sealer: e.envelope}, clk)
	safety := &safetyservice.Service{
		Store: &safetyadapter.PostgresStore{Pool: e.pool}, Sealer: e.envelope,
		Directory: &MemberDirectory{Members: members, Phones: identities}, Clock: clk, Logger: logger,
		Settings: safetyservice.Settings{BackupAfter: apiCfg.EscalationBackupAfter, StaffLang: apiCfg.StaffSMSLang},
	}
	lateID, _, err := safety.OpenEscalation(ctx, me.ID, "chat")
	if err != nil {
		t.Fatalf("late crisis escalation: %v", err)
	}
	var lateDistrict *uuid.UUID
	if err := e.pool.QueryRow(ctx, `SELECT district_id FROM safety.escalations WHERE id = $1`, lateID).Scan(&lateDistrict); err != nil || lateDistrict != nil {
		t.Errorf("late escalation district = %v, %v; want none", lateDistrict, err)
	}
	// moderation.Decided with block_author for her is a no-op, not an error
	// that chat would redeliver forever; the tombstone stays unsuspended.
	if err := members.SuspendAuthor(ctx, me.ID, "spam", uuid.New()); err != nil {
		t.Errorf("SuspendAuthor of a deleted member: %v", err)
	}
	var suspended int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM membership.members WHERE id = $1 AND suspended_at IS NOT NULL`, me.ID).Scan(&suspended); err != nil || suspended != 0 {
		t.Errorf("deleted member suspended = %d, %v", suspended, err)
	}

	out := logs.String()
	for _, secret := range []string{e2eDisplayName, "Example item", "Example review", testPhone} {
		if strings.Contains(out, secret) {
			t.Errorf("logs contain %q", secret)
		}
	}
	if !strings.Contains(out, `"event":"DeletionSkippedLegalHold"`) || !strings.Contains(out, seed.heldMessage.String()) {
		t.Errorf("legal hold skip not logged with the message ID")
	}
}

func countRowsWith(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string, memberID uuid.UUID) int {
	t.Helper()
	var n int
	// The table name comes from information_schema / deletionPolicy, never from input.
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+table+` t WHERE t::text LIKE '%' || $1 || '%'`, memberID.String()).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// checkInformationSchemaCovered fails when a table or a member-like column
// exists that deletionPolicy does not know.
func checkInformationSchemaCovered(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT table_schema || '.' || table_name FROM information_schema.tables
		WHERE table_type = 'BASE TABLE' AND `+appSchemasSQL+` ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	tables := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables[name] = true
		if _, ok := deletionPolicy[name]; !ok {
			t.Errorf("table %s has no account-deletion rule: add it to deletionPolicy and to the context's deletion", name)
		}
	}
	rows.Close()
	for name := range deletionPolicy {
		if !tables[name] {
			t.Errorf("deletionPolicy lists %s, which does not exist", name)
		}
	}

	cols, err := pool.Query(ctx, `SELECT table_schema || '.' || table_name, column_name FROM information_schema.columns
		WHERE data_type = 'uuid' AND column_name ~ $1 AND `+appSchemasSQL+` ORDER BY 1, 2`, memberColumnPattern)
	if err != nil {
		t.Fatal(err)
	}
	defer cols.Close()
	for cols.Next() {
		var table, column string
		if err := cols.Scan(&table, &column); err != nil {
			t.Fatal(err)
		}
		if p, ok := deletionPolicy[table]; ok && !slices.Contains(p.memberColumns, column) {
			t.Errorf("member-like column %s.%s is not covered by deletionPolicy", table, column)
		}
	}
}

// checkNoRowNamesMember: no row of a table without a keep rule contains the member ID, in any column.
func checkNoRowNamesMember(t *testing.T, ctx context.Context, pool *pgxpool.Pool, memberID uuid.UUID) {
	t.Helper()
	for table, p := range deletionPolicy {
		if p.keep != "" {
			continue
		}
		if n := countRowsWith(t, ctx, pool, table, memberID); n != 0 {
			t.Errorf("%s: %d rows still contain the deleted member's ID", table, n)
		}
	}
}

func checkTombstonesAndOthers(t *testing.T, ctx context.Context, pool *pgxpool.Pool, s deletionSeed, her, identity uuid.UUID) {
	t.Helper()
	expect := func(what, sql string, want int, args ...any) {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if n != want {
			t.Errorf("%s = %d, want %d", what, n, want)
		}
	}
	expect("member tombstone", `SELECT count(*) FROM membership.members WHERE id = $1 AND identity_id = $2
		AND display_name IS NULL AND district_id IS NULL AND cardinality(roles) = 0 AND last_active_day IS NULL
		AND deleted_at IS NOT NULL`, 1, her, identity)
	expect("shredded key", `SELECT count(*) FROM platform.member_keys WHERE member_id = $1 AND destroyed_at IS NOT NULL`, 1, her)
	expect("escalation shell", `SELECT count(*) FROM safety.escalations WHERE member_id = $1 AND note IS NULL
		AND district_id IS NULL AND outcome = 'contacted' AND status = 'closed'`, 1, her)
	expect("purged messages", `SELECT count(*) FROM chat.messages WHERE author_id = $1 AND body IS NULL AND purged_at IS NOT NULL`, 2, her)
	expect("held message untouched", `SELECT count(*) FROM chat.messages WHERE id = $1 AND body IS NOT NULL AND purged_at IS NULL`, 1, s.heldMessage)
	expect("other member's message", `SELECT count(*) FROM chat.messages WHERE id = $1 AND body IS NOT NULL`, 1, s.otherMessage)
	expect("direct room kept", `SELECT count(*) FROM chat.rooms WHERE id = $1`, 1, s.directRoom)
	expect("match tombstone", `SELECT count(*) FROM circle.matches WHERE id = $1 AND status = 'expired'`, 1, s.match)
	expect("her open request deleted, matched one kept", `SELECT count(*) FROM circle.tea_requests WHERE member_id = $1`, 1, her)
	expect("kept specialist", `SELECT count(*) FROM directory.specialists WHERE id = $1`, 1, s.keptSpecialist)
	expect("other member's review", `SELECT count(*) FROM directory.reviews WHERE id = $1`, 1, s.otherReview)
	expect("other member's notification", `SELECT count(*) FROM notify.notifications WHERE id = $1`, 1, s.otherNotification)
	expect("other member's listing", `SELECT count(*) FROM sharing.listings WHERE id = $1`, 1, s.otherListing)
	expect("other member's sos", `SELECT count(*) FROM emergency.sos_requests WHERE id = $1`, 1, s.otherSos)
	expect("other member", `SELECT count(*) FROM membership.members WHERE id = $1 AND cardinality(roles) > 0`, 1, s.other)
	expect("pending timers of her records", `SELECT count(*) FROM platform.timers WHERE subject_id = ANY($1) AND done_at IS NULL`, 0, s.herTimerSubjects)
	herActor := analyticsmodel.Pseudonym(s.analyticsKey, her)
	expect("her analytics facts", `SELECT count(*) FROM analytics.facts WHERE actor = $1`, 0, herActor)
	expect("her closed SOS fact", `SELECT count(*) FROM analytics.facts WHERE event_type = 'emergency.SosClosed'`, 0)
	expect("her rating (joined through her answer)", `SELECT count(*) FROM analytics.facts WHERE event_type = 'assistant.ReplyRated' AND subject IS DISTINCT FROM $1`, 0, s.otherAnswerSubject)
	expect("other member's rating", `SELECT count(*) FROM analytics.facts WHERE event_type = 'assistant.ReplyRated' AND subject = $1`, 1, s.otherAnswerSubject)
	expect("other member's analytics facts", `SELECT count(*) FROM analytics.facts WHERE actor = $1`, 2, analyticsmodel.Pseudonym(s.analyticsKey, s.other))
	expect("her erased pseudonym", `SELECT count(*) FROM analytics.erased_actors WHERE actor = $1`, 1, herActor)
	expect("her erased subjects (SOS, rights answer)", `SELECT count(*) FROM analytics.erased_subjects`, 2)
	expect("audit entries", `SELECT count(DISTINCT action) FROM platform.audit_log
		WHERE object_type = 'member' AND object_id = $1 AND action LIKE '%.member_deleted' AND actor_role = 'system'`, 12, her)
	expect("one audit entry per context", `SELECT count(*) FROM platform.audit_log
		WHERE object_type = 'member' AND object_id = $1 AND action LIKE '%.member_deleted'`, 12, her)
}

// databaseSnapshot returns an md5 of every table's rows, by table.
func databaseSnapshot(t *testing.T, ctx context.Context, pool *pgxpool.Pool) map[string]string {
	t.Helper()
	out := make(map[string]string, len(deletionPolicy))
	for table := range deletionPolicy {
		var sum *string
		if err := pool.QueryRow(ctx, `SELECT md5(string_agg(t::text, '|' ORDER BY t::text)) FROM `+table+` t`).Scan(&sum); err != nil {
			t.Fatalf("snapshot %s: %v", table, err)
		}
		if sum != nil {
			out[table] = *sum
		}
	}
	return out
}
