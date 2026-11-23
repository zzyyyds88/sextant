package engine

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"chopin-core/internal/challenge"
	"chopin-core/internal/httpx"
	"chopin-core/internal/risk"
)

// D30: human profile, verification-wall classification and handover.
//
// The human profile lives in internal/human; this file is the HTTP wiring plus
// the handover state machine. The state is deliberately in memory: it describes
// what the current daemon is doing with the phone, and a daemon restart already
// means "nothing is being driven right now".

// handoverState is the "only the user can do this" window.
type handoverState struct {
	mu           sync.Mutex
	active       bool
	pkg          string
	component    string
	agentDisplay int
	since        time.Time
}

func (h *handoverState) begin(pkg, component string, agentDisplay int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.active = true
	h.pkg = pkg
	h.component = component
	h.agentDisplay = agentDisplay
	h.since = time.Now()
}

func (h *handoverState) end() (pkg, component string, agentDisplay int, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	pkg, component, agentDisplay = h.pkg, h.component, h.agentDisplay
	ok = h.active
	h.active = false
	h.pkg, h.component, h.agentDisplay = "", "", 0
	h.since = time.Time{}
	return pkg, component, agentDisplay, ok
}

func (h *handoverState) snapshot() (bool, string, string, int, time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.active, h.pkg, h.component, h.agentDisplay, h.since
}

// refuseIfHandedOver is the single gate for every injection endpoint.
//
// While the app is with the user, an automated tap would fight the finger that
// is already on the screen. Refusing is the only honest answer: 409 with the
// package, not a silent no-op.
func (s *Server) refuseIfHandedOver(w http.ResponseWriter) bool {
	active, pkg, _, _, since := s.handover.snapshot()
	if !active {
		return false
	}
	httpx.WriteError(w, http.StatusConflict, "handed_over",
		fmt.Sprintf("the app is with the user for manual verification (since %s)", since.Format("15:04:05")),
		fmt.Errorf("package %s is in handover", pkg))
	return true
}

// requestSeed resolves the seed a request records. A caller-supplied seed makes
// the request reproducible; an absent one produces a new seed that the response
// carries so the caller can record it.
func (s *Server) requestSeed(given string) string {
	if given != "" {
		return given
	}
	if s.Human == nil {
		return ""
	}
	return s.Human.NewSeed()
}

// requestIDFor derives the per-request derivation key. A caller that wants
// several injections in one run to differ sends X-Chopin-Request-Id (the CLI
// sends "<seed>:<step>"), which keeps the path reproducible.
func requestIDFor(r *http.Request) string {
	return r.Header.Get("X-Chopin-Request-Id")
}

// pace applies the D30 interval without choosing a point. It exists for the
// accessibility channel: an ACTION_CLICK is not a touch, so there is no pixel to
// place, but the rate at which one app is driven is still worth bounding.
func (s *Server) pace(ctx context.Context, display int, seed, requestID string, step int) (func(), int64, int64, error) {
	if s.Human == nil {
		return func() {}, 0, 0, nil
	}
	gap := s.Human.Gap(seed, requestID, display, step)
	release, paced, interval, err := s.Human.Begin(ctx, display, gap)
	if err != nil {
		return func() {}, 0, 0, err
	}
	return release, paced.Milliseconds(), interval.Milliseconds(), nil
}

// injectionPoint computes the coordinate to inject and blocks for the pacing
// gap. The returned release must be deferred around the injection itself.
func (s *Server) injectionPoint(ctx context.Context, display int, bounds *[4]int, x, y int, seed, requestID string, step int) (int, int, func(), int64, int64, error) {
	if s.Human == nil {
		return x, y, func() {}, 0, 0, nil
	}
	px, py := s.Human.Point(bounds, x, y, seed, requestID, display, step)
	release, paced, interval, err := s.pace(ctx, display, seed, requestID, step)
	if err != nil {
		return 0, 0, func() {}, 0, 0, err
	}
	return px, py, release, paced, interval, nil
}

