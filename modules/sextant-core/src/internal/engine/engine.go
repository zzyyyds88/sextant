// Package engine wires the daemon together: config, status, tokens, probe
// layers, keep-alive loop and the HTTP server.
package engine

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"sextant-core/internal/a11y"
	"sextant-core/internal/audit"
	"sextant-core/internal/bg"
	"sextant-core/internal/config"
	"sextant-core/internal/device"
	"sextant-core/internal/display"
	"sextant-core/internal/helper"
	"sextant-core/internal/httpx"
	"sextant-core/internal/hud"
	"sextant-core/internal/human"
	"sextant-core/internal/ime"
	"sextant-core/internal/metrics"
	"sextant-core/internal/pairing"
	"sextant-core/internal/perms"
	"sextant-core/internal/session"
	"sextant-core/internal/status"
	"sextant-core/internal/sysprobe"
	"sextant-core/internal/token"
	"sextant-core/internal/trigger"
	"sextant-core/internal/ui"
	"sextant-core/internal/unlock"
)

// Version is stamped at build time.
var Version = "4.0.1-dev"

// Paths bundles every runtime location derived from the data dir.
type Paths struct {
	DataDir   string
	Config    string
	StateDir  string
	LogDir    string
	AppDir    string
	Status    string
	AppStatus string
	AppAPI    string
	AppToken  string
	AppHud    string
	Lock      string
	PID       string
	Tokens    string
	Pairing   string
	Baseline  string
	Risk      string
	Log       string
	Tmp       string
	PKI       string
}

// NewPaths derives all runtime paths.
func NewPaths(dataDir string) Paths {
	join := func(parts ...string) string { return filepath.Join(append([]string{dataDir}, parts...)...) }
	state := join("state")
	app := join("app")
	return Paths{
		DataDir:  dataDir,
		Config:   join("config", "core.json"),
		StateDir: state,
		LogDir:   join("log"),
		AppDir:   app,
		Status:   filepath.Join(state, "status.json"),
		// AppStatus/AppAPI/AppToken are filled in once the package uid is known:
		// they live in the app's own CE data dir, because /data/adb is 0700 root
		// and a normal app can never traverse it (DESIGN D23).
		AppStatus: filepath.Join(app, "status.json"),
		AppAPI:    filepath.Join(app, "api.json"),
		AppToken:  filepath.Join(app, "app-token"),
		AppHud:    filepath.Join(app, "hud.json"),
		Lock:      filepath.Join(state, "engine.lock"),
		PID:       filepath.Join(state, "engine.pid"),
		Tokens:    filepath.Join(state, "tokens.json"),
		Pairing:   filepath.Join(state, "pairing.json"),
		Baseline:  filepath.Join(state, "baseline"),
		Risk:      filepath.Join(state, "risk.json"),
		Log:       join("log", "core.log"),
		Tmp:       join("tmp"),
		PKI:       join("pki"),
	}
}

// Server is the running daemon.
type Server struct {
	Cfg       config.Config
	Paths     Paths
	ModuleDir string
	Probe     sysprobe.Probe
	Tokens    *token.Store
	Pairing   *pairing.Manager
	Status    *status.Publisher
	Audit     *audit.Log
	A11y      a11y.Layer
	Bg        bg.Layer
	Perms     perms.Layer
	Unlock    *unlock.Manager
	Sessions  *session.Manager
	Displays  display.Manager
	HUD       *hud.Hub
	Triggers  *trigger.Engine
	IME       *ime.Manager
	Device    device.Layer
	Dumper    ui.Dumper
	Tree      ui.TreeService
	Helper    *helper.Client

	// Human is the D30 injection profile (seeded points, pacing). One Book per
	// daemon; its per-display locks serialise injections the way the session
	// layer already serialises writers.
	Human *human.Book
	// handover is the D30 "only the user can do this" state. While it is active
	// every injection endpoint refuses instead of quietly competing with the
	// person holding the phone.
	handover handoverState

	started time.Time
	mu      sync.Mutex
	lockf   *os.File

	// frames is the recent display-0 JPEG shared by /v1/ui/frame and the pixel
	// primitives (D28). Its own mutex keeps capture handlers independent of mu.
	frames frameCache

	// Metrics is the D33 in-memory aggregator. It is never persisted: restart
	// resets it, and nothing here reaches status.json.
	Metrics *metrics.Aggregator

	// versions caches package versionName lookups for /v1/ui/page (D33).
	versions appVersions

	// treeFrames keeps recent observed trees for /v1/ui/diff (D33).
	treeFrames treeFrames

	// taskMu serialises session claims against finishTask's "last session ->
	// lock the screen" decision, so a new task cannot be locked out mid-claim.
	taskMu sync.Mutex

	// appFilesReady is false while the app's CE data dir is unavailable (the
	// daemon starts before the first unlock). keepalivePass retries the publish;
	// without this, a freshly installed package never receives status.json /
	// hud.json and the overlay stays unprovisioned forever.
	appFilesReady  bool
	appFilesWarned bool
}

