package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/analytics/model"
	"birge/internal/domains/analytics/port"
	"birge/internal/domains/analytics/service"
	"birge/internal/platform/auth"
)

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

// fakeStore answers every report query with canned counts and records
// audit entries, processed events and facts.
type fakeStore struct {
	err       error
	auditErr  error
	audits    []port.AuditEntry
	processed map[uuid.UUID]bool
	facts     []model.Fact
	counts    port.SosCounts
	rights    port.RightsCounts
	active    map[time.Time]int
	// erasedBefore is the last erasedBefore given to RollUpAndDelete.
	erasedBefore time.Time
}

func newFakeStore() *fakeStore { return &fakeStore{processed: map[uuid.UUID]bool{}} }

func (s *fakeStore) InTx(_ context.Context, fn func(port.Tx) error) error {
	if s.err != nil {
		return s.err
	}
	return fn(s)
}
func (s *fakeStore) MarkProcessed(_ context.Context, _ string, id uuid.UUID) (bool, error) {
	if s.processed[id] {
		return false, nil
	}
	s.processed[id] = true
	return true, nil
}
func (s *fakeStore) InsertFact(_ context.Context, f model.Fact) error {
	s.facts = append(s.facts, f)
	return nil
}
func (s *fakeStore) CountFacts(context.Context, time.Time, time.Time, []string) (map[string]port.FactCounts, error) {
	return map[string]port.FactCounts{model.EventTeaRequested: {Facts: 7, Subjects: 7, Members: 5}}, nil
}
func (s *fakeStore) ActiveMembers(context.Context, string, string, time.Time, time.Time) (map[time.Time]int, error) {
	return s.active, nil
}
func (s *fakeStore) Retention(context.Context, time.Time, time.Time, time.Time) (port.RetentionCounts, error) {
	return port.RetentionCounts{Eligible4w: 10, Retained4w: 4, Eligible8w: 4, Retained8w: 4}, nil
}
func (s *fakeStore) SosOutcomes(context.Context, time.Time, time.Time) (port.SosCounts, error) {
	return s.counts, nil
}
func (s *fakeStore) EscalationOutcomes(context.Context, time.Time, time.Time, time.Duration) (port.EscalationCounts, error) {
	return port.EscalationCounts{Opened: 6, Members: 5, TakenWithin: 3}, nil
}
func (s *fakeStore) RightsOutcomes(context.Context, time.Time, time.Time) (port.RightsCounts, error) {
	return s.rights, nil
}
func (s *fakeStore) RollUpAndDelete(_ context.Context, _ string, _, erasedBefore time.Time) (int64, error) {
	s.erasedBefore = erasedBefore
	return 0, nil
}
func (s *fakeStore) ScheduleRetention(context.Context, time.Time) error { return nil }
func (s *fakeStore) Audit(_ context.Context, e port.AuditEntry) error {
	if s.auditErr != nil {
		return s.auditErr
	}
	s.audits = append(s.audits, e)
	return nil
}

type fakeCohorts struct {
	agg model.CohortAggregates
	err error
}

func (f fakeCohorts) CohortAggregates(context.Context, time.Time, time.Time) (model.CohortAggregates, error) {
	return f.agg, f.err
}

var (
	admin = auth.Principal{IdentityID: uuid.MustParse("019a3000-0000-7000-8000-00000000ad01"), Staff: true, Roles: []string{"admin"}, MFA: true}
	from  = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	to    = time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)
)

func newService(store *fakeStore, k int) *service.Service {
	return &service.Service{
		Store: store,
		Cohorts: fakeCohorts{agg: model.CohortAggregates{
			StressBaseline: model.ScoreStats{Count: 6, Mean: 9.5, Median: 10},
			StressWeek8:    model.ScoreStats{Count: 4, Mean: 7, Median: 7},
		}},
		Clock:    fixedClock{t: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)},
		Settings: service.Settings{K: k, TimeZone: "Asia/Almaty", Key: bytes.Repeat([]byte{9}, 32)},
	}
}

