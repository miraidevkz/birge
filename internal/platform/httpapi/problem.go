// Package httpapi holds HTTP helpers shared by generated API packages:
// RFC 9457 problem responses and error handlers that never leak internals.
package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// Problem is an RFC 9457 problem details body.
type Problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Code   string `json:"code,omitempty"`
}

// WriteProblem writes a problem+json response.
func WriteProblem(w http.ResponseWriter, status int, code, title string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Problem{Type: "about:blank", Title: title, Status: status, Code: code})
}

// RequestErrorHandler answers malformed requests with 400 and a generic message.
func RequestErrorHandler(w http.ResponseWriter, _ *http.Request, _ error) {
	WriteProblem(w, http.StatusBadRequest, "invalid_request", "The request is invalid.")
}

// ResponseErrorHandler logs the error and answers 500 without details.
func ResponseErrorHandler(logger *slog.Logger) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		logger.Error("handler error", "method", r.Method, "pattern", r.Pattern, "error", err)
		WriteProblem(w, http.StatusInternalServerError, "internal", "Something went wrong.")
	}
}
