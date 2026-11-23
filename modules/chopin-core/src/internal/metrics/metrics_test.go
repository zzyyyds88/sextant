package metrics

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

func TestRouteIsAClosedLabelSet(t *testing.T) {
	if got := Route("/v1/ui/page"); got != "/v1/ui/page" {
		t.Fatalf("registered route = %q", got)
	}
	// Go 1.22's Request.Pattern carries the method; the label must not.
	if got := Route("GET /v1/ui/page"); got != "/v1/ui/page" {
		t.Fatalf("method-prefixed route = %q", got)
	}
	if got := Route("/v1/whatever/user-supplied"); got != routeOther {
		t.Fatalf("unknown route = %q, want %q", got, routeOther)
	}
	// A caller-supplied string with a space must never become a label.
	if got := Route("GET /v1/ui/page?secret=1"); got != routeOther {
		t.Fatalf("path with a query = %q, want %q", got, routeOther)
	}
}

func TestObserveCountsStatusClasses(t *testing.T) {
	a := New()
	a.Observe("/v1/ui/page", 200, 5)
	a.Observe("/v1/ui/page", 404, 7)
	a.Observe("/v1/ui/act", 500, 9)
	s := a.Snapshot()
	if s.Requests.Total != 3 || s.Requests.OK2xx != 1 || s.Requests.Err4xx != 1 || s.Requests.Err5xx != 1 {
		t.Fatalf("requests = %+v", s.Requests)
	}
	var page RouteSnapshot
	for _, r := range s.Routes {
		if r.Route == "/v1/ui/page" {
			page = r
		}
	}
	if page.Count != 2 || page.Errors != 1 {
		t.Fatalf("page route = %+v", page)
	}
	if page.AvgMS != 6 {
		t.Fatalf("avg_ms = %d, want 6", page.AvgMS)
	}
}

func TestPercentilesFollowTheSamples(t *testing.T) {
	a := New()
	for i := 1; i <= 100; i++ {
		a.Observe("/v1/ui/page", 200, int64(i))
	}
	var page RouteSnapshot
	for _, r := range a.Snapshot().Routes {
		if r.Route == "/v1/ui/page" {
			page = r
		}
	}
	if page.P50MS < 45 || page.P50MS > 55 {
		t.Fatalf("p50 = %d, want near 50", page.P50MS)
	}
	if page.P95MS < 90 || page.P95MS > 100 {
		t.Fatalf("p95 = %d, want near 95", page.P95MS)
	}
	if page.MaxMS != 100 {
		t.Fatalf("max = %d, want 100", page.MaxMS)
	}
}

// The ring is bounded: 1000 samples must not grow memory or change the shape.
func TestSampleRingIsBounded(t *testing.T) {
	a := New()
	for i := 0; i < 1000; i++ {
		a.Observe("/v1/ui/page", 200, int64(i))
	}
	stats := a.byRoute["/v1/ui/page"]
	if stats == nil || stats.filled != samplesPerRoute {
		t.Fatalf("filled = %v", stats)
	}
	if stats.Count != 1000 {
		t.Fatalf("count = %d", stats.Count)
	}
}

func TestUIActAndHelperCounters(t *testing.T) {
	a := New()
	a.ObserveCall()
	a.Act(true, "")
	a.Act(false, "no_effect")
	a.Act(false, "no_effect")
	a.HelperCall(nil, 10)
	a.HelperCall(errors.New("boom"), 30)
	a.Frame(1024)
	s := a.Snapshot()
	if s.UI.Observe != 1 || s.UI.Act != 3 || s.UI.ActsVerified != 1 || s.UI.ActsFailed != 2 {
		t.Fatalf("ui = %+v", s.UI)
	}
	if s.UI.Failures["no_effect"] != 2 {
		t.Fatalf("failures = %v", s.UI.Failures)
	}
	if s.Helper.Calls != 2 || s.Helper.Errors != 1 || s.Helper.AvgMS != 20 {
		t.Fatalf("helper = %+v", s.Helper)
	}
	if s.Frames.Captured != 1 || s.Frames.CachedBytes != 1024 {
		t.Fatalf("frames = %+v", s.Frames)
	}
}

// The response shape is fixed: every section is present even with no traffic.
func TestEmptySnapshotKeepsEveryField(t *testing.T) {
	raw, err := json.Marshal(New().Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"uptime_sec", "requests", "routes", "ui", "helper", "frames"} {
		if _, ok := m[key]; !ok {
			t.Fatalf("missing %q in %s", key, raw)
		}
	}
	if _, ok := m["routes"].([]any); !ok {
		t.Fatalf("routes is not an array: %s", raw)
	}
}

func TestConcurrentWritersDoNotRace(t *testing.T) {
	a := New()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				a.Observe("/v1/ui/page", 200, int64(j))
				a.HelperCall(nil, int64(j))
				a.Act(true, "")
				_ = a.Snapshot()
			}
		}(i)
	}
	wg.Wait()
	s := a.Snapshot()
	if s.Requests.Total != 1600 {
		t.Fatalf("total = %d, want 1600", s.Requests.Total)
	}
	if s.Helper.Calls != 1600 {
		t.Fatalf("helper calls = %d", s.Helper.Calls)
	}
}

// A nil aggregator is the zero value in tests and must not panic.
func TestNilAggregatorIsInert(t *testing.T) {
	var a *Aggregator
	a.Observe("/v1/ui/page", 200, 1)
	a.HelperCall(nil, 1)
	a.ObserveCall()
	a.Act(true, "")
	a.Frame(1)
	if got := a.Snapshot(); got.Requests.Total != 0 {
		t.Fatalf("snapshot = %+v", got)
	}
}
