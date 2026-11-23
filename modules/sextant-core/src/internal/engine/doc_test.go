package engine

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The document must describe the address the caller actually reached, not the
// listener's wildcard bind: "https://0.0.0.0:8443" is not connectable.
func TestOpenAPIDocumentUsesCallerHost(t *testing.T) {
	doc := openAPIDocument("sextant-device:8443")
	servers, ok := doc["servers"].([]map[string]any)
	if !ok || len(servers) != 1 {
		t.Fatalf("servers = %#v, want exactly one entry", doc["servers"])
	}
	if got, want := servers[0]["url"], "https://sextant-device:8443"; got != want {
		t.Fatalf("servers[0].url = %v, want %v", got, want)
	}
}

// /v1/health, /v1/pair and /doc answer without a bearer token. The document's
// global security requirement is bearer, so each must opt out explicitly.
func TestOpenAPIDocumentMarksPublicOperations(t *testing.T) {
	doc := openAPIDocument("host:8443")
	paths, ok := doc["paths"].(map[string]any)
	if !ok {
		t.Fatalf("paths = %#v", doc["paths"])
	}
	for _, key := range []string{"get /doc", "get /v1/health", "post /v1/pair"} {
		verb, path, found := strings.Cut(key, " ")
		if !found {
			t.Fatalf("bad test key %q", key)
		}
		op, ok := paths[path].(map[string]any)[verb].(map[string]any)
		if !ok {
			t.Fatalf("%s is missing from the document", key)
		}
		security, present := op["security"]
		if !present {
			t.Fatalf("%s does not override the global bearer requirement", key)
		}
		if list, ok := security.([]any); !ok || len(list) != 0 {
			t.Fatalf("%s security = %#v, want an empty array", key, security)
		}
	}
}

// A protected operation must keep inheriting the global bearer requirement:
// emitting an empty array here would document it as public.
func TestOpenAPIDocumentLeavesProtectedOperationsInheriting(t *testing.T) {
	doc := openAPIDocument("host:8443")
	op := doc["paths"].(map[string]any)["/v1/status"].(map[string]any)["get"].(map[string]any)
	if security, present := op["security"]; present {
		t.Fatalf("/v1/status carries its own security requirement: %#v", security)
	}
}

// A key in publicOperations that names a path or method apiPaths does not carry
// would silently document nothing, so the two must stay in step.
func TestPublicOperationsExistInAPIPaths(t *testing.T) {
	for key := range publicOperations {
		verb, path, found := strings.Cut(key, " ")
		if !found {
			t.Fatalf("bad publicOperations key %q", key)
		}
		methods, ok := apiPaths[path]
		if !ok {
			t.Fatalf("%q names a path apiPaths does not document", key)
		}
		if _, ok := methods[verb]; !ok {
			t.Fatalf("%q names a method apiPaths does not document", key)
		}
	}
}

// The agent's first request is /doc; the guidance there has to be complete
// enough to start from a bare device, and the two phases must both be present.
func TestGuidanceCoversInstallAndUse(t *testing.T) {
	doc := openAPIDocument("host:8443")
	g, ok := doc["x-sextant-guidance"].(map[string]any)
	if !ok {
		t.Fatalf("x-sextant-guidance missing: %#v", doc["x-sextant-guidance"])
	}
	if g["skill"] != "/doc/skill" || g["skill_zip"] != "/doc/skill.zip" {
		t.Fatalf("skill links are wrong: %#v", g)
	}
	steps, ok := g["steps"].([]map[string]any)
	if !ok || len(steps) < 6 {
		t.Fatalf("want at least 6 steps, got %#v", g["steps"])
	}
	phases := map[string]bool{}
	ids := map[string]bool{}
	for _, step := range steps {
		for _, key := range []string{"id", "phase", "title", "command", "expect"} {
			value, ok := step[key].(string)
			if !ok || value == "" {
				t.Fatalf("step %#v has an empty %s", step, key)
			}
		}
		phase := step["phase"].(string)
		if phase != "install" && phase != "use" {
			t.Fatalf("unknown phase %q", phase)
		}
		phases[phase] = true
		id := step["id"].(string)
		if ids[id] {
			t.Fatalf("duplicate step id %q", id)
		}
		ids[id] = true
		if id == "init" && !strings.Contains(step["command"].(string), "printf") {
			t.Fatalf("init must feed the credential through stdin, got %q", step["command"])
		}
	}
	if !phases["install"] || !phases["use"] {
		t.Fatalf("both phases must be present: %#v", phases)
	}
}

// /doc is what a human opens in a browser and what an agent's first fetch
// lands on. It must be a complete page, not the raw JSON document, and it must
// describe the address the caller actually used.
func TestDocServesHTMLForBrowsers(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest("GET", "https://sextant-device:8443/doc", nil)
	req.Host = "sextant-device:8443"
	rec := httptest.NewRecorder()
	s.handleDoc(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"<!doctype html>",
		"https://sextant-device:8443",
		"/doc/openapi.json",
		"/v1/ui/act",
		"/v1/status",
		"Authorization: Bearer",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("page is missing %q", want)
		}
	}
	if strings.Contains(body, `"openapi"`) {
		t.Fatal("page fell back to the raw OpenAPI JSON")
	}
}

// The machine contract stays reachable at the same address for clients that ask
// for JSON explicitly; the two representations must not drift.
func TestDocServesJSONWhenAsked(t *testing.T) {
	s := &Server{}
	for _, tc := range []struct {
		name   string
		target string
		accept string
	}{
		{"accept header", "/doc", "application/json"},
		{"format query", "/doc?format=json", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.target, nil)
			req.Host = "host:8443"
			if tc.accept != "" {
				req.Header.Set("Accept", tc.accept)
			}
			rec := httptest.NewRecorder()
			s.handleDoc(rec, req)
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Fatalf("Content-Type = %q, want application/json", ct)
			}
			if !strings.Contains(rec.Body.String(), `"openapi"`) {
				t.Fatal("JSON request did not receive the OpenAPI document")
			}
		})
	}
}

// /doc/openapi.json and /openapi.json are the stable machine entry points; they
// must serve the same document as the negotiated /doc.
func TestDocJSONAliasesServeTheSameDocument(t *testing.T) {
	s := &Server{}
	want := openAPIDocument("host:8443")
	for _, path := range []string{"/doc/openapi.json", "/openapi.json"} {
		req := httptest.NewRequest("GET", path, nil)
		req.Host = "host:8443"
		rec := httptest.NewRecorder()
		s.handleDocJSON(rec, req)
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Fatalf("%s Content-Type = %q", path, ct)
		}
		var got map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("%s is not JSON: %v", path, err)
		}
		if got["openapi"] != want["openapi"] {
			t.Fatalf("%s openapi = %v, want %v", path, got["openapi"], want["openapi"])
		}
	}
}

// Every documented endpoint must land in exactly one group of the page; an
// ungrouped path would silently disappear from the only human-facing index.
func TestDocGroupsCoverEveryDocumentedPath(t *testing.T) {
	seen := map[string]bool{}
	for _, g := range buildDocGroups() {
		for _, e := range g.Endpoints {
			key := e.Method + " " + e.Path
			if seen[key] {
				t.Fatalf("%s is listed twice", key)
			}
			seen[key] = true
			if e.Auth == "" {
				t.Fatalf("%s has no auth label", key)
			}
		}
	}
	for path, methods := range apiPaths {
		for method := range methods {
			key := strings.ToUpper(method) + " " + path
			if !seen[key] {
				t.Fatalf("%s is documented but missing from the page", key)
			}
		}
	}
}
