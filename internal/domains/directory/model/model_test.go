package model_test

import (
	"testing"

	"birge/internal/domains/directory/model"
)

// CodeIsValid is the contract pattern for profession and need codes.
func TestCodeIsValid(t *testing.T) {
	for code, want := range map[string]bool{
		"psychologist": true, "speech_therapist": true, "ab": true,
		"a": false, "": false, "Psychologist": false, "speech-therapist": false, "code1": false,
		"abcdefghijabcdefghijabcdefghijabcdefghij": true, "abcdefghijabcdefghijabcdefghijabcdefghijk": false,
	} {
		if got := model.CodeIsValid(code); got != want {
			t.Errorf("CodeIsValid(%q) = %v, want %v", code, got, want)
		}
	}
}
