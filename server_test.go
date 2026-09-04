package main

import (
	"context"
	"errors"
	"fmt"
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

func TestInitialCollectionFailureRendersUnknown(t *testing.T) {
	templates, err := loadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	offline := errors.New("first collection offline")
	collector := &testCollector{collect: func(context.Context, Instance) (Snapshot, error) {
		return Snapshot{}, offline
	}}
	inventory := testInventory()
	app := NewApp(inventory, collector, templates)
	app.refreshOnce(context.Background(), inventory.Instances[0])
	state, _ := app.state("one")
	if state.Snapshot != nil || !state.LastSuccess.IsZero() || state.LastAttempt.IsZero() || !errors.Is(state.Err, offline) {
		t.Fatalf("first failure state=%+v", state)
	}
	view := app.pageView("one", state.LastAttempt).Selected
	if view.Reachable || view.Freshness != string(Unknown) {
		t.Fatalf("first failure reachable=%t freshness=%s", view.Reachable, view.Freshness)
	}
	if want := "attempted " + formatTime(state.LastAttempt); view.LastObserved != want {
		t.Fatalf("first failure last observed=%q, want %q", view.LastObserved, want)
	}
	if len(view.Errors) != 1 || view.Errors[0] != offline.Error() {
		t.Fatalf("first failure errors=%q, want %q", view.Errors, offline.Error())
	}
	recorder := httptest.NewRecorder()
	app.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/fragments/instance?instance=one", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", recorder.Code)
	}
	body := recorder.Body.String()
	for _, want := range []string{`class="status-badge unknown"`, "attempted " + formatTime(state.LastAttempt), offline.Error(), `id="waiting-title"`} {
		if !strings.Contains(body, want) {
			t.Errorf("fragment lacks %q", want)
		}
	}
	for _, stale := range []string{`>fresh<`, `>stale<`} {
		if strings.Contains(body, stale) {
			t.Errorf("fragment shows %q classification, want unknown only", stale)
		}
	}
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
	for _, path := range []string{"/static/canopy.css", "/static/htmx.min.js"} {
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

func TestAuditDivergenceStaleOverride(t *testing.T) {
	templates, err := loadTemplates()
	if err != nil {
		t.Fatalf("loadTemplates: %v", err)
	}
	tests := []struct {
		name      string
		audit     AuditData
		wantClass string
		wantText  string
	}{
		{
			name:      "divergent pass is stale",
			audit:     AuditData{LastResult: "pass", LastMaster: "sha-new", AuditedMaster: "sha-old"},
			wantClass: "stale",
			wantText:  "pass",
		},
		{
			name:      "divergent trusted is stale",
			audit:     AuditData{LastResult: "trusted", LastMaster: "sha-new", AuditedMaster: "sha-old"},
			wantClass: "stale",
			wantText:  "trusted",
		},
		{
			name:      "equal revisions retain result class",
			audit:     AuditData{LastResult: "pass", LastMaster: "sha-same", AuditedMaster: "sha-same"},
			wantClass: "ok",
			wantText:  "pass",
		},
		{
			name:      "missing audited revision retains result class",
			audit:     AuditData{LastResult: "pass", LastMaster: "sha-new", AuditedMaster: ""},
			wantClass: "ok",
			wantText:  "pass",
		},
		{
			name:      "missing current revision retains result class",
			audit:     AuditData{LastResult: "pass", LastMaster: "", AuditedMaster: "sha-old"},
			wantClass: "ok",
			wantText:  "pass",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inventory := testInventory()
			instance := inventory.Instances[0]
			snapshot := Snapshot{
				Instance: instance,
				Version:  VersionData{BuildSHA: "abc"},
				Config:   ConfigData{Repo: "misty-step/canopy", Primary: "master"},
				Status:   StatusData{Audit: test.audit},
			}
			collector := &testCollector{}
			collector.collect = func(context.Context, Instance) (Snapshot, error) { return snapshot, nil }
			app := NewApp(inventory, collector, templates)
			app.refreshOnce(context.Background(), instance)
			recorder := httptest.NewRecorder()
			app.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/fragments/instance?instance=one", nil))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
			}
			wantBadge := fmt.Sprintf(`<span class="signal %s">%s</span>`, test.wantClass, test.wantText)
			if !strings.Contains(recorder.Body.String(), wantBadge) {
				t.Fatalf("body=%q, want audit badge %q", recorder.Body.String(), wantBadge)
			}
		})
	}
}
