// Package metrics is the D33 in-memory observability aggregator.
//
// It exists so an agent can answer "is this device getting slower?" without a
// time-series database and without the daemon growing a second state source:
// everything here is derived from requests the daemon already served, lives
// only in memory, and resets on restart.
package metrics

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// samplesPerRoute bounds the ring buffer behind p50/p95. 256 samples is enough
// for a percentile that is not noise, and the total memory is
// routes*samples*8 bytes, so the label set must stay closed.
const samplesPerRoute = 256

// routeOther is where anything outside the fixed route set lands. The set is
// closed on purpose: a metric label taken from a request would be unbounded and
// would leak caller-supplied strings into the status surface.
const routeOther = "other"

// routes is the closed label set. It mirrors the endpoint groups in DESIGN §8;
// a new endpoint that should be visible here is added deliberately.
var routes = []string{
	"/v1/health", "/v1/status", "/v1/device", "/v1/device/state",
	"/v1/device/lock-state", "/v1/device/unlock", "/v1/device/unlock-local",
	"/v1/ui/tree", "/v1/ui/screenshot", "/v1/ui/frame", "/v1/ui/observe",
	"/v1/ui/act", "/v1/ui/list", "/v1/ui/options", "/v1/ui/readback",
	"/v1/ui/wait_stable", "/v1/ui/page", "/v1/ui/diff",
	"/v1/ui/ocr", "/v1/ui/find", "/v1/ui/wait", "/v1/ui/chain",
	"/v1/ui/challenge", "/v1/ui/challenge/solve", "/v1/challenge/risk",
	"/v1/input/tap", "/v1/input/text", "/v1/input/global", "/v1/input/ime/text",
	"/v1/sessions", "/v1/apps", "/v1/apps/launch", "/v1/files",
	"/v1/notifications", "/v1/sms/messages", "/v1/location", "/v1/watchdog",
	"/v1/settings/daily", "/v1/audio", "/v1/triggers", "/v1/stream/stats",
	"/v1/metrics",
}

var routeSet = func() map[string]bool {
	m := make(map[string]bool, len(routes))
	for _, r := range routes {
		m[r] = true
	}
	return m
}()

// Route maps a request pattern onto the closed label set. Go 1.22 patterns
// carry the method ("GET /v1/ui/page"); the label drops it so one endpoint has
// one line regardless of method.
func Route(pattern string) string {
	if i := strings.IndexByte(pattern, ' '); i >= 0 {
		pattern = pattern[i+1:]
	}
	if routeSet[pattern] {
		return pattern
	}
	return routeOther
}

type routeStats struct {
	Count  int64
	Errors int64
	ring   [samplesPerRoute]int64
	next   int
	filled int
	total  int64
}

func (r *routeStats) add(ms int64) {
	r.Count++
	r.total += ms
	r.ring[r.next] = ms
	r.next = (r.next + 1) % samplesPerRoute
	if r.filled < samplesPerRoute {
		r.filled++
	}
}

// percentile returns the p-th percentile of the retained samples. With no
// samples it returns 0, which the response renders as an explicit zero rather
// than a missing field.
func (r *routeStats) percentile(p float64) int64 {
	if r.filled == 0 {
		return 0
	}
	buf := make([]int64, r.filled)
	copy(buf, r.ring[:r.filled])
	sort.Slice(buf, func(i, j int) bool { return buf[i] < buf[j] })
	idx := int(float64(len(buf)-1) * p)
	if idx < 0 {
		idx = 0
	}
	return buf[idx]
}

func (r *routeStats) max() int64 {
	var m int64
	for i := 0; i < r.filled; i++ {
		if r.ring[i] > m {
			m = r.ring[i]
		}
	}
	return m
}

// Aggregator is safe for concurrent use.
type Aggregator struct {
	mu      sync.Mutex
	started time.Time
	byRoute map[string]*routeStats
	total   int64
	ok2xx   int64
	err4xx  int64
	err5xx  int64

	helperCalls  int64
	helperErrors int64
	helperMS     int64

	observe    int64
	act        int64
	actsOK     int64
	actsFailed int64
	failures   map[string]int64
	frameCount int64
	frameBytes int64
}

// New returns an aggregator whose uptime starts now.
func New() *Aggregator {
	return &Aggregator{started: time.Now(), byRoute: map[string]*routeStats{}, failures: map[string]int64{}}
}

