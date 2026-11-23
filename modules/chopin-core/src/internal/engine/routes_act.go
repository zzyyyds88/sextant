package engine

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"chopin-core/internal/challenge"
	"chopin-core/internal/httpx"
	"chopin-core/internal/risk"
	"chopin-core/internal/ui"
)

// actionRequest is one verifiable action (D29).
//
// The point is not "an event was injected" — it is "the event landed and the
// state changed the way the caller expected". `dispatched`, `observed` and
// `verified` are reported separately so a failed verification cannot be read as
// success.
type actionRequest struct {
	FrameID    string         `json:"frame_id"`
	Target     map[string]any `json:"target"`
	Action     string         `json:"action"`
	Text       string         `json:"text"`
	Clear      bool           `json:"clear"`
	DurationMS int            `json:"duration_ms"`
	VerifyMS   int            `json:"verify_ms"`
	Expect     map[string]any `json:"expect"`
	// Precheck must hold before anything is injected; it is the "check the
	// composer is empty before pressing Send" hook.
	Precheck map[string]any `json:"precheck"`
	// AllowDanger bypasses the checkout/payment guard. It exists for an explicit
	// user request, not for a recipe to set by habit.
	AllowDanger bool `json:"allow_danger"`
	// Seed makes the D30 human profile reproducible; absent, the response
	// carries the seed that was used.
	Seed string `json:"seed"`
}

