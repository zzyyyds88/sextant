// Package risk records which packages answered automation with a verification
// wall, so a recipe does not walk back into the same wall on every run.
//
// The record is small, per package, and decays: an app that challenged a month
// ago is not the same as one that challenged an hour ago. The file is written
// atomically (tmp + rename) because the daemon is killed by OEM battery
// managers, and a truncated file here would silently disable the warning.
package risk

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Entry is one package's record.
type Entry struct {
	Count int   `json:"count"`
	Last  int64 `json:"last"`
	// Until is when this record stops counting. A zero value means "never".
	Until int64 `json:"until"`
	// Cooldown is when automatic work on this package may resume. It is shorter
	// than Until: the record is evidence, the cooldown is the rest period.
	Cooldown int64 `json:"cooldown,omitempty"`
}

// Load reads the store. A missing or broken file is an empty store: the warning
// is a safety net, not a reason to fail the daemon.
func Load(path string) map[string]Entry {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]Entry{}
	}
	var raw map[string]Entry
	if err := json.Unmarshal(data, &raw); err != nil {
		return map[string]Entry{}
	}
	if raw == nil {
		return map[string]Entry{}
	}
	return raw
}

// Record notes one challenge for a package and returns the new entry.
func Record(path, pkg string, now time.Time, days, cooldownSec int) (Entry, error) {
	if pkg == "" {
		return Entry{}, fmt.Errorf("package is empty")
	}
	store := Load(path)
	entry := store[pkg]
	entry.Count++
	entry.Last = now.Unix()
	if days <= 0 {
		days = 7
	}
	entry.Until = now.Add(time.Duration(days) * 24 * time.Hour).Unix()
	if cooldownSec > 0 {
		entry.Cooldown = now.Add(time.Duration(cooldownSec) * time.Second).Unix()
	}
	store[pkg] = entry
	if err := save(path, store); err != nil {
		return entry, err
	}
	return entry, nil
}

// Current returns the live entry for a package, treating an expired record as
// absent. The returned bool is "this package has a live record".
func Current(path, pkg string, now time.Time) (Entry, bool) {
	entry, ok := Load(path)[pkg]
	if !ok {
		return Entry{}, false
	}
	if entry.Until > 0 && now.Unix() > entry.Until {
		return Entry{}, false
	}
	return entry, true
}

// Cooling reports whether automatic work on a package must still wait.
func Cooling(path, pkg string, now time.Time) (bool, int64) {
	entry, ok := Current(path, pkg, now)
	if !ok || entry.Cooldown == 0 {
		return false, 0
	}
	if now.Unix() >= entry.Cooldown {
		return false, 0
	}
	return true, entry.Cooldown
}

// Describe renders a record for a human. It says when, not what: the reason the
// wall appeared is the app's business.
func Describe(entry Entry, now time.Time) string {
	if entry.Count == 0 || entry.Last == 0 {
		return ""
	}
	at := time.Unix(entry.Last, 0)
	ago := now.Sub(at)
	var when string
	switch {
	case ago < time.Hour:
		when = fmt.Sprintf("%d 分钟前", int(ago.Minutes()))
	case ago < 24*time.Hour:
		when = fmt.Sprintf("%d 小时前", int(ago.Hours()))
	default:
		when = fmt.Sprintf("%d 天前", int(ago.Hours()/24))
	}
	return fmt.Sprintf("这个 App %s弹过验证页（第 %d 次）", when, entry.Count)
}

func save(path string, store map[string]Entry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