// Option mutates the server before Run (tests use it to inject a fake runner).
type Option func(*Server)

// WithRunner injects the command runner.
func WithRunner(r sysprobe.Runner) Option {
	return func(s *Server) { s.Probe = sysprobe.Probe{Run: r} }
}

// New builds a Server from a config.
func New(cfg config.Config, opts ...Option) (*Server, error) {
	p := NewPaths(cfg.DataDir)
	s := &Server{Cfg: cfg, Paths: p, Probe: sysprobe.Probe{Run: sysprobe.ExecRunner{}}, Metrics: metrics.New()}
	for _, o := range opts {
		o(s)
	}
	exe, _ := os.Executable()
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	s.ModuleDir = moduleDirOf(exe)
	if err := s.init(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Server) init() error {
	for _, dir := range []string{s.Paths.DataDir, s.Paths.StateDir, s.Paths.LogDir, s.Paths.AppDir, s.Paths.Baseline, s.Paths.Tmp, s.Paths.PKI} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	tokens, err := token.Open(s.Paths.Tokens)
	if err != nil {
		return err
	}
	s.Tokens = tokens
	pair, err := pairing.Open(s.Paths.Pairing)
	if err != nil {
		return err
	}
	s.Pairing = pair
	s.Audit = audit.Open(s.Paths.Log, s.Cfg.Audit.MaxBytes, 500)

	appUID := 0
	if uid, err := s.Probe.PackageUID(context.Background(), s.Cfg.App.Package); err == nil {
		appUID = uid
	}
	s.Status = status.NewPublisher(s.Paths.Status, appUID, s.Paths.AppStatus)
	// Publish the observation interval and the session limits so clients can
	// show them without hardcoding a second copy of the config.
	_, _ = s.Status.Update(func(st *status.Status) {
		st.Sessions.Max = s.Cfg.Session.MaxWriteSessions
		st.App.Package = s.Cfg.App.Package
	})
	s.A11y = a11y.Layer{Probe: s.Probe, AppPkg: s.Cfg.App.Package, Component: s.Cfg.App.A11y, Compat: s.Cfg.App.A11yCompat, SettleMax: s.Cfg.Keepalive.A11y.SettleMaxSec}
	s.Bg = bg.Layer{Probe: s.Probe}
	s.Perms = perms.Layer{Probe: s.Probe}
	s.Unlock = &unlock.Manager{
		ReleaseUI: func(ctx context.Context) {
			// The helper owns UiAutomation whenever it has run OCR; the dump the
			// unlock path needs would be killed. Ask it to let go (best effort:
			// no helper running means nothing holds the session).
			if err := s.Helper.StopAutomation(ctx); err != nil {
				s.Audit.Info("unlock: helper automation release: %v", err)
			}
		},
		Probe:     s.Probe,
		Dumper:    ui.Dumper{Probe: s.Probe, Tmp: s.Paths.Tmp},
		Secret:    s.Cfg.Path(s.Cfg.Unlock.CredentialFile),
		SIMSecret: s.Cfg.Path(s.Cfg.Unlock.SIMPinFile),
		Attempt:   s.Cfg.Unlock.Attempts,
		Cooldown:  time.Duration(s.Cfg.Unlock.CooldownSec) * time.Second,
	}
	s.Device = device.Layer{Probe: s.Probe}
	s.Human = human.New(human.Params{
		Enabled:    s.Cfg.Human.Enabled,
		GapMinMS:   s.Cfg.Human.GapMinMS,
		GapMaxMS:   s.Cfg.Human.GapMaxMS,
		TapInset:   s.Cfg.Human.TapInset,
		JitterPx:   s.Cfg.Human.JitterPx,
		WaitMinPct: s.Cfg.Human.WaitMinPct,
		WaitMaxPct: s.Cfg.Human.WaitMaxPct,
	})
	s.Dumper = ui.Dumper{Probe: s.Probe, Tmp: s.Paths.Tmp}
	s.Helper = helper.New(helper.Options{
		DataDir: s.Cfg.DataDir,
		Jar:     s.modulePath(s.Cfg.Helper.Dex),
		Socket:  s.Cfg.Helper.Socket,
		Token:   s.Cfg.DataDir + "/state/helper.token",
		Log:     func(f string, a ...any) { s.Audit.Info(f, a...) },
		OnCall:  func(err error, ms int64) { s.Metrics.HelperCall(err, ms) },
	})
	// UiAutomation is a single global session: while the helper holds it, a
	// `uiautomator dump` started elsewhere is killed. So the helper wins whenever
	// it is up, and the dump is used only when the helper is not running.
	// (Wired here, after the helper exists: reading s.Helper inside the closures
	// keeps them correct even though the service outlives construction.)
	s.Tree = ui.TreeService{
		Dumper: s.Dumper,
		Helper: ui.HelperSource{Call: func(ctx context.Context, method string, params map[string]any, out any) error {
			return s.Helper.Call(ctx, method, params, out)
		}},
		HelperBusy: func(ctx context.Context) bool { return s.Helper != nil && s.Helper.Available(ctx) },
	}
	sess, err := session.Open(s.Paths.StateDir+"/sessions.json", session.Config{
		IdleSec:          s.Cfg.Session.IdleSec,
		StaleSec:         s.Cfg.Session.StaleSec,
		LingerSec:        s.Cfg.Session.LingerSec,
		HeartbeatSec:     s.Cfg.Session.HeartbeatSec,
		GuardStaleSec:    s.Cfg.Session.GuardStaleSec,
		RosterBanSec:     s.Cfg.Session.RosterBanSec,
		MaxWriteSessions: s.Cfg.Session.MaxWriteSessions,
	})
	if err != nil {
		return err
	}
	s.Sessions = sess
	s.Triggers = mustTriggers(s)
	s.IME = ime.New(s.Probe.Run, ime.Target{
		Service:   s.Cfg.App.IMEService,
		Authority: s.Cfg.App.IMEProvider,
		Package:   s.Cfg.App.Package,
	})
	s.IME.PreviousFile = filepath.Join(s.Paths.StateDir, "ime.previous")
	s.HUD = hud.NewHub(time.Duration(s.Cfg.Hud.HeartbeatSec) * time.Second)
	s.HUD.SetCallbacks(func(reason string) {
		n := s.Sessions.ReleaseAll(reason)
		if n > 0 {
			s.Audit.Warn("hud: released %d session(s) after stop", n)
		}
		s.finishTask("hud_stop")
	}, func(paused bool) {
		s.Audit.Info("hud: overlay paused=%v", paused)
	})
	s.Displays = display.Manager{
		Jar:     s.modulePath(s.Cfg.Scrcpy.Jar),
		DataDir: s.Cfg.DataDir,
		Scid:    func() int { return int(time.Now().UnixNano() & 0x7fffffff) },
	}
	s.started = time.Now()
	return nil
}

// Run starts the HTTP server and the keep-alive loop, and blocks until SIGTERM.
func (s *Server) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if err := s.acquireLock(); err != nil {
		return err
	}
	defer s.releaseLock()

	cert, err := s.ensureCert()
	if err != nil {
		return err
	}
	if err := s.ensureCLILink(); err != nil {
		s.Audit.Warn("cli link: %v", err)
	}
	if err := s.publishAppFiles(); err != nil {
		s.Audit.Warn("app files: %v", err)
		s.appFilesWarned = true
	} else {
		s.appFilesReady = true
	}
	if err := s.refreshStatus(ctx); err != nil {
		s.Audit.Warn("initial status: %v", err)
	}

	mux := s.routes()
	srv := &http.Server{
		Addr:              s.Cfg.Listen,
		Handler:           s.withMetrics(mux),
		TLSConfig:         &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
		ReadHeaderTimeout: 10 * time.Second,
	}
	ln, err := net.Listen("tcp", s.Cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.Cfg.Listen, err)
	}

	// Triggers run for the daemon's whole life. They are started before the
	// listener so a watcher that cannot start is reported in the startup log
	// rather than after the first request.
	go s.Triggers.Run(ctx)
	// D35: reap expired write sessions in the background. HUD stop/disconnect
	// still release immediately; this covers an agent that vanished without one.
	go s.Sessions.ReapLoop(ctx, time.Duration(s.Cfg.Session.ReapSec)*time.Second, func(n int) {
		s.publishSessions()
		s.Audit.Warn("session: reaped %d expired session(s)", n)
		s.finishTask("reap_loop")
	})
	// D37: a live heartbeat is not progress. Reap sessions that keep the
	// heartbeat but have no business activity.
	go s.Sessions.ZombieLoop(ctx, time.Duration(s.Cfg.Session.ReapSec)*time.Second,
		time.Duration(s.Cfg.Session.ZombieSec)*time.Second, func(n int) {
			s.publishSessions()
			s.Audit.Warn("session: reaped %d zombie session(s) (no business activity)", n)
			s.finishTask("zombie_loop")
		})
	s.Audit.Info("daemon: listening on %s (version %s, pid %d)", s.Cfg.Listen, Version, os.Getpid())

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := srv.ServeTLS(ln, "", ""); err != nil && err != http.ErrServerClosed {
			log.Printf("serve: %v", err)
			cancel()
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.keepaliveLoop(ctx)
	}()
	if s.Cfg.Unlock.AutoOnBoot {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.bootUnlock(ctx)
		}()
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	select {
	case <-ctx.Done():
	case <-sig:
	}
	shutdownCtx, sc := context.WithTimeout(context.Background(), 10*time.Second)
	defer sc()
	_ = srv.Shutdown(shutdownCtx)
	cancel()
	wg.Wait()
	// Hand the device's default IME back before exiting. Leaving it on ours means
	// the user's next sentence is typed into a 48dp invisible TextView.
	stopCtx, sc2 := context.WithTimeout(context.Background(), 15*time.Second)
	if err := s.IME.Restore(stopCtx); err != nil {
		s.Audit.Warn("ime: could not restore the previous default: %v", err)
	} else {
		s.Audit.Info("ime: previous default restored")
	}
	sc2()
	s.Audit.Info("daemon: stopped")
	_ = os.Remove(s.Paths.PID)
	return nil
}

