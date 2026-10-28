package testinfra_test

import (
	"testing"

	"birge/internal/platform/testinfra"
)

func TestValidateEventPayloadAccepts(t *testing.T) {
	testinfra.ValidateEventPayload(t, "assistant.QuestionAnswered.v1.json",
		[]byte(`{"message_id":"019a2f00-0000-7000-8000-00000000000a","scope":"rights","source":"knowledge"}`))
	testinfra.ValidateEventPayload(t, "assistant.ReplyRated.v1.json",
		[]byte(`{"message_id":"019a2f00-0000-7000-8000-00000000000a","helped":false}`))
	testinfra.ValidateEventPayload(t, "membership.MemberActiveDay.v1.json",
		[]byte(`{"member_id":"019a2f00-0000-7000-8000-00000000000a","day":"2026-03-02"}`))
}

func TestValidateEventPayloadRejects(t *testing.T) {
	// The check itself must fail on what the contract forbids; run it in a
	// recorder, so this test proves the check is not vacuous.
	for name, tc := range map[string]struct{ file, payload string }{
		"extra property": {"assistant.QuestionAnswered.v1.json", `{"message_id":"019a2f00-0000-7000-8000-00000000000a","scope":"general","source":"knowledge","text":"x"}`},
		"not in enum":    {"assistant.QuestionAnswered.v1.json", `{"message_id":"019a2f00-0000-7000-8000-00000000000a","scope":"general","source":"llm"}`},
		"missing":        {"assistant.QuestionAnswered.v1.json", `{"message_id":"019a2f00-0000-7000-8000-00000000000a","source":"crisis"}`},
		"bad uuid":       {"assistant.QuestionAnswered.v1.json", `{"message_id":"42","scope":"rights","source":"crisis"}`},
		"not a string":   {"assistant.QuestionAnswered.v1.json", `{"message_id":"019a2f00-0000-7000-8000-00000000000a","scope":1,"source":"crisis"}`},
		"not an object":  {"assistant.QuestionAnswered.v1.json", `[]`},
		"not a boolean":  {"assistant.ReplyRated.v1.json", `{"message_id":"019a2f00-0000-7000-8000-00000000000a","helped":"yes"}`},
		"bad date":       {"membership.MemberActiveDay.v1.json", `{"member_id":"019a2f00-0000-7000-8000-00000000000a","day":"02.03.2026"}`},
		"no such file":   {"does.not.Exist.v1.json", `{}`},
	} {
		rec := &failRecorder{TB: t}
		func() {
			defer func() { _ = recover() }()
			testinfra.ValidateEventPayload(rec, tc.file, []byte(tc.payload))
		}()
		if !rec.failed {
			t.Fatalf("%s: payload %s passed the contract check", name, tc.payload)
		}
	}
}

// failRecorder turns Fatal and Fatalf into a recorded failure that stops the
// check (via panic) without failing the outer test.
type failRecorder struct {
	testing.TB
	failed bool
}

func (r *failRecorder) Helper() {}

func (r *failRecorder) Fatalf(string, ...any) {
	r.failed = true
	panic("contract check failed")
}

func (r *failRecorder) Fatal(...any) {
	r.failed = true
	panic("contract check failed")
}
