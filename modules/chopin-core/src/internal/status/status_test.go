package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPublishIsAtomicAndParseable(t *testing.T) {
	dir := t.TempDir()
	primary := filepath.Join(dir, "state", "status.json")
	copyPath := filepath.Join(dir, "app", "status.json")
	p := NewPublisher(primary, 0, copyPath)
	if _, err := p.Update(func(s *Status) {
		s.Daemon = Daemon{PID: 42, Version: "test", State: "running"}
		s.App.Package = "com.x"
		s.App.A11y = A11y{State: OK, Detail: "bound", Component: "com.x/.A"}
	}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{primary, copyPath} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		st, err := Parse(data)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if st.Daemon.PID != 42 || st.App.A11y.State != OK {
			t.Fatalf("%s: unexpected status %+v", path, st)
		}
	}
}

func TestSchemaIsStamped(t *testing.T) {
	dir := t.TempDir()
	p := NewPublisher(filepath.Join(dir, "s.json"), 0)
	data := mustUpdate(t, p)
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["schema"].(float64) != SchemaVersion {
		t.Fatalf("schema = %v", raw["schema"])
	}
	if _, ok := raw["errors"]; !ok {
		t.Fatal("errors must always be present (not null)")
	}
}

func TestParseRejectsUnknownSchema(t *testing.T) {
	if _, err := Parse([]byte(`{"schema":99}`)); err == nil {
		t.Fatal("schema 99 accepted")
	}
}

func TestErrorsAreBounded(t *testing.T) {
	dir := t.TempDir()
	p := NewPublisher(filepath.Join(dir, "s.json"), 0)
	for i := 0; i < 40; i++ {
		p.AddError("code", "message")
	}
	if got := len(p.Current().Errors); got > 20 {
		t.Fatalf("errors not bounded: %d", got)
	}
}

func mustUpdate(t *testing.T, p *Publisher) []byte {
	t.Helper()
	path := p.primary
	if _, err := p.Update(func(s *Status) { s.Daemon.State = "running" }); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
