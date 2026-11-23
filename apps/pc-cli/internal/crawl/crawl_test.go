package crawl

import (
	"testing"

	"chopin/internal/appmap"
	"chopin/internal/uiapi"
)

func TestConsequentialLabelsAreGuarded(t *testing.T) {
	for _, label := range []string{"新建闹钟", "删除", "立即支付", "发送", "关注", "退出登录", "确定"} {
		if !consequential.MatchString(label) {
			t.Fatalf("%q must be treated as consequential", label)
		}
	}
	for _, label := range []string{"WLAN", "蓝牙", "设置", "我的设备", "返回"} {
		if consequential.MatchString(label) {
			t.Fatalf("%q must not be treated as consequential", label)
		}
	}
}

func TestForeignHosts(t *testing.T) {
	if foreign("com.android.phone", "com.android.settings") {
		t.Fatal("the phone app legitimately hosts settings pages")
	}
	if foreign("com.miui.securitycenter", "com.android.settings") {
		t.Fatal("the security centre legitimately hosts permission pages")
	}
	if !foreign("com.xiaomi.account", "com.example.app") {
		t.Fatal("another app is foreign to a non-settings package")
	}
	if foreign("com.android.systemui", "com.example.app") {
		t.Fatal("system UI is chrome, not another app")
	}
}

func TestTrimBoot(t *testing.T) {
	steps := []appmap.Step{
		{Action: "force_stop"}, {Action: "launch"}, {Action: "tap", Text: "WLAN"},
	}
	got := trimBoot(steps)
	if len(got) != 1 || got[0].Text != "WLAN" {
		t.Fatalf("want one tap, got %+v", got)
	}
}

func TestSimilarityContainment(t *testing.T) {
	known := []appmap.Element{
		{Text: "WLAN"}, {Text: "蓝牙"}, {Text: "移动网络"}, {Text: "我的设备"},
	}
	// Same labels, plus content that appears only in one capture: containment
	// should still call this the same screen.
	texts := map[string]bool{"WLAN": true, "蓝牙": true, "移动网络": true, "我的设备": true, "<user>": true}
	if got := similarity(texts, known); got < 0.85 {
		t.Fatalf("want a high similarity, got %.2f", got)
	}
	other := map[string]bool{"完全": true, "不同": true}
	if got := similarity(other, known); got >= 0.5 {
		t.Fatalf("unrelated screens must score low, got %.2f", got)
	}
}

func TestSkipLabel(t *testing.T) {
	for _, label := range []string{"x", "-", "back", "More options"} {
		if !skipLabel(label) {
			t.Fatalf("%q should be skipped", label)
		}
	}
	for _, label := range []string{"WLAN", "蓝牙", "我"} {
		if skipLabel(label) {
			t.Fatalf("%q should be kept", label)
		}
	}
}

func TestStepsForRecordsOnlyTheTextTap(t *testing.T) {
	// The executor scrolls to find the label; a fixed scroll count was tried and
	// rejected because one node scroll is not one page on every list.
	steps := stepsFor(appmap.Element{Text: "WLAN", Center: [2]float64{0.25, 0.5}, FoundAtScroll: 2}, 1000, 2000, "screen_0")
	if len(steps) != 1 {
		t.Fatalf("want one tap step, got %+v", steps)
	}
	tap := steps[0]
	if tap.Action != "tap" || tap.X != 250 || tap.Y != 1000 {
		t.Fatalf("want tap 250,1000 got %+v", tap)
	}
	if tap.Screen != "screen_0" || tap.Text != "WLAN" {
		t.Fatalf("screen/text lost: %+v", tap)
	}
}

func TestBuildRefusesNonZeroDisplay(t *testing.T) {
	_, err := Build(nil, nil, Options{Package: "com.android.settings", Display: 1})
	if err == nil {
		t.Fatal("display 1 is not implemented; it must not silently crawl display 0")
	}
}

func TestToElementSkipsTinyAndUnlabelled(t *testing.T) {
	big := uiapi.Node{Text: "WLAN", Bounds: [4]int{100, 100, 400, 180}}
	if _, ok := toElement(big, 1000, 2000); !ok {
		t.Fatal("a labelled, visible control should be kept")
	}
	tiny := uiapi.Node{Text: "WLAN", Bounds: [4]int{0, 0, 5, 5}}
	if _, ok := toElement(tiny, 1000, 2000); ok {
		t.Fatal("a 5px node is not a control")
	}
	empty := uiapi.Node{Bounds: [4]int{0, 0, 100, 100}}
	if _, ok := toElement(empty, 1000, 2000); ok {
		t.Fatal("an unlabelled node cannot identify a screen")
	}
}
