package main

import (
	"bytes"
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testTemplates(t *testing.T) *template.Template {
	t.Helper()
	return template.Must(template.New("all").Parse(`
{{define "page.html"}}page{{end}}
{{define "fleet.html"}}fleet{{end}}
{{define "instance.html"}}instance{{end}}
{{define "log.html"}}{{.State}}:{{.Error}}{{end}}
`))
}

func TestHandlerRejectsUnknownInstance(t *testing.T) {
	app := NewApp(testInventory(), &testCollector{}, testTemplates(t))
	recorder := httptest.NewRecorder()
	app.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/?instance=missing", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want %d", recorder.Code, http.StatusNotFound)
	}
}

func TestHandlerRejectsUnknownAndMalformedRun(t *testing.T) {
	collector := &testCollector{}
	collector.logs = func(_ context.Context, _ Instance, _ string, _ bool) (LogResult, error) {
		return LogResult{}, &CLIError{Command: "run logs", Exit: 4, Message: "not found"}
	}
	app := NewApp(testInventory(), collector, testTemplates(t))
	for _, test := range []struct {
		name string
		url  string
		code int
	}{
		{name: "unknown", url: "/logs?instance=one&run=missing-run", code: http.StatusOK},
		{name: "malformed", url: "/logs?instance=one&run=bad%2Frun", code: http.StatusBadRequest},
		{name: "missing", url: "/logs?instance=one", code: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			app.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.url, nil))
			if recorder.Code != test.code {
				t.Fatalf("status=%d, want %d; body=%s", recorder.Code, test.code, recorder.Body.String())
			}
			if test.name == "unknown" && !strings.Contains(recorder.Body.String(), "unknown") {
				t.Fatalf("body=%q, want unknown log state", recorder.Body.String())
			}
		})
	}
}

func TestHandlerMapsEvictedLogToFragmentState(t *testing.T) {
	collector := &testCollector{}
	collector.logs = func(_ context.Context, _ Instance, runID string, _ bool) (LogResult, error) {
		return LogResult{RunID: runID, Complete: true, Retained: false}, nil
	}
	app := NewApp(testInventory(), collector, testTemplates(t))
	recorder := httptest.NewRecorder()
	app.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/logs?instance=one&run=old-run", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "evicted") {
		t.Fatalf("status=%d body=%q, want evicted fragment", recorder.Code, recorder.Body.String())
	}
}

func TestReadOnlyRoutesRejectMutationMethods(t *testing.T) {
	app := NewApp(testInventory(), &testCollector{}, testTemplates(t))
	for _, path := range []string{"/", "/fragments/fleet", "/fragments/instance", "/logs?instance=one&run=run-1", "/healthz", "/static/canopy.css"} {
		recorder := httptest.NewRecorder()
		app.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, nil))
		if recorder.Code != http.StatusMethodNotAllowed {
			t.Fatalf("POST %s status=%d, want %d", path, recorder.Code, http.StatusMethodNotAllowed)
		}
	}
}

func TestHandlerServesEmbeddedStaticFiles(t *testing.T) {
	app := NewApp(testInventory(), &testCollector{}, testTemplates(t))
	for _, path := range []string{"/static/canopy.css", "/static/canopy.js", "/static/htmx.min.js"} {
		recorder := httptest.NewRecorder()
		app.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK || recorder.Body.Len() == 0 {
			t.Fatalf("GET %s status=%d bytes=%d, want non-empty 200", path, recorder.Code, recorder.Body.Len())
		}
	}
}

func TestInstanceSelectionTriggersFleetRefresh(t *testing.T) {
	inventory := testInventory()
	inventory.Instances = append(inventory.Instances, Instance{ID: "two", Label: "Two", Root: "/tmp/two", Forest: "forest"})
	app := NewApp(inventory, &testCollector{}, testTemplates(t))
	recorder := httptest.NewRecorder()
	app.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/fragments/instance?instance=two", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Header().Get("HX-Trigger-After-Swap"); got != "canopy-selection" {
		t.Fatalf("HX-Trigger-After-Swap=%q, want canopy-selection", got)
	}
}

