package display

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

// scrcpy wire constants (Streamer.java, scrcpy-server v4.x).
const (
	codecH264          = 0x68323634 // "h264"
	packetFlagConfig   = uint64(1) << 62
	packetFlagKeyFrame = uint64(1) << 61
	maxPacketSize      = 32 << 20
	ptsMask            = (uint64(1) << 61) - 1
	sessionMetaFlag    = 0x80000000
)

// Frame is one encoded H.264 access unit.
//
// Config frames carry SPS/PPS and must be prepended to every key frame: a
// decoder that only ever sees the key frame will not start, which looks like
// "the stream connects but stays black".
type Frame struct {
	Data     []byte
	PTS      int64
	Config   bool
	KeyFrame bool
}

// ReadHeader consumes the codec id and the session metadata that scrcpy sends
// once, right after the handshake.
func (p *Process) ReadHeader() error {
	var codec [4]byte
	if _, err := io.ReadFull(p.conn, codec[:]); err != nil {
		return fmt.Errorf("read codec id: %w", err)
	}
	id := binary.BigEndian.Uint32(codec[:])
	if id != codecH264 {
		return fmt.Errorf("unexpected codec id 0x%08x (only H.264 is supported)", id)
	}
	var session [12]byte
	if _, err := io.ReadFull(p.conn, session[:]); err != nil {
		return fmt.Errorf("read session metadata: %w", err)
	}
	flags := binary.BigEndian.Uint32(session[0:4])
	if flags&sessionMetaFlag == 0 {
		return fmt.Errorf("missing scrcpy session metadata")
	}
	p.Width = int(binary.BigEndian.Uint32(session[4:8]))
	p.Height = int(binary.BigEndian.Uint32(session[8:12]))
	if p.Width <= 0 || p.Height <= 0 {
		return fmt.Errorf("invalid stream size %dx%d", p.Width, p.Height)
	}
	return nil
}

// StartReading begins parsing frames and broadcasting them to subscribers.
//
// Frames are fanned out to bounded channels: a subscriber that cannot keep up
// drops frames instead of stalling the reader, because a stalled reader stops
// the whole stream for every other viewer.
func (p *Process) StartReading() {
	go p.readLoop()
}

func (p *Process) readLoop() {
	var hdr [12]byte
	for {
		if _, err := io.ReadFull(p.conn, hdr[:]); err != nil {
			p.setErr(err)
			p.closeSubs()
			return
		}
		ptsFlags := binary.BigEndian.Uint64(hdr[0:8])
		size := int(binary.BigEndian.Uint32(hdr[8:12]))
		if size <= 0 || size > maxPacketSize {
			p.setErr(fmt.Errorf("invalid packet size %d", size))
			p.closeSubs()
			return
		}
		buf := make([]byte, size)
		if _, err := io.ReadFull(p.conn, buf); err != nil {
			p.setErr(err)
			p.closeSubs()
			return
		}
		f := Frame{
			Data:     normalizeAnnexB(buf),
			PTS:      int64(ptsFlags & ptsMask),
			Config:   ptsFlags&packetFlagConfig != 0,
			KeyFrame: ptsFlags&packetFlagKeyFrame != 0,
		}
		if f.Config {
			p.mu.Lock()
			p.spsPPS = append([]byte(nil), f.Data...)
			p.mu.Unlock()
			// A config packet is the encoder announcing new parameters. It is not
			// a picture, so it must never be counted as one: doing so made an idle
			// display look like it was producing frames.
			continue
		}
		atomic.AddInt64(&p.frames, 1)
		atomic.AddInt64(&p.bytes, int64(size))
		p.mu.Lock()
		p.lastPTS = f.PTS
		p.lastFrameAt = time.Now()
		p.mu.Unlock()
		p.fanout(f)
	}
}

// Subscribe returns a frame channel plus an unsubscribe function.
func (p *Process) Subscribe() (<-chan Frame, func()) {
	ch := make(chan Frame, 8)
	p.mu.Lock()
	p.subs[ch] = struct{}{}
	p.mu.Unlock()
	return ch, func() {
		p.mu.Lock()
		if _, ok := p.subs[ch]; ok {
			delete(p.subs, ch)
			close(ch)
		}
		p.mu.Unlock()
	}
}

// SPSPPS returns the cached config NAL units.
func (p *Process) SPSPPS() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]byte(nil), p.spsPPS...)
}

// Stats reports what the stream has carried so far.
func (p *Process) Stats() map[string]any {
	p.mu.Lock()
	errText := p.lastErr
	width, height := p.Width, p.Height
	lastFrameAge := -1.0
	if !p.lastFrameAt.IsZero() {
		lastFrameAge = time.Since(p.lastFrameAt).Seconds()
	}
	p.mu.Unlock()
	// A display that has not produced a frame in the last second is idle: reporting
	// the lifetime average there would claim a stream that is not happening.
	fps := 0.0
	if !p.lastFrameAt.IsZero() && time.Since(p.lastFrameAt) < time.Second {
		fps = float64(atomic.LoadInt64(&p.frames)) / time.Since(p.Created).Seconds()
	}
	return map[string]any{
		"display_id": p.ID, "width": width, "height": height,
		"frames": atomic.LoadInt64(&p.frames), "bytes": atomic.LoadInt64(&p.bytes),
		"subscribers": p.subCount(), "error": errText,
		"last_frame_age_sec": lastFrameAge,
		"avg_fps":            fps,
	}
}

func (p *Process) subCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.subs)
}

func (p *Process) fanout(f Frame) {
	p.mu.Lock()
	subs := make([]chan Frame, 0, len(p.subs))
	for ch := range p.subs {
		subs = append(subs, ch)
	}
	p.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- f:
		default: // slow subscriber: drop rather than stall the reader
		}
	}
}

func (p *Process) closeSubs() {
	p.mu.Lock()
	for ch := range p.subs {
		close(ch)
		delete(p.subs, ch)
	}
	p.mu.Unlock()
}

// Err returns the reason this stream stopped producing frames, or nil while it is
// still healthy. Callers use it to decide whether a cached mirror is reusable.
func (p *Process) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.lastErr == "" {
		return nil
	}
	return errors.New(p.lastErr)
}

// Stop tears this stream down without destroying the display it mirrors.
func (p *Process) Stop() {
	if p.conn != nil {
		_ = p.conn.Close()
	}
	if p.listener != nil {
		_ = p.listener.Close()
	}
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	p.closeSubs()
}

func (p *Process) setErr(err error) {
	p.mu.Lock()
	if p.lastErr == "" {
		p.lastErr = err.Error()
	}
	p.mu.Unlock()
}

// normalizeAnnexB accepts Annex-B or AVCC output and returns Annex-B.
func normalizeAnnexB(data []byte) []byte {
	if len(data) < 4 {
		return data
	}
	if data[0] == 0 && data[1] == 0 && (data[2] == 1 || (data[2] == 0 && data[3] == 1)) {
		return data
	}
	out := make([]byte, 0, len(data)+16)
	off := 0
	for off+4 <= len(data) {
		n := int(binary.BigEndian.Uint32(data[off : off+4]))
		if n <= 0 || off+4+n > len(data) {
			return data
		}
		nal := data[off+4 : off+4+n]
		if nal[0]&0x80 != 0 {
			return data
		}
		out = append(out, 0, 0, 0, 1)
		out = append(out, nal...)
		off += 4 + n
	}
	if off != len(data) || len(out) == 0 {
		return data
	}
	return out
}
