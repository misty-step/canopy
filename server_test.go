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

func TestHandlerInstanceParamValidationAcrossRoutes(t *testing.T) {
	for _, test := range []struct {
		name     string
		url      string
		code     int
		wantBody string
	}{
		{name: "page missing optional", url: "/", code: http.StatusOK},
		{name: "fleet missing optional", url: "/fragments/fleet", code: http.StatusOK},
		{name: "instance missing optional", url: "/fragments/instance", code: http.StatusOK},
		{name: "logs missing required", url: "/logs?run=run-1", code: http.StatusBadRequest, wantBody: "instance is required"},

		{name: "page present empty", url: "/?instance=", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "fleet present empty", url: "/fragments/fleet?instance=", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "instance present empty", url: "/fragments/instance?instance=", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "logs present empty", url: "/logs?instance=&run=run-1", code: http.StatusBadRequest, wantBody: "invalid instance"},

		{name: "page duplicate", url: "/?instance=one&instance=one", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "fleet duplicate", url: "/fragments/fleet?instance=one&instance=two", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "instance duplicate", url: "/fragments/instance?instance=one&instance=one", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "logs duplicate", url: "/logs?instance=one&instance=one&run=run-1", code: http.StatusBadRequest, wantBody: "invalid instance"},

		{name: "page traversal dotdot", url: "/?instance=..", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "fleet traversal dotdot", url: "/fragments/fleet?instance=..", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "instance traversal dotdot", url: "/fragments/instance?instance=..", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "logs traversal dotdot", url: "/logs?instance=..&run=run-1", code: http.StatusBadRequest, wantBody: "invalid instance"},

		{name: "page separator slash", url: "/?instance=bad%2Fid", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "fleet separator slash", url: "/fragments/fleet?instance=bad%2Fid", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "instance separator slash", url: "/fragments/instance?instance=bad%2Fid", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "logs separator slash", url: "/logs?instance=bad%2Fid&run=run-1", code: http.StatusBadRequest, wantBody: "invalid instance"},

		{name: "page dot", url: "/?instance=.", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "page backslash", url: "/?instance=bad%5Cid", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "page space", url: "/?instance=bad%20id", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "page unicode nbsp", url: "/?instance=bad%C2%A0id", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "page unicode em space", url: "/?instance=bad%E2%80%83id", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "page control", url: "/?instance=bad%01id", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "page tab", url: "/?instance=bad%09id", code: http.StatusBadRequest, wantBody: "invalid instance"},

		{name: "question", url: "/?instance=bad%3Fid", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "hash", url: "/?instance=bad%23id", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "ampersand", url: "/?instance=bad%26id", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "semicolon", url: "/?instance=bad%3Bid", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "pipe", url: "/?instance=bad%7Cid", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "backtick", url: "/?instance=bad%60id", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "dollar", url: "/?instance=bad%24id", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "squote", url: "/?instance=bad%27id", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "dquote", url: "/?instance=bad%22id", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "lt", url: "/?instance=bad%3Cid", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "gt", url: "/?instance=bad%3Eid", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "star", url: "/?instance=bad%2Aid", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "lparen", url: "/?instance=bad%28id", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "rparen", url: "/?instance=bad%29id", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "lbracket", url: "/?instance=bad%5Bid", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "rbracket", url: "/?instance=bad%5Did", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "lbrace", url: "/?instance=bad%7Bid", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "rbrace", url: "/?instance=bad%7Did", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "bang", url: "/?instance=bad%21id", code: http.StatusBadRequest, wantBody: "invalid instance"},
		{name: "equals", url: "/?instance=bad%3Did", code: http.StatusBadRequest, wantBody: "invalid instance"},

		{name: "page unknown", url: "/?instance=missing", code: http.StatusNotFound, wantBody: "unknown instance"},
		{name: "fleet unknown", url: "/fragments/fleet?instance=missing", code: http.StatusNotFound, wantBody: "unknown instance"},
		{name: "instance unknown", url: "/fragments/instance?instance=missing", code: http.StatusNotFound, wantBody: "unknown instance"},
		{name: "logs unknown", url: "/logs?instance=missing&run=run-1", code: http.StatusNotFound, wantBody: "unknown instance"},
	} {
		t.Run(test.name, func(t *testing.T) {
			collector := &testCollector{}
			logsCalls := 0
			collector.logs = func(context.Context, Instance, string, bool) (LogResult, error) {
				logsCalls++
				return LogResult{}, &CLIError{Command: "run logs", Exit: 4, Message: "not found"}
			}
			app := NewApp(testInventory(), collector, testTemplates(t))
			recorder := httptest.NewRecorder()
			app.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.url, nil))
			if recorder.Code != test.code {
				t.Fatalf("GET %s status=%d, want %d; body=%s", test.url, recorder.Code, test.code, recorder.Body.String())
			}
			if test.wantBody != "" && !strings.Contains(recorder.Body.String(), test.wantBody) {
				t.Fatalf("GET %s body=%q, want %q", test.url, recorder.Body.String(), test.wantBody)
			}
			if strings.HasPrefix(test.url, "/logs") && test.code != http.StatusOK && logsCalls != 0 {
				t.Fatalf("GET %s made %d collector Logs calls, want 0", test.url, logsCalls)
			}
		})
	}
}

func TestHandlerLogsRunParamValidationSkipsCollector(t *testing.T) {
	for _, test := range []struct {
		name      string
		url       string
		code      int
		wantBody  string
		wantCalls int
	}{
		{name: "missing run", url: "/logs?instance=one", code: http.StatusBadRequest, wantBody: "run is required", wantCalls: 0},
		{name: "empty run", url: "/logs?instance=one&run=", code: http.StatusBadRequest, wantBody: "run is required", wantCalls: 0},
		{name: "duplicate run", url: "/logs?instance=one&run=run-1&run=run-1", code: http.StatusBadRequest, wantBody: "invalid run", wantCalls: 0},
		{name: "malformed run slash", url: "/logs?instance=one&run=bad%2Frun", code: http.StatusBadRequest, wantBody: "invalid run", wantCalls: 0},
		{name: "malformed run traversal", url: "/logs?instance=one&run=..", code: http.StatusBadRequest, wantBody: "invalid run", wantCalls: 0},
		{name: "unknown run reaches collector", url: "/logs?instance=one&run=missing-run", code: http.StatusOK, wantBody: "unknown", wantCalls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			collector := &testCollector{}
			logsCalls := 0
			collector.logs = func(_ context.Context, _ Instance, _ string, _ bool) (LogResult, error) {
				logsCalls++
				return LogResult{}, &CLIError{Command: "run logs", Exit: 4, Message: "not found"}
			}
			app := NewApp(testInventory(), collector, testTemplates(t))
			recorder := httptest.NewRecorder()
			app.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.url, nil))
			if recorder.Code != test.code {
				t.Fatalf("GET %s status=%d, want %d; body=%s", test.url, recorder.Code, test.code, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), test.wantBody) {
				t.Fatalf("GET %s body=%q, want %q", test.url, recorder.Body.String(), test.wantBody)
			}
			if logsCalls != test.wantCalls {
				t.Fatalf("GET %s collector Logs calls=%d, want %d", test.url, logsCalls, test.wantCalls)
			}
		})
	}
}
