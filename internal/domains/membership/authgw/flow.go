package authgw

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

var (
	errBadBody        = errors.New("flow body is not a JSON object")
	errMethod         = errors.New("flow method is not code")
	errNumberMissing  = errors.New("phone number missing")
	errNumberFormat   = errors.New("phone number is not E.164")
	errNumberDisabled = errors.New("phone number range not allowed")
)

// flowNumbers parses a Kratos flow submission for the code method and returns
// every phone number in it, together with the canonical body to forward.
//
// The body is re-encoded from what was parsed, so Kratos sees exactly the
// fields that were checked: duplicate keys collapse to the checked value, and
// keys that differ from a checked key only by case are rejected.
//
// Login carries the number in "identifier" (and optionally "address");
// registration carries it in traits.<phoneTrait>.
func flowNumbers(body []byte, registration bool, phoneTrait string) (numbers []string, canonical []byte, err error) {
	var top map[string]json.RawMessage
	if err := decodeObject(body, &top); err != nil {
		return nil, nil, err
	}
	if err := rejectCaseVariants(top, "method", "identifier", "address", "traits"); err != nil {
		return nil, nil, err
	}
	var method string
	if err := json.Unmarshal(top["method"], &method); err != nil || method != "code" {
		return nil, nil, errMethod
	}
	if registration {
		var traits map[string]json.RawMessage
		if err := decodeObject(top["traits"], &traits); err != nil {
			return nil, nil, errNumberMissing
		}
		if err := rejectCaseVariants(traits, phoneTrait); err != nil {
			return nil, nil, err
		}
		n, err := stringField(traits, phoneTrait, true)
		if err != nil {
			return nil, nil, err
		}
		numbers = append(numbers, n)
		if top["traits"], err = json.Marshal(traits); err != nil {
			return nil, nil, errBadBody
		}
	} else {
		n, err := stringField(top, "identifier", true)
		if err != nil {
			return nil, nil, err
		}
		numbers = append(numbers, n)
		if a, err := stringField(top, "address", false); err != nil {
			return nil, nil, err
		} else if a != "" && a != n {
			numbers = append(numbers, a)
		}
	}
	canonical, err = json.Marshal(top)
	if err != nil {
		return nil, nil, errBadBody
	}
	return numbers, canonical, nil
}

func decodeObject(raw []byte, v *map[string]json.RawMessage) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return errBadBody
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return errBadBody
	}
	return nil
}

// rejectCaseVariants fails when obj has a key equal to one of keys under
// Unicode case folding but not byte-equal, so a differently-cased duplicate
// cannot carry a value past the check.
func rejectCaseVariants(obj map[string]json.RawMessage, keys ...string) error {
	for k := range obj {
		for _, want := range keys {
			if k != want && strings.EqualFold(k, want) {
				return errBadBody
			}
		}
	}
	return nil
}

// stringField returns obj[key] as a string. A missing or null field is
// errNumberMissing when required, "" otherwise; any other type is errNumberFormat.
func stringField(obj map[string]json.RawMessage, key string, required bool) (string, error) {
	raw, ok := obj[key]
	if !ok || string(bytes.TrimSpace(raw)) == "null" {
		if required {
			return "", errNumberMissing
		}
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", errNumberFormat
	}
	if s == "" && required {
		return "", errNumberMissing
	}
	return s, nil
}

// isE164 reports whether s is "+" followed by 8 to 15 digits. Numbers in any
// other form are rejected rather than normalized, so the gateway and Kratos
// cannot disagree on which number was checked.
func isE164(s string) bool {
	if len(s) < 9 || len(s) > 16 || s[0] != '+' || s[1] == '0' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// checkNumber returns nil when n is E.164 and starts with one of prefixes.
func checkNumber(n string, prefixes []string) error {
	if !isE164(n) {
		return errNumberFormat
	}
	for _, p := range prefixes {
		if strings.HasPrefix(n, p) {
			return nil
		}
	}
	return errNumberDisabled
}
