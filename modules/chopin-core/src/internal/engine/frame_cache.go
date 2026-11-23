package engine

import (
	"context"
	"os"
	"sync"
	"time"
)

// frameCache keeps the most recent display-0 JPEG.
//
// The agent's perception loop and the WebUI's snapshot mode both read the screen
// repeatedly. Paying for a fresh capture on every read made the loop slower than
// the decisions it was feeding, so one recent frame is retained and shared.
//
// D28: memory is capped by construction (one frame, at most 4 MiB admitted) and
// nothing runs in the background. A cached entry is only replaced when someone
// asks for a frame; a stale entry is simply not used.
type frameCache struct {
	mu     sync.Mutex
	jpeg   []byte
	at     time.Time
	width  int
	height int
}

const (
	frameCacheMaxBytes = 4 << 20
	frameCacheTTL      = 500 * time.Millisecond
)

// rememberFrame stores a frame only when it is plausible: empty or oversized
// data is dropped rather than pinned in memory.
func (s *Server) rememberFrame(data []byte, width, height int) {
	if len(data) == 0 || len(data) > frameCacheMaxBytes {
		return
	}
	s.frames.mu.Lock()
	s.frames.jpeg = data
	s.frames.at = time.Now()
	s.frames.width = width
	s.frames.height = height
	s.frames.mu.Unlock()
}

// recentFrame returns the cached frame when it is younger than maxAge.
func (s *Server) recentFrame(maxAge time.Duration) ([]byte, int, int, bool) {
	s.frames.mu.Lock()
	defer s.frames.mu.Unlock()
	if len(s.frames.jpeg) == 0 || time.Since(s.frames.at) > maxAge {
		return nil, 0, 0, false
	}
	return s.frames.jpeg, s.frames.width, s.frames.height, true
}

// frameJPEG returns a JPEG of display 0, capturing only when the cache is stale
// or the caller asked for parameters the cache does not hold.
func (s *Server) frameJPEG(ctx context.Context, quality, maxSize int, useCache bool) ([]byte, int, int, int64, error) {
	// The cache always holds the default frame (quality 80, native size). A caller
	// asking for a scaled or differently compressed frame must get a fresh capture;
	// serving it the cached default and pretending the parameters applied would be
	// a quiet lie.
	if useCache && (quality == 80 || quality <= 0) && maxSize <= 0 {
		if data, w, h, ok := s.recentFrame(frameCacheTTL); ok {
			return data, w, h, 0, nil
		}
	}
	start := time.Now()
	path := s.Paths.Tmp + "/frame.jpg"
	_ = os.Remove(path)
	out, err := s.Helper.Screenshot(ctx, map[string]any{
		"display_id":  0,
		"format":      "jpeg",
		"quality":     quality,
		"max_size":    maxSize,
		"output_path": path,
	})
	if err != nil {
		return nil, 0, 0, 0, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	w, h := intOf(out["width"]), intOf(out["height"])
	s.rememberFrame(data, w, h)
	return data, w, h, time.Since(start).Milliseconds(), nil
}
