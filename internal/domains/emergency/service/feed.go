package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/emergency/model"
	"birge/internal/domains/emergency/port"
	"birge/internal/platform/auth"
)

// Feed returns one page of open SOS requests shared to the caller's district,
// newest first. Verified members only (model.ErrForbidden otherwise); a
// member without a district gets an empty page. The page never holds a
// childcare SOS (N5), the caller's own SOS, or an SOS of a member blocked in
// either direction (ADR-0019); such rows are dropped, so a page may be
// shorter than the limit while NextCursor is still set.
func (s *Service) Feed(ctx context.Context, p auth.Principal, cursor string) (model.FeedPage, error) {
	viewer, err := memberOf(p)
	if err != nil {
		return model.FeedPage{}, err
	}
	prof, err := s.verifiedProfile(ctx, viewer)
	if err != nil {
		return model.FeedPage{}, err
	}
	page := model.FeedPage{Items: []model.FeedItem{}}
	if prof.DistrictID == nil || *prof.DistrictID == uuid.Nil {
		return page, nil
	}
	f := model.FeedFilter{ViewerID: viewer, DistrictID: *prof.DistrictID, Now: s.now(), Limit: model.FeedPageLimit}
	if cursor != "" {
		at, id, err := decodeCursor(cursor)
		if err != nil {
			return model.FeedPage{}, err
		}
		f.CursorCreatedAt, f.CursorID = &at, &id
	}
	var rows []model.FeedRow
	if err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		rows, err = tx.ListSosFeed(ctx, f)
		return err
	}); err != nil {
		return model.FeedPage{}, err
	}
	if len(rows) == int(f.Limit) {
		last := rows[len(rows)-1]
		page.NextCursor = encodeCursor(last.CreatedAt, last.ID)
	}
	visible := make([]model.FeedRow, 0, len(rows))
	authors := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		if !model.Shareable(r.Type) || r.AuthorID == viewer {
			// The query excludes these; reaching here is a bug, never shown.
			s.log().Error("feed row must not be visible", "event", "SosFeedFilterBreach", "sos_id", r.ID)
			continue
		}
		b, err := s.blocked(ctx, viewer, r.AuthorID)
		if err != nil {
			return model.FeedPage{}, err
		}
		if b {
			continue
		}
		visible = append(visible, r)
		authors = append(authors, r.AuthorID)
	}
	profs, err := s.profiles(ctx, authors...)
	if err != nil {
		return model.FeedPage{}, err
	}
	for _, r := range visible {
		author, ok := profs[r.AuthorID]
		if !ok {
			continue // deleted or unknown author
		}
		text, err := s.open(ctx, r.AuthorID, r.Text, model.FieldSosText, r.ID)
		if err != nil {
			// E.g. the author's key was shredded: skip the item, keep the feed.
			s.log().Warn("sos text unavailable", "event", "SosTextUnavailable", "sos_id", r.ID)
			continue
		}
		page.Items = append(page.Items, model.FeedItem{
			ID: r.ID, Type: r.Type, Text: text, DisplayName: author.DisplayName,
			CreatedAt: r.CreatedAt, RespondedByMe: r.RespondedByMe,
		})
	}
	return page, nil
}

// verifiedProfile returns the member's profile, or model.ErrForbidden when
// she is unknown or not verified (§ 3: the district feed is for verified members).
func (s *Service) verifiedProfile(ctx context.Context, memberID uuid.UUID) (model.Profile, error) {
	prof, err := s.profile(ctx, memberID)
	if errors.Is(err, model.ErrNotFound) {
		return model.Profile{}, model.ErrForbidden
	}
	if err != nil {
		return model.Profile{}, err
	}
	if !prof.Verified {
		return model.Profile{}, model.ErrForbidden
	}
	return prof, nil
}

// visibleInFeed reports whether the SOS appears in the feed of viewer, a
// verified member of district, at now (same rules as the feed query).
func visibleInFeed(sos model.Sos, viewer uuid.UUID, district *uuid.UUID, now time.Time) bool {
	return district != nil && sos.DistrictID != nil && *sos.DistrictID == *district &&
		sos.Status == model.SosOpen && sos.Shared() && model.Shareable(sos.Type) &&
		sos.MemberID != viewer && sos.ClosesAt.After(now)
}

