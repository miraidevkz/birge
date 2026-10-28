// Package api exposes the chat HTTP handlers (OpenAPI tag chat): rooms,
// joining and leaving, mute, history, sending, reports, room moderation,
// and staff mentor / legal-hold operations. The WebSocket is in package
// gateway. Authorization is enforced in the service; handlers validate the
// shape of the input, map errors to problem responses and never echo
// message text into errors.
package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"birge/internal/domains/chat/model"
	"birge/internal/domains/chat/service"
	"birge/internal/platform/auth"
)

// Server implements the generated StrictServerInterface.
type Server struct {
	Service *service.Service
}

var _ StrictServerInterface = (*Server)(nil)

func principal(ctx context.Context) auth.Principal {
	p, _ := auth.FromContext(ctx)
	return p
}

// problemFor maps a service error to a status, code and generic title. It
// returns ok=false for unexpected errors, which go to the 500 handler.
func problemFor(err error) (status int, code, title string, ok bool) {
	switch {
	case errors.Is(err, model.ErrUnauthorized):
		return http.StatusUnauthorized, "unauthorized", "Sign in required.", true
	case errors.Is(err, model.ErrForbidden):
		return http.StatusForbidden, "forbidden", "Not allowed.", true
	case errors.Is(err, model.ErrNotMember):
		return http.StatusForbidden, "not_member", "Join the room first.", true
	case errors.Is(err, model.ErrBanned):
		return http.StatusForbidden, "banned", "You cannot post in this room now.", true
	case errors.Is(err, model.ErrNotFound):
		return http.StatusNotFound, "not_found", "Not found.", true
	case errors.Is(err, model.ErrInvalidInput):
		return http.StatusBadRequest, "invalid_request", "The request is invalid.", true
	case errors.Is(err, model.ErrConflict):
		return http.StatusConflict, "conflict", "The current state does not allow this.", true
	case errors.Is(err, model.ErrRateLimited):
		return http.StatusTooManyRequests, "rate_limited", "Too many messages. Try again later.", true
	case errors.Is(err, model.ErrSlowMode):
		return http.StatusTooManyRequests, "slow_mode", "Slow mode is on in this room.", true
	}
	return 0, "", "", false
}

func problem(status int, code, title string) Problem {
	return Problem{Type: "about:blank", Title: title, Status: status, Code: &code}
}

func invalid() (int, string, string) {
	return http.StatusBadRequest, "invalid_request", "The request is invalid."
}

func toAPIRoom(v model.RoomView) Room {
	r := Room{Id: v.ID, Kind: RoomKind(v.Kind), Title: v.Title, Joined: v.Joined, Muted: v.Muted}
	seq := int(v.LastSeq)
	r.LastSeq = &seq
	if v.SlowModeSeconds > 0 {
		sm := v.SlowModeSeconds
		r.SlowModeSeconds = &sm
	}
	return r
}

func toAPIMessage(v model.MessageView) ChatMessage {
	mine := v.Mine
	return ChatMessage{
		Id: v.ID, RoomId: v.RoomID, Seq: int(v.Seq), AuthorId: v.AuthorID, AuthorName: v.AuthorName,
		Status: ChatMessageStatus(v.Status), Text: v.Text, Mine: &mine, CreatedAt: v.CreatedAt,
	}
}

func toAPIMessageWithCrisis(res model.SendResult) ChatMessage {
	msg := toAPIMessage(res.Message)
	if res.Crisis == nil {
		return msg
	}
	phones := make([]CrisisPhone, 0, len(res.Crisis.Phones))
	for _, p := range res.Crisis.Phones {
		phones = append(phones, CrisisPhone{Number: p.Number, Label: p.Label})
	}
	msg.CrisisPhones = &phones
	if res.Crisis.EscalationID != nil {
		id := *res.Crisis.EscalationID
		msg.EscalationId = &id
	}
	return msg
}

