package engine

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"chopin-core/internal/ui"
)

// Screen text helpers.
//
// OCR output arrives as a list of {text, bounds} objects. Everything that needs
// to interpret it (a countdown, an action button, a combined transcript) lives
// here so the rules are in one place instead of being re-derived per handler.

var countdownRE = regexp.MustCompile(`(\d{1,3})\s*(?:s\b|秒)`)

// actionButtonWords are the phrasings that mean "press me to take part". They are
// deliberately few and literal: guessing wider would press the wrong thing.
var actionButtonWords = []string{
	"发表评论", "参与", "已参与", "立即参与", "点击参与", "领券", "领取", "立即领取",
}

// ocrLines pulls the text list out of a helper OCR response.
func ocrLines(raw map[string]any) []string {
	items, ok := raw["lines"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if s, ok := m["text"].(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

// extractTextAndCountdown returns the OCR lines and the largest countdown value
// they contain (-1 when there is none).
func extractTextAndCountdown(raw map[string]any) ([]string, int) {
	texts := ocrLines(raw)
	countdown := -1
	for _, t := range texts {
		for _, m := range countdownRE.FindAllStringSubmatch(t, -1) {
			if len(m) != 2 {
				continue
			}
			if n, err := strconv.Atoi(m[1]); err == nil && n > countdown {
				countdown = n
			}
		}
	}
	return texts, countdown
}

// findActionButton locates the first OCR line that looks like an action button.
func findActionButton(raw map[string]any) (map[string]any, bool) {
	items, ok := raw["lines"].([]any)
	if !ok {
		return nil, false
	}
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		text, _ := m["text"].(string)
		for _, word := range actionButtonWords {
			if !strings.Contains(text, word) {
				continue
			}
			x, y := lineCenter(m)
			return map[string]any{"text": text, "x": x, "y": y, "bounds": m["bounds"]}, true
		}
	}
	return nil, false
}

// intOf converts a JSON number or a numeric string to int.
func intOf(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case string:
		value := 0
		for _, r := range n {
			if r < '0' || r > '9' {
				continue
			}
			value = value*10 + int(r-'0')
		}
		return value
	}
	return 0
}

// lineCenter computes the centre of an OCR line's bounds.
func lineCenter(line map[string]any) (int, int) {
	b, ok := line["bounds"].(map[string]any)
	if !ok {
		// Some builds return [left, top, right, bottom].
		if arr, ok := line["bounds"].([]any); ok && len(arr) == 4 {
			l, t := intOf(arr[0]), intOf(arr[1])
			r, bm := intOf(arr[2]), intOf(arr[3])
			return (l + r) / 2, (t + bm) / 2
		}
		return 0, 0
	}
	left, top := intOf(b["left"]), intOf(b["top"])
	right, bottom := intOf(b["right"]), intOf(b["bottom"])
	return (left + right) / 2, (top + bottom) / 2
}

// findNodeByText finds a node whose text or content description contains want.
//
// Containment is used on purpose: a button's label is often split by layout, and
// exact matching would miss it. The smallest containing node wins, so a click
// lands on the button rather than on the whole screen.
func findNodeByText(nodes []ui.Node, want string) (ui.Node, bool) {
	var best ui.Node
	found := false
	bestArea := 0
	for _, n := range nodes {
		hay := n.Attrs["text"] + "\x00" + n.Attrs["content-desc"]
		if !strings.Contains(hay, want) {
			continue
		}
		area := (n.Bounds[2] - n.Bounds[0]) * (n.Bounds[3] - n.Bounds[1])
		if !found || (area > 0 && area < bestArea) {
			best, bestArea, found = n, area, true
		}
	}
	return best, found
}

// findOCRLine finds the smallest OCR line whose text contains want.
func findOCRLine(raw map[string]any, want string) (map[string]any, bool) {
	items, ok := raw["lines"].([]any)
	if !ok {
		return nil, false
	}
	var best map[string]any
	bestArea := 0
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		text, _ := m["text"].(string)
		if !strings.Contains(text, want) {
			continue
		}
		b, _ := m["bounds"].(map[string]any)
		area := (intOf(b["right"]) - intOf(b["left"])) * (intOf(b["bottom"]) - intOf(b["top"]))
		if best == nil || (area > 0 && area < bestArea) {
			best, bestArea = m, area
		}
	}
	return best, best != nil
}

// strOf renders a JSON value as a string.
func strOf(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case nil:
		return ""
	default:
		return fmt.Sprint(s)
	}
}

// findNodeByResourceID finds a node by its exact resource id.
func findNodeByResourceID(nodes []ui.Node, want string) (ui.Node, bool) {
	for _, n := range nodes {
		if n.Attrs["resource-id"] == want {
			return n, true
		}
	}
	return ui.Node{}, false
}
