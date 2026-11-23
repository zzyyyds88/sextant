package trigger

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func newEngine(t *testing.T, src Sources) *Engine {
	t.Helper()
	e, err := Open(filepath.Join(t.TempDir(), "triggers.json"), src, nil)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestCreateRejectsUnknownType(t *testing.T) {
	e := newEngine(t, Sources{})
	if _, err := e.Create("x", "magic", Spec{}, true); err == nil {
		t.Fatal("unknown type was accepted")
	}
}

func TestLogcatNeedsAPattern(t *testing.T) {
	e := newEngine(t, Sources{})
	if _, err := e.Create("x", TypeLogcat, Spec{}, true); err == nil {
		t.Fatal("logcat without a pattern was accepted")
	}
	if _, err := e.Create("x", TypeLogcat, Spec{Pattern: "("}, true); err == nil {
		t.Fatal("invalid regex was accepted")
	}
}

func TestTimerUsesAMinimumInterval(t *testing.T) {
	e := newEngine(t, Sources{})
	tr, err := e.Create("tick", TypeTimer, Spec{IntervalSeconds: 1}, true)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Spec.IntervalSeconds != 60 {
		t.Fatalf("interval = %d, want the 60s floor", tr.Spec.IntervalSeconds)
	}
}

func TestTimerFiresAndRecordsARun(t *testing.T) {
	e := newEngine(t, Sources{})
	if _, err := e.Create("tick", TypeTimer, Spec{IntervalSeconds: 60}, true); err != nil {
		t.Fatal(err)
	}
	e.runTimers()
	runs := e.Runs("", 10)
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	if runs[0].Data["kind"] != "timer" {
		t.Fatalf("run data = %v", runs[0].Data)
	}
	// A second immediate pass must not fire again: the trigger remembers LastRun.
	e.runTimers()
	if got := len(e.Runs("", 10)); got != 1 {
		t.Fatalf("runs after second pass = %d, want 1", got)
	}
}

func TestDisabledTriggerDoesNotFire(t *testing.T) {
	e := newEngine(t, Sources{})
	if _, err := e.Create("tick", TypeTimer, Spec{IntervalSeconds: 5}, false); err != nil {
		t.Fatal(err)
	}
	e.runTimers()
	if got := len(e.Runs("", 10)); got != 0 {
		t.Fatalf("disabled trigger fired %d time(s)", got)
	}
}

func TestSMSOnlyFiresForNewMessages(t *testing.T) {
	var after []map[string]any
	src := Sources{
		SMSMaxID: func(context.Context) (int64, error) { return 10, nil },
		SMSAfter: func(context.Context, int64) ([]map[string]any, error) { return after, nil },
	}
	e := newEngine(t, src)
	if _, err := e.Create("code", TypeSMS, Spec{TextContains: "验证码"}, true); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	e.prime(ctx)
	e.pollSMS(ctx)
	if got := len(e.Runs("", 10)); got != 0 {
		t.Fatalf("priming produced %d run(s)", got)
	}
	after = []map[string]any{{"id": float64(11), "address": "106", "body": "您的验证码是 123456"}}
	e.pollSMS(ctx)
	runs := e.Runs("", 10)
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	// The same message must not fire twice on the next poll.
	e.pollSMS(ctx)
	if got := len(e.Runs("", 10)); got != 1 {
		t.Fatalf("the same SMS fired again: %d runs", got)
	}
}

func TestNotificationMatching(t *testing.T) {
	items := []map[string]any{{"package": "com.tencent.mm", "key": "1", "title": "张三", "text": "在吗"}}
	src := Sources{Notifications: func(context.Context) ([]map[string]any, error) { return items, nil }}
	e := newEngine(t, src)
	if _, err := e.Create("wx", TypeNotification, Spec{Package: "com.tencent.mm", TextContains: "在吗"}, true); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	e.prime(ctx)
	e.pollNotifications(ctx)
	if got := len(e.Runs("", 10)); got != 0 {
		t.Fatalf("an existing notification fired: %d", got)
	}
	items = append(items, map[string]any{"package": "com.tencent.mm", "key": "2", "title": "李四", "text": "在吗？"})
	e.pollNotifications(ctx)
	if got := len(e.Runs("", 10)); got != 1 {
		t.Fatalf("new notification produced %d run(s)", got)
	}
}

func TestRunsSurviveReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "triggers.json")
	e, err := Open(path, Sources{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := e.Create("tick", TypeTimer, Spec{IntervalSeconds: 5}, true)
	if err != nil {
		t.Fatal(err)
	}
	e.runTimers()

	again, err := Open(path, Sources{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(again.Runs("", 10)); got != 1 {
		t.Fatalf("runs after reopen = %d, want 1", got)
	}
	if _, ok := again.Get(tr.ID); !ok {
		t.Fatal("trigger did not survive reopen")
	}
}

func TestSubscribeReceivesRun(t *testing.T) {
	e := newEngine(t, Sources{})
	if _, err := e.Create("tick", TypeTimer, Spec{IntervalSeconds: 5}, true); err != nil {
		t.Fatal(err)
	}
	ch, cancel := e.Subscribe()
	defer cancel()
	e.runTimers()
	select {
	case run := <-ch:
		if run.TriggerID == "" {
			t.Fatalf("run = %+v", run)
		}
	case <-time.After(time.Second):
		t.Fatal("no run was delivered")
	}
}

func TestUpdateKeepsIdentity(t *testing.T) {
	e := newEngine(t, Sources{})
	tr, err := e.Create("tick", TypeTimer, Spec{IntervalSeconds: 5}, true)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := e.Update(tr.ID, func(x *Trigger) error {
		x.Name = "renamed"
		x.Enabled = false
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != tr.ID || updated.CreatedAt != tr.CreatedAt {
		t.Fatal("update changed the identity")
	}
	if updated.Name != "renamed" || updated.Enabled {
		t.Fatalf("update did not apply: %+v", updated)
	}
}
