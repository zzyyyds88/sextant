// Package crawl builds an App Map by driving the app and recording what it
// reaches.
//
// Portions are derived from PhoneGo (https://github.com/HKUDS/PhoneGo),
// MIT License, Copyright (c) 2024 xuyifan-0731 and 2025 yangqin-jiang.
//
// Every branch is verified: the app is brought to the foreground and checked,
// the tap is resolved by label before coordinates are trusted, and a screen is
// only recorded when the foreground really is the app being crawled. The guards
// are the point: a crawler that records whatever it happens to see produces a
// map that out-scores the real screen at replay time.
package crawl

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"sextant/internal/appmap"
	"sextant/internal/macro"
	"sextant/internal/uiapi"
)

// Options configures one crawl.
type Options struct {
	Package string
	AppName string

	MaxScreens  int
	MaxDepth    int
	MaxMinutes  float64
	ScrollPages int

	// Display selects where to crawl. Only 0 (the phone's own screen) is
	// supported; PhoneGo's host-side crawler drives the same display.
	Display int
	// AllowConsequential turns off the create/delete/send/pay guard. Only for an
	// app whose every screen is behind such a button.
	AllowConsequential bool

	Warn  func(string)
	Sleep func(time.Duration)
	Now   func() time.Time
}

// hostFor maps a package to the other packages that may legitimately host its
// pages. MIUI hands many settings pages over to the phone/account apps; for any
// other app those packages are "the crawl left the app".
var hostFor = map[string]map[string]bool{
	"com.android.settings": {
		"com.android.phone":       true,
		"com.miui.securitycenter": true,
		"com.xiaomi.account":      true,
	},
}

// chrome is the keyboard and system UI, which sit on top without being another
// app.
var chrome = map[string]bool{
	"com.android.systemui":             true,
	"com.google.android.inputmethod":   true,
	"com.sohu.inputmethod":             true,
	"com.baidu.input":                  true,
	"com.iflytek.inputmethod":          true,
	"com.miui.inputmethod":             true,
	"com.android.permissioncontroller": true,
}

// consequential are labels whose tap changes the world instead of opening a
// page. Ported from PhoneGo; measured there as "one afternoon of crawling the
// clock left a dozen alarms behind".
var consequential = regexp.MustCompile(
	`(新建|创建|添加|删除|移除|清除|清空|重置|格式化|卸载|恢复出厂` +
		`|安装|更新全部|立即更新|升级` +
		`|确定|保存|提交|发送|拨号|呼叫|支付|购买|下单|充值` +
		`|拍照|录像|录音` +
		`|开始|启动|关机|重启|注销|退出登录|解绑|取消配对` +
		`|^赞$|^点赞|^转发|^评论$|^关注|^已关注|^收藏|^打赏|^投币|^三连` +
		`|不喜欢|不感兴趣|屏蔽|举报|投诉|拉黑|减少推荐|踩)`)

var noiseLabels = []string{
	"profile picture", "navigate up", "more options", "dismiss", "clear",
	"learn more", "back", "cancel", "no unused", "no recent", "no nearby",
}

const minScreenElements = 4

type queued struct {
	element appmap.Element
	path    []string
	macro   []appmap.Step
}

