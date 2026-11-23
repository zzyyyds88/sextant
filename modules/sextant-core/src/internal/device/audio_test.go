package device

import "testing"

// Ordinary playback keeps the cheap format. Uplinking at 48 kHz stereo was only
// needed for the removed call path (D34); plain speech stays 16 kHz mono.
func TestPlainPlaybackIsStill16kMono(t *testing.T) {
	if PlaybackRate != 16000 || PlaybackChannels != 1 {
		t.Fatalf("plain playback drifted to %d Hz / %d ch", PlaybackRate, PlaybackChannels)
	}
}
