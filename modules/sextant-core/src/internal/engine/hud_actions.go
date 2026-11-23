package engine

// What the overlay says while the agent works.
//
// The reference (/root/chopin-research/research/dsh-go docs/design/phone-overlay.md §2) is explicit that
// the capsule shows the ACTION, not a generic busy line: 「点「发送」」 rather than
// 「正在操作」. That difference is the point of the overlay for the person holding
// the phone — "something is happening" versus "it is about to press Send".
//
// Before this file the daemon never called NoteAction, so the APK fell back to a
// hardcoded 「控制中」 for every single action: the overlay was on, the safety
// promise held, and the human was told nothing.
//
// Typed content is reduced to a length on purpose: the person needs to know typing
// is happening and how much, not what was typed into a private message.

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"sextant-core/internal/httpx"
)

// noteAction tells the overlay what just happened.
//
// It also makes the overlay VISIBLE. That is not a side effect to hide: the whole
// promise is "when an agent drives the phone, the person holding it can see that and
// stop it", and an action without a visible overlay breaks it. This was found the
// hard way — a long real session of taps and typing ran with `visible=false` the
// entire time, because the overlay was only ever turned on by claiming a write
// session and nothing here requires one.
//
// Visible-without-a-session is the honest state: someone IS driving. Claiming is
// still what a well-behaved client does (it also gets exclusive input), but the
// safety surface can no longer depend on the client behaving.
func (s *Server) noteAction(format string, args ...any) {
	if s.HUD == nil {
		return
	}
	s.HUD.NoteAction(fmt.Sprintf(format, args...))
}

// refuseIfPaused is the single gate every action endpoint runs through.
//
// One gate, not one per handler: the pause promise is "nothing moves the screen
// while you are reading it", and a promise implemented nine times is one that gets
// implemented eight times next year. It covers every injection path there is —
// tap, swipe, long press, key, global, chain, type, and the find+click helpers.
func (s *Server) refuseIfPaused(w http.ResponseWriter) bool {
	if s.HUD == nil || !s.HUD.Paused() {
		return false
	}
	httpx.WriteError(w, http.StatusConflict, httpx.CodeConflict,
		"the on-device overlay is paused or stopped; nothing will move the screen until it is resumed",
		nil)
	return true
}

// stepLabel describes a tap by what it lands on, when the caller knows.
//
// A bare coordinate tells the human nothing; "点「发送」" tells them whether to
// reach for Stop. Callers that resolved a node pass its label, the rest fall back
// to coordinates rather than inventing a name.
func stepLabel(text, resourceID string, x, y int) string {
	label := strings.TrimSpace(text)
	if label == "" {
		label = strings.TrimSpace(resourceID)
		if i := strings.LastIndex(label, ":id/"); i >= 0 {
			label = label[i+len(":id/"):]
		}
	}
	if label == "" {
		return fmt.Sprintf("点 (%d, %d)", x, y)
	}
	if len([]rune(label)) > 16 {
		label = string([]rune(label)[:16]) + "…"
	}
	return fmt.Sprintf("点「%s」", label)
}

// describeText keeps typed content out of the overlay and the log.
func describeText(text string) string {
	n := len([]rune(strings.TrimSpace(text)))
	if n == 0 {
		return "清空输入框"
	}
	return fmt.Sprintf("输入 %d 字", n)
}

// describeKey turns a key name into something a person reads.
func describeKey(name string) string {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "KEYCODE_BACK":
		return "返回"
	case "KEYCODE_HOME":
		return "回桌面"
	case "KEYCODE_APP_SWITCH":
		return "最近任务"
	case "KEYCODE_ENTER":
		return "回车"
	default:
		return "按键 " + name
	}
}

// clickNode asks the helper to click a node by itself (text or resource id).
//
// It returns the route that worked ("node", "ancestor") or "none" when the helper
// could not click it — in which case the caller taps the coordinate, exactly as
// before. The helper being unreachable is also "none": this is an improvement on a
// working path, not a new dependency it can fail on.
func (s *Server) clickNode(ctx context.Context, text, resourceID string, long bool, display, x, y int) string {
	if s.Helper == nil {
		return "none"
	}
	params := map[string]any{"text": text, "rid": resourceID, "long": long, "display_id": display, "x": x, "y": y}
	var out struct {
		Via string `json:"via"`
	}
	if err := s.Helper.Call(ctx, "a11y_click", params, &out); err != nil {
		s.Audit.Warn("ui: node click unavailable (%v); using the coordinate tap", err)
		return "none"
	}
	return out.Via
}

// scrollNode asks the helper to scroll the list under (x, y) with the node's own
// ACTION_SCROLL_*, and reports whether it did.
//
// The gesture path stays for lists that are app-drawn (WeChat's chat view is one) —
// those expose no scrollable node, and the helper answers "none".
func (s *Server) scrollNode(ctx context.Context, direction string, display, x, y int) bool {
	if s.Helper == nil {
		return false
	}
	out, err := s.Helper.ScrollViaA11y(ctx, direction, display, x, y)
	if err != nil {
		s.Audit.Warn("ui: node scroll unavailable (%v); using a gesture", err)
		return false
	}
	via, _ := out["via"].(string)
	return via == "node"
}