func (s *Server) acquireLock() error {
	f, err := os.OpenFile(s.Paths.Lock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open lock: %w", err)
	}
	// A restart right after a stop is normal (activate-device does exactly that),
	// and the outgoing daemon needs a moment to release the lock: its shutdown
	// waits up to 10s for in-flight requests. Failing on the first EWOULDBLOCK
	// turned that ordinary overlap into "nothing is running and nobody noticed",
	// so retry for as long as a graceful shutdown can take.
	deadline := time.Now().Add(15 * time.Second)
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK || time.Now().After(deadline) {
			_ = f.Close()
			return fmt.Errorf("another daemon already holds %s: %w", s.Paths.Lock, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	s.mu.Lock()
	s.lockf = f
	s.mu.Unlock()
	if err := os.WriteFile(s.Paths.PID, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600); err != nil {
		return fmt.Errorf("write pid: %w", err)
	}
	return nil
}

func (s *Server) releaseLock() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lockf != nil {
		_ = syscall.Flock(int(s.lockf.Fd()), syscall.LOCK_UN)
		_ = s.lockf.Close()
		s.lockf = nil
	}
}

// keepaliveLoop runs the a11y / background / permission passes on an interval.
func (s *Server) keepaliveLoop(ctx context.Context) {
	interval := time.Duration(s.Cfg.Keepalive.IntervalSec) * time.Second
	if interval < 10*time.Second {
		interval = 10 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.keepalivePass(ctx)
		}
	}
}

