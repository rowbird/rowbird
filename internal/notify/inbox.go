package notify

import (
	"context"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/store"
)

// List returns a page of a user's notifications and how many are unread.
func (s *Service) List(ctx context.Context, userID uuid.UUID, unreadOnly bool, page store.PageRequest) (store.Page[store.Notification], int, error) {
	p, err := s.store.Notifications().List(ctx, userID, unreadOnly, page)
	if err != nil {
		return p, 0, err
	}
	n, err := s.store.Notifications().UnreadCount(ctx, userID)
	return p, n, err
}

// MarkRead marks one of the user's notifications as read.
func (s *Service) MarkRead(ctx context.Context, userID, id uuid.UUID) (*store.Notification, error) {
	if err := s.store.Notifications().MarkRead(ctx, userID, id, s.clock()); err != nil {
		return nil, err
	}
	return s.store.Notifications().Get(ctx, userID, id)
}

// MarkAllRead marks all of the user's notifications as read.
func (s *Service) MarkAllRead(ctx context.Context, userID uuid.UUID) (int, error) {
	return s.store.Notifications().MarkAllRead(ctx, userID, s.clock())
}
