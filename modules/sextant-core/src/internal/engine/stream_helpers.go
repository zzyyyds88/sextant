package engine

import (
	"crypto/rand"
	"encoding/hex"

	sdisplay "sextant-core/internal/display"
)

// randHex returns n random bytes as hex.
func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "00000000"
	}
	return hex.EncodeToString(b)
}

// sdisplayList lists live overlay displays.
func sdisplayList() []*sdisplay.Process { return sdisplay.List() }

// displayProcess adapts a display.Process to the viewer's stream interface.
func (s *Server) displayProcess(id int) (interface {
	Subscribe() (<-chan frameLike, func())
	SPSPPS() []byte
	Stats() map[string]any
}, bool) {
	proc, ok := sdisplay.Get(id)
	if !ok {
		return nil, false
	}
	return &displayAdapter{proc: proc}, true
}

// displayAdapter narrows display.Process to the viewer's frame type.
type displayAdapter struct{ proc *sdisplay.Process }

func (a *displayAdapter) Subscribe() (<-chan frameLike, func()) {
	src, unsub := a.proc.Subscribe()
	dst := make(chan frameLike, 8)
	done := make(chan struct{})
	go func() {
		defer close(dst)
		for f := range src {
			select {
			case dst <- frameLike{Data: f.Data, PTS: f.PTS, Config: f.Config, KeyFrame: f.KeyFrame}:
			case <-done:
				return
			}
		}
	}()
	return dst, func() {
		unsub()
		close(done)
	}
}

func (a *displayAdapter) SPSPPS() []byte        { return a.proc.SPSPPS() }
func (a *displayAdapter) Stats() map[string]any { return a.proc.Stats() }
