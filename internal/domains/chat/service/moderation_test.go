package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/chat/chattest"
	"birge/internal/domains/chat/model"
	"birge/internal/domains/chat/port"
	"birge/internal/domains/chat/service"
)

func TestApplyModerationDecidedIgnoresOtherKinds(t *testing.T) {
	svc := &service.Service{Clock: &chattest.Clock{T: time.Now().UTC()}}
	err := svc.ApplyModerationDecided(context.Background(), uuid.New(), model.ModerationDecidedData{
		ItemID: uuid.New(), Kind: "listing", ObjectID: uuid.New(), Approve: true,
	})
	if err != nil {
		t.Fatalf("expected ignore, got %v", err)
	}
}

func TestApplyModerationDecidedRequiresSuspender(t *testing.T) {
	svc := &service.Service{
		Clock: &chattest.Clock{T: time.Now().UTC()},
		Store: rejectStore{},
	}
	err := svc.ApplyModerationDecided(context.Background(), uuid.New(), model.ModerationDecidedData{
		ItemID: uuid.New(), Kind: model.ModerationKindChatMessage, ObjectID: uuid.New(),
		Approve: false, BlockAuthor: true, Reason: "unsafe",
	})
	if err == nil || err.Error() != "chat: author suspender not configured" {
		t.Fatalf("expected suspender error, got %v", err)
	}
}

func TestApplyModerationDecidedRejectsNilIDs(t *testing.T) {
	svc := &service.Service{Clock: &chattest.Clock{T: time.Now().UTC()}}
	err := svc.ApplyModerationDecided(context.Background(), uuid.Nil, model.ModerationDecidedData{
		ItemID: uuid.New(), Kind: model.ModerationKindReport, ObjectID: uuid.New(),
	})
	if !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("expected invalid input, got %v", err)
	}
}

// rejectStore fails if InTx is reached; used to assert early returns.
type rejectStore struct{}

func (rejectStore) InTx(context.Context, func(port.Tx) error) error {
	return errors.New("store should not be called")
}
