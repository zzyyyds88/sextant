// Command probe attaches to the daemon's WebRTC stream and reports what it got.
//
// It exists to prove the stream path end to end without a browser: it makes a
// real offer, completes ICE, and counts RTP packets and bytes actually received.
package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/pion/webrtc/v4"
)

func main() {
	base := flag.String("base", "", "https://host:8443")
	token := flag.String("token", "", "api token")
	display := flag.Int("display", 0, "display id")
	seconds := flag.Int("seconds", 10, "how long to listen")
	flag.Parse()
	if *base == "" || *token == "" {
		fmt.Fprintln(os.Stderr, "-base and -token are required")
		os.Exit(2)
	}

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		fail("peer: %v", err)
	}
	defer pc.Close()

	// A viewer only receives, but the offer still needs a video m-line: without a
	// transceiver Pion emits an SDP with no media section, and the answering side
	// rejects it ("no ice-ufrag").
	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo,
		webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		fail("transceiver: %v", err)
	}

	var mu = make(chan struct{}, 1)
	mu <- struct{}{}
	var packets, bytes64 int
	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		for {
			pkt, _, err := track.ReadRTP()
			if err != nil {
				return
			}
			<-mu
			packets++
			bytes64 += len(pkt.Payload)
			mu <- struct{}{}
		}
	})

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		fail("offer: %v", err)
	}
	gather := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		fail("local desc: %v", err)
	}
	<-gather
	if os.Getenv("PROBE_DUMP") != "" {
		fmt.Println("--- offer ---")
		fmt.Println(pc.LocalDescription().SDP)
	}
	body, _ := json.Marshal(map[string]any{"sdp": pc.LocalDescription().SDP, "display_id": *display})
	resp, err := post(*base+"/v1/stream/webrtc/offer", *token, body)
	if err != nil {
		fail("offer post: %v", err)
	}
	var answer struct {
		SDP      string `json:"sdp"`
		ViewerID string `json:"viewer_id"`
		Error    struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Detail  string `json:"detail"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp, &answer); err != nil {
		fail("answer decode: %v (%s)", err, string(resp[:min(200, len(resp))]))
	}
	if answer.SDP == "" {
		fail("no answer sdp: %s", string(resp))
	}
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer.SDP}); err != nil {
		fail("remote desc: %v", err)
	}
	fmt.Printf("viewer=%s display=%d\n", answer.ViewerID, *display)
	deadline := time.Now().Add(time.Duration(*seconds) * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(time.Second)
		<-mu
		fmt.Printf("  t+%2ds state=%s packets=%d bytes=%d\n",
			*seconds-int(time.Until(deadline).Seconds()), pc.ConnectionState(), packets, bytes64)
		mu <- struct{}{}
	}
	if packets == 0 {
		fail("no RTP packets arrived: the stream did not reach the viewer")
	}
	fmt.Printf("OK: received %d RTP packets (%d payload bytes) over %d s\n", packets, bytes64, *seconds)
}

func post(url, token string, body []byte) ([]byte, error) {
	tr := &http.Transport{TLSClientConfig: &tls.Config{
		// The daemon's certificate is self-signed and pinned by the real client;
		// this probe is a local diagnostic, so it skips verification deliberately.
		InsecureSkipVerify: true,
	}}
	c := &http.Client{Transport: tr, Timeout: 20 * time.Second}
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	return io.ReadAll(res.Body)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var _ = x509.NewCertPool
