package service

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	"birge/internal/domains/moderation/model"
	"birge/internal/domains/moderation/port"
	"birge/internal/platform/auth"
)

// maxPhotoURLLen is the contract limit of ModerationItem.photoUrl.
const maxPhotoURLLen = 2048

// ListQueue returns a page of open items, oldest first, optionally of one
// kind, each with its summary from the owning context. Summaries are fetched
// after the database read (no transaction held across another context) and
// are not logged.
func (s *Service) ListQueue(ctx context.Context, p auth.Principal, kind model.Kind, cursor string) (model.QueuePage, error) {
	if _, err := staff(p); err != nil {
		return model.QueuePage{}, err
	}
	if kind != "" && !kind.Valid() {
		return model.QueuePage{}, fmt.Errorf("%w: kind", model.ErrInvalidInput)
	}
	f := port.ListFilter{Kind: kind, Limit: model.PageSize + 1}
	if cursor != "" {
		at, id, err := decodeCursor(cursor)
		if err != nil {
			return model.QueuePage{}, err
		}
		f.CursorCreatedAt, f.CursorID = at, id
	}
	var items []model.Item
	if err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		items, err = tx.ListOpenItems(ctx, f)
		return err
	}); err != nil {
		return model.QueuePage{}, err
	}
	var page model.QueuePage
	if len(items) > model.PageSize {
		items = items[:model.PageSize]
		last := items[len(items)-1]
		page.NextCursor = encodeCursor(last.CreatedAt, last.ID)
	}
	page.Items = make([]model.QueueItem, 0, len(items))
	for _, it := range items {
		sum, err := s.summary(ctx, it)
		if err != nil {
			return model.QueuePage{}, err
		}
		page.Items = append(page.Items, model.QueueItem{Item: it, Summary: sum})
	}
	return page, nil
}

// summary fetches and trims the summary of one item. A missing object shows
// as an empty summary; an outage fails the page so that no item is shown
// without the content a decision is about.
func (s *Service) summary(ctx context.Context, it model.Item) (model.Summary, error) {
	src := s.Sources[it.Kind]
	if src == nil {
		return model.Summary{}, fmt.Errorf("moderation: no content source for kind %s", it.Kind)
	}
	sum, err := src.Summary(ctx, it.ObjectID)
	if errors.Is(err, model.ErrNotFound) {
		return model.Summary{}, nil
	}
	if err != nil {
		s.log().WarnContext(ctx, "moderation summary unavailable", "event", "ModerationSummaryUnavailable", "item_id", it.ID, "kind", it.Kind)
		return model.Summary{}, fmt.Errorf("moderation: summary of item %s: %w", it.ID, err)
	}
	if utf8.RuneCountInString(sum.Text) > model.MaxSummaryLen {
		sum.Text = string([]rune(sum.Text)[:model.MaxSummaryLen])
	}
	if it.Kind != model.KindListing || len(sum.PhotoURL) > maxPhotoURLLen {
		sum.PhotoURL = ""
	}
	return sum, nil
}
