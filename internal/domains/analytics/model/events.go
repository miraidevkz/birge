package model

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Payloads of the consumed events (contracts/events/*.v1.json). Only the
// fields analytics reads are declared; nothing else is kept.
type (
	memberActiveDayData struct {
		MemberID uuid.UUID `json:"member_id"`
		Day      string    `json:"day"`
	}
	teaRequestedData struct {
		RequestID uuid.UUID `json:"request_id"`
		MemberID  uuid.UUID `json:"member_id"`
		Format    string    `json:"format"`
	}
	pairData struct {
		MatchID uuid.UUID `json:"match_id"`
		MemberA uuid.UUID `json:"member_a"`
		MemberB uuid.UUID `json:"member_b"`
	}
	sosRaisedData struct {
		SosID    uuid.UUID `json:"sos_id"`
		MemberID uuid.UUID `json:"member_id"`
		Type     string    `json:"type"`
	}
	sosResponseAcceptedData struct {
		SosID    uuid.UUID `json:"sos_id"`
		AuthorID uuid.UUID `json:"author_id"`
	}
	sosClosedData struct {
		SosID uuid.UUID `json:"sos_id"`
	}
	escalationOpenedData struct {
		EscalationID uuid.UUID `json:"escalation_id"`
		MemberID     uuid.UUID `json:"member_id"`
		Source       string    `json:"source"`
	}
	escalationTakenData struct {
		EscalationID uuid.UUID `json:"escalation_id"`
	}
	questionAnsweredData struct {
		MessageID uuid.UUID `json:"message_id"`
		Scope     string    `json:"scope"`
		Source    string    `json:"source"`
	}
	replyRatedData struct {
		MessageID uuid.UUID `json:"message_id"`
		Helped    *bool     `json:"helped"`
	}
)

// Event is a consumed event as FactsFromEvent needs it.
type Event struct {
	ID         uuid.UUID
	Type       string
	OccurredAt time.Time
	Data       json.RawMessage
}

// FactsFromEvent maps one event to its facts (without IDs) per ADR-0024:
//
//   - MemberActiveDay: actor = member, at noon of the payload day in loc (a
//     time-zone database an hour off on either side keeps it on its day);
//   - TeaRequested: actor = member, subject = request, code = format;
//   - MatchAccepted, AcquaintanceEstablished: one fact per member of the
//     pair, sharing one subject (the match, or the event for an acquaintance);
//   - SosRaised: actor = author, subject = SOS, code = type;
//   - SosResponseAccepted: actor = author, subject = SOS;
//   - SosClosed, EscalationTaken: subject only (the raised or opened fact
//     carries the member; the staff member who took it is never stored);
//   - EscalationOpened: actor = member, subject = escalation, code = source;
//   - QuestionAnswered: subject = message, code = <scope>_<source>;
//   - ReplyRated: subject = message, code = helped | not_helped.
//
// QuestionAnswered and ReplyRated name no member, so their facts have no
// actor. Unknown types return no facts and no error. A payload that cannot
// be read returns ErrInvalidInput: redelivery cannot fix it.
func FactsFromEvent(key []byte, e Event, loc *time.Location) ([]Fact, error) {
	bad := func(what string) ([]Fact, error) {
		return nil, fmt.Errorf("%w: %s %s", ErrInvalidInput, e.Type, what)
	}
	actor := func(id uuid.UUID) []byte { return Pseudonym(key, id) }
	subject := func(id uuid.UUID) []byte { return SubjectPseudonym(key, id) }
	fact := func(code string, a, s []byte) Fact {
		return Fact{Type: e.Type, OccurredAt: e.OccurredAt.UTC(), Code: code, Actor: a, Subject: s}
	}
	switch e.Type {
	case EventMemberActiveDay:
		var d memberActiveDayData
		if json.Unmarshal(e.Data, &d) != nil || d.MemberID == uuid.Nil {
			return bad("payload")
		}
		day, err := time.ParseInLocation(time.DateOnly, d.Day, loc)
		if err != nil {
			return bad("day")
		}
		f := fact("", actor(d.MemberID), nil)
		f.OccurredAt = day.Add(12 * time.Hour).UTC()
		return []Fact{f}, nil
	case EventTeaRequested:
		var d teaRequestedData
		if json.Unmarshal(e.Data, &d) != nil || d.MemberID == uuid.Nil || d.RequestID == uuid.Nil || !codeOK(d.Format) {
			return bad("payload")
		}
		return []Fact{fact(d.Format, actor(d.MemberID), subject(d.RequestID))}, nil
	case EventMatchAccepted, EventAcquaintanceEstablished:
		var d pairData
		if json.Unmarshal(e.Data, &d) != nil || d.MemberA == uuid.Nil || d.MemberB == uuid.Nil {
			return bad("payload")
		}
		obj := d.MatchID
		if e.Type == EventMatchAccepted && obj == uuid.Nil {
			return bad("match_id")
		}
		if e.Type == EventAcquaintanceEstablished {
			obj = e.ID
		}
		s := subject(obj)
		return []Fact{fact("", actor(d.MemberA), s), fact("", actor(d.MemberB), s)}, nil
	case EventSosRaised:
		var d sosRaisedData
		if json.Unmarshal(e.Data, &d) != nil || d.MemberID == uuid.Nil || d.SosID == uuid.Nil || !codeOK(d.Type) {
			return bad("payload")
		}
		return []Fact{fact(d.Type, actor(d.MemberID), subject(d.SosID))}, nil
	case EventSosResponseAccepted:
		var d sosResponseAcceptedData
		if json.Unmarshal(e.Data, &d) != nil || d.AuthorID == uuid.Nil || d.SosID == uuid.Nil {
			return bad("payload")
		}
		return []Fact{fact("", actor(d.AuthorID), subject(d.SosID))}, nil
	case EventSosClosed:
		var d sosClosedData
		if json.Unmarshal(e.Data, &d) != nil || d.SosID == uuid.Nil {
			return bad("payload")
		}
		return []Fact{fact("", nil, subject(d.SosID))}, nil
	case EventEscalationOpened:
		var d escalationOpenedData
		if json.Unmarshal(e.Data, &d) != nil || d.MemberID == uuid.Nil || d.EscalationID == uuid.Nil || !codeOK(d.Source) {
			return bad("payload")
		}
		return []Fact{fact(d.Source, actor(d.MemberID), subject(d.EscalationID))}, nil
	case EventEscalationTaken:
		var d escalationTakenData
		if json.Unmarshal(e.Data, &d) != nil || d.EscalationID == uuid.Nil {
			return bad("payload")
		}
		return []Fact{fact("", nil, subject(d.EscalationID))}, nil
	case EventQuestionAnswered:
		var d questionAnsweredData
		if json.Unmarshal(e.Data, &d) != nil || d.MessageID == uuid.Nil {
			return bad("payload")
		}
		code := d.Scope + "_" + d.Source
		if !codeOK(d.Scope) || !codeOK(d.Source) || !codeOK(code) {
			return bad("scope or source")
		}
		return []Fact{fact(code, nil, subject(d.MessageID))}, nil
	case EventReplyRated:
		var d replyRatedData
		if json.Unmarshal(e.Data, &d) != nil || d.MessageID == uuid.Nil || d.Helped == nil {
			return bad("payload")
		}
		code := CodeNotHelped
		if *d.Helped {
			code = CodeHelped
		}
		return []Fact{fact(code, nil, subject(d.MessageID))}, nil
	}
	return nil, nil
}

// codeOK reports whether s fits the facts.code column: a short lower-case
// identifier, so no payload text can be stored as a code.
func codeOK(s string) bool {
	if len(s) == 0 || len(s) > 32 || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && r != '_' {
			return false
		}
	}
	return true
}