// Build crawls one app and returns its map.
func Build(ctx context.Context, d *uiapi.Driver, opts Options) (*appmap.Map, error) {
	if opts.Package == "" {
		return nil, fmt.Errorf("package is required")
	}
	if opts.Display != 0 {
		return nil, fmt.Errorf("display %d: only display 0 is supported in this build", opts.Display)
	}
	if opts.MaxScreens <= 0 {
		opts.MaxScreens = 12
	}
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = 3
	}
	if opts.ScrollPages < 0 {
		opts.ScrollPages = 0
	}
	if opts.Sleep == nil {
		opts.Sleep = time.Sleep
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	warn := func(format string, args ...any) {
		if opts.Warn != nil {
			opts.Warn(fmt.Sprintf(format, args...))
		}
	}

	radios := snapshotRadios(ctx, d)
	defer restoreRadios(ctx, d, radios, warn)

	if err := ensureHome(ctx, d, opts.Package, warn); err != nil {
		return nil, err
	}

	width, height, err := d.ScreenSize(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot read the screen size: %w", err)
	}
	boot := []appmap.Step{
		{Action: "force_stop", Package: opts.Package},
		{Action: "launch", Package: opts.Package},
	}
	elements0, err := collectScreenReady(ctx, d, width, height, opts.ScrollPages)
	if err != nil {
		return nil, err
	}
	if len(elements0) == 0 {
		return nil, fmt.Errorf("the app shows nothing an accessibility tree can read; no map written")
	}
	// D33: record the page identity this screen was captured from. A missing
	// value stays empty; a wrong one would make every later drift check lie.
	firstPage, _ := d.Page(ctx)

	out := &appmap.Map{
		App:          opts.AppName,
		Package:      opts.Package,
		ScreenW:      width,
		ScreenH:      height,
		Display:      intPtr(0),
		Screens:      []appmap.Screen{{ID: "screen_0", Elements: elements0, Package: opts.Package, Fingerprint: firstPage.Fingerprint, AppVersion: firstPage.AppVersion}},
		ScreenMacros: map[string][]appmap.Step{"screen_0": boot},
		CrawledAt:    opts.Now().UTC().Format(time.RFC3339),
		Coverage:     appmap.Coverage{DepthLimit: opts.MaxDepth},
		KnownLimitations: []string{
			"no_llm_enrichment",
		},
	}
	known := []knownScreen{{elements: elements0}}

	queue := queueChildren(elements0, []string{"screen_0"}, boot, width, height, opts)
	deadline := time.Time{}
	if opts.MaxMinutes > 0 {
		deadline = opts.Now().Add(time.Duration(opts.MaxMinutes * float64(time.Minute)))
	}
	stopped := ""
	for len(queue) > 0 && len(out.Screens) < opts.MaxScreens {
		if !deadline.IsZero() && opts.Now().After(deadline) {
			stopped = "time_budget"
			break
		}
		item := queue[0]
		queue = queue[1:]
		parentID := item.path[len(item.path)-1]
		tapSteps := stepsFor(item.element, width, height, parentID)
		nextMacro := append(append([]appmap.Step(nil), item.macro...), tapSteps...)
		next, ok, err := exploreBranch(ctx, d, opts, out, known, item, tapSteps, width, height, warn)
		if err != nil {
			warn("branch %q: %v", item.element.Text, err)
			continue
		}
		if !ok {
			continue
		}
		known = append(known, knownScreen{elements: next.Elements})
		out.Screens = append(out.Screens, *next)
		out.ScreenMacros[next.ID] = nextMacro
		out.Coverage.Screens = len(out.Screens)
		warn("screen %s: %d elements (from %q)", next.ID, len(next.Elements), item.element.Text)
		queue = append(queue, queueChildren(next.Elements, append(item.path, next.ID), nextMacro, width, height, opts)...)
	}
	out.Coverage.Queued = len(queue)
	out.Coverage.Stopped = stopped
	out.Coverage.Complete = stopped == "" && len(queue) == 0
	return out, nil
}

type knownScreen struct {
	elements []appmap.Element
}

