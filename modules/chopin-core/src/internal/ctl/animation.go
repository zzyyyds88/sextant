package ctl

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"chopin-core/internal/audit"
	"chopin-core/internal/engine"
	"chopin-core/internal/sysprobe"
)

// Animation scales are the cheapest way to make every UI transition shorter for
// an agent: launchers, dialogs and app windows all honour them. Turning them off
// changes device state, so the original values are recorded first and restored by
// `ctl animation restore` (and by uninstall).
var animationKeys = [][2]string{
	{"global", "window_animation_scale"},
	{"global", "transition_animation_scale"},
	{"global", "animator_duration_scale"},
}

type animationBaseline struct {
	Values map[string]string `json:"values"`
}

func animationBaselinePath(paths engine.Paths) string {
	return filepath.Join(paths.Baseline, "animation.json")
}

func cmdAnimation(paths engine.Paths, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: ctl animation off|restore|status")
		return 2
	}
	probe := sysprobe.Probe{Run: sysprobe.ExecRunner{}}
	ctx := context.Background()
	baseline := animationBaselinePath(paths)
	switch args[0] {
	case "status":
		for _, k := range animationKeys {
			v, err := probe.SettingsGet(ctx, k[0], k[1])
			if err != nil {
				fmt.Fprintf(os.Stderr, "animation: read %s/%s: %v\n", k[0], k[1], err)
				return 1
			}
			fmt.Printf("%s/%s=%q\n", k[0], k[1], v)
		}
		if _, err := os.Stat(baseline); err == nil {
			fmt.Println("baseline: present")
		} else {
			fmt.Println("baseline: absent")
		}
		return 0
	case "off":
		values := map[string]string{}
		for _, k := range animationKeys {
			v, err := probe.SettingsGet(ctx, k[0], k[1])
			if err != nil {
				fmt.Fprintf(os.Stderr, "animation: read %s/%s: %v\n", k[0], k[1], err)
				return 1
			}
			values[k[0]+"/"+k[1]] = v
		}
		// Never overwrite an existing baseline with the values we are about to
		// replace: a second `off` must still be able to restore the originals.
		if _, err := os.Stat(baseline); os.IsNotExist(err) {
			if err := writeAnimationBaseline(baseline, values); err != nil {
				fmt.Fprintf(os.Stderr, "animation: record baseline: %v\n", err)
				return 1
			}
		}
		for _, k := range animationKeys {
			if err := probe.SettingsPut(ctx, k[0], k[1], "0"); err != nil {
				fmt.Fprintf(os.Stderr, "animation: set %s/%s: %v\n", k[0], k[1], err)
				return 1
			}
			// Read back: a settings put that is accepted but not reflected is the
			// failure mode this project treats as a defect, not a success.
			v, err := probe.SettingsGet(ctx, k[0], k[1])
			if err != nil {
				fmt.Fprintf(os.Stderr, "animation: read back %s/%s: %v\n", k[0], k[1], err)
				return 1
			}
			if v != "0" {
				fmt.Fprintf(os.Stderr, "animation: %s/%s is %q after the write, want 0\n", k[0], k[1], v)
				return 1
			}
		}
		audit.Open(paths.Log, 1<<20, 500).Info("animation: disabled (baseline recorded)")
		fmt.Println("disabled")
		return 0
	case "restore":
		data, err := os.ReadFile(baseline)
		if err != nil {
			fmt.Fprintf(os.Stderr, "animation: no baseline to restore: %v\n", err)
			return 1
		}
		var b animationBaseline
		if err := json.Unmarshal(data, &b); err != nil {
			fmt.Fprintf(os.Stderr, "animation: parse baseline: %v\n", err)
			return 1
		}
		for _, k := range animationKeys {
			key := k[0] + "/" + k[1]
			want, ok := b.Values[key]
			if !ok {
				continue
			}
			if want == "" {
				// The key had no explicit value before: delete rather than store "".
				if _, err := (probe.Run.Run(ctx, "settings", "delete", k[0], k[1])); err != nil {
					fmt.Fprintf(os.Stderr, "animation: delete %s/%s: %v\n", k[0], k[1], err)
					return 1
				}
				continue
			}
			if err := probe.SettingsPut(ctx, k[0], k[1], want); err != nil {
				fmt.Fprintf(os.Stderr, "animation: restore %s/%s: %v\n", k[0], k[1], err)
				return 1
			}
			got, err := probe.SettingsGet(ctx, k[0], k[1])
			if err != nil {
				fmt.Fprintf(os.Stderr, "animation: read back %s/%s: %v\n", k[0], k[1], err)
				return 1
			}
			if got != want {
				fmt.Fprintf(os.Stderr, "animation: %s/%s is %q after restore, want %q\n", k[0], k[1], got, want)
				return 1
			}
		}
		audit.Open(paths.Log, 1<<20, 500).Info("animation: restored from baseline")
		fmt.Println("restored")
		return 0
	}
	fmt.Fprintf(os.Stderr, "unknown animation subcommand %q\n", args[0])
	return 2
}

func writeAnimationBaseline(path string, values map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(animationBaseline{Values: values}, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
