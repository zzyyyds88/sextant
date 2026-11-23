package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"sextant/internal/appmap"
	"sextant/internal/crawl"
	"sextant/internal/macro"
	"sextant/internal/uiapi"
)

// D31: the App Map CLI. The map is a local artefact; the daemon only sees the
// primitives this file calls.

func cmdMap(a *args) error {
	if len(a.pos) == 0 {
		return fmt.Errorf("usage: sextant map build|list|show|ops|nav|bench ...")
	}
	sub := a.pos[0]
	a.pos = a.pos[1:]
	switch sub {
	case "build":
		return cmdMapBuild(a)
	case "list":
		return cmdMapList(a)
	case "show":
		return cmdMapShow(a)
	case "ops":
		return cmdMapOps(a)
	case "nav":
		return cmdMapNav(a)
	case "bench":
		return cmdMapBench(a)
	case "where":
		return cmdMapWhere(a)
	}
	return fmt.Errorf("unknown map subcommand %q", sub)
}

func cmdMapBuild(a *args) error {
	pkg := a.optStr("package", "")
	if pkg == "" {
		return fmt.Errorf("usage: map build --package P --app NAME [--max-screens N] [--max-minutes M] [--scroll-pages N] [--out FILE]")
	}
	appName := a.optStr("app", pkg)
	c, err := paired()
	if err != nil {
		return err
	}
	driver := uiapi.New(c)
	maxScreens, _ := a.optInt("max-screens", 12)
	maxDepth, _ := a.optInt("max-depth", 3)
	maxMinutes, _ := a.optFloat("max-minutes", 0)
	scrollPages, _ := a.optInt("scroll-pages", 1)
	display, _ := a.optInt("display", 0)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()

	m, err := crawl.Build(ctx, driver, crawl.Options{
		Package:            pkg,
		AppName:            appName,
		MaxScreens:         maxScreens,
		MaxDepth:           maxDepth,
		MaxMinutes:         maxMinutes,
		ScrollPages:        scrollPages,
		Display:            display,
		AllowConsequential: a.has("allow-consequential"),
		Warn: func(message string) {
			fmt.Fprintln(os.Stderr, "warn:", message)
		},
	})
	if err != nil {
		return err
	}
	out := a.optStr("out", "")
	if out == "" {
		dir, err := appmap.DefaultDir()
		if err != nil {
			return err
		}
		out = filepath.Join(dir, m.Package+".yaml")
	}
	if err := m.Save(out); err != nil {
		return err
	}
	printJSONMap(map[string]any{
		"ok": true, "path": out, "package": m.Package, "app": m.App,
		"screens": len(m.Screens), "complete": m.Coverage.Complete,
		"stopped": m.Coverage.Stopped, "queued": m.Coverage.Queued,
	})
	return nil
}

func cmdMapList(a *args) error {
	dir, err := appmap.DefaultDir()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("no maps")
			return nil
		}
		return err
	}
	var rows []map[string]any
	for _, entry := range entries {
		if entry.IsDir() || (!strings.HasSuffix(entry.Name(), ".yaml") && !strings.HasSuffix(entry.Name(), ".yml")) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		m, err := appmap.Load(path)
		if err != nil {
			continue
		}
		rows = append(rows, map[string]any{
			"package": m.Package, "app": m.App, "screens": len(m.Screens),
			"crawled_at": m.CrawledAt, "path": path,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i]["package"].(string) < rows[j]["package"].(string)
	})
	return printJSONMap(map[string]any{"ok": true, "maps": rows})
}

func cmdMapShow(a *args) error {
	m, path, err := loadMapArg(a)
	if err != nil {
		return err
	}
	screens := make([]map[string]any, 0, len(m.Screens))
	for _, s := range m.Screens {
		screens = append(screens, map[string]any{
			"id": s.ID, "description": s.Description, "package": s.Package,
			"elements": len(s.Elements), "selected": s.Selected,
		})
	}
	return printJSONMap(map[string]any{
		"ok": true, "path": path, "app": m.App, "package": m.Package,
		"screen_w": m.ScreenW, "screen_h": m.ScreenH, "display": m.Display,
		"crawled_at": m.CrawledAt, "coverage": m.Coverage, "bench": m.Bench,
		"known_limitations": m.KnownLimitations, "screens": screens,
	})
}

