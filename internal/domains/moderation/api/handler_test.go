package api

import (
	"fmt"
	"net/http"
	"testing"

	"birge/internal/domains/moderation/model"
)

func TestProblemMapping(t *testing.T) {
	for err, want := range map[error]int{
		model.ErrUnauthorized:   http.StatusUnauthorized,
		model.ErrForbidden:      http.StatusForbidden,
		model.ErrNotFound:       http.StatusNotFound,
		model.ErrInvalidInput:   http.StatusBadRequest,
		model.ErrAlreadyDecided: http.StatusConflict,
	} {
		st, _, _, ok := problemFor(fmt.Errorf("wrapped: %w", err))
		if !ok || st != want {
			t.Errorf("%v: got %d %v, want %d", err, st, ok, want)
		}
	}
	if _, _, _, ok := problemFor(fmt.Errorf("db down")); ok {
		t.Error("unexpected mapping for an internal error")
	}
}
