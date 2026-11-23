package main

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// wav builds a minimal RIFF/WAVE file with the given format and sample frames.
func wav(t *testing.T, rate, channels, bits int, samples []int16) []byte {
	t.Helper()
	var buf bytes.Buffer
	dataLen := len(samples) * 2
	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(36+dataLen))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(16))
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // PCM
	binary.Write(&buf, binary.LittleEndian, uint16(channels))
	binary.Write(&buf, binary.LittleEndian, uint32(rate))
	binary.Write(&buf, binary.LittleEndian, uint32(rate*channels*2))
	binary.Write(&buf, binary.LittleEndian, uint16(channels*2))
	binary.Write(&buf, binary.LittleEndian, uint16(bits))
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(dataLen))
	binary.Write(&buf, binary.LittleEndian, samples)
	return buf.Bytes()
}

// The rate is the point of this test: the device opens its track at 16000, and
// espeak-ng emits 22050. Passing the samples through unchanged plays the clip at
// the wrong speed — 38% fast and shrill — while every layer reports success.
func TestToDevicePCMResamplesTo16k(t *testing.T) {
	const inRate = 22050
	in := make([]int16, inRate) // exactly one second
	for i := range in {
		in[i] = int16(i % 1000)
	}
	out, err := toDevicePCM(wav(t, inRate, 1, 16, in))
	if err != nil {
		t.Fatalf("toDevicePCM: %v", err)
	}
	gotFrames := len(out) / 2
	if gotFrames < 15990 || gotFrames > 16010 {
		t.Fatalf("one second at 22050 Hz became %d frames at 16000 Hz, want ~16000", gotFrames)
	}
}

func TestToDevicePCMDownmixesStereo(t *testing.T) {
	// A hard-left-only signal must survive at half amplitude, not vanish.
	in := []int16{1000, 0, 1000, 0, 1000, 0, 1000, 0}
	out, err := toDevicePCM(wav(t, 16000, 2, 16, in))
	if err != nil {
		t.Fatalf("toDevicePCM: %v", err)
	}
	if len(out) != 8 {
		t.Fatalf("four stereo frames became %d bytes, want 8", len(out))
	}
	got := int16(binary.LittleEndian.Uint16(out[0:2]))
	if got != 500 {
		t.Fatalf("left-only 1000 downmixed to %d, want 500", got)
	}
}

func TestToDevicePCMRejectsNonWAV(t *testing.T) {
	if _, err := toDevicePCM([]byte("this is not audio")); err == nil {
		t.Fatal("a non-WAV file was accepted")
	}
}

// A WAV with a LIST chunk before data is what ffmpeg actually emits; assuming a
// 44-byte header reads the chunk headers as samples and plays noise.
func TestToDevicePCMWalksChunks(t *testing.T) {
	base := wav(t, 16000, 1, 16, []int16{100, 200, 300, 400})
	// Insert a fake LIST chunk between "WAVE" and "fmt ".
	list := []byte("LIST")
	binary.Write(&bytes.Buffer{}, binary.LittleEndian, 0)
	chunk := make([]byte, 12)
	copy(chunk[0:4], list)
	binary.LittleEndian.PutUint32(chunk[4:8], 4)
	copy(chunk[8:12], "INFO")

	out := append([]byte{}, base[:12]...)
	out = append(out, chunk...)
	out = append(out, base[12:]...)
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(out)-8))

	got, err := toDevicePCM(out)
	if err != nil {
		t.Fatalf("toDevicePCM with a LIST chunk: %v", err)
	}
	if len(got) != 8 {
		t.Fatalf("got %d bytes, want 8", len(got))
	}
	if v := int16(binary.LittleEndian.Uint16(got[0:2])); v != 100 {
		t.Fatalf("first sample = %d, want 100", v)
	}
}
