// Package challenge classifies a screen that is asking for human verification.
//
// The classification is deliberately conjunctive: a word from a list is not
// enough. A search results page that merely mentions 验证码 must not be treated
// as a wall, and a page that says 安全验证 while offering no slider and no input
// is not a slider either. The caller gets a kind plus the evidence, so a wrong
// classification is diagnosable instead of mysterious.
package challenge

import (
	"strings"

	"sextant-core/internal/ui"
)

// Kind is what the screen is asking for.
type Kind string

const (
	// KindNone means nothing actionable was found. A page that merely mentions a
	// verification word (a search result, a help article) is none: treating text
	// as a wall would stop the agent on ordinary content.
	KindNone Kind = "none"
	// KindSlider is a drag-to-verify control. The daemon may attempt it.
	KindSlider Kind = "slider"
	// KindCode is a one-time code delivered by SMS or email. Only the user can
	// complete it, so the daemon must hand over and never retype.
	KindCode Kind = "code"
)

// Result is the classification plus the observations it rests on.
type Result struct {
	Kind     Kind     `json:"kind"`
	Evidence []string `json:"evidence"`
}

var sliderPhrases = []string{
	"拖动滑块", "拖动下方滑块", "滑动验证", "拖动滑块完成拼图", "完成安全验证",
	"人机身份验证", "请完成验证", "拖动到最右", "滑动以验证",
	"verify you are human", "slide to verify", "drag the slider",
}

var codePhrases = []string{
	"短信验证码", "邮箱验证码", "请输入验证码", "验证码已发送", "获取验证码",
	"验证码登录", "输入验证码", "verification code", "security code",
}

// Classify reads a captured tree.
func Classify(nodes []ui.Node) Result {
	texts := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if t := n.Attrs["text"]; t != "" {
			texts = append(texts, t)
		}
		if t := n.Attrs["content-desc"]; t != "" {
			texts = append(texts, t)
		}
	}
	joined := strings.Join(texts, "\n")
	lowered := strings.ToLower(joined)

	codeHits := phraseHits(lowered, codePhrases)
	sliderHits := phraseHits(lowered, sliderPhrases)
	editable := editableNodes(nodes)
	draggable := draggableNodes(nodes)

	res := Result{Kind: KindNone}
	switch {
	case len(codeHits) > 0 && len(editable) > 0:
		res.Kind = KindCode
		res.Evidence = append(res.Evidence, prefixEach("phrase:", codeHits)...)
		res.Evidence = append(res.Evidence, editable...)
	case len(sliderHits) > 0 && len(draggable) > 0:
		res.Kind = KindSlider
		res.Evidence = append(res.Evidence, prefixEach("phrase:", sliderHits)...)
		res.Evidence = append(res.Evidence, draggable...)
	case len(codeHits) > 0 || len(sliderHits) > 0:
		// A phrase with no control behind it: a description, a search result, or a
		// wall rendered in a way this tree cannot see. It is not actionable, so it
		// is none -- but the phrase is kept as evidence so the decision is
		// diagnosable.
		res.Evidence = append(res.Evidence, prefixEach("phrase-without-control:", append(codeHits, sliderHits...))...)
	}
	return res
}

// SliderTarget returns the widest draggable node, which is the track to drag
// along. The handle is a child of it; dragging from the left end of the track
// to the right end is the "slide to the far right" attempt.
func SliderTarget(nodes []ui.Node) (ui.Node, bool) {
	var best ui.Node
	width := -1
	for _, n := range nodes {
		if !isDraggable(n) {
			continue
		}
		w := n.Bounds[2] - n.Bounds[0]
		if w > width {
			best, width = n, w
		}
	}
	return best, width >= 0
}

func phraseHits(haystack string, phrases []string) []string {
	var hits []string
	for _, p := range phrases {
		if strings.Contains(haystack, strings.ToLower(p)) {
			hits = append(hits, p)
		}
	}
	return hits
}

func editableNodes(nodes []ui.Node) []string {
	var out []string
	for _, n := range nodes {
		class := strings.ToLower(n.Attrs["class"])
		if n.Attrs["editable"] == "true" || strings.Contains(class, "edittext") {
			label := n.Attrs["text"]
			if label == "" {
				label = n.Attrs["resource-id"]
			}
			out = append(out, "editable:"+label)
		}
	}
	return out
}

func draggableNodes(nodes []ui.Node) []string {
	var out []string
	for _, n := range nodes {
		if !isDraggable(n) {
			continue
		}
		out = append(out, "draggable:"+n.Attrs["class"])
	}
	return out
}

func isDraggable(n ui.Node) bool {
	class := strings.ToLower(n.Attrs["class"])
	if strings.Contains(class, "seekbar") || strings.Contains(class, "slider") {
		return true
	}
	label := n.Attrs["text"] + " " + n.Attrs["content-desc"]
	return strings.Contains(label, "滑块") || strings.Contains(label, "拖动") ||
		strings.Contains(label, "滑动")
}

func prefixEach(prefix string, items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, prefix+item)
	}
	return out
}
