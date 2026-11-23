package main

// D31 journey: build a small map from the live device, identify the current
// screen with it, then replay one of its own navigation operations and confirm
// where it landed through a second channel (a fresh tree, not the replay itself).

import (
	"context"
	"fmt"
	"time"

	"sextant/internal/appmap"
	"sextant/internal/client"
	"sextant/internal/crawl"
	"sextant/internal/macro"
	"sextant/internal/uiapi"
)

func runJourneyMap(c *client.Client, step func(string, string, func() (stepOutcome, error))) {
	step("map", "build, identify and replay a navigation", func() (stepOutcome, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		driver := uiapi.New(c)

		m, err := crawl.Build(ctx, driver, crawl.Options{
			Package:     "com.android.settings",
			AppName:     "Settings",
			MaxScreens:  3,
			MaxDepth:    1,
			MaxMinutes:  2,
			ScrollPages: 0,
		})
		if err != nil {
			return stepOutcome{}, err
		}
		if len(m.Screens) < 2 {
			return stepOutcome{}, fmt.Errorf("the crawl produced %d screen(s); a map needs at least 2", len(m.Screens))
		}

		page, err := driver.Page(ctx)
		if err != nil {
			return stepOutcome{}, err
		}
		nodes, err := driver.Tree(ctx)
		if err != nil {
			return stepOutcome{}, err
		}
		from, confidence := m.Identify(nodes, page.Package)
		if from == "" {
			return stepOutcome{}, fmt.Errorf("the map did not identify the current screen (%s, ≤%.2f)", page.Package, confidence)
		}

		var nav appmap.Operation
		for _, op := range m.BuildOperations() {
			if op.Type == "NAV" && op.TargetScreen != "" {
				nav = op
				break
			}
		}
		if nav.ID == "" {
			return stepOutcome{}, fmt.Errorf("the map has no NAV operation to replay")
		}

		res := macro.Run(ctx, driver, nav.Macro, macro.Options{
			SourceW: m.ScreenW, SourceH: m.ScreenH,
			CurrentW: m.ScreenW, CurrentH: m.ScreenH,
			Identify: m.Identify,
		})
		if !res.OK {
			return stepOutcome{}, fmt.Errorf("replay %q: %s", nav.ID, res.Error)
		}
		_, _ = driver.WaitStable(ctx, 3000, 150)

		// The second channel: a fresh page + tree, identified independently of the
		// replay's own report.
		page2, err := driver.Page(ctx)
		if err != nil {
			return stepOutcome{}, err
		}
		nodes2, err := driver.Tree(ctx)
		if err != nil {
			return stepOutcome{}, err
		}
		landed, landedConfidence := m.Identify(nodes2, page2.Package)
		if landed != nav.TargetScreen {
			return stepOutcome{}, fmt.Errorf("landed on %q (%.2f), expected %q", landed, landedConfidence, nav.TargetScreen)
		}
		return stepOutcome{
			observed: fmt.Sprintf("%d screens; %s navigated %s → %s (%.2f)",
				len(m.Screens), nav.ID, from, landed, landedConfidence),
			logMust: []string{"apps: launch"},
			logWant: []string{"ui: act", "input: tap"},
		}, nil
	})
}
