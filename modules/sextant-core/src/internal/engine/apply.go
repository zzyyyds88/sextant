package engine

import (
	"context"
	"fmt"
	"time"

	"sextant-core/internal/bg"
	"sextant-core/internal/status"
)

// ApplyNow runs one full keep-alive pass immediately (used by ctl apply and by
// the tests that need a deterministic pass).
func (s *Server) ApplyNow(ctx context.Context) error {
	passCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	res := s.A11y.Restore(passCtx, s.Cfg.App.A11y)
	if res.State != status.OK && res.State != status.PENDING {
		return fmt.Errorf("a11y: %s (%s)", res.State, res.Detail)
	}
	bgRes := s.Bg.Apply(passCtx, s.Cfg.App.Package, bg.Policy{
		Autostart: s.Cfg.Keepalive.Bg.Autostart,
		BgControl: s.Cfg.Keepalive.Bg.BgControl,
		Doze:      s.Cfg.Keepalive.Bg.Doze,
	})
	if bgRes.State != status.OK {
		return fmt.Errorf("background: %s (%s)", bgRes.State, bgRes.Detail)
	}
	return s.refreshStatus(passCtx)
}