// TestReportsDenyByDefault: only staff admin with MFA reads or exports, and
// a refused call writes no audit entry and runs no query.
func TestReportsDenyByDefault(t *testing.T) {
	staffID := uuid.New()
	tests := []struct {
		name string
		p    auth.Principal
		want error
	}{
		{"anonymous", auth.Principal{}, model.ErrUnauthorized},
		{"member", auth.Principal{IdentityID: uuid.New(), MemberID: uuid.New()}, model.ErrUnauthorized},
		{"staff without id", auth.Principal{Staff: true, Roles: []string{"admin"}, MFA: true}, model.ErrUnauthorized},
		{"admin without MFA", auth.Principal{IdentityID: staffID, Staff: true, Roles: []string{"admin"}}, model.ErrForbidden},
		{"moderator", auth.Principal{IdentityID: staffID, Staff: true, Roles: []string{"moderator"}, MFA: true}, model.ErrForbidden},
		{"psychologist", auth.Principal{IdentityID: staffID, Staff: true, Roles: []string{"psychologist"}, MFA: true}, model.ErrForbidden},
		{"lawyer", auth.Principal{IdentityID: staffID, Staff: true, Roles: []string{"lawyer"}, MFA: true}, model.ErrForbidden},
		{"no roles", auth.Principal{IdentityID: staffID, Staff: true, MFA: true}, model.ErrForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore()
			svc := newService(store, 5)
			if _, err := svc.Metrics(context.Background(), tt.p, from, to); !errors.Is(err, tt.want) {
				t.Fatalf("Metrics err = %v, want %v", err, tt.want)
			}
			if _, _, err := svc.MetricsCSV(context.Background(), tt.p, from, to); !errors.Is(err, tt.want) {
				t.Fatalf("MetricsCSV err = %v, want %v", err, tt.want)
			}
			if len(store.audits) != 0 {
				t.Fatalf("refused call audited: %v", store.audits)
			}
		})
	}
}

func TestReportPeriodLimit(t *testing.T) {
	store := newFakeStore()
	svc := newService(store, 5)
	if _, err := svc.Metrics(context.Background(), admin, from, from.AddDate(1, 0, 1)); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("13-month period: err = %v", err)
	}
	if _, err := svc.Metrics(context.Background(), admin, to, from); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("reversed period: err = %v", err)
	}
	if len(store.audits) != 0 {
		t.Fatal("invalid period audited")
	}
	if _, err := svc.Metrics(context.Background(), admin, from, from.AddDate(1, 0, 0)); err != nil {
		t.Fatalf("12-month period: %v", err)
	}
}