func TestHealthIsProcessLivenessOnly(t *testing.T) {
	collector := &testCollector{collect: func(_ context.Context, _ Instance) (Snapshot, error) {
		return Snapshot{}, errors.New("offline")
	}}
	app := NewApp(testInventory(), collector, testTemplates(t))
	recorder := httptest.NewRecorder()
	app.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "ok\n" {
		t.Fatalf("health status=%d body=%q, want 200 ok", recorder.Code, recorder.Body.String())
	}
}

func TestLogLinksCarryRunIdentityForDrawer(t *testing.T) {
	templates, err := loadTemplates()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}

	selected := InstanceView{
		ID:         "one",
		Freshness:  "fresh",
		LiveRuns:   []LiveRunViewModel{{RunID: "live-1", Agent: "builder"}},
		RecentRuns: []RunViewModel{{RunID: "recent-1", ExitLabel: "exit 0", Status: "passed"}},
	}
	var instanceHTML bytes.Buffer
	if err := templates.ExecuteTemplate(&instanceHTML, "instance.html", selected); err != nil {
		t.Fatalf("render instance template: %v", err)
	}
	// Every drawer log link must identify its run so the viewer can abort the
	// previous in-flight request and discard stale responses that arrive after
	// a newer run was opened into the shared #log-drawer-body target.
	for _, want := range []string{
		`data-log-open data-log-run="live-1"`,
		`data-log-open data-log-run="recent-1"`,
		`hx-target="#log-drawer-body"`,
	} {
		if !strings.Contains(instanceHTML.String(), want) {
			t.Fatalf("instance template missing %q", want)
		}
	}
}

func TestStaticCanopyJSGuardsStaleDrawerSwaps(t *testing.T) {
	script, err := staticFiles.ReadFile("static/canopy.js")
	if err != nil {
		t.Fatalf("read embedded canopy.js: %v", err)
	}
	// The drawer must serialize log requests: opening a different run aborts
	// the previous in-flight request, and a response for a run that is no
	// longer open is discarded before it can swap into #log-drawer-body.
	for _, want := range []string{"htmx:beforeSwap", "htmx:abort", "data-log-run"} {
		if !bytes.Contains(script, []byte(want)) {
			t.Fatalf("static/canopy.js missing %q", want)
		}
	}
}

func TestLogViewerTemplatesRenderDrawerAndCopyControl(t *testing.T) {
	templates, err := loadTemplates()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}

	var page bytes.Buffer
	if err := templates.ExecuteTemplate(&page, "page.html", PageView{}); err != nil {
		t.Fatalf("render page template: %v", err)
	}
	for _, want := range []string{`id="log-drawer"`, `id="log-drawer-body"`, `data-log-close`, `/static/canopy.js`} {
		if !strings.Contains(page.String(), want) {
			t.Fatalf("page template missing %q", want)
		}
	}

	selected := InstanceView{
		ID:         "one",
		Freshness:  "fresh",
		LiveRuns:   []LiveRunViewModel{{RunID: "live-1", Agent: "builder"}},
		RecentRuns: []RunViewModel{{RunID: "recent-1", ExitLabel: "exit 0", Status: "passed"}},
	}
	var instanceHTML bytes.Buffer
	if err := templates.ExecuteTemplate(&instanceHTML, "instance.html", selected); err != nil {
		t.Fatalf("render instance template: %v", err)
	}
	for _, want := range []string{`data-log-open`, `hx-target="#log-drawer-body"`} {
		if !strings.Contains(instanceHTML.String(), want) {
			t.Fatalf("instance template missing %q", want)
		}
	}

	exit := 0
	var log bytes.Buffer
	if err := templates.ExecuteTemplate(&log, "log.html", LogView{RunID: "run-1", State: "retained", Text: "line one", Exit: &exit}); err != nil {
		t.Fatalf("render log template: %v", err)
	}
	for _, want := range []string{`data-log-copy`, "run-1", "line one"} {
		if !strings.Contains(log.String(), want) {
			t.Fatalf("log template missing %q", want)
		}
	}
}