// ListRooms returns the caller's rooms and/or the group rooms she may join.
func (s *Server) ListRooms(ctx context.Context, req ListRoomsRequestObject) (ListRoomsResponseObject, error) {
	kind := ""
	if req.Params.Kind != nil {
		if !req.Params.Kind.Valid() {
			st, c, t := invalid()
			return ListRoomsdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		kind = string(*req.Params.Kind)
	}
	rooms, err := s.Service.ListRooms(ctx, principal(ctx), kind, req.Params.Joined)
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return ListRoomsdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	out := make(ListRooms200JSONResponse, 0, len(rooms))
	for _, r := range rooms {
		out = append(out, toAPIRoom(r))
	}
	return out, nil
}

// JoinRoom adds the caller to a group room.
func (s *Server) JoinRoom(ctx context.Context, req JoinRoomRequestObject) (JoinRoomResponseObject, error) {
	room, err := s.Service.JoinRoom(ctx, principal(ctx), req.RoomId)
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return JoinRoomdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	return JoinRoom200JSONResponse(toAPIRoom(room)), nil
}

// LeaveRoom removes the caller from a room (idempotent).
func (s *Server) LeaveRoom(ctx context.Context, req LeaveRoomRequestObject) (LeaveRoomResponseObject, error) {
	if err := s.Service.LeaveRoom(ctx, principal(ctx), req.RoomId); err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return LeaveRoomdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	return LeaveRoom204Response{}, nil
}

// SetRoomMute sets the caller's push mute for a room.
func (s *Server) SetRoomMute(ctx context.Context, req SetRoomMuteRequestObject) (SetRoomMuteResponseObject, error) {
	if req.Body == nil {
		st, c, t := invalid()
		return SetRoomMutedefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
	}
	if err := s.Service.SetMute(ctx, principal(ctx), req.RoomId, req.Body.Muted); err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return SetRoomMutedefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	return SetRoomMute204Response{}, nil
}

// ListMessages returns history after or before a sequence number.
func (s *Server) ListMessages(ctx context.Context, req ListMessagesRequestObject) (ListMessagesResponseObject, error) {
	var after, before *int64
	if req.Params.After != nil {
		v := int64(*req.Params.After)
		after = &v
	}
	if req.Params.Before != nil {
		v := int64(*req.Params.Before)
		before = &v
	}
	limit := 0
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
		if limit < 1 {
			st, c, t := invalid()
			return ListMessagesdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
	}
	views, err := s.Service.ListMessages(ctx, principal(ctx), req.RoomId, after, before, limit)
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return ListMessagesdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	out := make(ListMessages200JSONResponse, 0, len(views))
	for _, v := range views {
		out = append(out, toAPIMessage(v))
	}
	return out, nil
}

// SendMessage stores a message; repeats of clientId return the stored one.
// When the crisis lexicon fires, crisisPhones and escalationId are set for
// the author only (the message stays held and invisible to others).
func (s *Server) SendMessage(ctx context.Context, req SendMessageRequestObject) (SendMessageResponseObject, error) {
	if req.Body == nil {
		st, c, t := invalid()
		return SendMessagedefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
	}
	res, err := s.Service.Send(ctx, principal(ctx), req.RoomId, req.Body.ClientId, req.Body.Text)
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			if st == http.StatusTooManyRequests {
				return SendMessage429ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem(st, c, t))}, nil
			}
			return SendMessagedefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	return SendMessage201JSONResponse(toAPIMessageWithCrisis(res)), nil
}

// ReportMessage records the caller's report of a message she can see.
func (s *Server) ReportMessage(ctx context.Context, req ReportMessageRequestObject) (ReportMessageResponseObject, error) {
	if req.Body == nil || !req.Body.Reason.Valid() {
		st, c, t := invalid()
		return ReportMessagedefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
	}
	if err := s.Service.ReportMessage(ctx, principal(ctx), req.MessageId, string(req.Body.Reason)); err != nil {
		if st, c, t, ok := problemFor(err); ok {
			return ReportMessagedefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	return ReportMessage204Response{}, nil
}

// ModerateRoom applies a moderation action (staff, or the room's mentor
// with limited actions).
func (s *Server) ModerateRoom(ctx context.Context, req ModerateRoomRequestObject) (ModerateRoomResponseObject, error) {
	if req.Body == nil || !req.Body.Action.Valid() {
		st, c, t := invalid()
		return ModerateRoomdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
	}
	err := s.Service.ModerateRoom(ctx, principal(ctx), req.RoomId, model.Moderation{
		Action: string(req.Body.Action), MessageID: req.Body.MessageId, MemberID: req.Body.MemberId, SlowModeSeconds: req.Body.SlowModeSeconds,
	})
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			if st == http.StatusForbidden {
				return ModerateRoom403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem(st, c, t))}, nil
			}
			return ModerateRoomdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	return ModerateRoom204Response{}, nil
}