func (s *Server) handleAct(w http.ResponseWriter, r *http.Request) {
	var body actionRequest
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	body.Action = strings.ToLower(strings.TrimSpace(body.Action))
	switch body.Action {
	case "click", "select", "longpress", "set_text":
	default:
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest,
			"action must be click, select, longpress or set_text", nil)
		return
	}
	if len(body.Target) == 0 {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "target is required", nil)
		return
	}
	if s.refuseIfPaused(w) || s.refuseIfHandedOver(w) {
		return
	}
	ctx, cancel := contextWithTimeout(r, 60*time.Second)
	defer cancel()

	snap, err := s.Tree.Snapshot(ctx, 0)
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "ui dump failed", err)
		return
	}
	target, selector, found := resolveTarget(snap.Nodes, body.Target)
	if !found {
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound,
			"target not found", nil)
		return
	}
	if target.Attrs["visible"] == "false" {
		httpx.WriteError(w, http.StatusConflict, httpx.CodeConflict,
			"target is not visible", nil)
		return
	}
	if target.Attrs["enabled"] == "false" {
		httpx.WriteError(w, http.StatusConflict, httpx.CodeConflict,
			"target is disabled", nil)
		return
	}
	x, y := target.Center()
	if x < 0 || y < 0 {
		httpx.WriteError(w, http.StatusConflict, httpx.CodeConflict,
			"target is off screen", nil)
		return
	}
	if (body.Action == "click" || body.Action == "select") && !body.AllowDanger {
		if word := dangerWord(target.Attrs["text"], target.Attrs["content-desc"]); word != "" {
			httpx.WriteError(w, http.StatusConflict, "danger_button",
				"refusing to press a checkout/payment control",
				fmt.Errorf("matched %q; allow_danger=true is only for an explicit user request", word))
			return
		}
	}
	if len(body.Precheck) > 0 {
		if !expectMet(snap.Nodes, body.Precheck, selector) {
			httpx.WriteError(w, http.StatusConflict, "precondition_failed",
				"precheck did not hold before the action", nil)
			return
		}
	}

	// Touch injections go through the same guard, so a stroke can never hit the
	// overlay's Stop control and a capture cannot race it. set_text writes through
	// the accessibility node and does not touch the screen.
	if body.Action != "set_text" {
		restore, guardErr := s.passThroughForInput(ctx)
		if guardErr != nil {
			httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeNotReady,
				"cannot pass touches through the overlay", guardErr)
			return
		}
		if restore != nil {
			defer restore()
		}
	}

	channel := "input"
	clickedBounds := target.Bounds
	seed := s.requestSeed(body.Seed)
	reqID := requestIDFor(r)
	var pacedMS, dispatchMS, intervalMS int64
	// usedPoint stays nil on the accessibility channel: an ACTION_CLICK has no
	// landing pixel, and echoing the target's centre would read as if a touch had
	// been placed there.
	var usedPoint []int
	bounds := clickedBounds
	switch body.Action {
	case "click", "select":
		release, paced, interval, paceErr := s.pace(ctx, 0, seed, reqID, 0)
		if paceErr != nil {
			httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "cannot pace the action", paceErr)
			return
		}
		start := time.Now()
		via := s.clickNode(ctx, target.Attrs["text"], target.Attrs["resource-id"], false, 0, x, y)
		if via != "" && via != "none" {
			release()
			dispatchMS = time.Since(start).Milliseconds()
			pacedMS = paced
			intervalMS = interval
			channel = "a11y"
			if body.Action == "select" {
				channel = "a11y-select"
			}
			break
		}
		px, py := x, y
		if s.Human != nil {
			px, py = s.Human.Point(&bounds, x, y, seed, reqID, 0, 0)
		}
		tapErr := s.Device.Tap(ctx, px, py, 0)
		release()
		dispatchMS = time.Since(start).Milliseconds()
		pacedMS = paced
		intervalMS = interval
		if tapErr != nil {
			httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "tap failed", tapErr)
			return
		}
		usedPoint = []int{px, py}
	case "longpress":
		duration := body.DurationMS
		if duration <= 0 {
			duration = 800
		}
		px, py, release, paced, interval, paceErr := s.injectionPoint(ctx, 0, &bounds, x, y, seed, reqID, 0)
		if paceErr != nil {
			httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "cannot pace the long press", paceErr)
			return
		}
		start := time.Now()
		pressErr := s.Device.LongPress(ctx, px, py, duration, 0)
		release()
		dispatchMS = time.Since(start).Milliseconds()
		pacedMS = paced
		intervalMS = interval
		if pressErr != nil {
			httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "long press failed", pressErr)
			return
		}
		usedPoint = []int{px, py}
	case "set_text":
		out, err := s.Helper.SetTextViaA11y(ctx, body.Text, target.Attrs["resource-id"], body.Clear, 0, x, y)
		if err != nil {
			httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "text injection failed", err)
			return
		}
		if via, _ := out["via"].(string); via != "" {
			channel = "a11y-set-text"
		}
	}

	// set_text is verified by default: the caller's intent is "this text is in the
	// field", so reporting only "the action was dispatched" would repeat the old
	// fake-success bug. The caller can override with an explicit expect.
	if body.Action == "set_text" && len(body.Expect) == 0 {
		body.Expect = map[string]any{"input_text": body.Text}
		if body.VerifyMS <= 0 {
			body.VerifyMS = 800
		}
	}

	s.Audit.Info("ui: act %s %s target=%s channel=%s", body.Action, body.FrameID,
		describeTarget(selector["text"], selector["resource_id"]), channel)
	s.noteAction("%s", actionLabel(body.Action, selector["text"]))

	verified := "unknown"
	var failure map[string]any
	if body.VerifyMS > 0 && len(body.Expect) > 0 {
		ok, readable := s.verifyExpect(ctx, body.Expect, body.VerifyMS, selector)
		switch {
		case !readable:
			verified = "unknown"
		case ok:
			verified = "true"
		default:
			verified = "false"
			failure = map[string]any{"code": "no_effect", "detail": "the expectation was not met before verify_ms"}
		}
	}

	// observed is deliberately cheap: a fresh tree count plus the foreground
	// package, not another capture. A caller that needs pixels calls observe.
	//
	// D30 safety net: an action that did not take effect may have failed because a
	// verification wall is in front. Turning that into a challenge failure means a
	// caller that forgot to ask cannot hammer the wall until its budget runs out.
	observed := map[string]any{}
	if snap2, err := s.Tree.Snapshot(ctx, 0); err == nil {
		observed["nodes"] = len(snap2.Nodes)
		observed["truncated"] = snap2.Truncated
		if verified == "false" && s.Cfg.Challenge.Enabled {
			if ch := challenge.Classify(snap2.Nodes); ch.Kind != challenge.KindNone {
				failure = map[string]any{
					"code":      "challenge_" + string(ch.Kind),
					"detail":    "the action did not take effect and the page is asking for verification",
					"challenge": ch,
				}
				if ch.Kind == challenge.KindCode {
					if fg, ferr := s.foregroundInfo(ctx); ferr == nil {
						if pkg, _ := fg["package"].(string); pkg != "" {
							_, _ = risk.Record(s.Paths.Risk, pkg, time.Now(), s.Cfg.Challenge.RiskDays, s.Cfg.Challenge.CooldownSec)
						}
					}
				}
			}
		}
	}

	failureCode := ""
	if failure != nil {
		if code, ok := failure["code"].(string); ok {
			failureCode = code
		}
	}
	s.Metrics.Act(verified == "true", failureCode)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"dispatched":     true,
		"channel":        channel,
		"clicked_bounds": clickedBounds,
		"point":          usedPoint,
		"seed":           seed,
		"dispatch_ms":    dispatchMS,
		"paced_ms":       pacedMS,
		"interval_ms":    intervalMS,
		"verified":       verified,
		"failure":        failure,
		"observed":       observed,
		"frame_id":       body.FrameID,
	})
}