// Observe records one served request.
func (a *Aggregator) Observe(pattern string, status int, ms int64) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	label := Route(pattern)
	stats := a.byRoute[label]
	if stats == nil {
		stats = &routeStats{}
		a.byRoute[label] = stats
	}
	stats.add(ms)
	a.total++
	switch {
	case status >= 500:
		a.err5xx++
		stats.Errors++
	case status >= 400:
		a.err4xx++
		stats.Errors++
	default:
		a.ok2xx++
	}
}

// HelperCall records one helper RPC.
func (a *Aggregator) HelperCall(err error, ms int64) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.helperCalls++
	a.helperMS += ms
	if err != nil {
		a.helperErrors++
	}
}

// ObserveCall records one /v1/ui/observe.
func (a *Aggregator) ObserveCall() {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.observe++
	a.mu.Unlock()
}

// Act records one /v1/ui/act and its verification outcome. failure is empty
// when the action verified or when it has no expectation.
func (a *Aggregator) Act(verified bool, failure string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.act++
	if verified {
		a.actsOK++
	} else {
		a.actsFailed++
		if failure != "" {
			a.failures[failure]++
		}
	}
}

// Frame records one captured frame and how many bytes were cached.
func (a *Aggregator) Frame(bytes int) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.frameCount++
	a.frameBytes += int64(bytes)
}

// Snapshot is the /v1/metrics response. It is a fixed shape: every field is
// always present so a client never has to guess whether zero means "none" or
// "not collected".
type Snapshot struct {
	UptimeSec int64           `json:"uptime_sec"`
	Requests  Requests        `json:"requests"`
	Routes    []RouteSnapshot `json:"routes"`
	UI        UISnapshot      `json:"ui"`
	Helper    HelperSnapshot  `json:"helper"`
	Frames    FrameSnapshot   `json:"frames"`
}

type Requests struct {
	Total  int64 `json:"total"`
	OK2xx  int64 `json:"status_2xx"`
	Err4xx int64 `json:"status_4xx"`
	Err5xx int64 `json:"status_5xx"`
}

type RouteSnapshot struct {
	Route  string `json:"route"`
	Count  int64  `json:"count"`
	Errors int64  `json:"errors"`
	AvgMS  int64  `json:"avg_ms"`
	P50MS  int64  `json:"p50_ms"`
	P95MS  int64  `json:"p95_ms"`
	MaxMS  int64  `json:"max_ms"`
}

type UISnapshot struct {
	Observe      int64            `json:"observe"`
	Act          int64            `json:"act"`
	ActsVerified int64            `json:"acts_verified"`
	ActsFailed   int64            `json:"acts_failed"`
	Failures     map[string]int64 `json:"failures"`
}

type HelperSnapshot struct {
	Calls  int64 `json:"calls"`
	Errors int64 `json:"errors"`
	AvgMS  int64 `json:"avg_ms"`
}

type FrameSnapshot struct {
	Captured    int64 `json:"captured"`
	CachedBytes int64 `json:"cached_bytes"`
}

// Snapshot copies the current state. Routes are ordered by the fixed label
// list so the response is deterministic between calls.
func (a *Aggregator) Snapshot() Snapshot {
	if a == nil {
		return Snapshot{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	out := Snapshot{
		UptimeSec: int64(time.Since(a.started).Seconds()),
		Requests:  Requests{Total: a.total, OK2xx: a.ok2xx, Err4xx: a.err4xx, Err5xx: a.err5xx},
		UI: UISnapshot{
			Observe: a.observe, Act: a.act, ActsVerified: a.actsOK, ActsFailed: a.actsFailed,
			Failures: map[string]int64{},
		},
		Helper: HelperSnapshot{Calls: a.helperCalls, Errors: a.helperErrors},
		Frames: FrameSnapshot{Captured: a.frameCount, CachedBytes: a.frameBytes},
	}
	if a.helperCalls > 0 {
		out.Helper.AvgMS = a.helperMS / a.helperCalls
	}
	for code, n := range a.failures {
		out.UI.Failures[code] = n
	}
	for _, label := range append(append([]string{}, routes...), routeOther) {
		stats := a.byRoute[label]
		if stats == nil {
			continue
		}
		avg := int64(0)
		if stats.Count > 0 {
			avg = stats.total / stats.Count
		}
		out.Routes = append(out.Routes, RouteSnapshot{
			Route: label, Count: stats.Count, Errors: stats.Errors,
			AvgMS: avg, P50MS: stats.percentile(0.50), P95MS: stats.percentile(0.95), MaxMS: stats.max(),
		})
	}
	if out.Routes == nil {
		out.Routes = []RouteSnapshot{}
	}
	return out
}
