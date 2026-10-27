// Package meta serves client configuration: minimum supported app version,
// feature flags and crisis phone numbers (ADR-0018). It is reachable without
// a session and by blocked app versions.
package meta

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

//go:generate go -C ../../../tools tool oapi-codegen -config ../contracts/codegen/meta.yaml -o ../internal/platform/meta/api.gen.go ../contracts/openapi/birge.yaml

var versionRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// Settings is the deployment configuration served to clients.
type Settings struct {
	MinVersion    string
	LatestVersion string
	CrisisPhones  []CrisisPhone
	Features      map[string]bool
}

// Service implements StrictServerInterface.
type Service struct {
	Settings Settings
}

var _ StrictServerInterface = (*Service)(nil)

// GetClientConfig returns the configuration for the calling app version.
func (s *Service) GetClientConfig(_ context.Context, req GetClientConfigRequestObject) (GetClientConfigResponseObject, error) {
	v := req.Params.XAppVersion
	if !req.Params.XAppPlatform.Valid() {
		return badRequest("unsupported platform"), nil
	}
	if !versionRe.MatchString(v) {
		return badRequest("invalid app version"), nil
	}
	outdated, err := less(v, s.Settings.MinVersion)
	if err != nil {
		return nil, fmt.Errorf("meta: min version: %w", err)
	}
	features := s.Settings.Features
	if features == nil {
		features = map[string]bool{}
	}
	return GetClientConfig200JSONResponse{
		MinSupportedVersion: s.Settings.MinVersion,
		LatestVersion:       s.Settings.LatestVersion,
		UpdateRequired:      outdated,
		CrisisPhones:        s.Settings.CrisisPhones,
		Features:            features,
	}, nil
}

func badRequest(title string) GetClientConfigdefaultApplicationProblemPlusJSONResponse {
	return GetClientConfigdefaultApplicationProblemPlusJSONResponse{
		StatusCode: 400,
		Body:       Problem{Type: "about:blank", Title: title, Status: 400, Code: ptr("invalid_request")},
	}
}

// less reports whether semantic version a is lower than b (both x.y.z).
func less(a, b string) (bool, error) {
	pa, err := parse(a)
	if err != nil {
		return false, err
	}
	pb, err := parse(b)
	if err != nil {
		return false, err
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] < pb[i], nil
		}
	}
	return false, nil
}

func parse(v string) ([3]int, error) {
	var out [3]int
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, fmt.Errorf("version %q is not x.y.z", v)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, fmt.Errorf("version %q is not x.y.z", v)
		}
		out[i] = n
	}
	return out, nil
}

func ptr[T any](v T) *T { return &v }
