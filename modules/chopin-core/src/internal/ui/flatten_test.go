package ui

import "testing"

func flatFixture() []Node {
	return []Node{
		{Index: 0, Parent: -1, Depth: 0, Bounds: [4]int{0, 0, 100, 100}, Attrs: map[string]string{"class": "root", "text": ""}},
		{Index: 1, Parent: 0, Depth: 1, Bounds: [4]int{0, 0, 50, 50}, Attrs: map[string]string{"class": "TextView", "text": "one"}},
		{Index: 2, Parent: 0, Depth: 1, Bounds: [4]int{50, 0, 100, 50}, Attrs: map[string]string{"class": "TextView", "text": "two"}},
		{Index: 3, Parent: -1, Depth: 0, Bounds: [4]int{0, 100, 100, 200}, Attrs: map[string]string{"class": "root", "text": ""}},
	}
}

func TestFlattenAssignsStructuralPaths(t *testing.T) {
	flat := Flatten(flatFixture())
	want := []string{"0", "0/0", "0/1", "1"}
	for i, n := range flat {
		if n.Path != want[i] {
			t.Fatalf("node %d path = %q, want %q", i, n.Path, want[i])
		}
	}
	if flat[1].Parent != "0" || flat[1].NodeID != "n_1" {
		t.Fatalf("child = %+v", flat[1])
	}
}

func TestDiffIdenticalFrames(t *testing.T) {
	a := Flatten(flatFixture())
	d := Diff(a, a, 100)
	if d.Counts.Added != 0 || d.Counts.Removed != 0 || d.Counts.Changed != 0 || d.Truncated {
		t.Fatalf("diff = %+v", d)
	}
	if len(d.Added) != 0 || len(d.Removed) != 0 || len(d.Changed) != 0 {
		t.Fatalf("lists are not empty: %+v", d)
	}
}

func TestDiffTextChange(t *testing.T) {
	before := Flatten(flatFixture())
	after := flatFixture()
	after[1].Attrs = map[string]string{"class": "TextView", "text": "changed"}
	d := Diff(before, Flatten(after), 100)
	if d.Counts.Changed != 1 || d.Counts.Added != 0 || d.Counts.Removed != 0 {
		t.Fatalf("counts = %+v", d.Counts)
	}
	change := d.Changed[0]
	if change.NodeID != "n_1" || change.Fields["text"].Before != "one" || change.Fields["text"].After != "changed" {
		t.Fatalf("change = %+v", change)
	}
}

func TestDiffAddedAndRemoved(t *testing.T) {
	before := Flatten(flatFixture())
	after := flatFixture()
	after = append(after, Node{Index: 4, Parent: 3, Depth: 1, Bounds: [4]int{0, 120, 30, 150},
		Attrs: map[string]string{"class": "Button", "text": "new"}})
	d := Diff(before, Flatten(after), 100)
	if d.Counts.Added != 1 || len(d.Added) != 1 || d.Added[0].NodeID != "n_4" {
		t.Fatalf("added = %+v", d)
	}

	d = Diff(Flatten(after), before, 100)
	if d.Counts.Removed != 1 || len(d.Removed) != 1 || d.Removed[0].NodeID != "n_4" {
		t.Fatalf("removed = %+v", d)
	}
}

func TestDiffBoundsChangeIsAField(t *testing.T) {
	before := Flatten(flatFixture())
	after := flatFixture()
	after[2].Bounds = [4]int{40, 0, 100, 50}
	d := Diff(before, Flatten(after), 100)
	if d.Counts.Changed != 1 {
		t.Fatalf("counts = %+v", d.Counts)
	}
	if _, ok := d.Changed[0].Fields["bounds"]; !ok {
		t.Fatalf("fields = %+v", d.Changed[0].Fields)
	}
}

func TestDiffTruncatesButCountsEverything(t *testing.T) {
	var before, after []Node
	for i := 0; i < 10; i++ {
		before = append(before, Node{Index: i, Parent: -1, Attrs: map[string]string{"text": "x"}})
	}
	d := Diff(Flatten(before), Flatten(after), 3)
	if !d.Truncated {
		t.Fatal("truncated = false")
	}
	if d.Counts.Removed != 10 {
		t.Fatalf("counts.removed = %d, want 10", d.Counts.Removed)
	}
	if len(d.Removed) != 3 {
		t.Fatalf("listed removals = %d, want 3", len(d.Removed))
	}
}
