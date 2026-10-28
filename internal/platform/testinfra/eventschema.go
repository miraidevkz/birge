package testinfra

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
)

// eventSchema is the subset of JSON Schema the flat event contracts use:
// a closed object, required keys, string properties with an enum or the uuid
// or date format, and plain boolean properties. ValidateEventPayload fails on any keyword it does not know, so a
// richer contract cannot pass unchecked; extend the check together with the
// first contract that needs more.
type eventSchema struct {
	Type                 string                    `json:"type"`
	AdditionalProperties *bool                     `json:"additionalProperties"`
	Required             []string                  `json:"required"`
	Properties           map[string]map[string]any `json:"properties"`
}

// ValidateEventPayload fails t unless payload (an outbox event's data, as
// JSON) satisfies the contract contracts/events/<schemaFile>, resolved from
// the repository root. Tests call it on the payload read back from
// platform.outbox, so the stored bytes, not a Go struct, are what is checked.
//
// Hand-written on purpose: no JSON Schema library is a direct dependency
// (ADR-0012), and the event contracts are flat. Unsupported keywords, types
// or formats in the contract fail the test rather than being skipped.
func ValidateEventPayload(t testing.TB, schemaFile string, payload []byte) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "contracts", "events", schemaFile))
	if err != nil {
		t.Fatal(err)
	}
	var schema eventSchema
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Type != "object" || schema.AdditionalProperties == nil || *schema.AdditionalProperties {
		t.Fatalf("%s: expected a closed object schema", schemaFile)
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(payload, &data); err != nil {
		t.Fatalf("payload %s: %v", payload, err)
	}
	for _, k := range schema.Required {
		if _, ok := data[k]; !ok {
			t.Fatalf("payload %s: missing required %q", payload, k)
		}
	}
	for k, v := range data {
		prop, ok := schema.Properties[k]
		if !ok {
			t.Fatalf("payload %s: property %q not in %s", payload, k, schemaFile)
		}
		for kw := range prop {
			if kw != "type" && kw != "enum" && kw != "format" && kw != "description" {
				t.Fatalf("%s: keyword %q of %q not supported by this check", schemaFile, kw, k)
			}
		}
		if prop["type"] == "boolean" {
			if _, ok := prop["format"]; ok {
				t.Fatalf("%s: format of boolean %q not supported by this check", schemaFile, k)
			}
			if _, ok := prop["enum"]; ok {
				t.Fatalf("%s: enum of boolean %q not supported by this check", schemaFile, k)
			}
			var b bool
			if err := json.Unmarshal(v, &b); err != nil {
				t.Fatalf("payload %s: %q is not a boolean", payload, k)
			}
			continue
		}
		if prop["type"] != "string" {
			t.Fatalf("%s: type of %q not supported by this check", schemaFile, k)
		}
		var str string
		if err := json.Unmarshal(v, &str); err != nil {
			t.Fatalf("payload %s: %q is not a string", payload, k)
		}
		if f, ok := prop["format"]; ok {
			switch f {
			case "uuid":
				if _, err := uuid.Parse(str); err != nil {
					t.Fatalf("payload %s: %q is not a uuid", payload, k)
				}
			case "date":
				if _, err := time.Parse(time.DateOnly, str); err != nil {
					t.Fatalf("payload %s: %q is not a date", payload, k)
				}
			default:
				t.Fatalf("%s: format %v not supported by this check", schemaFile, f)
			}
		}
		if enum, ok := prop["enum"].([]any); ok && !slices.Contains(enum, any(str)) {
			t.Fatalf("payload %s: %q = %q not in %v", payload, k, str, enum)
		}
	}
}
