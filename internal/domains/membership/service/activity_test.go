package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/membership/model"
	"birge/internal/domains/membership/service"
	"birge/internal/platform/outbox"
)

// deployZone stands in for BIRGE_TIME_ZONE (Asia/Almaty is UTC+5 all year);
// a fixed zone keeps the test independent of the host tzdata.
var deployZone = time.FixedZone("UTC+5", 5*60*60)

type activityEnv struct {
	*env
	logs *bytes.Buffer
	act  *service.ActiveDays
}

func newActivityEnv(t *testing.T) *activityEnv {
	t.Helper()
	e := newEnv(t)
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	return &activityEnv{env: e, logs: logs, act: service.NewActiveDays(e.store, e.clock, deployZone, logger)}
}

func activeDayEvents(e *env) []outbox.Event {
	var out []outbox.Event
	for _, ev := range e.store.events() {
		if ev.Type == model.EventMemberActiveDay {
			out = append(out, ev)
		}
	}
	return out
}

func eventDay(t *testing.T, ev outbox.Event) string {
	t.Helper()
	b, err := json.Marshal(ev.Data)
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		Day string `json:"day"`
	}
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	return p.Day
}

func TestActiveDaysOncePerDay(t *testing.T) {
	// UTC times; the deployment day changes at 19:00 UTC (midnight UTC+5).
	at := func(day, hour, minute int) time.Time { return time.Date(2026, 10, day, hour, minute, 0, 0, time.UTC) }
	tests := []struct {
		name     string
		requests []time.Time
		wantDays []string
	}{
		{"one request", []time.Time{at(27, 9, 0)}, []string{"2026-10-27"}},
		{"repeat on the same day emits nothing", []time.Time{at(27, 9, 0), at(27, 9, 1), at(27, 18, 59)}, []string{"2026-10-27"}},
		{"next day emits once", []time.Time{at(27, 9, 0), at(28, 9, 0), at(28, 10, 0)}, []string{"2026-10-27", "2026-10-28"}},
		{"midnight in the deployment zone, not UTC", []time.Time{at(27, 18, 59), at(27, 19, 0), at(27, 23, 0)}, []string{"2026-10-27", "2026-10-28"}},
		{"a late request from yesterday emits nothing", []time.Time{at(28, 9, 0), at(27, 18, 0)}, []string{"2026-10-28"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newActivityEnv(t)
			_, m := newMember(t, e.env)
			for _, now := range tt.requests {
				e.clock.t = now
				e.act.Record(context.Background(), m.ID)
			}
			var days []string
			for _, ev := range activeDayEvents(e.env) {
				if ev.Version != 1 {
					t.Errorf("version = %d", ev.Version)
				}
				days = append(days, eventDay(t, ev))
			}
			if !slices.Equal(days, tt.wantDays) {
				t.Errorf("days = %v, want %v", days, tt.wantDays)
			}
			if e.logs.Len() != 0 {
				t.Errorf("unexpected log: %s", e.logs)
			}
		})
	}
}

func TestActiveDaysCacheSkipsStore(t *testing.T) {
	e := newActivityEnv(t)
	_, m := newMember(t, e.env)
	e.act.Record(context.Background(), m.ID)
	// A store failure is invisible on the same day: the cache answers.
	e.store.fail["MarkActiveDay"] = errors.New("db down")
	e.act.Record(context.Background(), m.ID)
	if e.logs.Len() != 0 {
		t.Errorf("store reached on a cached day: %s", e.logs)
	}
	if n := len(activeDayEvents(e.env)); n != 1 {
		t.Errorf("events = %d, want 1", n)
	}
}

