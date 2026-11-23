// Package human applies the D30 injection profile: a seeded random point inside
// the target, a paced gap between injections, and scaled waits.
//
// The randomness is seeded, not ambient. Two runs with the same seed produce
// the same points and the same gaps, which is what keeps "human-like" and
// "reproducible" from being opposites: the target app sees a different pixel
// each run, while an acceptance run can replay one exact path.
package human

import (
	"context"
	"crypto/hmac"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"
)

// Params is the validated form of the config's human section.
type Params struct {
	Enabled    bool
	GapMinMS   int
	GapMaxMS   int
	TapInset   int
	JitterPx   int
	WaitMinPct int
	WaitMaxPct int
}

// Book holds the per-display pacing state. One Book belongs to one daemon.
type Book struct {
	params Params

	mu    sync.Mutex
	locks map[int]*sync.Mutex
	last  map[int]time.Time
}

// New builds a Book.
func New(p Params) *Book {
	return &Book{
		params: p,
		locks:  map[int]*sync.Mutex{},
		last:   map[int]time.Time{},
	}
}

// Params returns the active profile (the config can be reloaded only at start).
func (b *Book) Params() Params { return b.params }

// NewSeed returns the seed a request should record when the caller did not
// supply one. 8 bytes of crypto randomness, hex encoded.
func (b *Book) NewSeed() string {
	var raw [8]byte
	if _, err := cryptorand.Read(raw[:]); err != nil {
		// crypto/rand failing is not a reason to stop driving the phone; the
		// timestamp still gives a distinct, recordable seed.
		return fmt.Sprintf("t%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw[:])
}

// rng derives an independent deterministic source for one injection.
//
// "display|request|step" is the key so that a chain of actions in one request
// gets different points, while replaying the request with the same seed
// reproduces every one of them.
func (b *Book) rng(seed, requestID string, display, step int) *rand.Rand {
	mac := hmac.New(sha256.New, []byte(seed))
	fmt.Fprintf(mac, "%d|%s|%d", display, requestID, step)
	sum := mac.Sum(nil)
	return rand.New(rand.NewPCG(
		binary.BigEndian.Uint64(sum[0:8]),
		binary.BigEndian.Uint64(sum[8:16]),
	))
}

// Gap returns the paced interval for the next injection on a display. It is
// drawn from the configured range; when pacing is disabled it is zero.
func (b *Book) Gap(seed, requestID string, display, step int) time.Duration {
	if !b.params.Enabled || b.params.GapMaxMS <= 0 {
		return 0
	}
	low, high := b.params.GapMinMS, b.params.GapMaxMS
	if high < low {
		high = low
	}
	r := b.rng(seed, requestID, display, step)
	ms := low
	if high > low {
		ms += r.IntN(high - low + 1)
	}
	return time.Duration(ms) * time.Millisecond
}

// Begin serialises injections on one display and waits out the pacing gap.
// The returned release records the completion time and unlocks the display;
// every caller must call it, normally with defer.
//
// paced is how long this call actually slept; interval is the full time since
// the previous injection completed. The second number is the one an acceptance
// run can compare against gap_min/gap_max: paced alone is only the remainder
// left after the caller's own overhead.
//
// Serialising per display is not a throughput loss: the session layer already
// allows one writer per display, and a second injection that starts while the
// first is still being delivered is exactly what the pacing exists to prevent.
func (b *Book) Begin(ctx context.Context, display int, gap time.Duration) (func(), time.Duration, time.Duration, error) {
	b.mu.Lock()
	lock := b.locks[display]
	if lock == nil {
		lock = &sync.Mutex{}
		b.locks[display] = lock
	}
	b.mu.Unlock()

	lock.Lock()
	release := func() {
		b.mu.Lock()
		b.last[display] = time.Now()
		b.mu.Unlock()
		lock.Unlock()
	}
	b.mu.Lock()
	last := b.last[display]
	b.mu.Unlock()
	if gap <= 0 || last.IsZero() {
		return release, 0, 0, nil
	}

	wait := gap - time.Since(last)
	if wait > 0 {
		select {
		case <-ctx.Done():
			lock.Unlock()
			return func() {}, 0, 0, ctx.Err()
		case <-time.After(wait):
		}
	}
	return release, maxDuration(wait, 0), time.Since(last), nil
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

// Point picks the coordinate a request will actually inject.
//
// bounds is optional: when present the point stays inside it with an inset from
// every edge; when absent the caller's coordinate is jittered by JitterPx.
// A disabled profile returns the geometric centre (or the given point), which is
// the pre-D30 behaviour and the control arm of any A/B comparison.
func (b *Book) Point(bounds *[4]int, x, y int, seed, requestID string, display, step int) (int, int) {
	if !b.params.Enabled {
		if bounds != nil {
			return (bounds[0] + bounds[2]) / 2, (bounds[1] + bounds[3]) / 2
		}
		return x, y
	}
	r := b.rng(seed, requestID, display, step)
	intIn := func(low, high int) int {
		if high <= low {
			return low
		}
		return low + r.IntN(high-low+1)
	}
	if bounds == nil {
		px, py := x, y
		if b.params.JitterPx > 0 {
			px = intIn(x-b.params.JitterPx, x+b.params.JitterPx)
			py = intIn(y-b.params.JitterPx, y+b.params.JitterPx)
		}
		return nonNegative(px), nonNegative(py)
	}

	left, top, right, bottom := bounds[0], bounds[1], bounds[2], bounds[3]
	width, height := right-left, bottom-top
	if width <= 0 || height <= 0 {
		return nonNegative(left), nonNegative(top)
	}
	// A control smaller than 8px is a hairline: jittering inside it would leave
	// the control, so fall back to a small radius around the centre.
	if width < 8 || height < 8 {
		cx, cy := (left+right)/2, (top+bottom)/2
		return nonNegative(intIn(cx-3, cx+3)), nonNegative(intIn(cy-3, cy+3))
	}
	insetX := width / maxInt(b.params.TapInset, 1)
	if insetX < 2 {
		insetX = 2
	}
	insetY := height / maxInt(b.params.TapInset, 1)
	if insetY < 2 {
		insetY = 2
	}
	if width-2*insetX <= 0 || height-2*insetY <= 0 {
		cx, cy := (left+right)/2, (top+bottom)/2
		return nonNegative(intIn(cx-3, cx+3)), nonNegative(intIn(cy-3, cy+3))
	}
	return nonNegative(intIn(left+insetX, right-insetX)),
		nonNegative(intIn(top+insetY, bottom-insetY))
}

// ScaleWait applies the configured wait range to an explicit delay.
//
// It exists for the caller-side waits (recipe steps). The daemon never inserts
// a wait on a caller's behalf: pacing is the gap between injections, not a
// hidden sleep inside a request.
func (b *Book) ScaleWait(d time.Duration, seed, requestID string, display, step int) time.Duration {
	if !b.params.Enabled || d <= 0 {
		return d
	}
	low, high := b.params.WaitMinPct, b.params.WaitMaxPct
	if high < low {
		high = low
	}
	if low <= 0 {
		return d
	}
	r := b.rng(seed, requestID, display, step)
	pct := low
	if high > low {
		pct += r.IntN(high - low + 1)
	}
	return d * time.Duration(pct) / 100
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func nonNegative(v int) int {
	if v < 0 {
		return 0
	}
	return v
}
