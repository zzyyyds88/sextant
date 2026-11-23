package engine

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"

	"sextant-core/internal/httpx"
)

// WebRTC viewer.
//
// The video source is a virtual display backed by scrcpy-server; frames are
// H.264 access units already, so no transcoding happens here — the daemon only
// re-times and forwards them. A config (SPS/PPS) frame is prepended to every key
// frame: browsers will not decode if they join mid-stream without it.

type viewer struct {
	ID      string
	Display int

	pc    *webrtc.PeerConnection
	track *webrtc.TrackLocalStaticSample
	proc  interface {
		Subscribe() (<-chan frameLike, func())
		SPSPPS() []byte
		Stats() map[string]any
	}
	cancel func()

	mu      sync.Mutex
	frames  int64
	bytes   int64
	lastErr string
}

// frameLike mirrors display.Frame without importing it here (keeps the route
// layer testable with a fake stream).
type frameLike struct {
	Data     []byte
	PTS      int64
	Config   bool
	KeyFrame bool
}

var viewersMu sync.Mutex
var viewers = map[string]*viewer{}

const maxViewers = 4

// handleWebRTCOffer answers an SDP offer with an H.264 video track.
func (s *Server) handleWebRTCOffer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SDP     string `json:"sdp"`
		Display int    `json:"display_id"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if body.SDP == "" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "sdp offer is required", nil)
		return
	}
	// Display 0 is the phone's own screen. It is mirrored on demand: a browser that
	// asked for "the main display" should not have to know that the frame source
	// is scrcpy.
	proc, ok := s.displayProcess(body.Display)
	if !ok {
		if body.Display != 0 {
			httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound,
				"no live display with that id: create one with POST /v1/displays", nil)
			return
		}
		startCtx, cancelStart := contextWithTimeout(r, 20*time.Second)
		defer cancelStart()
		if _, err := s.Displays.EnsureMirror(startCtx, 0); err != nil {
			httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable,
				"cannot mirror the main display", err)
			return
		}
		proc, ok = s.displayProcess(0)
		if !ok {
			httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable,
				"mirror started but its stream is not registered", nil)
			return
		}
	}
	viewersMu.Lock()
	count := len(viewers)
	viewersMu.Unlock()
	if count >= maxViewers {
		httpx.WriteError(w, http.StatusConflict, httpx.CodeConflict,
			fmt.Sprintf("viewer limit %d reached", maxViewers), nil)
		return
	}

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "webrtc peer failed", err)
		return
	}
	track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
		MimeType:    webrtc.MimeTypeH264,
		ClockRate:   90000,
		SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f",
	}, "video", "sextant-core")
	if err != nil {
		_ = pc.Close()
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "webrtc track failed", err)
		return
	}
	sender, err := pc.AddTrack(track)
	if err != nil {
		_ = pc.Close()
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "webrtc add track failed", err)
		return
	}
	go func() {
		buf := make([]byte, 1500)
		for {
			if _, _, err := sender.Read(buf); err != nil {
				return
			}
		}
	}()

	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: body.SDP}); err != nil {
		_ = pc.Close()
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid SDP offer", err)
		return
	}
	gatherComplete := webrtc.GatheringCompletePromise(pc)
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		_ = pc.Close()
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "webrtc answer failed", err)
		return
	}
	if err := pc.SetLocalDescription(answer); err != nil {
		_ = pc.Close()
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "webrtc local description failed", err)
		return
	}
	select {
	case <-gatherComplete:
	case <-time.After(8 * time.Second):
		_ = pc.Close()
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "ICE gathering timed out", nil)
		return
	case <-r.Context().Done():
		_ = pc.Close()
		return
	}
	local := pc.LocalDescription()
	if local == nil {
		_ = pc.Close()
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "missing local description", nil)
		return
	}

	id := newViewerID()
	v := &viewer{ID: id, Display: body.Display, pc: pc, track: track, proc: proc}
	v.cancel = v.run()
	viewersMu.Lock()
	viewers[id] = v
	viewersMu.Unlock()

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		switch state {
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed:
			v.Close()
		}
	})
	s.Audit.Info("stream: viewer %s attached to display %d", id, body.Display)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"viewer_id": id, "type": "answer", "sdp": local.SDP,
		"display_id": body.Display, "codec": "h264",
	})
}

// run pumps frames from the display into the track.
func (v *viewer) run() func() {
	ch, unsub := v.proc.Subscribe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		var lastPTS int64
		for f := range ch {
			if f.Config {
				continue
			}
			data := f.Data
			if f.KeyFrame {
				if sps := v.proc.SPSPPS(); len(sps) > 0 {
					merged := make([]byte, 0, len(sps)+len(data))
					merged = append(merged, sps...)
					merged = append(merged, data...)
					data = merged
				}
			}
			dur := 33 * time.Millisecond
			if lastPTS > 0 && f.PTS > lastPTS {
				if d := time.Duration(f.PTS-lastPTS) * time.Microsecond; d > 0 && d < time.Second {
					dur = d
				}
			}
			if f.PTS > 0 {
				lastPTS = f.PTS
			}
			if err := v.track.WriteSample(media.Sample{Data: data, Duration: dur}); err != nil {
				v.mu.Lock()
				v.lastErr = err.Error()
				v.mu.Unlock()
				return
			}
			v.mu.Lock()
			v.frames++
			v.bytes += int64(len(data))
			v.mu.Unlock()
		}
	}()
	return func() {
		unsub()
		<-done
	}
}

// Close tears the viewer down once.
func (v *viewer) Close() {
	if v.cancel != nil {
		c := v.cancel
		v.cancel = nil
		c()
	}
	_ = v.pc.Close()
	viewersMu.Lock()
	delete(viewers, v.ID)
	viewersMu.Unlock()
}

// handleWebRTCICE adds a trickled candidate.
func (s *Server) handleWebRTCICE(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ViewerID      string  `json:"viewer_id"`
		Candidate     string  `json:"candidate"`
		SDPMid        string  `json:"sdp_mid"`
		SDPMLineIndex *uint16 `json:"sdp_mline_index"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	viewersMu.Lock()
	v := viewers[body.ViewerID]
	viewersMu.Unlock()
	if v == nil {
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, "no such viewer", nil)
		return
	}
	init := webrtc.ICECandidateInit{Candidate: body.Candidate}
	if body.SDPMid != "" {
		init.SDPMid = &body.SDPMid
	}
	init.SDPMLineIndex = body.SDPMLineIndex
	if err := v.pc.AddICECandidate(init); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid candidate", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleStreamStats reports viewers and displays.
func (s *Server) handleStreamStats(w http.ResponseWriter, r *http.Request) {
	viewersMu.Lock()
	out := make([]map[string]any, 0, len(viewers))
	for _, v := range viewers {
		v.mu.Lock()
		out = append(out, map[string]any{
			"viewer_id": v.ID, "display_id": v.Display,
			"frames": v.frames, "bytes": v.bytes, "error": v.lastErr,
		})
		v.mu.Unlock()
	}
	viewersMu.Unlock()
	displays := []map[string]any{}
	for _, p := range sdisplayList() {
		displays = append(displays, p.Stats())
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"viewers": out, "viewer_count": len(out), "max_viewers": maxViewers,
		"displays": displays,
	})
}

// handleStreamClose detaches a viewer.
func (s *Server) handleStreamClose(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	viewersMu.Lock()
	v := viewers[id]
	viewersMu.Unlock()
	if v == nil {
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, "no such viewer", nil)
		return
	}
	v.Close()
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func newViewerID() string {
	return "wv_" + randHex(8)
}

var _ = context.Background