func TestActiveDaysFailureNeverBlocks(t *testing.T) {
	for _, method := range []string{"MarkActiveDay", "Publish"} {
		t.Run(method, func(t *testing.T) {
			e := newActivityEnv(t)
			_, m := newMember(t, e.env)
			e.store.fail[method] = errors.New("db down")
			e.act.Record(context.Background(), m.ID) // returns; nothing to fail
			if n := len(activeDayEvents(e.env)); n != 0 {
				t.Fatalf("events after failure = %d", n)
			}
			log := e.logs.String()
			if !strings.Contains(log, model.EventMemberActiveDay) || !strings.Contains(log, m.ID.String()) {
				t.Errorf("log lacks event name or member id: %s", log)
			}
			// A failure is not cached and a failed publish rolled back the
			// day: the next request records it once.
			delete(e.store.fail, method)
			e.act.Record(context.Background(), m.ID)
			e.act.Record(context.Background(), m.ID)
			if n := len(activeDayEvents(e.env)); n != 1 {
				t.Errorf("events after recovery = %d, want 1", n)
			}
		})
	}
}

func TestActiveDaysReplicasAndConcurrency(t *testing.T) {
	e := newActivityEnv(t)
	_, m := newMember(t, e.env)
	// Two processes (replicas) with their own caches share one database.
	other := service.NewActiveDays(e.store, e.clock, deployZone, nil)
	var wg sync.WaitGroup
	for i := range 20 {
		act := e.act
		if i%2 == 1 {
			act = other
		}
		wg.Go(func() { act.Record(context.Background(), m.ID) })
	}
	wg.Wait()
	if n := len(activeDayEvents(e.env)); n != 1 {
		t.Errorf("events = %d, want 1", n)
	}
}

func TestActiveDaysUnknownMember(t *testing.T) {
	e := newActivityEnv(t)
	e.act.Record(context.Background(), uuid.New())
	e.act.Record(context.Background(), uuid.Nil)
	if n := len(activeDayEvents(e.env)); n != 0 {
		t.Errorf("events = %d, want 0", n)
	}
	if e.logs.Len() != 0 {
		t.Errorf("unexpected log: %s", e.logs)
	}
}

// TestActiveDayPayloadMatchesSchema validates the payload against
// contracts/events/membership.MemberActiveDay.v1.json: exactly the declared
// properties (additionalProperties is false), all required ones, and the uuid
// and date formats.
func TestActiveDayPayloadMatchesSchema(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "contracts", "events", "membership.MemberActiveDay.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		ID                   string                     `json:"$id"`
		AdditionalProperties *bool                      `json:"additionalProperties"`
		Required             []string                   `json:"required"`
		Properties           map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.ID != "urn:birge:events:"+model.EventMemberActiveDay+".v1" {
		t.Fatalf("schema $id = %q", schema.ID)
	}
	if schema.AdditionalProperties == nil || *schema.AdditionalProperties {
		t.Fatal("schema allows additional properties; this test assumes it does not")
	}

	e := newActivityEnv(t)
	_, m := newMember(t, e.env)
	e.act.Record(context.Background(), m.ID)
	evs := activeDayEvents(e.env)
	if len(evs) != 1 {
		t.Fatalf("events = %d", len(evs))
	}
	if evs[0].Version != 1 {
		t.Fatalf("version = %d", evs[0].Version)
	}
	b, err := json.Marshal(evs[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(b, &payload); err != nil {
		t.Fatal(err)
	}
	for k := range payload {
		if _, ok := schema.Properties[k]; !ok {
			t.Errorf("payload has %q, not in the schema", k)
		}
	}
	for _, k := range schema.Required {
		if _, ok := payload[k]; !ok {
			t.Errorf("payload lacks required %q", k)
		}
	}
	id, _ := payload["member_id"].(string)
	if parsed, err := uuid.Parse(id); err != nil || parsed != m.ID {
		t.Errorf("member_id = %q", id)
	}
	day, _ := payload["day"].(string)
	if _, err := time.Parse(time.DateOnly, day); err != nil || day != "2026-10-27" {
		t.Errorf("day = %q", day)
	}
}
