package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/pion/webrtc/v4"

	"chopin/internal/client"
)

// cmdStream checks that the WebRTC path actually carries video.
//
// "The endpoint answered" is not the claim; the claim is that RTP packets arrive
// and keep arriving. A viewer that connects but receives nothing is the normal
// failure mode of a mis-set capture source, and only counting packets tells them
// apart.
func cmdStream(a *args) error {
	c, err := paired()
	if err != nil {
		return err
	}
	display, err := a.optInt("display", 0)
	if err != nil {
		return err
	}
	seconds, err := a.optInt("seconds", 8)
	if err != nil {
		return err
	}
	if seconds < 2 || seconds > 60 {
		seconds = 8
	}
	detail, err := streamProbeWith(c, display, seconds, true)
	if err != nil {
		return err
	}
	fmt.Println(detail)
	return nil
}

// streamProbe is the quiet form used by the acceptance matrix.
func streamProbe(c *client.Client, display, seconds int) (string, error) {
	return streamProbeWith(c, display, seconds, false)
}

// streamProbeWith attaches a viewer and counts RTP packets for `seconds`.
func streamProbeWith(c *client.Client, display, seconds int, verbose bool) (string, error) {

	if seconds < 2 || seconds > 60 {
		seconds = 8
	}
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return "", fmt.Errorf("peer: %w", err)
	}
	defer pc.Close()
	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo,
		webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		return "", fmt.Errorf("transceiver: %w", err)
	}
	packets := make(chan int, 4096)
	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		for {
			pkt, _, err := track.ReadRTP()
			if err != nil {
				return
			}
			select {
			case packets <- len(pkt.Payload):
			default:
			}
		}
	})

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return "", fmt.Errorf("offer: %w", err)
	}
	gather := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		return "", fmt.Errorf("local description: %w", err)
	}
	<-gather

	body, _ := json.Marshal(map[string]any{"sdp": pc.LocalDescription().SDP, "display_id": display})
	resp, err := c.Post("/v1/stream/webrtc/offer", json.RawMessage(body))
	if err != nil {
		return "", err
	}
	var answer struct {
		SDP      string `json:"sdp"`
		ViewerID string `json:"viewer_id"`
	}
	if err := json.Unmarshal(resp, &answer); err != nil {
		return "", fmt.Errorf("answer: %w", err)
	}
	if answer.SDP == "" {
		return "", fmt.Errorf("the daemon returned no SDP answer")
	}
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer.SDP}); err != nil {
		return "", fmt.Errorf("remote description: %w", err)
	}
	if verbose {
		fmt.Printf("viewer %s attached to display %d\n", answer.ViewerID, display)
	}

	total, bytesSeen := 0, 0
	deadline := time.After(time.Duration(seconds) * time.Second)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case n := <-packets:
			total++
			bytesSeen += n
		case <-tick.C:
			if verbose {
				fmt.Printf("  %-12s packets=%-6d bytes=%d\n", pc.ConnectionState(), total, bytesSeen)
			}
		case <-deadline:
			_, _ = c.Delete("/v1/stream/viewers/" + answer.ViewerID)
			if total == 0 {
				return "", fmt.Errorf("no RTP packets arrived: the stream carries no video")
			}
			return fmt.Sprintf("%d RTP packets (%d bytes) in %ds", total, bytesSeen, seconds), nil
		}
	}
}
