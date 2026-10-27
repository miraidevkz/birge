// Package model holds privacy domain types and invariants. No I/O.
package model

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Consent types from the OpenAPI / event contracts.
const (
	ConsentDataProcessing = "data_processing"
	ConsentAICrossborder  = "ai_crossborder"
	ConsentResearch       = "research"
)

// AllConsentTypes is the fixed set of consent types in stable order.
var AllConsentTypes = []string{
	ConsentDataProcessing,
	ConsentAICrossborder,
	ConsentResearch,
}

// Platform timer kind (ADR-0020): fires when deletion is due.
const TimerDeletionDue = "privacy.DeletionDue"

// Domain event types (contracts/events).
const (
	EventConsentChanged        = "privacy.ConsentChanged"
	EventDataDeletionRequested = "privacy.DataDeletionRequested"
	EventConsentChangedVersion = 1
	EventDataDeletionVersion   = 1
)

var (
	ErrUnauthorized = errors.New("privacy: unauthorized")
	ErrInvalidInput = errors.New("privacy: invalid input")
	ErrNotFound     = errors.New("privacy: not found")
)

// ConsentTypeIsValid reports whether t is a known consent type.
func ConsentTypeIsValid(t string) bool {
	switch t {
	case ConsentDataProcessing, ConsentAICrossborder, ConsentResearch:
		return true
	default:
		return false
	}
}

// ConsentRecord is one append-only history row.
type ConsentRecord struct {
	ID        uuid.UUID
	MemberID  uuid.UUID
	Type      string
	Version   string
	Granted   bool
	ChangedAt time.Time
}

// ConsentState is the API-facing current state for one consent type.
type ConsentState struct {
	Type           string
	Granted        bool
	GrantedVersion *string
	CurrentVersion string
	ChangedAt      *time.Time
}

// DeletionRequest is the member's account-deletion request.
type DeletionRequest struct {
	MemberID    uuid.UUID
	RequestedAt time.Time
	DueAt       time.Time
	CompletedAt *time.Time
}

// ConsentChangedData is the payload of privacy.ConsentChanged.v1.
type ConsentChangedData struct {
	MemberID uuid.UUID `json:"member_id"`
	Type     string    `json:"type"`
	Version  string    `json:"version"`
	Granted  bool      `json:"granted"`
}

// DataDeletionRequestedData is the payload of privacy.DataDeletionRequested.v1.
type DataDeletionRequestedData struct {
	MemberID uuid.UUID `json:"member_id"`
	DueAt    time.Time `json:"due_at"`
}

// ValidateConsentChange checks type and version length before persistence.
func ValidateConsentChange(consentType, version string) error {
	if !ConsentTypeIsValid(consentType) {
		return fmt.Errorf("%w: type", ErrInvalidInput)
	}
	if version == "" || len(version) > 32 {
		return fmt.Errorf("%w: version", ErrInvalidInput)
	}
	return nil
}