func (s *Server) keepalivePass(ctx context.Context) {
	passCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	// The daemon starts before the first unlock; /data/user/0/<pkg> is CE and
	// does not exist yet. Retry the app-file publish until it succeeds, otherwise
	// a freshly installed package never gets status.json/hud.json.
	if !s.appFilesReady {
		if err := s.publishAppFiles(); err != nil {
			if !s.appFilesWarned {
				s.Audit.Warn("app files: %v", err)
				s.appFilesWarned = true
			}
		} else {
			s.appFilesReady = true
			s.appFilesWarned = false
			s.Audit.Info("app files: published after unlock")
		}
	}

	if s.Cfg.Keepalive.A11y.Enabled {
		res := s.A11y.Restore(passCtx, s.Cfg.App.A11y)
		var prev status.A11y
		s.Status.Update(func(st *status.Status) {
			prev = st.App.A11y
			st.App.A11y = status.A11y{State: string(res.State), Detail: res.Detail, Component: s.Cfg.App.A11y, Compat: s.Cfg.App.A11yCompat}
		})
		// Log every change, not every pass: a keep-alive that silently repairs is
		// indistinguishable from one that never had to, and the difference is what
		// tells you whether the phone or the daemon is doing the work.
		if prev.State != string(res.State) || prev.Detail != res.Detail {
			s.Audit.Info("a11y: %s -> %s (%s)", describeState(prev.State, prev.Detail), res.State, res.Detail)
		}
	}
	if s.Cfg.Keepalive.Bg.Enabled {
		res := s.Bg.Apply(passCtx, s.Cfg.App.Package, bg.Policy{
			Autostart: s.Cfg.Keepalive.Bg.Autostart,
			BgControl: s.Cfg.Keepalive.Bg.BgControl,
			Doze:      s.Cfg.Keepalive.Bg.Doze,
		})
		s.Status.Update(func(st *status.Status) {
			st.App.Bg = status.Background{
				Autostart: res.Reading.Autostart,
				BgControl: res.Reading.BgControl,
				Doze:      res.Reading.Doze,
				OK:        res.State == status.OK,
			}
		})
	}
	// A silent overlay is a lost safety net, and DESIGN 9.4 says that releases the
	// write sessions: an agent must not keep driving a screen where nobody can
	// press Stop. Expiry is checked here rather than in the request path because a
	// dead overlay produces no requests at all.
	if expired, silent := s.HUD.Expire(); expired {
		if n := s.Sessions.ReleaseAll("hud_disconnected"); n > 0 {
			s.publishSessions()
			s.Audit.Warn("hud: silent for %s; released %d session(s)", silent.Round(time.Second), n)
			s.finishTask("hud_disconnected")
		}
	}
	// An action makes the overlay visible even without a claim, so something has to
	// take it back down when the actions stop.
	//
	// This was 10 s, and 10 s is too short for real work: an agent that taps, then
	// reads the screen for 15 s, then taps again, produced an overlay that blinked in
	// and out and looked broken. 25 s outlasts ordinary think-and-read gaps while
	// still clearing itself soon after the agent stops.
	idle := time.Duration(s.Cfg.Hud.IdleHideSec) * time.Second
	if idle <= 0 {
		idle = 25 * time.Second
	}
	s.HUD.QuietAfter(idle)
	if len(s.Cfg.Keepalive.Perms) > 0 {
		states, _ := s.Perms.Apply(passCtx, s.Cfg.App.Package, s.Cfg.Keepalive.Perms)
		s.Status.Update(func(st *status.Status) {
			st.App.Perms = status.Perms(states)
		})
	}
	_ = s.refreshStatus(passCtx)
}