// ---------------------------------------------------------------------------
// challenge

type challengeRequest struct {
	FrameID string `json:"frame_id"`
	Seed    string `json:"seed"`
}

func (s *Server) handleChallenge(w http.ResponseWriter, r *http.Request) {
	var body challengeRequest
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	ctx, cancel := contextWithTimeout(r, 20*time.Second)
	defer cancel()

	snap, err := s.Tree.Snapshot(ctx, 0)
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "ui dump failed", err)
		return
	}
	res := challenge.Classify(snap.Nodes)
	fg, _ := s.foregroundInfo(ctx)
	pkg, _ := fg["package"].(string)

	if s.Cfg.Challenge.Enabled && res.Kind == challenge.KindCode {
		if pkg != "" {
			if entry, recErr := risk.Record(s.Paths.Risk, pkg, time.Now(), s.Cfg.Challenge.RiskDays, s.Cfg.Challenge.CooldownSec); recErr != nil {
				s.Audit.Warn("challenge: cannot record risk for %s: %v", pkg, recErr)
			} else {
				s.Audit.Warn("challenge: %s asked for %s verification (count=%d)", pkg, res.Kind, entry.Count)
			}
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok": true, "kind": res.Kind, "evidence": res.Evidence,
		"package": pkg, "frame_id": body.FrameID,
	})
}

type solveRequest struct {
	FrameID string `json:"frame_id"`
	DX      int    `json:"dx"`
	Seed    string `json:"seed"`
}

// handleChallengeRisk reports the package-level challenge records so a recipe
// can decide before it starts. It is read-only and exposes no screen content.
func (s *Server) handleChallengeRisk(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	pkg := r.URL.Query().Get("package")
	if pkg != "" {
		entry, ok := risk.Current(s.Paths.Risk, pkg, now)
		out := map[string]any{"ok": true, "package": pkg, "active": ok}
		if ok {
			cooling, until := risk.Cooling(s.Paths.Risk, pkg, now)
			out["count"] = entry.Count
			out["last"] = entry.Last
			out["until"] = entry.Until
			out["cooling"] = cooling
			if cooling {
				out["cooldown_until"] = until
			}
			out["describe"] = risk.Describe(entry, now)
		}
		httpx.WriteJSON(w, http.StatusOK, out)
		return
	}
	entries := map[string]any{}
	for name := range risk.Load(s.Paths.Risk) {
		entry, ok := risk.Current(s.Paths.Risk, name, now)
		if !ok {
			continue
		}
		cooling, until := risk.Cooling(s.Paths.Risk, name, now)
		row := map[string]any{
			"count": entry.Count, "last": entry.Last, "until": entry.Until,
			"cooling": cooling, "describe": risk.Describe(entry, now),
		}
		if cooling {
			row["cooldown_until"] = until
		}
		entries[name] = row
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "entries": entries})
}

