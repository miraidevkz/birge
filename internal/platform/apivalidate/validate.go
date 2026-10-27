// Package apivalidate validates HTTP requests against the OpenAPI contract
// before they reach a handler (ADR-0005, threat model § 1: tampered requests).
//
// The generated strict server does not enforce schema constraints
// (additionalProperties, pattern, maxLength, enums, required headers).
// Middleware loads the document once at startup and rejects non-conforming
// /v1 requests with problem+json. Security requirements are not checked here;
// session middleware owns authentication. Response bodies are not validated.
//
// Failure responses never echo the offending value (it may be personal data).
// Logs carry only the operation id and the kind of violation.
package apivalidate

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"

	"birge/internal/platform/httpapi"
)

// DefaultMaxBodyBytes is the request body limit when Options.MaxBodyBytes is unset.
const DefaultMaxBodyBytes int64 = 64 << 10

// Options configures a Validator.
type Options struct {
	// MaxBodyBytes caps the request body before schema validation.
	// Zero or negative selects DefaultMaxBodyBytes (64 KiB).
	MaxBodyBytes int64
	// Logger receives validation failures. Only operation id and violation
	// kind are logged; never parameter or body values. Optional; defaults to
	// slog.Default.
	Logger *slog.Logger
}

// Validator holds a loaded OpenAPI document and validates requests.
type Validator struct {
	router  routers.Router
	maxBody int64
	logger  *slog.Logger
	filter  *openapi3filter.Options
}

// New loads and validates the OpenAPI document once. spec is typically
// contracts/openapi.Spec. Returns an error if the document cannot be loaded
// or does not validate.
func New(spec []byte, opts Options) (*Validator, error) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(spec)
	if err != nil {
		return nil, fmt.Errorf("load openapi: %w", err)
	}
	if err := doc.Validate(loader.Context); err != nil {
		return nil, fmt.Errorf("validate openapi: %w", err)
	}
	router, err := gorillamux.NewRouter(doc)
	if err != nil {
		return nil, fmt.Errorf("openapi router: %w", err)
	}

	maxBody := opts.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = DefaultMaxBodyBytes
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &Validator{
		router:  router,
		maxBody: maxBody,
		logger:  logger,
		filter: &openapi3filter.Options{
			// Session middleware authenticates; do not re-check security here.
			AuthenticationFunc:  openapi3filter.NoopAuthenticationFunc,
			SkipSettingDefaults: true,
		},
	}, nil
}

// Middleware returns a handler that validates method, path, parameters and
// JSON bodies against the loaded contract, then calls next. Unknown /v1
// routes get 404; schema and parameter failures get 400; oversized bodies
// get 413. All error bodies are problem+json via httpapi.WriteProblem.
func (v *Validator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > v.maxBody {
			v.reject(w, "", "body_size", http.StatusRequestEntityTooLarge,
				"body_too_large", "The request is too large.")
			return
		}
		if r.Body != nil && r.Body != http.NoBody {
			r.Body = http.MaxBytesReader(w, r.Body, v.maxBody)
		}

		route, pathParams, err := v.router.FindRoute(r)
		if err != nil {
			kind := "path"
			if errors.Is(err, routers.ErrMethodNotAllowed) {
				kind = "method"
			}
			v.reject(w, "", kind, http.StatusNotFound, "not_found", "Not found.")
			return
		}

		opID := operationID(route)
		input := &openapi3filter.RequestValidationInput{
			Request:    r,
			PathParams: pathParams,
			Route:      route,
			Options:    v.filter,
		}
		if err := openapi3filter.ValidateRequest(r.Context(), input); err != nil {
			if isBodyTooLarge(err) {
				v.reject(w, opID, "body_size", http.StatusRequestEntityTooLarge,
					"body_too_large", "The request is too large.")
				return
			}
			v.reject(w, opID, requestViolationKind(err), http.StatusBadRequest,
				"invalid_request", "The request is invalid.")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (v *Validator) reject(w http.ResponseWriter, opID, kind string, status int, code, title string) {
	// Log only operation id and violation kind — never values (may be personal data).
	v.logger.Info("request validation failed", "operation", opID, "kind", kind)
	httpapi.WriteProblem(w, status, code, title)
}

func operationID(route *routers.Route) string {
	if route == nil || route.Operation == nil {
		return ""
	}
	return route.Operation.OperationID
}

func isBodyTooLarge(err error) bool {
	var maxBytes *http.MaxBytesError
	return errors.As(err, &maxBytes)
}

// requestViolationKind classifies a ValidateRequest error without reading
// values that may be personal data from the error text.
func requestViolationKind(err error) string {
	var reqErr *openapi3filter.RequestError
	if errors.As(err, &reqErr) {
		if reqErr.Parameter != nil {
			return "parameter"
		}
		if reqErr.RequestBody != nil {
			return "body"
		}
		return "request"
	}
	var secErr *openapi3filter.SecurityRequirementsError
	if errors.As(err, &secErr) {
		return "security"
	}
	return "request"
}