// describeState renders a previous state for a transition log line.
func describeState(state, detail string) string {
	if state == "" {
		return "unknown"
	}
	if detail == "" {
		return state
	}
	return state + "/" + detail
}

// refreshStatus reads the cheap facts every client needs.
func (s *Server) refreshStatus(ctx context.Context) error {
	installed, _ := s.Probe.PackageInstalled(ctx, s.Cfg.App.Package)
	versionName := s.packageVersion(ctx)
	// The keyguard is the signal that matters: `dumpsys user` reports
	// RUNNING_UNLOCKED from the first unlock on, while the phone can be locked
	// again (screen off) without that changing. Falling back to the user state
	// when the keyguard cannot be read keeps the field honest.
	locked := false
	if showing, err := s.Probe.KeyguardShowing(ctx); err == nil {
		locked = showing
	} else if ok, uerr := s.Probe.UserUnlocked(ctx); uerr == nil {
		locked = !ok
	}
	a11yState := s.A11y.Status(ctx)
	s.Status.Update(func(st *status.Status) {
		st.Daemon = status.Daemon{
			PID: os.Getpid(), Version: Version, State: "running",
			UptimeSec: int64(time.Since(s.started).Seconds()),
		}
		st.App.Package = s.Cfg.App.Package
		st.App.Installed = installed
		st.App.VersionName = versionName
		st.App.A11y = status.A11y{
			State: string(a11yState.State), Detail: a11yState.Detail,
			Component: s.Cfg.App.A11y, Compat: s.Cfg.App.A11yCompat,
		}
		st.Unlock = unlock.StatusValue(s.Unlock.CredentialPresent(), locked, st.Unlock.LastAttempt)
	})
	return nil
}

