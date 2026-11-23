package risk

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecordAccumulatesAndDescribes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "risk.json")
	now := time.Unix(1_700_000_000, 0)
	e1, err := Record(path, "com.example.app", now, 7, 600)
	if err != nil {
		t.Fatal(err)
	}
	if e1.Count != 1 {
		t.Fatalf("want count 1, got %d", e1.Count)
	}
	e2, err := Record(path, "com.example.app", now.Add(time.Minute), 7, 600)
	if err != nil {
		t.Fatal(err)
	}
	if e2.Count != 2 {
		t.Fatalf("want count 2, got %d", e2.Count)
	}
	if got, ok := Current(path, "com.example.app", now.Add(2*time.Minute)); !ok || got.Count != 2 {
		t.Fatalf("current should see count 2, got %d ok=%v", got.Count, ok)
	}
	if desc := Describe(e2, now.Add(10*time.Minute)); desc == "" {
		t.Fatal("describe should not be empty")
	}
}

func TestRecordsDecay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "risk.json")
	now := time.Unix(1_700_000_000, 0)
	if _, err := Record(path, "com.example.app", now, 1, 600); err != nil {
		t.Fatal(err)
	}
	if _, ok := Current(path, "com.example.app", now.Add(48*time.Hour)); ok {
		t.Fatal("a one-day record must be gone after two days")
	}
	if _, ok := Current(path, "com.example.app", now.Add(time.Hour)); !ok {
		t.Fatal("a one-day record must still hold after an hour")
	}
}

func TestCooldownIsShorterThanTheRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "risk.json")
	now := time.Unix(1_700_000_000, 0)
	if _, err := Record(path, "com.example.app", now, 7, 600); err != nil {
		t.Fatal(err)
	}
	if cooling, _ := Cooling(path, "com.example.app", now.Add(time.Minute)); !cooling {
		t.Fatal("a ten-minute cooldown must still hold after one minute")
	}
	if cooling, _ := Cooling(path, "com.example.app", now.Add(11*time.Minute)); cooling {
		t.Fatal("the cooldown must be over after eleven minutes")
	}
	if _, ok := Current(path, "com.example.app", now.Add(11*time.Minute)); !ok {
		t.Fatal("the record itself must outlive the cooldown")
	}
}

func TestFileIsOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "risk.json")
	if _, err := Record(path, "com.example.app", time.Now(), 7, 600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("want 0600, got %o", perm)
	}
}

func TestBrokenFileIsAnEmptyStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "risk.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Load(path); len(got) != 0 {
		t.Fatalf("want an empty store, got %v", got)
	}
}
