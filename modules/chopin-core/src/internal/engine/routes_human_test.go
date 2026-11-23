package engine

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandoverGateRefusesEveryInjection(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	if s.refuseIfHandedOver(rec) {
		t.Fatal("nothing is handed over yet")
	}

	s.handover.begin("com.example.app", "com.example.app/.Main", 0)
	rec = httptest.NewRecorder()
	if !s.refuseIfHandedOver(rec) {
		t.Fatal("a handed-over app must refuse injection")
	}
	if rec.Code != 409 {
		t.Fatalf("want 409, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "handed_over") {
		t.Fatalf("body should carry the code: %s", rec.Body.String())
	}

	pkg, comp, display, ok := s.handover.end()
	if !ok || pkg != "com.example.app" || comp != "com.example.app/.Main" || display != 0 {
		t.Fatalf("end returned %q %q %d ok=%v", pkg, comp, display, ok)
	}
	if s.refuseIfHandedOver(httptest.NewRecorder()) {
		t.Fatal("the gate must open again after return")
	}
}

func TestInjectionPointWithoutAHumanProfile(t *testing.T) {
	s := &Server{}
	px, py, release, paced, interval, err := s.injectionPoint(context.Background(), 0, nil, 10, 20, "seed", "req", 0)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if px != 10 || py != 20 || paced != 0 || interval != 0 {
		t.Fatalf("want the original point and no pacing, got (%d,%d) paced=%d interval=%d", px, py, paced, interval)
	}
}