// handleChallengeSolve attempts the slider case only. A code wall is never
// solved here: the daemon cannot receive an SMS and must not guess one.
func (s *Server) handleChallengeSolve(w http.ResponseWriter, r *http.Request) {
	var body solveRequest
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if !s.Cfg.Challenge.Enabled {
		httpx.WriteError(w, http.StatusConflict, "challenge_disabled", "challenge handling is disabled in config", nil)
		return
	}
	if s.refuseIfHandedOver(w) {
		return
	}
	ctx, cancel := contextWithTimeout(r, 60*time.Second)
	defer cancel()

	snap, err := s.Tree.Snapshot(ctx, 0)
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "ui dump failed", err)
		return
	}
	res := challenge.Classify(snap.Nodes)
	switch res.Kind {
	case challenge.KindCode:
		httpx.WriteError(w, http.StatusConflict, "challenge_needs_human",
			"this wall needs a code only the user can receive; call /v1/device/handover instead",
			fmt.Errorf("evidence: %s", strings.Join(res.Evidence, ", ")))
		return
	case challenge.KindSlider:
		// fall through
	default:
		httpx.WriteError(w, http.StatusConflict, "no_challenge", "no verification wall on the current screen", nil)
		return
	}

	track, ok := challenge.SliderTarget(snap.Nodes)
	if !ok {
		httpx.WriteError(w, http.StatusConflict, "challenge_unresolved",
			"slider phrases were found but no draggable node is visible", nil)
		return
	}
	left, top, right, bottom := track.Bounds[0], track.Bounds[1], track.Bounds[2], track.Bounds[3]
	height := bottom - top
	handle := height
	if handle < 8 {
		handle = 8
	}
	if handle > (right-left)/3 {
		handle = (right - left) / 3
	}
	dx := body.DX
	if dx <= 0 {
		dx = (right - left) - handle
	}
	seed := s.requestSeed(body.Seed)
	reqID := requestIDFor(r)

	s.Audit.Info("challenge: slider on %v, dx=%d, attempts=%d",
		track.Attrs["class"], dx, s.Cfg.Challenge.SliderAttempts)

	attempts := 0
	for attempts < s.Cfg.Challenge.SliderAttempts {
		attempts++
		startX := left + handle/2
		startY := (top + bottom) / 2
		px, py, release, pacedMS, intervalMS, err := s.injectionPoint(ctx, 0, nil, startX, startY, seed, reqID, attempts)
		if err != nil {
			httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "cannot pace the gesture", err)
			return
		}
		endX := px + dx
		if endX > right-handle/2 {
			endX = right - handle/2
		}
		func() {
			defer release()
			if err := s.Device.Swipe(ctx, px, py, endX, py, 650, 0); err != nil {
				s.Audit.Warn("challenge: slider swipe failed: %v", err)
			}
		}()
		s.Audit.Info("challenge: slider attempt %d swipe %d,%d -> %d,%d paced=%dms interval=%dms",
			attempts, px, py, endX, py, pacedMS, intervalMS)
		s.waitTreeStable(ctx, 250, 3000)

		snap2, err := s.Tree.Snapshot(ctx, 0)
		if err != nil {
			continue
		}
		switch challenge.Classify(snap2.Nodes).Kind {
		case challenge.KindSlider:
			continue
		case challenge.KindCode:
			pkg, _ := s.foregroundInfo(ctx)
			pkgName, _ := pkg["package"].(string)
			if pkgName != "" {
				_, _ = risk.Record(s.Paths.Risk, pkgName, time.Now(), s.Cfg.Challenge.RiskDays, s.Cfg.Challenge.CooldownSec)
			}
			httpx.WriteError(w, http.StatusConflict, "challenge_needs_human",
				"the slider gave way to a code wall, which only the user can complete", nil)
			return
		default:
			s.noteAction("滑块验证通过")
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"ok": true, "solved": true, "attempts": attempts,
				"dx": dx, "kind": challenge.KindNone,
			})
			return
		}
	}

	pkg, _ := s.foregroundInfo(ctx)
	pkgName, _ := pkg["package"].(string)
	if pkgName != "" {
		if entry, recErr := risk.Record(s.Paths.Risk, pkgName, time.Now(), s.Cfg.Challenge.RiskDays, s.Cfg.Challenge.CooldownSec); recErr == nil {
			s.Audit.Warn("challenge: slider not solved on %s (count=%d)", pkgName, entry.Count)
		}
	}
	httpx.WriteError(w, http.StatusConflict, "challenge_unresolved",
		"the slider was attempted and the wall is still there", nil)
}

// ---------------------------------------------------------------------------
// handover

type handoverRequest struct {
	Package              string `json:"package"`
	Component            string `json:"component"`
	Reason               string `json:"reason"`
	AgentDisplay         int    `json:"agent_display"`
	LaunchOnAgentDisplay bool   `json:"launch_on_agent_display"`
}

