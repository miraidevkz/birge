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

func TestApplyMentorRoleChangedEarlyReturns(t *testing.T) {
	member := uuid.New()
	tests := []struct {
		name    string
		eventID uuid.UUID
		data    model.MentorRoleChangedData
		wantErr error
	}{
		// Granting the role assigns no room: assignment stays a staff action.
		{"grant is a no-op", uuid.New(), model.MentorRoleChangedData{MemberID: member, Mentor: true}, nil},
		{"nil event id", uuid.Nil, model.MentorRoleChangedData{MemberID: member}, model.ErrInvalidInput},
		{"nil member id", uuid.New(), model.MentorRoleChangedData{}, model.ErrInvalidInput},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &service.Service{Clock: &chattest.Clock{T: time.Now().UTC()}, Store: rejectStore{}}
			err := svc.ApplyMentorRoleChanged(context.Background(), tt.eventID, tt.data)
			if tt.wantErr == nil && err != nil {
				t.Fatalf("expected no-op, got %v", err)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected %v, got %v", tt.wantErr, err)
			}
		})
	}
}