func TestMetricsBuiltSuppressedAndAudited(t *testing.T) {
	store := newFakeStore()
	store.counts = port.SosCounts{Raised: 6, RaisedMembers: 5, Closed: 3, Responded: 4, RespondedMembers: 4, MedianMinutesToFirstResponse: 12}
	store.active = map[time.Time]int{time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC): 8}
	store.rights = port.RightsCounts{AnsweredFromKnowledge: 9, KnowledgeMembers: 5, Rated: 4, Helped: 3, RatedMembers: 4}
	svc := newService(store, 5)

	m, err := svc.Metrics(context.Background(), admin, from, to)
	if err != nil {
		t.Fatal(err)
	}
	cell := func(name string, c model.Cell, members int, value float64) {
		t.Helper()
		if c.Suppressed || c.Members != members || c.Value != value {
			t.Errorf("%s = %+v, want members %d value %v", name, c, members, value)
		}
	}
	hidden := func(name string, c model.Cell) {
		t.Helper()
		if !c.Suppressed || c.Members != 0 || c.Value != 0 {
			t.Errorf("%s = %+v, want suppressed", name, c)
		}
	}
	if m.K != 5 || !m.Period.From.Equal(from) || !m.Period.To.Equal(to) {
		t.Fatalf("k %d period %+v", m.K, m.Period)
	}
	cell("stress baseline mean", m.StressBaseline.Mean, 6, 9.5)
	hidden("stress week8 median (4 < k)", m.StressWeek8.Median)
	hidden("isolation before", m.IsolationBefore.Mean)
	cell("retention 4w", m.Retention4w, 10, 0.4)
	hidden("retention 8w (4 < k)", m.Retention8w)
	cell("tea requests", m.TeaRequests, 5, 7)
	hidden("tea pairs", m.TeaPairs)
	cell("sos raised", m.SosRaised, 5, 6)
	cell("sos closed share", m.SosClosedShare, 5, 0.5)
	hidden("sos first response (4 < k)", m.SosFirstResponseMedianMinutes)
	cell("escalations opened", m.EscalationsOpened, 5, 6)
	cell("escalations taken within 15m", m.EscalationsTakenWithin15mShare, 5, 0.5)
	cell("rights answered from knowledge", m.RightsAnsweredFromKnowledge, 5, 9)
	hidden("rights helped share (4 < k)", m.RightsHelpedShare)
	if len(m.ActiveWeekly) != 6 || len(m.ActiveMonthly) != 1 {
		t.Fatalf("buckets: %d weekly, %d monthly", len(m.ActiveWeekly), len(m.ActiveMonthly))
	}
	hidden("week of 23 February (1 March only)", m.ActiveWeekly[0].Cell)
	cell("week of 2 March", m.ActiveWeekly[1].Cell, 8, 8)
	hidden("week of 9 March", m.ActiveWeekly[2].Cell)

	if len(store.audits) != 1 || store.audits[0].Action != service.AuditActionRead ||
		store.audits[0].ActorID != admin.IdentityID || store.audits[0].ActorRole != "admin" ||
		store.audits[0].ObjectType != service.AuditObject {
		t.Fatalf("audit = %+v", store.audits)
	}

	body, name, err := svc.MetricsCSV(context.Background(), admin, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if name != "birge-metrics-2026-03-01-2026-03-31.csv" || !strings.HasPrefix(string(body), "metric,bucket,members,value,suppressed\n") {
		t.Fatalf("csv %q: %s", name, body)
	}
	if len(store.audits) != 2 || store.audits[1].Action != service.AuditActionExport {
		t.Fatalf("export not audited: %+v", store.audits)
	}
}

func TestMetricsDemoK1(t *testing.T) {
	store := newFakeStore()
	store.counts = port.SosCounts{Raised: 1, RaisedMembers: 1}
	m, err := newService(store, 1).Metrics(context.Background(), admin, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if m.K != 1 || m.SosRaised.Suppressed || m.SosRaised.Value != 1 {
		t.Fatalf("k=1: %+v", m.SosRaised)
	}
	if !m.RightsHelpedShare.Suppressed {
		t.Fatal("a cell from no member must stay suppressed even with k=1")
	}
}

// TestRightsSuppression: both rights cells are counted by the members behind
// them and suppressed at k-1, shown at k.
func TestRightsSuppression(t *testing.T) {
	const k = 5
	for _, tt := range []struct {
		name    string
		members int
		hidden  bool
	}{{"k-1 members", k - 1, true}, {"k members", k, false}} {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore()
			store.rights = port.RightsCounts{
				AnsweredFromKnowledge: 12, KnowledgeMembers: tt.members,
				Rated: 8, Helped: 6, RatedMembers: tt.members,
			}
			m, err := newService(store, k).Metrics(context.Background(), admin, from, to)
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]model.Cell{
				"answered from knowledge": {Members: tt.members, Value: 12},
				"helped share":            {Members: tt.members, Value: 0.75},
			}
			if tt.hidden {
				want = map[string]model.Cell{"answered from knowledge": {Suppressed: true}, "helped share": {Suppressed: true}}
			}
			for name, got := range map[string]model.Cell{
				"answered from knowledge": m.RightsAnsweredFromKnowledge, "helped share": m.RightsHelpedShare,
			} {
				if got != want[name] {
					t.Errorf("%s = %+v, want %+v", name, got, want[name])
				}
			}
		})
	}
}