func (s *Server) handleHandover(w http.ResponseWriter, r *http.Request) {
	var body handoverRequest
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if body.Package == "" && body.Component == "" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "package or component is required", nil)
		return
	}
	component := body.Component
	pkg := body.Package
	if component == "" {
		var err error
		component, pkg, err = s.resolveLauncher(r.Context(), body.Package)
		if err != nil {
			httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "cannot resolve the launcher activity", err)
			return
		}
	} else if pkg == "" {
		pkg = strings.SplitN(component, "/", 2)[0]
	}

	ctx, cancel := contextWithTimeout(r, 20*time.Second)
	defer cancel()
	if _, err := s.Probe.Run.Run(ctx, "am", "start", "--display", "0", "-n", component); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "handover launch failed", err)
		return
	}
	// Verify the app actually came to the user's display. On this ROM an intent
	// can be answered by another app, and the handover would then point at the
	// wrong screen while claiming success.
	landed := ""
	for i := 0; i < 10; i++ {
		fg, _ := s.foregroundInfo(ctx)
		landed, _ = fg["package"].(string)
		if landed == pkg {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(200 * time.Millisecond):
		}
	}
	if landed != pkg {
		httpx.WriteError(w, http.StatusServiceUnavailable, "handover_not_landed",
			"the app did not come to the user's display", fmt.Errorf("foreground is %q, want %q", landed, pkg))
		return
	}

	s.handover.begin(pkg, component, body.AgentDisplay)
	s.Audit.Info("handover: %s moved to display 0 (agent display %d)", pkg, body.AgentDisplay)
	s.noteAction("已交给你操作")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok": true, "active": true, "package": pkg, "component": component, "display": 0,
	})
}

func (s *Server) handleHandoverReturn(w http.ResponseWriter, r *http.Request) {
	var body struct {
		LaunchOnAgentDisplay bool `json:"launch_on_agent_display"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	pkg, component, agentDisplay, wasActive := s.handover.end()
	if !wasActive {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "active": false})
		return
	}
	s.Audit.Info("handover: %s returned from the user", pkg)
	s.noteAction("继续")
	if body.LaunchOnAgentDisplay && component != "" {
		ctx, cancel := contextWithTimeout(r, 20*time.Second)
		defer cancel()
		args := []string{"start"}
		if agentDisplay > 0 {
			args = append(args, "--display", fmt.Sprintf("%d", agentDisplay))
		}
		args = append(args, "-n", component)
		if _, err := s.Probe.Run.Run(ctx, "am", args...); err != nil {
			httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable,
				"handover cleared, but relaunching on the agent display failed", err)
			return
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok": true, "active": false, "package": pkg, "agent_display": agentDisplay,
	})
}

func (s *Server) handleHandoverStatus(w http.ResponseWriter, r *http.Request) {
	active, pkg, component, display, since := s.handover.snapshot()
	out := map[string]any{"ok": true, "active": active}
	if active {
		out["package"] = pkg
		out["component"] = component
		out["agent_display"] = display
		out["since"] = since.Format(time.RFC3339)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// resolveLauncher maps a package to its launcher component.
//
// `monkey -p` is not usable on this ROM (no shebang on /system/bin/monkey), so
// the component is resolved first and started directly.
func (s *Server) resolveLauncher(ctx context.Context, pkg string) (component, resolvedPkg string, err error) {
	out, err := s.Probe.Run.Run(ctx, "cmd", "package", "resolve-activity",
		"--brief", "-c", "android.intent.category.LAUNCHER", pkg)
	if err != nil {
		return "", pkg, err
	}
	component = lastComponentLine(out)
	if component == "" {
		return "", pkg, fmt.Errorf("no launcher activity for %s", pkg)
	}
	return component, pkg, nil
}
