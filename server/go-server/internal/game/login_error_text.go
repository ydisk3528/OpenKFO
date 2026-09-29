package game

import (
	"context"
	"kungfu.local/server/internal/loginerrors"
	"time"
)

// Refresh only on login rejection, at most once every five seconds. A busy or
// unavailable database must not prevent a login error from being displayed.
func (s *Server) loginErrorText(code string) string {
	if s.loginTextMu.TryLock() {
		if time.Since(s.loginTextAt) >= 5*time.Second && s.Hub != nil && s.Hub.Store != nil && s.Hub.Store.DB != nil {
			s.loginTextAt = time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			if a, err := s.Hub.Store.LoginErrorSettingsContext(ctx); err == nil {
				s.loginText.Store(&a.Messages)
			}
			cancel()
		}
		s.loginTextMu.Unlock()
	}
	if messages := s.loginText.Load(); messages != nil {
		if msg := (*messages)[code]; msg != "" {
			return msg
		}
	}
	e, _ := loginerrors.Find(code)
	return e.Default
}
