package api_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/privacy/api"
	"birge/internal/domains/privacy/model"
	"birge/internal/domains/privacy/privacytest"
	"birge/internal/domains/privacy/service"
	"birge/internal/platform/auth"
)

type fixedClock struct{ t time.Time }

func (c *fixedClock) Now() time.Time { return c.t }

func TestHandlersRequireAuth(t *testing.T) {
	t.Parallel()
	clk := &fixedClock{t: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	svc := &service.Service{
		Store:  privacytest.NewMemoryStore(),
		Clock:  clk,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Settings: service.Settings{
			CurrentVersions: map[string]string{model.ConsentDataProcessing: "v1"},
		},
	}
	server := &api.Server{Service: svc}
	ctx := context.Background()

	listResp, err := server.ListConsents(ctx, api.ListConsentsRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := listResp.(api.ListConsentsdefaultApplicationProblemPlusJSONResponse); !ok || p.StatusCode != http.StatusUnauthorized {
		t.Fatalf("ListConsents = %#v", listResp)
	}

	setResp, err := server.SetConsent(ctx, api.SetConsentRequestObject{
		Body: &api.ConsentChange{Type: api.DataProcessing, Version: "v1", Granted: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := setResp.(api.SetConsentdefaultApplicationProblemPlusJSONResponse); !ok || p.StatusCode != http.StatusUnauthorized {
		t.Fatalf("SetConsent = %#v", setResp)
	}

	delResp, err := server.RequestDeletion(ctx, api.RequestDeletionRequestObject{
		Body: &api.RequestDeletionJSONRequestBody{Confirm: api.DELETE},
	})
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := delResp.(api.RequestDeletiondefaultApplicationProblemPlusJSONResponse); !ok || p.StatusCode != http.StatusUnauthorized {
		t.Fatalf("RequestDeletion = %#v", delResp)
	}
}

func TestSetConsentAndRequestDeletionHappyPath(t *testing.T) {
	t.Parallel()
	clk := &fixedClock{t: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	store := privacytest.NewMemoryStore()
	svc := &service.Service{
		Store:    store,
		Shredder: &privacytest.FakeShredder{},
		Clock:    clk,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Settings: service.Settings{
			CurrentVersions: map[string]string{
				model.ConsentDataProcessing: "v1",
				model.ConsentAICrossborder:  "v1",
				model.ConsentResearch:       "v1",
			},
			DeletionDueAfter: 24 * time.Hour,
		},
	}
	server := &api.Server{Service: svc}
	member := uuid.Must(uuid.NewV7())
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{IdentityID: member})

	setResp, err := server.SetConsent(ctx, api.SetConsentRequestObject{
		Body: &api.ConsentChange{Type: api.DataProcessing, Version: "v1", Granted: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := setResp.(api.SetConsent200JSONResponse)
	if !ok || !got.Granted {
		t.Fatalf("SetConsent = %#v", setResp)
	}

	badResp, err := server.SetConsent(ctx, api.SetConsentRequestObject{
		Body: &api.ConsentChange{Type: api.DataProcessing, Version: "old", Granted: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := badResp.(api.SetConsentdefaultApplicationProblemPlusJSONResponse); !ok || p.StatusCode != http.StatusBadRequest {
		t.Fatalf("wrong version = %#v", badResp)
	}

	delResp, err := server.RequestDeletion(ctx, api.RequestDeletionRequestObject{
		Body: &api.RequestDeletionJSONRequestBody{Confirm: api.DELETE},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := delResp.(api.RequestDeletion202JSONResponse); !ok {
		t.Fatalf("RequestDeletion = %#v", delResp)
	}
}
