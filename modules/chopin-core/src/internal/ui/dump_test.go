package ui

import "testing"

func TestFindSkipsOffscreenCopies(t *testing.T) {
	// WeChat keeps a floating-window copy of the conversation row at negative
	// coordinates and lists it first. Tapping it does nothing, which reads as "the
	// app did not open" rather than "we tapped the wrong copy".
	nodes := []Node{
		{Attrs: map[string]string{"text": "serendipity"}, Bounds: [4]int{-837, 264, -205, 326}},
		{Attrs: map[string]string{"text": "serendipity"}, Bounds: [4]int{205, 264, 866, 326}},
	}
	got, ok := FindByText(nodes, "serendipity")
	if !ok {
		t.Fatal("not found")
	}
	if got.Bounds[0] != 205 {
		t.Fatalf("picked the off-screen copy: %v", got.Bounds)
	}
}

func TestFindPrefersAnExactLabelOverAContainingOne(t *testing.T) {
	// Measured: `find serendipity --click` tapped the avatar (「serendipity头像」) and
	// opened the contact card instead of the conversation.
	nodes := []Node{
		{Attrs: map[string]string{"content-desc": "serendipity头像"}, Bounds: [4]int{16, 723, 156, 863}},
		{Attrs: map[string]string{"text": "serendipity"}, Bounds: [4]int{205, 700, 866, 762}},
	}
	got, ok := FindByText(nodes, "serendipity")
	if !ok {
		t.Fatal("not found")
	}
	if got.Attrs["text"] != "serendipity" {
		t.Fatalf("picked %v", got.Attrs)
	}
}
