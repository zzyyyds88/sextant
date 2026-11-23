package human

import (
	"context"
	"testing"
	"time"
)

func params() Params {
	return Params{Enabled: true, GapMinMS: 700, GapMaxMS: 1600, TapInset: 6, JitterPx: 6, WaitMinPct: 80, WaitMaxPct: 135}
}

func TestPointIsReproducibleForTheSameSeed(t *testing.T) {
	b := New(params())
	bounds := [4]int{100, 200, 500, 600}
	x1, y1 := b.Point(&bounds, 300, 400, "seed-a", "req-1", 0, 0)
	x2, y2 := b.Point(&bounds, 300, 400, "seed-a", "req-1", 0, 0)
	if x1 != x2 || y1 != y2 {
		t.Fatalf("same seed produced different points: (%d,%d) vs (%d,%d)", x1, y1, x2, y2)
	}
}

func TestDifferentSeedsMoveThePoint(t *testing.T) {
	b := New(params())
	bounds := [4]int{100, 200, 500, 600}
	seen := map[[2]int]bool{}
	for i := 0; i < 20; i++ {
		x, y := b.Point(&bounds, 300, 400, string(rune('a'+i%26))+string(rune('a'+i/26)), "req", 0, 0)
		seen[[2]int{x, y}] = true
	}
	if len(seen) < 10 {
		t.Fatalf("expected the point to vary across seeds, saw %d distinct points", len(seen))
	}
}

func TestPointStaysInsideTheInset(t *testing.T) {
	b := New(params())
	bounds := [4]int{0, 0, 600, 1200}
	insetX, insetY := 600/6, 1200/6
	for i := 0; i < 200; i++ {
		x, y := b.Point(&bounds, 300, 600, "seed", "req", 0, i)
		if x < insetX || x > 600-insetX {
			t.Fatalf("x=%d outside [%d,%d]", x, insetX, 600-insetX)
		}
		if y < insetY || y > 1200-insetY {
			t.Fatalf("y=%d outside [%d,%d]", y, insetY, 1200-insetY)
		}
	}
}

func TestDisabledProfileKeepsTheCentre(t *testing.T) {
	p := params()
	p.Enabled = false
	b := New(p)
	bounds := [4]int{100, 100, 300, 300}
	x, y := b.Point(&bounds, 0, 0, "seed", "req", 0, 0)
	if x != 200 || y != 200 {
		t.Fatalf("disabled profile should use the centre, got (%d,%d)", x, y)
	}
	if got, want := b.Gap("seed", "req", 0, 0), time.Duration(0); got != want {
		t.Fatalf("disabled profile should not pace, got %v", got)
	}
}

func TestGapFallsInTheConfiguredRange(t *testing.T) {
	b := New(params())
	for i := 0; i < 100; i++ {
		g := b.Gap("seed", "req", 0, i)
		if g < 700*time.Millisecond || g > 1600*time.Millisecond {
			t.Fatalf("gap %v outside [700ms,1600ms]", g)
		}
	}
}

func TestGapIsReproducible(t *testing.T) {
	b := New(params())
	a := b.Gap("seed", "req", 0, 3)
	c := b.Gap("seed", "req", 0, 3)
	if a != c {
		t.Fatalf("same seed/step produced %v and %v", a, c)
	}
}

func TestBeginPacesAfterAPriorInjection(t *testing.T) {
	p := params()
	p.GapMinMS, p.GapMaxMS = 120, 120
	b := New(p)
	release, _, _, err := b.Begin(context.Background(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	release()

	start := time.Now()
	release2, paced, interval, err := b.Begin(context.Background(), 0, 120*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	release2()
	if paced < 80*time.Millisecond || interval < 120*time.Millisecond {
		t.Fatalf("expected the second injection to wait, paced=%v elapsed=%v", paced, time.Since(start))
	}
}

func TestBeginHonoursCancellation(t *testing.T) {
	p := params()
	p.GapMinMS, p.GapMaxMS = 5000, 5000
	b := New(p)
	release, _, _, _ := b.Begin(context.Background(), 0, 0)
	release()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, _, _, err := b.Begin(ctx, 0, 5*time.Second); err == nil {
		t.Fatal("expected context cancellation while pacing")
	}
}

func TestScaleWaitStaysInRange(t *testing.T) {
	b := New(params())
	for i := 0; i < 100; i++ {
		got := b.ScaleWait(1000*time.Millisecond, "seed", "req", 0, i)
		if got < 800*time.Millisecond || got > 1350*time.Millisecond {
			t.Fatalf("scaled wait %v outside [800ms,1350ms]", got)
		}
	}
}
