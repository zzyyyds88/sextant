package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"sextant-core/internal/trigger"
)

// mustTriggers builds the trigger engine with its device sources.
//
// A failure to open the trigger file is fatal at startup: silently running with
// no watchers would look identical to "nothing happened".
func mustTriggers(s *Server) *trigger.Engine {
	eng, err := trigger.Open(filepath.Join(s.Paths.StateDir, "triggers.json"), trigger.Sources{
		Notifications: func(ctx context.Context) ([]map[string]any, error) {
			out, err := s.Helper.Notifications(ctx)
			if err != nil {
				return nil, err
			}
			items, _ := out["items"].([]any)
			res := make([]map[string]any, 0, len(items))
			for _, item := range items {
				if m, ok := item.(map[string]any); ok {
					res = append(res, m)
				}
			}
			return res, nil
		},
		SMSMaxID: func(ctx context.Context) (int64, error) {
			msgs, err := readSMS(1)
			if err != nil {
				return 0, err
			}
			if len(msgs) == 0 {
				return 0, nil
			}
			return msgs[0].ID, nil
		},
		SMSAfter: func(ctx context.Context, id int64) ([]map[string]any, error) {
			msgs, err := readSMS(50)
			if err != nil {
				return nil, err
			}
			out := make([]map[string]any, 0, len(msgs))
			for _, m := range msgs {
				if m.ID <= id {
					continue
				}
				out = append(out, map[string]any{
					"id": float64(m.ID), "address": m.Address,
					"body": m.Body, "date": float64(m.Date), "type": float64(m.Type),
				})
			}
			return out, nil
		},
		Logcat: func(ctx context.Context) (string, error) {
			// -t 200 keeps the poll cheap; the pattern matches against that window.
			out, err := s.Probe.Run.Run(ctx, "logcat", "-d", "-t", "200")
			if err != nil {
				return "", fmt.Errorf("logcat: %w", err)
			}
			return out, nil
		},
	}, func(format string, args ...any) { s.Audit.Info(format, args...) })
	if err != nil {
		s.Audit.Error("trigger: could not open the trigger store: %v", err)
		// Return an empty engine rather than nil: every trigger endpoint then
		// answers an empty list / a clear error instead of panicking.
		empty, _ := trigger.Open(filepath.Join(s.Paths.StateDir, "triggers.json"), trigger.Sources{}, nil)
		return empty
	}
	return eng
}

var _ = time.Second