// AssignRoomMentor assigns a mentor to a group room (staff).
func (s *Server) AssignRoomMentor(ctx context.Context, req AssignRoomMentorRequestObject) (AssignRoomMentorResponseObject, error) {
	err := s.Service.AssignMentor(ctx, principal(ctx), req.RoomId, req.MemberId, true)
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			switch st {
			case http.StatusForbidden:
				return AssignRoomMentor403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem(st, c, t))}, nil
			case http.StatusConflict:
				return AssignRoomMentor409ApplicationProblemPlusJSONResponse(problem(st, c, t)), nil
			}
			return AssignRoomMentordefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	return AssignRoomMentor204Response{}, nil
}

// StaffListRooms lists group rooms with member counts and mentors (staff).
func (s *Server) StaffListRooms(ctx context.Context, _ StaffListRoomsRequestObject) (StaffListRoomsResponseObject, error) {
	rooms, err := s.Service.StaffListRooms(ctx, principal(ctx))
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			if st == http.StatusForbidden {
				return StaffListRooms403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem(st, c, t))}, nil
			}
			return StaffListRoomsdefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	out := StaffListRooms200JSONResponse{Items: make([]StaffRoom, 0, len(rooms))}
	for _, r := range rooms {
		room := StaffRoom{Id: r.ID, Kind: StaffRoomKind(r.Kind), Title: r.Title, MemberCount: r.MemberCount}
		room.Mentors = make([]struct {
			DisplayName string    `json:"displayName"`
			MemberId    uuid.UUID `json:"memberId"`
		}, 0, len(r.Mentors))
		for _, m := range r.Mentors {
			room.Mentors = append(room.Mentors, struct {
				DisplayName string    `json:"displayName"`
				MemberId    uuid.UUID `json:"memberId"`
			}{DisplayName: m.DisplayName, MemberId: m.MemberID})
		}
		out.Items = append(out.Items, room)
	}
	return out, nil
}

// RemoveRoomMentor removes a mentor from a group room (staff).
func (s *Server) RemoveRoomMentor(ctx context.Context, req RemoveRoomMentorRequestObject) (RemoveRoomMentorResponseObject, error) {
	err := s.Service.AssignMentor(ctx, principal(ctx), req.RoomId, req.MemberId, false)
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			if st == http.StatusForbidden {
				return RemoveRoomMentor403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem(st, c, t))}, nil
			}
			return RemoveRoomMentordefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	return RemoveRoomMentor204Response{}, nil
}

// PutMessageLegalHold puts a message under legal hold (staff).
func (s *Server) PutMessageLegalHold(ctx context.Context, req PutMessageLegalHoldRequestObject) (PutMessageLegalHoldResponseObject, error) {
	err := s.Service.SetLegalHold(ctx, principal(ctx), req.MessageId, true)
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			if st == http.StatusForbidden {
				return PutMessageLegalHold403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem(st, c, t))}, nil
			}
			return PutMessageLegalHolddefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	return PutMessageLegalHold204Response{}, nil
}

// DeleteMessageLegalHold releases a message from legal hold (staff).
func (s *Server) DeleteMessageLegalHold(ctx context.Context, req DeleteMessageLegalHoldRequestObject) (DeleteMessageLegalHoldResponseObject, error) {
	err := s.Service.SetLegalHold(ctx, principal(ctx), req.MessageId, false)
	if err != nil {
		if st, c, t, ok := problemFor(err); ok {
			if st == http.StatusForbidden {
				return DeleteMessageLegalHold403ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem(st, c, t))}, nil
			}
			return DeleteMessageLegalHolddefaultApplicationProblemPlusJSONResponse{StatusCode: st, Body: problem(st, c, t)}, nil
		}
		return nil, err
	}
	return DeleteMessageLegalHold204Response{}, nil
}
