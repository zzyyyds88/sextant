package challenge

import (
	"testing"

	"sextant-core/internal/ui"
)

func node(class, text, desc string) ui.Node {
	attrs := map[string]string{"class": class}
	if text != "" {
		attrs["text"] = text
	}
	if desc != "" {
		attrs["content-desc"] = desc
	}
	return ui.Node{Attrs: attrs}
}

func TestCodeWallNeedsAnEditableField(t *testing.T) {
	res := Classify([]ui.Node{
		node("android.widget.TextView", "短信验证码已发送", ""),
		node("android.widget.EditText", "", ""),
	})
	if res.Kind != KindCode {
		t.Fatalf("want code, got %s (%v)", res.Kind, res.Evidence)
	}
}

func TestASearchResultMentioningTheWordIsNotAWall(t *testing.T) {
	// A results page lists snippets that contain 验证码. There is no field to type
	// into and no slider: this must stay none, or every search would be handed to
	// the user.
	res := Classify([]ui.Node{
		node("android.widget.TextView", "如何获取验证码", ""),
		node("android.widget.TextView", "短信验证码收不到怎么办", ""),
	})
	if res.Kind != KindNone {
		t.Fatalf("want none, got %s (%v)", res.Kind, res.Evidence)
	}
}

func TestAPhraseWithoutAControlIsNone(t *testing.T) {
	// Rendered text alone is not actionable. Returning a wall here would stop the
	// agent on every search result that mentions 验证码.
	res := Classify([]ui.Node{
		node("android.widget.TextView", "请输入验证码", ""),
	})
	if res.Kind != KindNone {
		t.Fatalf("want none, got %s (%v)", res.Kind, res.Evidence)
	}
	if len(res.Evidence) == 0 {
		t.Fatal("the ignored phrase should still be reported as evidence")
	}
}

func TestSliderNeedsADraggableNode(t *testing.T) {
	res := Classify([]ui.Node{
		node("android.widget.TextView", "拖动滑块完成拼图", ""),
		node("android.widget.SeekBar", "", ""),
	})
	if res.Kind != KindSlider {
		t.Fatalf("want slider, got %s (%v)", res.Kind, res.Evidence)
	}
}

func TestSliderTargetPicksTheWidestTrack(t *testing.T) {
	small := node("android.widget.SeekBar", "", "")
	small.Bounds = [4]int{100, 100, 140, 140}
	wide := node("android.widget.SeekBar", "", "")
	wide.Bounds = [4]int{100, 900, 900, 960}
	got, ok := SliderTarget([]ui.Node{small, wide})
	if !ok || got.Bounds != wide.Bounds {
		t.Fatalf("want the wide track, got %v ok=%v", got.Bounds, ok)
	}
}

func TestEmptyTreeIsNone(t *testing.T) {
	if res := Classify(nil); res.Kind != KindNone {
		t.Fatalf("want none, got %s", res.Kind)
	}
}