// packageVersion reads the installed APK's versionName for the status page.
func (s *Server) packageVersion(ctx context.Context) string {
	return s.appVersion(ctx, s.Cfg.App.Package)
}

type versionEntry struct {
	version string
	at      time.Time
}

// appVersions is a bounded 30s cache behind app_version. A dumpsys per page
// read would add ~100ms to every perception call, and the version does not
// change between two reads a second apart.
type appVersions struct {
	mu sync.Mutex
	m  map[string]versionEntry
}

const (
	appVersionTTL     = 30 * time.Second
	appVersionMaxSize = 32
)

func (c *appVersions) get(pkg string, now time.Time) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[pkg]
	if !ok || now.Sub(e.at) > appVersionTTL {
		return "", false
	}
	return e.version, true
}

func (c *appVersions) put(pkg, version string, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]versionEntry{}
	}
	if len(c.m) >= appVersionMaxSize {
		for key, e := range c.m {
			if now.Sub(e.at) > appVersionTTL {
				delete(c.m, key)
			}
		}
		if len(c.m) >= appVersionMaxSize {
			return
		}
	}
	c.m[pkg] = versionEntry{version: version, at: now}
}

// appVersion returns the installed versionName for pkg, or "" when it cannot be
// read. The empty value is honest: an unknown version must not look like a
// match in a drift comparison.
func (s *Server) appVersion(ctx context.Context, pkg string) string {
	if pkg == "" {
		return ""
	}
	now := time.Now()
	if v, ok := s.versions.get(pkg, now); ok {
		return v
	}
	out, err := s.Probe.Run.Run(ctx, "dumpsys", "package", pkg)
	if err != nil {
		return ""
	}
	version := ""
	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "versionName=") {
			version = strings.TrimPrefix(t, "versionName=")
			break
		}
	}
	s.versions.put(pkg, version, now)
	return version
}

// bootUnlock unlocks once after boot when the device is locked.
func (s *Server) bootUnlock(ctx context.Context) {
	// Give the framework a moment to settle so the keyguard state is real.
	select {
	case <-ctx.Done():
		return
	case <-time.After(8 * time.Second):
	}
	if !s.Unlock.CredentialPresent() {
		s.Status.Update(func(st *status.Status) { st.Unlock.LastAttempt = "skipped: no credential" })
		return
	}
	if ok, err := s.Probe.UserUnlocked(ctx); err == nil && ok {
		return
	}
	if _, err := s.Unlock.EnsureUnlocked(ctx); err != nil {
		s.Audit.Warn("boot unlock: %v", err)
		s.Status.Update(func(st *status.Status) { st.Unlock.LastAttempt = err.Error() })
		return
	}
	s.Audit.Info("boot unlock: succeeded")
	s.Status.Update(func(st *status.Status) { st.Unlock.LastAttempt = "ok" })
}