func exploreBranch(ctx context.Context, d *uiapi.Driver, opts Options, out *appmap.Map, known []knownScreen, item queued, tapSteps []appmap.Step, width, height int, warn func(string, ...any)) (*appmap.Screen, bool, error) {
	parentID := item.path[len(item.path)-1]
	// Bring the app up, then walk to the parent screen. The parent macro starts
	// with its own boot steps; strip them so the launch is not paid twice.
	if err := launchVerified(ctx, d, opts.Package); err != nil {
		return nil, false, err
	}
	parent := trimBoot(item.macro)
	if len(parent) > 0 {
		res := macro.Run(ctx, d, parent, macro.Options{SourceW: out.ScreenW, SourceH: out.ScreenH, CurrentW: width, CurrentH: height, Sleep: opts.Sleep})
		if !res.OK {
			return nil, false, fmt.Errorf("parent macro: %s", res.Error)
		}
	}

	res := macro.Run(ctx, d, tapSteps, macro.Options{SourceW: out.ScreenW, SourceH: out.ScreenH, CurrentW: width, CurrentH: height, Sleep: opts.Sleep})
	if !res.OK {
		return nil, false, fmt.Errorf("tap %q: %s", item.element.Text, res.Error)
	}

	page, err := d.Page(ctx)
	if err != nil {
		return nil, false, err
	}
	if foreign(page.Package, opts.Package) {
		return nil, false, fmt.Errorf("left the app for %q", page.Package)
	}

	nodes, err := d.Tree(ctx)
	if err != nil {
		return nil, false, err
	}
	if target := matchedID(nodes, known); target != "" {
		// A known screen: record the edge, do not collect it again. A self-edge
		// says the tap did nothing, which is not a navigation.
		if target != parentID {
			recordEdge(out, parentID, item.element.Text, target)
		}
		return nil, false, nil
	}

	elements, err := collectScreenReady(ctx, d, width, height, opts.ScrollPages)
	if err != nil {
		return nil, false, err
	}
	if len(elements) < minScreenElements {
		return nil, false, fmt.Errorf("only %d element(s); not a screen", len(elements))
	}
	if stable := stableElements(ctx, d, elements); len(stable) >= maxInt(3, len(elements)/3) {
		elements = stable
	}
	if len(elements) < minScreenElements {
		return nil, false, fmt.Errorf("only %d stable element(s); not a screen", len(elements))
	}
	id := fmt.Sprintf("screen_%d", len(out.Screens))
	recordEdge(out, parentID, item.element.Text, id)
	// The page's own package, not the crawled one: MIUI hands settings pages to
	// com.android.phone / com.xiaomi.account, and a screen tagged with the wrong
	// package can never be identified at replay time.
	return &appmap.Screen{ID: id, Elements: elements, Package: page.Package,
		Fingerprint: page.Fingerprint, AppVersion: page.AppVersion}, true, nil
}

func queueChildren(elements []appmap.Element, path []string, baseMacro []appmap.Step, width, height int, opts Options) []queued {
	var out []queued
	for _, element := range elements {
		if element.Text == "" || skipLabel(element.Text) {
			continue
		}
		if !opts.AllowConsequential && consequential.MatchString(element.Text) {
			continue
		}
		// PhoneGo compares the path length itself: path is [screen_0, child, ...],
		// so max-depth 1 means "direct children of the home screen only".
		if len(path) > opts.MaxDepth {
			continue
		}
		out = append(out, queued{element: element, path: append([]string(nil), path...), macro: append([]appmap.Step(nil), baseMacro...)})
	}
	return out
}

// stepsFor is the path from the parent screen's top to this element: scroll
// back to the page it was found on, then tap. Collecting a screen with scroll
// records folded elements; without the scroll the tap lands on whatever row
// happens to occupy that coordinate at the top, which is how a map acquires an
// edge that points at the wrong screen.
// stepsFor is the path from the parent screen's top to this element.
//
// The executor resolves the label in the live tree and scrolls a bounded number
// of pages to find it, so no fixed scroll count is recorded. An explicit count
// was tried and rejected: one ACTION_SCROLL_DOWN does not move every list by the
// same amount, and a tap measured at the wrong offset opened the neighbouring
// row (measured: 声音与触感 opened the Xiaomi account page).
func stepsFor(element appmap.Element, width, height int, parentID string) []appmap.Step {
	return []appmap.Step{{
		Action: "tap",
		X:      int(math.Round(element.Center[0] * float64(width))),
		Y:      int(math.Round(element.Center[1] * float64(height))),
		Wait:   1.0,
		Text:   element.Text,
		Screen: parentID,
	}}
}

