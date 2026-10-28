package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func date(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func TestNewPeriod(t *testing.T) {
	tests := []struct {
		name     string
		from, to time.Time
		ok       bool
	}{
		{"one day", date(2026, 3, 1), date(2026, 3, 1), true},
		{"exactly 12 months", date(2026, 1, 15), date(2027, 1, 15), true},
		{"12 months and a day", date(2026, 1, 15), date(2027, 1, 16), false},
		{"to before from", date(2026, 3, 2), date(2026, 3, 1), false},
		{"zero", time.Time{}, date(2026, 3, 1), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewPeriod(tt.from, tt.to)
			if tt.ok && err != nil {
				t.Fatalf("unexpected error %v", err)
			}
			if !tt.ok && !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestPeriodBucketsAndBounds(t *testing.T) {
	// 2026-03-04 is a Wednesday; 2026-04-05 a Sunday.
	p, err := NewPeriod(date(2026, 3, 4), date(2026, 4, 5))
	if err != nil {
		t.Fatal(err)
	}
	weeks := p.Weeks()
	if len(weeks) != 5 || !weeks[0].Equal(date(2026, 3, 2)) || !weeks[4].Equal(date(2026, 3, 30)) {
		t.Fatalf("weeks = %v", weeks)
	}
	months := p.Months()
	if len(months) != 2 || !months[0].Equal(date(2026, 3, 1)) || !months[1].Equal(date(2026, 4, 1)) {
		t.Fatalf("months = %v", months)
	}
	loc := time.FixedZone("UTC+5", 5*3600)
	start, end := p.Bounds(loc)
	if !start.Equal(time.Date(2026, 3, 3, 19, 0, 0, 0, time.UTC)) || !end.Equal(time.Date(2026, 4, 5, 19, 0, 0, 0, time.UTC)) {
		t.Fatalf("bounds = %s .. %s", start, end)
	}
	// The longest period stays inside the contract's maxItems (60 weeks, 13 months).
	long, _ := NewPeriod(date(2026, 1, 31), date(2027, 1, 31))
	if len(long.Weeks()) > 60 || len(long.Months()) > 13 {
		t.Fatalf("weeks %d months %d", len(long.Weeks()), len(long.Months()))
	}
}

func sample() Metrics {
	c := func(n int) Cell { return Cell{Members: n, Value: float64(n) * 1.5} }
	return Metrics{
		StressBaseline:    Score{Mean: c(4), Median: c(5)},
		ActiveWeekly:      []Bucket{{Start: date(2026, 3, 2), Cell: c(4)}, {Start: date(2026, 3, 9), Cell: c(5)}},
		ActiveMonthly:     []Bucket{{Start: date(2026, 3, 1), Cell: c(6)}},
		TeaRequests:       c(5),
		SosRaised:         c(1),
		RightsHelpedShare: Cell{},
	}
}

func TestSuppressAtThreshold(t *testing.T) {
	tests := []struct {
		name        string
		k           int
		wantVisible int // cells left unsuppressed
	}{
		{"k-1 = 4 shows 4, 5 and 6", 4, 6},
		{"k = 5 hides 4", 5, 4},
		{"k = 6 hides 4 and 5", 6, 1},
		{"demo k = 1 shows all but empty cells", 1, 7},
		{"k below 1 counts as 1", 0, 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := sample()
			m := Suppress(in, tt.k)
			visible := 0
			for _, c := range m.cells() {
				switch {
				case c.Suppressed:
					if c.Members != 0 || c.Value != 0 {
						t.Fatalf("suppressed cell keeps numbers: %+v", *c)
					}
				default:
					if c.Members < m.K {
						t.Fatalf("cell from %d members shown with k=%d", c.Members, m.K)
					}
					visible++
				}
			}
			if visible != tt.wantVisible {
				t.Fatalf("visible = %d, want %d", visible, tt.wantVisible)
			}
			if in.ActiveWeekly[0].Cell.Suppressed {
				t.Fatal("Suppress changed its input")
			}
		})
	}
}

func TestWriteCSV(t *testing.T) {
	m := Suppress(sample(), 5)
	m.Period = Period{From: date(2026, 3, 1), To: date(2026, 3, 31)}
	var buf bytes.Buffer
	if err := WriteCSV(&buf, m); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if lines[0] != "metric,bucket,members,value,suppressed" {
		t.Fatalf("header = %q", lines[0])
	}
	if len(lines) != 1+len(Rows(m)) {
		t.Fatalf("%d lines for %d rows", len(lines), len(Rows(m)))
	}
	for _, want := range []string{
		"stress.baseline.mean,,,,true",
		"stress.baseline.median,,5,7.5,false",
		"activity.weekly,2026-03-02,,,true",
		"activity.weekly,2026-03-09,5,7.5,false",
		"tea.requests,,5,7.5,false",
		"sos.raised,,,,true",
		"rights.helpedShare,,,,true",
	} {
		if !strings.Contains(buf.String(), want+"\n") {
			t.Errorf("csv lacks %q:\n%s", want, buf.String())
		}
	}
	if got := CSVFilename(m.Period); got != "birge-metrics-2026-03-01-2026-03-31.csv" {
		t.Fatalf("filename = %q", got)
	}
}

func TestCSVSafe(t *testing.T) {
	for in, want := range map[string]string{
		"=1+1": "'=1+1", "+cmd": "'+cmd", "-2": "'-2", "@SUM(A1)": "'@SUM(A1)", "\tx": "'\tx",
		"tea.requests": "tea.requests", "": "", "0.5": "0.5",
	} {
		if got := csvSafe(in); got != want {
			t.Errorf("csvSafe(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFactsFromEvent(t *testing.T) {
	key := bytes.Repeat([]byte{1}, 32)
	member, other, obj, evID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	at := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	almaty := time.FixedZone("UTC+5", 5*3600)
	ev := func(typ string, data any) Event {
		b, _ := json.Marshal(data)
		return Event{ID: evID, Type: typ, OccurredAt: at, Data: b}
	}
	me, them, subj := Pseudonym(key, member), Pseudonym(key, other), SubjectPseudonym(key, obj)
	tests := []struct {
		name  string
		e     Event
		want  []Fact
		inval bool
	}{
		{"active day at local noon", ev(EventMemberActiveDay, map[string]string{"member_id": member.String(), "day": "2026-03-04"}),
			[]Fact{{Type: EventMemberActiveDay, OccurredAt: time.Date(2026, 3, 4, 7, 0, 0, 0, time.UTC), Actor: me}}, false},
		{"tea requested", ev(EventTeaRequested, map[string]string{"request_id": obj.String(), "member_id": member.String(), "format": "call"}),
			[]Fact{{Type: EventTeaRequested, OccurredAt: at, Code: "call", Actor: me, Subject: subj}}, false},
		{"match accepted: one fact per member", ev(EventMatchAccepted, map[string]string{"match_id": obj.String(), "member_a": member.String(), "member_b": other.String()}),
			[]Fact{{Type: EventMatchAccepted, OccurredAt: at, Actor: me, Subject: subj}, {Type: EventMatchAccepted, OccurredAt: at, Actor: them, Subject: subj}}, false},
		{"acquaintance: subject is the event", ev(EventAcquaintanceEstablished, map[string]string{"member_a": member.String(), "member_b": other.String()}),
			[]Fact{{Type: EventAcquaintanceEstablished, OccurredAt: at, Actor: me, Subject: SubjectPseudonym(key, evID)},
				{Type: EventAcquaintanceEstablished, OccurredAt: at, Actor: them, Subject: SubjectPseudonym(key, evID)}}, false},
		{"sos raised", ev(EventSosRaised, map[string]string{"sos_id": obj.String(), "member_id": member.String(), "type": "childcare"}),
			[]Fact{{Type: EventSosRaised, OccurredAt: at, Code: "childcare", Actor: me, Subject: subj}}, false},
		{"sos response accepted counts for the author", ev(EventSosResponseAccepted, map[string]string{"sos_id": obj.String(), "author_id": member.String(), "responder_id": other.String()}),
			[]Fact{{Type: EventSosResponseAccepted, OccurredAt: at, Actor: me, Subject: subj}}, false},
		{"sos closed has no actor", ev(EventSosClosed, map[string]string{"sos_id": obj.String()}),
			[]Fact{{Type: EventSosClosed, OccurredAt: at, Subject: subj}}, false},
		{"escalation opened", ev(EventEscalationOpened, map[string]string{"escalation_id": obj.String(), "member_id": member.String(), "source": "checkin"}),
			[]Fact{{Type: EventEscalationOpened, OccurredAt: at, Code: "checkin", Actor: me, Subject: subj}}, false},
		{"escalation taken never stores the staff member", ev(EventEscalationTaken, map[string]string{"escalation_id": obj.String(), "taken_by": other.String()}),
			[]Fact{{Type: EventEscalationTaken, OccurredAt: at, Subject: subj}}, false},
		{"question answered counts for the member who asked", ev(EventQuestionAnswered, map[string]string{"message_id": obj.String(), "member_id": member.String(), "scope": "rights", "source": "no_answer"}),
			[]Fact{{Type: EventQuestionAnswered, OccurredAt: at, Code: "rights_no_answer", Actor: me, Subject: subj}}, false},
		{"question answered without member", ev(EventQuestionAnswered, map[string]string{"message_id": obj.String(), "scope": "rights", "source": "knowledge"}), nil, true},
		{"question answered with text as source", ev(EventQuestionAnswered, map[string]string{"message_id": obj.String(), "member_id": member.String(), "scope": "rights", "source": "Call me"}), nil, true},
		{"reply rated has no actor (joined to its answer at report time)", ev(EventReplyRated, map[string]any{"message_id": obj.String(), "helped": false}),
			[]Fact{{Type: EventReplyRated, OccurredAt: at, Code: CodeNotHelped, Subject: subj}}, false},
		{"unknown type", ev("chat.MessageHeld", map[string]string{}), nil, false},
		{"bad day", ev(EventMemberActiveDay, map[string]string{"member_id": member.String(), "day": "04.03.2026"}), nil, true},
		{"missing member", ev(EventSosRaised, map[string]string{"sos_id": obj.String(), "type": "other"}), nil, true},
		{"code with text", ev(EventSosRaised, map[string]string{"sos_id": obj.String(), "member_id": member.String(), "type": "Call me at +7"}), nil, true},
		{"rating without helped", ev(EventReplyRated, map[string]string{"message_id": obj.String()}), nil, true},
		{"not json", Event{ID: evID, Type: EventSosClosed, Data: json.RawMessage(`{`)}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FactsFromEvent(key, tt.e, almaty)
			if tt.inval {
				if !errors.Is(err, ErrInvalidInput) {
					t.Fatalf("err = %v, want ErrInvalidInput", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d facts, want %d", len(got), len(tt.want))
			}
			for i := range got {
				g, w := got[i], tt.want[i]
				if g.Type != w.Type || !g.OccurredAt.Equal(w.OccurredAt) || g.Code != w.Code ||
					!bytes.Equal(g.Actor, w.Actor) || !bytes.Equal(g.Subject, w.Subject) {
					t.Fatalf("fact %d = %+v, want %+v", i, g, w)
				}
			}
		})
	}
}

func TestPseudonyms(t *testing.T) {
	key, other := bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32)
	id := uuid.New()
	if len(Pseudonym(key, id)) != 32 || !bytes.Equal(Pseudonym(key, id), Pseudonym(key, id)) {
		t.Fatal("pseudonym is not a stable 32-byte value")
	}
	if bytes.Equal(Pseudonym(key, id), Pseudonym(other, id)) {
		t.Fatal("pseudonym does not depend on the key")
	}
	if bytes.Equal(Pseudonym(key, id), SubjectPseudonym(key, id)) {
		t.Fatal("member and subject pseudonyms of one ID collide")
	}
	if bytes.Contains(Pseudonym(key, id), id[:]) {
		t.Fatal("pseudonym contains the ID")
	}
}

func TestRetentionCutoffAndNextRun(t *testing.T) {
	loc := time.FixedZone("UTC+5", 5*3600)
	now := time.Date(2028, 3, 9, 12, 0, 0, 0, time.UTC) // Thursday
	cut := RetentionCutoff(now, loc)
	// now-24 months = 2026-03-09 (Monday) 17:00 local → that Monday 00:00 local.
	if want := time.Date(2026, 3, 9, 0, 0, 0, 0, loc); !cut.Equal(want) {
		t.Fatalf("cutoff = %s, want %s", cut, want)
	}
	if cut.In(loc).Weekday() != time.Monday {
		t.Fatal("cutoff is not a Monday")
	}
	at := 3*time.Hour + 30*time.Minute
	if got := NextDailyRun(time.Date(2026, 3, 9, 3, 0, 0, 0, time.UTC), at); !got.Equal(time.Date(2026, 3, 9, 3, 30, 0, 0, time.UTC)) {
		t.Fatalf("next run = %s", got)
	}
	if got := NextDailyRun(time.Date(2026, 3, 9, 3, 30, 0, 0, time.UTC), at); !got.Equal(time.Date(2026, 3, 10, 3, 30, 0, 0, time.UTC)) {
		t.Fatalf("next run = %s", got)
	}
}

func TestLockOrder(t *testing.T) {
	lo, hi := bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32)
	for _, in := range [][]Fact{
		{{Actor: hi}, {Actor: lo}},
		{{Actor: lo}, {Actor: hi}},
	} {
		LockOrder(in)
		if !bytes.Equal(in[0].Actor, lo) || !bytes.Equal(in[1].Actor, hi) {
			t.Fatalf("order = %x, %x", in[0].Actor, in[1].Actor)
		}
	}
	// Without an actor the subject is the key.
	facts := []Fact{{Subject: hi}, {Actor: lo}}
	LockOrder(facts)
	if !bytes.Equal(facts[0].Actor, lo) {
		t.Fatalf("subject-only fact sorted before %x", facts[0].Actor)
	}
}