func cmdMapOps(a *args) error {
	m, _, err := loadMapArg(a)
	if err != nil {
		return err
	}
	catalog := m.Catalog(a.has("nav-only"))
	if catalog == "" {
		fmt.Println("no operations")
		return nil
	}
	fmt.Print(catalog)
	return nil
}

func cmdMapNav(a *args) error {
	if len(a.pos) < 2 {
		return fmt.Errorf("usage: map nav FILE OP_ID [--from-screen ID] [--strict-screen]")
	}
	path := a.pos[0]
	opID := a.pos[1]
	m, err := appmap.Load(path)
	if err != nil {
		return err
	}
	op, ok := findOp(m, opID)
	if !ok {
		return fmt.Errorf("operation %q is not in %s", opID, path)
	}
	c, err := paired()
	if err != nil {
		return err
	}
	driver := uiapi.New(c)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	from := a.optStr("from-screen", "")
	if from == "" {
		page, err := driver.Page(ctx)
		if err != nil {
			return err
		}
		nodes, err := driver.Tree(ctx)
		if err != nil {
			return err
		}
		matched, _ := m.IdentifyScreen(nodes, page.Package)
		if matched != nil {
			from = matched.ID
		}
		if warn := appmap.CompareDrift(matched, page).Warn(from); warn != "" {
			fmt.Fprintln(os.Stderr, warn)
		}
	}
	steps := op.Macro
	if op.Type == "NAV" && op.TargetScreen != "" && from != "" {
		if from == op.TargetScreen {
			steps = nil
		} else if rel := m.RelativeMacro(from, op.TargetScreen); len(rel) > 0 {
			steps = rel
		}
	}
	res := macro.Run(ctx, driver, steps, macro.Options{
		StrictScreen: a.has("strict-screen"),
		SourceW:      m.ScreenW,
		SourceH:      m.ScreenH,
		Identify:     m.Identify,
		Warn:         func(message string) { fmt.Fprintln(os.Stderr, "warn:", message) },
	})
	printJSONMap(map[string]any{
		"ok": res.OK, "op": op.ID, "type": op.Type, "from": from,
		"target": op.TargetScreen, "steps": len(res.Steps),
		"warnings": res.Warnings, "error": res.Error,
	})
	if !res.OK {
		return fmt.Errorf("%s", res.Error)
	}
	return nil
}

func cmdMapWhere(a *args) error {
	m, path, err := loadMapArg(a)
	if err != nil {
		return err
	}
	c, err := paired()
	if err != nil {
		return err
	}
	driver := uiapi.New(c)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	page, err := driver.Page(ctx)
	if err != nil {
		return err
	}
	nodes, err := driver.Tree(ctx)
	if err != nil {
		return err
	}
	matched, confidence := m.IdentifyScreen(nodes, page.Package)
	screenID := ""
	if matched != nil {
		screenID = matched.ID
	}
	drift := appmap.CompareDrift(matched, page)
	if warn := drift.Warn(screenID); warn != "" {
		fmt.Fprintln(os.Stderr, warn)
	}
	return printJSONMap(map[string]any{
		"ok": true, "path": path, "package": page.Package,
		"screen": screenID, "confidence": confidence, "drift": drift,
	})
}