// launchVerified brings the package to the front and checks it really is there.
func launchVerified(ctx context.Context, d *uiapi.Driver, pkg string) error {
	_ = d.Stop(ctx, pkg)
	if err := d.Launch(ctx, pkg, false); err != nil {
		return err
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		page, err := d.Page(ctx)
		if err == nil && page.Package == pkg {
			// The tree lags the window; wait for it before anyone reads or taps.
			_, _ = d.WaitStable(ctx, 3000, 150)
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("did not land in %s (foreground %q)", pkg, page.Package)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}

func ensureHome(ctx context.Context, d *uiapi.Driver, pkg string, warn func(string, ...any)) error {
	var last error
	for attempt := 1; attempt <= 3; attempt++ {
		if err := launchVerified(ctx, d, pkg); err == nil {
			return nil
		} else {
			last = err
		}
		if nodes, err := d.Tree(ctx); err == nil {
			if dismissDialog(ctx, d, nodes, warn) {
				_, _ = d.WaitStable(ctx, 2000, 150)
				if page, err := d.Page(ctx); err == nil && page.Package == pkg {
					return nil
				}
			}
		}
	}
	return fmt.Errorf("cannot bring %s up: %w", pkg, last)
}

func collectScreen(ctx context.Context, d *uiapi.Driver, width, height, scrollPages int) ([]appmap.Element, error) {
	seen := map[string]bool{}
	var out []appmap.Element
	for page := 0; page <= scrollPages; page++ {
		nodes, err := d.Tree(ctx)
		if err != nil {
			return nil, err
		}
		for i, n := range nodes {
			if !clickableForCrawl(nodes, i) {
				continue
			}
			element, ok := toElement(n, width, height)
			if !ok || seen[element.Text] {
				continue
			}
			seen[element.Text] = true
			element.FoundAtScroll = page
			out = append(out, element)
		}
		if page < scrollPages {
			if err := d.Swipe(ctx, width/2, int(float64(height)*0.7), width/2, int(float64(height)*0.2), 400); err != nil {
				return nil, err
			}
			_, _ = d.WaitStable(ctx, 2000, 150)
		}
	}
	return out, nil
}

// clickableForCrawl keeps a labelled node only when it or an ancestor can be
// clicked. A section header is a label, not a destination; queueing it spends
// the crawl's budget on taps that change nothing.
// collectScreenReady retries an empty first read once. A window can be
// foreground while its first real frame is still being laid out; without this,
// a crawl started right after another app (the journey's HUD scenario) failed
// with "the app shows nothing" before the page existed. A second empty read is
// still reported as the same honest error.
func collectScreenReady(ctx context.Context, d *uiapi.Driver, width, height, scrollPages int) ([]appmap.Element, error) {
	elements, err := collectScreen(ctx, d, width, height, scrollPages)
	if err != nil || len(elements) > 0 {
		return elements, err
	}
	if _, err := d.WaitStable(ctx, 3000, 150); err == nil {
		return collectScreen(ctx, d, width, height, scrollPages)
	}
	return elements, nil
}

func clickableForCrawl(nodes []uiapi.Node, i int) bool {
	for p := i; p >= 0 && p < len(nodes); p = nodes[p].Parent {
		if nodes[p].Clickable == uiapi.TriTrue {
			return true
		}
	}
	return false
}

// toggleNode is a switch, not a page. Tapping it changes a setting, which is
// exactly the kind of side effect a crawl must not produce by accident.
func toggleNode(n uiapi.Node) bool {
	class := strings.ToLower(n.Class)
	return strings.Contains(class, "switch") || strings.Contains(class, "checkbox") || strings.Contains(class, "togglebutton")
}

func toElement(n uiapi.Node, width, height int) (appmap.Element, bool) {
	label := n.Label()
	if len(label) < 2 || skipLabel(label) || toggleNode(n) {
		return appmap.Element{}, false
	}
	if n.Visible == uiapi.TriFalse || n.Width() < 20 || n.Height() < 20 {
		return appmap.Element{}, false
	}
	cx, cy := n.Center()
	return appmap.Element{
		Text: label,
		Center: [2]float64{
			round4(float64(cx) / float64(width)),
			round4(float64(cy) / float64(height)),
		},
	}, true
}

// stableElements keeps only labels present in two consecutive dumps. A capture
// catches toasts and spinners; a map built from them can never match again.
func stableElements(ctx context.Context, d *uiapi.Driver, elements []appmap.Element) []appmap.Element {
	nodes, err := d.Tree(ctx)
	if err != nil {
		return elements
	}
	here := map[string]bool{}
	for _, n := range nodes {
		if label := n.Label(); label != "" {
			here[label] = true
		}
	}
	var out []appmap.Element
	for _, e := range elements {
		if here[e.Text] {
			out = append(out, e)
		}
	}
	return out
}

func sameAsKnown(nodes []uiapi.Node, known []knownScreen) bool {
	return matchedID(nodes, known) != ""
}

func matchedID(nodes []uiapi.Node, known []knownScreen) string {
	texts := map[string]bool{}
	for _, n := range nodes {
		if label := n.Label(); len(label) > 1 {
			texts[label] = true
		}
	}
	for i, screen := range known {
		if similarity(texts, screen.elements) >= 0.85 {
			return fmt.Sprintf("screen_%d", i)
		}
	}
	return ""
}

func similarity(texts map[string]bool, elements []appmap.Element) float64 {
	known := map[string]bool{}
	for _, e := range elements {
		known[e.Text] = true
	}
	if len(known) == 0 || len(texts) == 0 {
		return 0
	}
	shared := 0
	for text := range texts {
		if known[text] {
			shared++
		}
	}
	jaccard := float64(shared) / float64(len(texts)+len(known)-shared)
	if shared < 4 {
		return jaccard
	}
	containment := float64(shared) / float64(minInt(len(texts), len(known)))
	if containment > jaccard {
		return containment
	}
	return jaccard
}

func recordEdge(m *appmap.Map, parentID, label, targetID string) {
	parent := m.Screen(parentID)
	if parent == nil {
		return
	}
	for i := range parent.Elements {
		if parent.Elements[i].Text == label {
			parent.Elements[i].LeadsTo = targetID
			return
		}
	}
}

func snapshotRadios(ctx context.Context, d *uiapi.Driver) map[string]string {
	out := map[string]string{}
	for _, key := range []string{"wifi_on", "bluetooth_on", "airplane_mode_on"} {
		if v, err := d.SettingGet(ctx, "global", key); err == nil {
			out[key] = v
		}
	}
	return out
}

func restoreRadios(ctx context.Context, d *uiapi.Driver, before map[string]string, warn func(string, ...any)) {
	for key, value := range before {
		if err := d.SettingPut(ctx, "global", key, value); err != nil {
			warn("restore %s=%s: %v", key, value, err)
		}
	}
}

func dismissDialog(ctx context.Context, d *uiapi.Driver, nodes []uiapi.Node, warn func(string, ...any)) bool {
	labels := map[string]bool{}
	for _, n := range nodes {
		labels[n.Label()] = true
	}
	deny := []string{"拒绝", "取消", "不允许", "禁止", "退出", "以后再说"}
	grant := []string{"始终允许", "本次允许", "使用时允许", "允许", "启动应用", "同意"}
	if !hasAny(labels, deny) || !hasAny(labels, grant) {
		return false
	}
	for _, label := range append(deny, grant...) {
		if !labels[label] {
			continue
		}
		if _, err := d.Act(ctx, map[string]any{"text": label}, "click", "", 0, 0, nil); err == nil {
			warn("dismissed a system dialog with %q", label)
			return true
		}
	}
	return false
}

func foreign(foreground, pkg string) bool {
	if foreground == "" || foreground == pkg {
		return false
	}
	if hostFor[pkg][foreground] || chrome[foreground] {
		return false
	}
	return true
}

func hasAny(set map[string]bool, list []string) bool {
	for _, item := range list {
		if set[item] {
			return true
		}
	}
	return false
}

func skipLabel(text string) bool {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) <= 1 && isASCII(trimmed) {
		return true
	}
	lower := strings.ToLower(trimmed)
	for _, word := range noiseLabels {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}

func isASCII(s string) bool {
	for _, r := range s {
		if r > 127 {
			return false
		}
	}
	return true
}

func trimBoot(steps []appmap.Step) []appmap.Step {
	i := 0
	for i < len(steps) && (steps[i].Action == "force_stop" || steps[i].Action == "launch") {
		i++
	}
	return steps[i:]
}

func round4(v float64) float64 { return math.Round(v*1e4) / 1e4 }

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func intPtr(v int) *int { return &v }