// ensureCLILink puts the binary on the su PATH.
//
// service.sh does this at boot; doing it here too means a daemon started by hand
// (during development, or after a manual restart) still leaves
// `su -c sextant-core ...` working, which is the only entry point the APK and
// `sextant pair` have.
func (s *Server) ensureCLILink() error {
	binDir := "/data/adb/ksu/bin"
	if st, err := os.Stat("/data/adb/ksu"); err != nil || !st.IsDir() {
		return nil // no KernelSU bin dir: nothing to link into
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return err
	}
	link := filepath.Join(binDir, "sextant-core")
	_ = os.Remove(link)
	return os.Symlink(self, link)
}

// modulePath resolves a path that ships INSIDE the module (helper.jar,
// scrcpy-server.jar, webui assets).
//
// These are not runtime state: they live next to the binary, in the module
// directory, and are replaced wholesale on upgrade. Resolving them against
// data_dir would put them in a directory the installer never populates.
func (s *Server) modulePath(rel string) string {
	if rel == "" {
		return ""
	}
	if rel[0] == '/' {
		return rel
	}
	return s.ModuleDir + "/" + rel
}

// moduleDir is the directory the running binary belongs to.
func moduleDirOf(exe string) string {
	// <module>/bin/sextant-core → <module>
	dir := filepath.Dir(exe)
	if filepath.Base(dir) == "bin" {
		return filepath.Dir(dir)
	}
	return dir
}

// appDataDir is where the app can actually read from.
//
// Not /data/adb: that directory is 0700 root, so a normal app cannot even
// traverse it, no matter how the files inside are chowned. The app's own CE data
// dir is the only place both sides can reach — the daemon writes it as root, the
// app reads it as itself.
func appDataDir(pkg string) string { return "/data/user/0/" + pkg + "/files" }

// bindAppPaths points the published copies at the app's private dir.
func (s *Server) bindAppPaths() {
	dir := appDataDir(s.Cfg.App.Package)
	s.Paths.AppDir = dir
	s.Paths.AppStatus = filepath.Join(dir, "status.json")
	s.Paths.AppAPI = filepath.Join(dir, "api.json")
	s.Paths.AppToken = filepath.Join(dir, "app-token")
	s.Paths.AppHud = filepath.Join(dir, "hud.json")
}

// publishAppFiles writes the discovery files the APK reads, chowned to its uid.
func (s *Server) publishAppFiles() error {
	uid, err := s.Probe.PackageUID(context.Background(), s.Cfg.App.Package)
	if err != nil {
		return err
	}
	s.bindAppPaths()
	if err := os.MkdirAll(s.Paths.AppDir, 0o700); err != nil {
		return fmt.Errorf("app data dir: %w", err)
	}
	if err := os.Chown(s.Paths.AppDir, uid, uid); err != nil {
		return fmt.Errorf("chown app dir: %w", err)
	}
	if err := os.Chmod(s.Paths.AppDir, 0o700); err != nil {
		return err
	}
	// The publisher was constructed before the uid was known; repoint its copies.
	s.Status.SetCopies([]string{s.Paths.AppStatus}, uid)
	cert, err := s.certFingerprint()
	if err != nil {
		return err
	}
	api, err := jsonMarshalIndent(map[string]any{
		"schema": 1, "url": s.Cfg.Listen, "cert_sha256": cert, "app_token_path": s.Paths.AppToken,
	})
	if err != nil {
		return err
	}
	if err := writeOwned(s.Paths.AppAPI, api, uid); err != nil {
		return err
	}
	// The overlay's own provisioning file.
	//
	// The APK's accessibility service cannot be given this over its own status
	// endpoint: it needs the token, the pinned certificate and the socket secret
	// BEFORE it can talk to anything. It polls for this file, so writing it here
	// (chowned to the app) is what turns the overlay on.
	if err := s.publishHudConfig(uid, cert); err != nil {
		s.Audit.Warn("hud config: %v", err)
	}
	// The app token is minted once and reused, so an app restart keeps working.
	//
	// The check is on the FILE, not only on the token record: the plaintext is
	// stored exactly once (only the hash stays on the device), so a record whose
	// file was deleted can never be handed out again. In that case the record is
	// revoked and a fresh token is minted, otherwise the app would silently have
	// no token forever.
	if rec := s.Tokens.Find("app"); rec != nil {
		if data, err := os.ReadFile(s.Paths.AppToken); err == nil && len(strings.TrimSpace(string(data))) > 0 {
			return nil
		}
		s.Audit.Warn("app token file missing; issuing a new app token")
		_, _ = s.Tokens.Revoke("app")
	}
	tok, _, err := s.Tokens.Create("app", token.AppScopes(), true)
	if err != nil {
		return err
	}
	return writeOwned(s.Paths.AppToken, []byte(tok+"\n"), uid)
}

