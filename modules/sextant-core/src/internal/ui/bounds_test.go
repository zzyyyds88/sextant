package ui

import "testing"

func TestInvertedBoundsAreStillTappable(t *testing.T) {
	// Measured on WeChat's conversation list: the row title came back as
	// [194 2351 430 2249] — bottom above top. Reading it literally made the node look
	// empty, so the search kept the off-screen copy at x = -107 and the click landed
	// nowhere while reporting success.
	n := Node{Attrs: map[string]string{"text": "serendipity"}, Bounds: [4]int{194, 2351, 430, 2249}}
	if !tappable(n) {
		t.Fatal("an inverted rectangle with real area must still count as on screen")
	}
	if got := n.CenterY(); got != 2300 {
		t.Fatalf("centre = %d", got)
	}
}

func TestFindPrefersTheOnScreenRowOverTheOffscreenCopy(t *testing.T) {
	nodes := []Node{
		{Attrs: map[string]string{"text": "serendipity"}, Bounds: [4]int{0, 652, -214, 714}},
		{Attrs: map[string]string{"text": "serendipity"}, Bounds: [4]int{194, 2351, 430, 2249}},
	}
	got, ok := FindByText(nodes, "serendipity")
	if !ok {
		t.Fatal("not found")
	}
	if got.Bounds[0] != 194 {
		t.Fatalf("picked the off-screen copy: %v", got.Bounds)
	}
}