// resolveTarget turns a JSON target into a node plus the selector echoed back in
// the audit line. It accepts node_id, text, resource_id and explicit bounds.
func resolveTarget(nodes []ui.Node, target map[string]any) (ui.Node, map[string]string, bool) {
	selector := map[string]string{"text": "", "resource_id": ""}
	if v, ok := target["text"].(string); ok {
		selector["text"] = v
	}
	if v, ok := target["resource_id"].(string); ok {
		selector["resource_id"] = v
	}
	if id, ok := target["node_id"].(string); ok && strings.HasPrefix(id, "n_") {
		if idx, err := strconv.Atoi(strings.TrimPrefix(id, "n_")); err == nil && idx >= 0 && idx < len(nodes) {
			n := nodes[idx]
			if selector["text"] == "" {
				selector["text"] = n.Attrs["text"]
			}
			if selector["resource_id"] == "" {
				selector["resource_id"] = n.Attrs["resource-id"]
			}
			return n, selector, true
		}
	}
	if selector["resource_id"] != "" {
		if n, ok := ui.FindByResourceID(nodes, selector["resource_id"]); ok {
			return n, selector, true
		}
	}
	if selector["text"] != "" {
		if n, ok := ui.FindByText(nodes, selector["text"]); ok {
			return n, selector, true
		}
	}
	if b, ok := target["bounds"].([]any); ok && len(b) == 4 {
		var n ui.Node
		for i := 0; i < 4; i++ {
			n.Bounds[i] = intOf(b[i])
		}
		n.Attrs = map[string]string{"bounds": fmt.Sprintf("[%d,%d][%d,%d]",
			n.Bounds[0], n.Bounds[1], n.Bounds[2], n.Bounds[3])}
		return n, selector, true
	}
	return ui.Node{}, selector, false
}

// verifyExpect polls the tree until the expectation holds or verify_ms expires.
// readable=false means the tree itself could not be read, which is unknown, not
// a failure.
func (s *Server) verifyExpect(ctx context.Context, expect map[string]any, verifyMS int, selector map[string]string) (bool, bool) {
	deadline := time.Now().Add(time.Duration(verifyMS) * time.Millisecond)
	for {
		snap, err := s.Tree.Snapshot(ctx, 0)
		if err != nil {
			return false, false
		}
		if expectMet(snap.Nodes, expect, selector) {
			return true, true
		}
		if time.Now().After(deadline) {
			return false, true
		}
		select {
		case <-ctx.Done():
			return false, false
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// dangerButtons are controls that turn a read/browse task into a purchase. The
// guard is deliberately conservative: refusing one click is recoverable, an
// unintended order is not.
// dangerButtons is PhoneGo's SPEND_WORDS plus the two cart controls that turn a
// browse into a purchase. The list is deliberately specific: a substring such as
// 支付 already covers 确认支付 / 免密支付, but keeping the full labels makes the
// audit line say which control was refused.
var dangerButtons = []string{
	"立即购买", "立即抢购", "去结算", "提交订单", "确认订单", "确认下单",
	"确认支付", "立即支付", "去支付", "确认付款", "免密支付",
	"充值", "打赏", "续费", "开通会员",
	"加入购物车",
}

func dangerWord(texts ...string) string {
	joined := strings.Join(texts, " ")
	for _, word := range dangerButtons {
		if strings.Contains(joined, word) {
			return word
		}
	}
	return ""
}

func expectMet(nodes []ui.Node, expect map[string]any, selector map[string]string) bool {
	if want, ok := expect["input_text"].(string); ok {
		node, found := findSame(nodes, selector)
		if !found {
			return false
		}
		return strings.Contains(node.Attrs["text"], want)
	}
	if want, ok := expect["selected"].(bool); ok {
		node, found := findSame(nodes, selector)
		if !found {
			return false
		}
		return node.Attrs["selected"] == strconv.FormatBool(want)
	}
	if want, ok := expect["text_contains"].(string); ok {
		for _, n := range nodes {
			if strings.Contains(n.Attrs["text"], want) || strings.Contains(n.Attrs["content-desc"], want) {
				return true
			}
		}
		return false
	}
	if want, ok := expect["enabled"].(bool); ok {
		node, found := findSame(nodes, selector)
		return found && node.Attrs["enabled"] == strconv.FormatBool(want)
	}
	return false
}

func findSame(nodes []ui.Node, selector map[string]string) (ui.Node, bool) {
	if selector["resource_id"] != "" {
		if n, ok := ui.FindByResourceID(nodes, selector["resource_id"]); ok {
			return n, true
		}
	}
	if selector["text"] != "" {
		return ui.FindByText(nodes, selector["text"])
	}
	return ui.Node{}, false
}

func actionLabel(action, target string) string {
	switch action {
	case "longpress":
		return "长按「" + target + "」"
	case "set_text":
		return "输入文字"
	default:
		return "点「" + target + "」"
	}
}
