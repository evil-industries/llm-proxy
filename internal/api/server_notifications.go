package api

import "context"

func (s *Server) startNotifications() {
	if s == nil || s.notifications == nil {
		return
	}
	s.notificationsMu.Lock()
	defer s.notificationsMu.Unlock()
	if s.notificationsCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.notificationsCancel = cancel
	s.notificationsDone = done
	go func() {
		defer close(done)
		s.notifications.Run(ctx)
	}()
}

func (s *Server) stopNotifications(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.notificationsMu.Lock()
	cancel, done := s.notificationsCancel, s.notificationsDone
	s.notificationsMu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		s.notificationsMu.Lock()
		if s.notificationsDone == done {
			s.notificationsCancel = nil
			s.notificationsDone = nil
		}
		s.notificationsMu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
