package main

import "fmt"

// cmdDiff compares two observed trees. The frame ids come from
// `observe --tree` responses and expire after 60 seconds (D33).
func cmdDiff(a *args) error {
	before := a.optStr("before", "")
	if before == "" {
		return fmt.Errorf("usage: sextant diff --before FRAME_ID [--after FRAME_ID]")
	}
	c, err := paired()
	if err != nil {
		return err
	}
	body := map[string]any{"before_frame_id": before}
	if after := a.optStr("after", ""); after != "" {
		body["after_frame_id"] = after
	}
	data, err := c.Post("/v1/ui/diff", body)
	if err != nil {
		return err
	}
	return printJSON(data)
}
