package engine

import (
	"context"
	"errors"
	"fmt"

	"sextant-core/internal/ime"
)

// typeText injects text into the focused field.
//
// Three paths, tried in order, because none alone is enough:
//
//  1. The root helper's InputConnection path (the IME provider). On MIUI,
//     `input text` only produces a composition string that Chinese input does not
//     commit, so it fails exactly where it matters.
//  2. The APK's IME InputConnection, for fields that are not EditText widgets
//     (WeChat's self-drawn box) and therefore reject ACTION_SET_TEXT. It is the
//     only path that reaches them, and it is a global single-session resource, so
//     the IME manager serialises it.
//  3. `input text` as a last resort for ASCII-only fields.
//
// The caller gets told which path ran; a silent downgrade would hide a broken
// IME chain.
func (s *Server) typeText(ctx context.Context, text string) error {
	return s.typeTextOpts(ctx, text, false)
}

// typeTextOpts is typeText with the clear flag, so a clear can take the fast path.
func (s *Server) typeTextOpts(ctx context.Context, text string, clear bool) error {
	return s.typeTextAt(ctx, text, clear, -1, -1)
}

// typeTextAt also names the field, for callers that know where it is.
//
// A search box that has just been tapped is often not reported as the focused input
// yet, and the helper then answers "no editable node on display 0" — which used to
// mean a 1.6 s keyboard switch for a field the caller could simply point at.
func (s *Server) typeTextAt(ctx context.Context, text string, clear bool, x, y int) error {
	if text == "" && !clear {
		return nil
	}
	// One place to tell the overlay, because every typing path ends up here.
	defer s.noteAction("%s", describeText(text))
	// Fast path first: ACTION_SET_TEXT through the helper.
	//
	// Measured before this existed: every commit took ~1.6 s and switched the device's
	// default keyboard to ours and back, even for one ASCII letter. The reason it was
	// the only path is that the helper's inputText() demanded a node that was already
	// focused and reported "no editable node" otherwise — which is a lookup bug, not a
	// platform limit. /root/chopin-research/research/dsh-go's Actuator.setText does exactly this and nothing
	// else: find the editable node, focus it, set the text, read it back.
	if s.Helper != nil {
		if _, err := s.Helper.Health(ctx); err == nil {
			if out, err := s.Helper.SetTextViaA11y(ctx, text, "", clear, 0, x, y); err == nil {
				if verified, _ := out["verified"].(bool); verified {
					s.Audit.Info("input: text via a11y_set_text (%d chars, verified)", len([]rune(text)))
					return nil
				}
				// A clear that verified as "field is empty" is a success with 0 chars.
				if clear && text == "" {
					s.Audit.Info("input: cleared via a11y_set_text")
					return nil
				}
				s.Audit.Warn("input: a11y_set_text did not verify; trying the IME")
			} else {
				s.Audit.Info("input: a11y_set_text unavailable (%v); trying the IME", err)
			}
		}
	}
	if _, err := s.Helper.Health(ctx); err == nil {
		var out map[string]any
		helperErr := s.Helper.Call(ctx, "input_text", map[string]any{"text": text}, &out)
		if helperErr == nil {
			// The helper answers {ok, via} and proves the write by reading the
			// field back before replying, so a nil error already means "the text
			// is in the box". `result` is the IME-style verdict and is absent
			// here; treating that absence as "nothing happened" made the audit
			// log claim the IME had done work the helper had actually done.
			via, _ := out["via"].(string)
			if via == "" {
				via = "helper"
			}
			if res, _ := out["result"].(string); res != "" && res != "VERIFIED" && res != "ACCEPTED_UNVERIFIED" {
				return fmt.Errorf("helper input_text returned %s", res)
			}
			s.Audit.Info("input: text via %s (%d chars, verified)", via, len([]rune(text)))
			return nil
		}
		// Record WHY the fallback happened: silently switching mechanisms is how
		// a broken IME chain looks like a working one.
		s.Audit.Warn("input: helper text path failed (%v); trying the IME provider", helperErr)
	}
	if s.IME != nil {
		verdict, err := s.IME.Commit(ctx, text, clear)
		// The IME is handed back right after the commit; say so in the log when it
		// could not be, because the user's next keystroke would land in our
		// invisible view and nothing else would tell them why.
		if rerr := s.IME.LastRestoreError(); rerr != nil {
			s.Audit.Warn("ime: could not hand the default keyboard back: %v", rerr)
		} else if back := s.IME.LastRestored(); back != "" {
			s.Audit.Info("ime: keyboard handed back to %s", back)
		}
		switch {
		case err == nil && (verdict == "VERIFIED" || verdict == "ACCEPTED_UNVERIFIED"):
			s.Audit.Info("input: text via IME (%d chars, %s)", len([]rune(text)), verdict)
			return nil
		case err == nil:
			return fmt.Errorf("IME reported %s for %d chars", verdict, len([]rune(text)))
		case errors.Is(err, ime.ErrBusy):
			return err
		default:
			s.Audit.Warn("input: IME path failed (%v); falling back to input(1)", err)
		}
	}
	return s.inputTextFallback(ctx, text, nil)
}

// inputTextFallback uses `input text`, which only commits ASCII on MIUI. It is a
// fallback for ROMs without a helper, not a general path.
func (s *Server) inputTextFallback(ctx context.Context, text string, cause error) error {
	if _, err := s.Probe.Run.Run(ctx, "input", "text", escapeInputText(text)); err != nil {
		if cause != nil {
			return fmt.Errorf("helper path failed (%v) and input(1) failed: %w", cause, err)
		}
		return fmt.Errorf("input text: %w", err)
	}
	s.Audit.Info("input: text via input(1) fallback (%d chars)", len([]rune(text)))
	return nil
}

// escapeInputText prepares text for `input text`, which treats spaces as
// argument separators and %s as a format placeholder.
func escapeInputText(text string) string {
	out := ""
	for _, r := range text {
		switch r {
		case ' ':
			out += "%s"
		case '%':
			out += "%%"
		default:
			out += string(r)
		}
	}
	return out
}