// RespondToSos records "I can help" from the caller on an SOS in her feed and,
// the first time, asks notify to tell the author (class normal). Verified
// members only (model.ErrForbidden); an SOS not in her feed (other district,
// closed, childcare, her own, a blocked author) is model.ErrNotFound.
// Idempotent per responder.
func (s *Service) RespondToSos(ctx context.Context, p auth.Principal, sosID uuid.UUID) error {
	viewer, err := memberOf(p)
	if err != nil {
		return err
	}
	prof, err := s.verifiedProfile(ctx, viewer)
	if err != nil {
		return err
	}
	var sos model.Sos
	if err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		sos, err = tx.GetSos(ctx, sosID, false)
		return err
	}); err != nil {
		return err
	}
	if !visibleInFeed(sos, viewer, prof.DistrictID, s.now()) {
		return model.ErrNotFound
	}
	b, err := s.blocked(ctx, viewer, sos.MemberID)
	if err != nil {
		return err
	}
	if b {
		return model.ErrNotFound
	}
	now := s.now()
	var created model.SosResponse
	var inserted bool
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		// Re-read under lock: the author may have closed it meanwhile.
		cur, err := tx.GetSos(ctx, sosID, true)
		if err != nil {
			return err
		}
		if !visibleInFeed(cur, viewer, prof.DistrictID, now) {
			return model.ErrNotFound
		}
		created, inserted, err = tx.InsertSosResponse(ctx, sosID, viewer, now)
		if err != nil || !inserted {
			return err
		}
		return tx.Publish(ctx, now, model.Delivery(created.ID, model.MemberRecipient(cur.MemberID),
			model.ClassNormal, model.TemplateSosResponded, map[string]string{"sos_id": sosID.String()}))
	})
	if err != nil {
		return err
	}
	if inserted {
		s.log().Info("sos response", "event", "SosResponded", "sos_id", sosID, "response_id", created.ID)
	}
	return nil
}

// ListResponses returns the responses to the caller's SOS, oldest first, with
// display names and whether author and responder met in person. Author only
// (another member's SOS is model.ErrNotFound). Responders blocked in either
// direction, or no longer known to membership, are left out.
func (s *Service) ListResponses(ctx context.Context, p auth.Principal, sosID uuid.UUID) ([]model.ResponseView, error) {
	author, err := memberOf(p)
	if err != nil {
		return nil, err
	}
	var list []model.SosResponse
	if err := s.Store.InTx(ctx, func(tx port.Tx) error {
		if _, err := ownSos(ctx, tx, author, sosID, false); err != nil {
			return err
		}
		var err error
		list, err = tx.ListSosResponses(ctx, sosID, model.ResponsesLimit)
		return err
	}); err != nil {
		return nil, err
	}
	keep := make([]model.SosResponse, 0, len(list))
	ids := make([]uuid.UUID, 0, len(list))
	for _, r := range list {
		b, err := s.blocked(ctx, author, r.ResponderID)
		if err != nil {
			return nil, err
		}
		if !b {
			keep = append(keep, r)
			ids = append(ids, r.ResponderID)
		}
	}
	profs, err := s.profiles(ctx, ids...)
	if err != nil {
		return nil, err
	}
	out := make([]model.ResponseView, 0, len(keep))
	for _, r := range keep {
		prof, ok := profs[r.ResponderID]
		if !ok {
			continue
		}
		met, err := s.met(ctx, author, r.ResponderID)
		if err != nil {
			return nil, err
		}
		out = append(out, model.ResponseView{SosResponse: r, DisplayName: prof.DisplayName, MetInPerson: met})
	}
	return out, nil
}

// AcceptResponse records that the author chose this responder and publishes
// emergency.SosResponseAccepted, on which chat opens a private room; contacts
// are never revealed. Author only; the response must belong to the SOS and
// its responder must not be blocked (model.ErrNotFound otherwise). Idempotent:
// accepting again returns nil and publishes nothing; a closed SOS can no
// longer get a new acceptance (model.ErrConflict).
func (s *Service) AcceptResponse(ctx context.Context, p auth.Principal, sosID, responseID uuid.UUID) error {
	author, err := memberOf(p)
	if err != nil {
		return err
	}
	var resp model.SosResponse
	if err := s.Store.InTx(ctx, func(tx port.Tx) error {
		if _, err := ownSos(ctx, tx, author, sosID, false); err != nil {
			return err
		}
		var err error
		resp, err = tx.GetSosResponse(ctx, responseID)
		return err
	}); err != nil {
		return err
	}
	if resp.SosID != sosID {
		return model.ErrNotFound
	}
	b, err := s.blocked(ctx, author, resp.ResponderID)
	if err != nil {
		return err
	}
	if b {
		return model.ErrNotFound
	}
	now := s.now()
	var accepted bool
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		sos, err := ownSos(ctx, tx, author, sosID, true)
		if err != nil {
			return err
		}
		cur, err := tx.GetSosResponse(ctx, responseID)
		if err != nil {
			return err
		}
		if cur.AcceptedAt != nil {
			return nil
		}
		if sos.Status != model.SosOpen {
			return model.ErrConflict
		}
		if _, accepted, err = tx.AcceptSosResponse(ctx, responseID, now); err != nil || !accepted {
			return err
		}
		return tx.Publish(ctx, now, model.Event{
			Type: model.EventSosResponseAccepted, Version: model.EventVersion,
			Data: model.SosResponseAcceptedData{SosID: sosID, AuthorID: author, ResponderID: cur.ResponderID},
		})
	})
	if err != nil {
		return err
	}
	if accepted {
		s.log().Info("sos response accepted", "event", model.EventSosResponseAccepted, "sos_id", sosID, "response_id", responseID)
	}
	return nil
}