// TestRetentionErasedBefore: erased pseudonyms are forgotten after the EVENTS
// stream's max age, and never when it is not configured.
func TestRetentionErasedBefore(t *testing.T) {
	store := newFakeStore()
	svc := newService(store, 5)
	now := svc.Clock.Now()

	svc.Settings.EventsMaxAge = 720 * time.Hour
	if err := svc.HandleRetention(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	if want := now.Add(-720 * time.Hour); !store.erasedBefore.Equal(want) {
		t.Fatalf("erasedBefore = %s, want %s", store.erasedBefore, want)
	}

	svc.Settings.EventsMaxAge = 0
	if err := svc.HandleRetention(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	if !store.erasedBefore.IsZero() {
		t.Fatalf("erasedBefore without a max age = %s, want the zero time (keep all)", store.erasedBefore)
	}
}

func TestMetricsFailures(t *testing.T) {
	boom := errors.New("down")
	t.Run("audit fails: nothing served", func(t *testing.T) {
		store := newFakeStore()
		store.auditErr = boom
		if _, err := newService(store, 5).Metrics(context.Background(), admin, from, to); !errors.Is(err, boom) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("store down", func(t *testing.T) {
		store := newFakeStore()
		store.err = boom
		if _, _, err := newService(store, 5).MetricsCSV(context.Background(), admin, from, to); !errors.Is(err, boom) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("wellbeing down", func(t *testing.T) {
		store := newFakeStore()
		svc := newService(store, 5)
		svc.Cohorts = fakeCohorts{err: boom}
		if _, err := svc.Metrics(context.Background(), admin, from, to); !errors.Is(err, boom) {
			t.Fatalf("err = %v", err)
		}
		if len(store.audits) != 0 {
			t.Fatal("failed report audited")
		}
	})
}

func TestHandleEvent(t *testing.T) {
	store := newFakeStore()
	svc := newService(store, 5)
	id := uuid.New()
	data, _ := json.Marshal(map[string]string{"sos_id": uuid.NewString(), "member_id": uuid.NewString(), "type": "other"})
	e := model.Event{ID: id, Type: model.EventSosRaised, OccurredAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), Data: data}
	for range 2 {
		if err := svc.HandleEvent(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.facts) != 1 || store.facts[0].ID == uuid.Nil || len(store.facts[0].Actor) != 32 {
		t.Fatalf("facts after a redelivery = %+v", store.facts)
	}

	old := e
	old.ID, old.OccurredAt = uuid.New(), time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := svc.HandleEvent(context.Background(), old); err != nil || len(store.facts) != 1 {
		t.Fatalf("fact older than retention stored: %v, %d facts", err, len(store.facts))
	}

	bad := e
	bad.ID, bad.Data = uuid.New(), json.RawMessage(`{"sos_id":"x"}`)
	if err := svc.HandleEvent(context.Background(), bad); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("malformed: err = %v", err)
	}
	if store.processed[bad.ID] {
		t.Fatal("malformed event marked processed")
	}

	svc.Settings.Key = nil
	if err := svc.HandleEvent(context.Background(), model.Event{ID: uuid.New(), Type: model.EventSosClosed}); err == nil {
		t.Fatal("event handled without a key")
	}
}

type fakeEraser struct {
	member uuid.UUID
	actor  []byte
	err    error
}

func (f *fakeEraser) EraseActor(_ context.Context, memberID uuid.UUID, actor []byte, _ time.Time) error {
	f.member, f.actor = memberID, actor
	return f.err
}

func TestDeletion(t *testing.T) {
	key := bytes.Repeat([]byte{3}, 32)
	er := &fakeEraser{}
	d := &service.Deletion{Store: er, Key: key, Clock: fixedClock{t: from}}
	member := uuid.New()
	if err := d.DeleteMember(context.Background(), member); err != nil {
		t.Fatal(err)
	}
	if er.member != member || !bytes.Equal(er.actor, model.Pseudonym(key, member)) {
		t.Fatalf("erased %s / %x", er.member, er.actor)
	}
	if err := d.DeleteMember(context.Background(), uuid.Nil); err == nil {
		t.Fatal("nil member accepted")
	}
	er.err = errors.New("down")
	if err := d.DeleteMember(context.Background(), member); !errors.Is(err, er.err) {
		t.Fatalf("err = %v", err)
	}
	if err := (&service.Deletion{Store: er, Clock: fixedClock{t: from}}).DeleteMember(context.Background(), member); err == nil {
		t.Fatal("deletion without a key accepted")
	}
}
