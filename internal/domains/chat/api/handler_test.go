package api

import (
	"fmt"
	"net/http"
	"testing"

	"birge/internal/domains/chat/model"
)

func TestProblemFor(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{model.ErrUnauthorized, http.StatusUnauthorized, "unauthorized"},
		{model.ErrForbidden, http.StatusForbidden, "forbidden"},
		{model.ErrNotMember, http.StatusForbidden, "not_member"},
		{model.ErrBanned, http.StatusForbidden, "banned"},
		{model.ErrNotFound, http.StatusNotFound, "not_found"},
		{fmt.Errorf("%w: text", model.ErrInvalidInput), http.StatusBadRequest, "invalid_request"},
		{model.ErrConflict, http.StatusConflict, "conflict"},
		{model.ErrRateLimited, http.StatusTooManyRequests, "rate_limited"},
		{model.ErrSlowMode, http.StatusTooManyRequests, "slow_mode"},
	}
	for _, c := range cases {
		st, code, _, ok := problemFor(c.err)
		if !ok || st != c.status || code != c.code {
			t.Errorf("%v: %d %s %v", c.err, st, code, ok)
		}
	}
	if _, _, _, ok := problemFor(fmt.Errorf("database down")); ok {
		t.Error("unexpected error mapped to a problem")
	}
}
