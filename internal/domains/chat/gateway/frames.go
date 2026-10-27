package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/chat/model"
)

// Frame types (contracts/realtime/chat.v1.md).
const (
	frameAuth      = "auth"
	frameSubscribe = "subscribe"
	frameSend      = "send"
	framePing      = "ping"
	frameMessage   = "message"
	frameAck       = "ack"
	frameCrisis    = "crisis"
	frameError     = "error"
	framePong      = "pong"
)

// Error codes of the error frame (contract).
const (
	codeRateLimited = "rate_limited"
	codeSlowMode    = "slow_mode"
	codeBanned      = "banned"
	codeNotMember   = "not_member"
	codeInvalid     = "invalid"
)

// inFrame is any client frame; unknown fields and types are ignored.
type inFrame struct {
	Type     string       `json:"type"`
	Token    string       `json:"token,omitempty"`
	Rooms    []roomCursor `json:"rooms,omitempty"`
	RoomID   uuid.UUID    `json:"roomId"`
	ClientID uuid.UUID    `json:"clientId"`
	Text     string       `json:"text,omitempty"`
}

type roomCursor struct {
	RoomID   uuid.UUID `json:"roomId"`
	AfterSeq int64     `json:"afterSeq"`
}

func decode(b []byte) (inFrame, error) {
	var f inFrame
	if err := json.Unmarshal(b, &f); err != nil {
		return inFrame{}, fmt.Errorf("decode frame: %w", err)
	}
	return f, nil
}

type typed struct {
	Type string `json:"type"`
}

// messageFrame is a ChatMessage (OpenAPI) with the frame type, flattened.
type messageFrame struct {
	Type       string    `json:"type"`
	ID         uuid.UUID `json:"id"`
	RoomID     uuid.UUID `json:"roomId"`
	Seq        int64     `json:"seq"`
	AuthorID   uuid.UUID `json:"authorId"`
	AuthorName string    `json:"authorName"`
	Text       *string   `json:"text,omitempty"`
	Status     string    `json:"status"`
	Mine       bool      `json:"mine"`
	CreatedAt  time.Time `json:"createdAt"`
}

func messageFrameOf(v model.MessageView) messageFrame {
	return messageFrame{
		Type: frameMessage, ID: v.ID, RoomID: v.RoomID, Seq: v.Seq, AuthorID: v.AuthorID, AuthorName: v.AuthorName,
		Text: v.Text, Status: v.Status, Mine: v.Mine, CreatedAt: v.CreatedAt,
	}
}

type ackFrame struct {
	Type      string    `json:"type"`
	ClientID  uuid.UUID `json:"clientId"`
	MessageID uuid.UUID `json:"messageId"`
	Seq       int64     `json:"seq"`
	Status    string    `json:"status"`
}

type crisisPhone struct {
	Number string `json:"number"`
	Label  string `json:"label"`
}

type crisisFrame struct {
	Type         string        `json:"type"`
	CrisisPhones []crisisPhone `json:"crisisPhones"`
	EscalationID *uuid.UUID    `json:"escalationId,omitempty"`
}

func crisisFrameOf(n *model.CrisisNotice) crisisFrame {
	f := crisisFrame{Type: frameCrisis, CrisisPhones: make([]crisisPhone, 0, len(n.Phones)), EscalationID: n.EscalationID}
	for _, p := range n.Phones {
		f.CrisisPhones = append(f.CrisisPhones, crisisPhone(p))
	}
	return f
}

type errorFrame struct {
	Type string `json:"type"`
	Code string `json:"code"`
}

// errorCode maps a service error to an error-frame code; "" for errors that
// close the connection instead (unexpected failures).
func errorCode(err error) string {
	switch {
	case errors.Is(err, model.ErrRateLimited):
		return codeRateLimited
	case errors.Is(err, model.ErrSlowMode):
		return codeSlowMode
	case errors.Is(err, model.ErrBanned):
		return codeBanned
	case errors.Is(err, model.ErrNotMember), errors.Is(err, model.ErrNotFound), errors.Is(err, model.ErrForbidden):
		return codeNotMember
	case errors.Is(err, model.ErrInvalidInput):
		return codeInvalid
	}
	return ""
}