func cmdMapBench(a *args) error {
	m, path, err := loadMapArg(a)
	if err != nil {
		return err
	}
	limit, _ := a.optInt("limit", 5)
	c, err := paired()
	if err != nil {
		return err
	}
	driver := uiapi.New(c)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	ops := m.BuildOperations()
	var nav []appmap.Operation
	for _, op := range ops {
		if op.Type == "NAV" && op.TargetScreen != "" {
			nav = append(nav, op)
		}
	}
	sort.Slice(nav, func(i, j int) bool { return nav[i].ID < nav[j].ID })
	// PhoneGo samples the NAV operations with a seed instead of taking the first
	// N: the first N sorted ids are not a sample of the map, they are a sample of
	// the alphabet.
	seed, _ := a.optInt("seed", int(time.Now().UnixNano()&0x7fffffff))
	if limit > 0 && len(nav) > limit {
		r := rand.New(rand.NewSource(int64(seed)))
		perm := r.Perm(len(nav))
		picked := make([]appmap.Operation, 0, limit)
		for i := 0; i < limit; i++ {
			picked = append(picked, nav[perm[i]])
		}
		nav = picked
	}
	var rows []map[string]any
	exact, sameApp := 0, 0
	for _, op := range nav {
		started := time.Now()
		res := macro.Run(ctx, driver, op.Macro, macro.Options{
			StrictScreen: a.has("strict-screen"),
			SourceW:      m.ScreenW,
			SourceH:      m.ScreenH,
			Identify:     m.Identify,
		})
		landed := ""
		driftNote := ""
		if res.OK {
			_, _ = driver.WaitStable(ctx, 3000, 150)
			page, _ := driver.Page(ctx)
			nodes, _ := driver.Tree(ctx)
			matched, _ := m.IdentifyScreen(nodes, page.Package)
			if matched != nil {
				landed = matched.ID
			}
			if page.Package == m.Package {
				sameApp++
			}
			if landed == op.TargetScreen {
				exact++
			}
			driftNote = appmap.CompareDrift(matched, page).Warn(landed)
		}
		rows = append(rows, map[string]any{
			"op": op.ID, "expected": op.TargetScreen, "landed": landed,
			"replay_ok": res.OK, "seconds": time.Since(started).Seconds(), "error": res.Error,
			"drift": driftNote,
		})
	}
	m.Bench = appmap.Bench{Ops: len(rows), Exact: exact, SameApp: sameApp}
	if err := m.Save(path); err != nil {
		return err
	}
	return printJSONMap(map[string]any{
		"ok": true, "seed": seed, "ops": len(rows), "exact": exact,
		"same_app": sameApp, "rows": rows,
	})
}

func loadMapArg(a *args) (*appmap.Map, string, error) {
	if len(a.pos) == 0 {
		return nil, "", fmt.Errorf("usage: map <subcommand> FILE ...")
	}
	path := a.pos[0]
	m, err := appmap.Load(path)
	if err != nil {
		return nil, path, err
	}
	return m, path, nil
}

func findOp(m *appmap.Map, id string) (appmap.Operation, bool) {
	for _, op := range m.BuildOperations() {
		if op.ID == id {
			return op, true
		}
	}
	return appmap.Operation{}, false
}

// mapNavFromRecipe is the recipe action's implementation. It shares the same
// executor as `sextant map nav`, so a recipe step and a direct call cannot drift.
func mapNavFromRecipe(params map[string]any) (map[string]any, error) {
	mapPath, _ := params["map"].(string)
	opID, _ := params["op"].(string)
	if mapPath == "" || opID == "" {
		return nil, fmt.Errorf("map_nav needs params.map and params.op")
	}
	m, err := appmap.Load(mapPath)
	if err != nil {
		return nil, err
	}
	op, ok := findOp(m, opID)
	if !ok {
		return nil, fmt.Errorf("operation %q is not in %s", opID, mapPath)
	}
	c, err := paired()
	if err != nil {
		return nil, err
	}
	driver := uiapi.New(c)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	from, _ := params["from_screen"].(string)
	expectFP, _ := params["expect_fingerprint"].(string)
	if expectFP != "" || from == "" {
		page, pageErr := driver.Page(ctx)
		if pageErr == nil {
			if expectFP != "" && page.Fingerprint != "" && page.Fingerprint != expectFP {
				return nil, fmt.Errorf("map_drift: page fingerprint %s, expected %s", page.Fingerprint, expectFP)
			}
			if from == "" {
				if nodes, err := driver.Tree(ctx); err == nil {
					if matched, _ := m.IdentifyScreen(nodes, page.Package); matched != nil {
						from = matched.ID
						if warn := appmap.CompareDrift(matched, page).Warn(from); warn != "" {
							fmt.Fprintln(os.Stderr, warn)
						}
					}
				}
			}
		}
	}
	steps := op.Macro
	if op.Type == "NAV" && op.TargetScreen != "" && from != "" {
		if from == op.TargetScreen {
			steps = nil
		} else if rel := m.RelativeMacro(from, op.TargetScreen); len(rel) > 0 {
			steps = rel
		}
	}
	res := macro.Run(ctx, driver, steps, macro.Options{
		SourceW: m.ScreenW, SourceH: m.ScreenH, Identify: m.Identify,
	})
	if !res.OK {
		return nil, fmt.Errorf("%s", res.Error)
	}
	return map[string]any{"ok": true, "op": op.ID, "from": from, "steps": len(res.Steps)}, nil
}

func printJSONMap(v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}
