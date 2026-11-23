package engine

import (
	"testing"

	"chopin-core/internal/ui"
)

func TestDangerWord(t *testing.T) {
	cases := []struct {
		text, desc, want string
	}{
		{"立即购买", "", "立即购买"},
		{"提交订单", "", "提交订单"},
		{"确认支付", "", "确认支付"},
		{"加入购物车（限1件）", "", "加入购物车"},
		{"", "去结算", "去结算"},
		{"充值", "", "充值"},
		{"打赏", "", "打赏"},
		{"续费", "", "续费"},
		{"开通会员", "", "开通会员"},
		{"确认订单", "", "确认订单"},
		{"设置", "", ""},
		{"商品详情", "", ""},
		{"搜索本地或网络结果", "", ""},
	}
	for _, c := range cases {
		if got := dangerWord(c.text, c.desc); got != c.want {
			t.Fatalf("dangerWord(%q, %q) = %q, want %q", c.text, c.desc, got, c.want)
		}
	}
}

func TestExpectMetInputText(t *testing.T) {
	nodes := []ui.Node{{
		Index: 0,
		Attrs: map[string]string{
			"resource-id": "com.tencent.mm:id/d98",
			"text":        "测试探针ABC",
		},
	}}
	selector := map[string]string{"text": "", "resource_id": "com.tencent.mm:id/d98"}
	if !expectMet(nodes, map[string]any{"input_text": "探针"}, selector) {
		t.Fatal("input_text should match a substring of the field")
	}
	if expectMet(nodes, map[string]any{"input_text": "不存在的词"}, selector) {
		t.Fatal("input_text must not match text that is absent")
	}
	// A missing field is not a match: verifying an input that disappeared must
	// fail rather than pass by absence.
	if expectMet(nil, map[string]any{"input_text": "探针"}, selector) {
		t.Fatal("input_text must not pass when the target is gone")
	}
}