// publishHudConfig writes the overlay's provisioning file.
//
// The existing token is reused when it still authenticates: the APK reloads this
// file whenever it changes, and rewriting it with a fresh token every start would
// disconnect a perfectly healthy overlay for no reason.
func (s *Server) publishHudConfig(uid int, certSHA string) error {
	tok := s.existingHUDToken()
	if tok == "" {
		var err error
		tok, _, err = s.Tokens.Create("hud", token.HUDScopes(), true)
		if err != nil {
			return fmt.Errorf("hud token: %w", err)
		}
		s.Audit.Info("hud: issued a new overlay token")
	}
	_, port, err := net.SplitHostPort(s.Cfg.Listen)
	if err != nil {
		port = "8443"
	}
	payload, err := jsonMarshalIndent(map[string]any{
		// Loopback, not the configured bind address: the overlay runs on this
		// phone and the hud endpoints reject anything that is not loopback.
		"schema":             1,
		"server_url":         "https://127.0.0.1:" + port,
		"server_cert_sha256": certSHA,
		"token":              tok,
		"agent":              "hud",
	})
	if err != nil {
		return err
	}
	return writeOwned(s.Paths.AppHud, payload, uid)
}

// existingHUDToken returns the token already published, if it still works.
func (s *Server) existingHUDToken() string {
	data, err := os.ReadFile(s.Paths.AppHud)
	if err != nil {
		return ""
	}
	var cfg struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil || cfg.Token == "" {
		return ""
	}
	rec, err := s.Tokens.Authenticate(cfg.Token)
	if err != nil || rec == nil || rec.Agent != "hud" {
		return ""
	}
	allowed := false
	for _, sc := range rec.Scopes {
		if sc == "hud" {
			allowed = true
			break
		}
	}
	if !allowed {
		return ""
	}
	return cfg.Token
}

func writeOwned(path string, data []byte, uid int) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if uid > 0 {
		if err := os.Chown(tmp, uid, uid); err != nil {
			_ = os.Remove(tmp)
			return err
		}
	}
	return os.Rename(tmp, path)
}

// ensureCert loads the self-signed certificate, generating it on first run.
func (s *Server) ensureCert() (tls.Certificate, error) {
	certPath := filepath.Join(s.Paths.PKI, "server.crt")
	keyPath := filepath.Join(s.Paths.PKI, "server.key")
	if _, err := os.Stat(certPath); err == nil {
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("load TLS pair: %w", err)
		}
		return cert, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "sextant-core", Organization: []string{"sextant"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(5, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              []string{"localhost", "sextant-core"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(certPEM, keyPEM)
}

// certFingerprint is the SHA-256 of the DER certificate, for client pinning.
func (s *Server) certFingerprint() (string, error) {
	certPEM, err := os.ReadFile(filepath.Join(s.Paths.PKI, "server.crt"))
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return "", fmt.Errorf("no PEM block in server.crt")
	}
	sum := sha256Sum(block.Bytes)
	return sum, nil
}

// Auth returns the middleware used by the routes.
func (s *Server) Auth() *httpx.Authenticator {
	return &httpx.Authenticator{Store: s.Tokens, OnAuthorized: s.touchSession}
}
