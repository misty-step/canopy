package main

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"regexp"
	"time"
)

// staticFiles is deliberately embedded at the package boundary: deployments
// do not depend on a source checkout after the binary is built.
//
//go:embed static/*
var staticFiles embed.FS

var (
	errMissingInstance = errors.New("instance is required")
	errBadInstance     = errors.New("invalid instance")
	errUnknownInstance = errors.New("unknown instance")
	errMissingRun      = errors.New("run is required")
	errBadRun          = errors.New("invalid run")
)

func (a *App) Handler() http.Handler {
	staticRoot, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(fmt.Sprintf("open embedded static files: %v", err))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.handlePage)
	mux.HandleFunc("/fragments/fleet", a.handleFleet)
	mux.HandleFunc("/fragments/instance", a.handleInstance)
	mux.HandleFunc("/logs", a.handleLogs)
	mux.HandleFunc("/healthz", a.handleHealth)
	mux.Handle("/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireGet(w, r) {
			return
		}
		http.StripPrefix("/static/", http.FileServer(http.FS(staticRoot))).ServeHTTP(w, r)
	}))
	return mux
}

func (a *App) handlePage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if !requireGet(w, r) {
		return
	}
	id, err := a.instanceParam(r, false)
	if err != nil {
		handleParamError(w, r, err)
		return
	}
	if id == "" {
		id = a.selectedID()
	}
	a.selectInstance(id)
	view := a.pageView(id, time.Now().UTC())
	a.render(w, r, []string{"page.html", "page"}, view)
}

func (a *App) handleFleet(w http.ResponseWriter, r *http.Request) {
	if !requireGet(w, r) {
		return
	}
	id, err := a.instanceParam(r, false)
	if err != nil {
		handleParamError(w, r, err)
		return
	}
	if id != "" {
		a.selectInstance(id)
	}
	view := a.pageView(id, time.Now().UTC())
	a.render(w, r, []string{"fleet.html", "fleet"}, view)
}

func (a *App) handleInstance(w http.ResponseWriter, r *http.Request) {
	if !requireGet(w, r) {
		return
	}
	id, err := a.instanceParam(r, false)
	if err != nil {
		handleParamError(w, r, err)
		return
	}
	if id == "" {
		id = a.selectedID()
	}
	selectionChanged := a.selectInstance(id)
	if selectionChanged {
		w.Header().Set("HX-Trigger-After-Swap", "canopy-selection")
	}
	view := a.pageView(id, time.Now().UTC())
	a.render(w, r, []string{"instance.html", "instance"}, view.Selected)
}

func (a *App) handleLogs(w http.ResponseWriter, r *http.Request) {
	if !requireGet(w, r) {
		return
	}
	instanceID, err := a.instanceParam(r, true)
	if err != nil {
		handleParamError(w, r, err)
		return
	}
	runID, err := runParam(r)
	if err != nil {
		handleParamError(w, r, err)
		return
	}
	instance, ok := a.instance(instanceID)
	if !ok {
		// instanceParam already validates this; retain a defensive boundary for
		// an App whose inventory is changed by a package-local test.
		http.NotFound(w, r)
		return
	}
	if a.collector == nil {
		http.Error(w, "collector is not configured", http.StatusInternalServerError)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), refreshTimeout)
	result, collectErr := a.collector.Logs(ctx, instance, runID, false)
	cancel()
	sanitizedText := sanitizeLogText(result.Text)
	view := LogView{RunID: runID, Complete: result.Complete, Retained: result.Retained, Exit: result.Exit, Text: sanitizedText}
	switch {
	case collectErr != nil && isUnknownRunError(collectErr):
		view.State = "unknown"
		view.Error = collectErr.Error()
	case collectErr != nil:
		view.State = "error"
		view.Error = collectErr.Error()
	case result.Retained:
		view.State = "retained"
	default:
		// The CLI deliberately reports exit 0 + retained=false for a known Run
		// whose log has been evicted. It is not an HTTP error.
		view.State = "evicted"
	}
	a.render(w, r, []string{"log.html", "log"}, view)
}

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	if !requireGet(w, r) {
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (a *App) instanceParam(r *http.Request, required bool) (string, error) {
	values, present := r.URL.Query()["instance"]
	if !present {
		if required {
			return "", errMissingInstance
		}
		return "", nil
	}
	if len(values) != 1 || values[0] == "" || validateRouteIdentifier(values[0], "instance id") != nil {
		return "", errBadInstance
	}
	if _, ok := a.instance(values[0]); !ok {
		return "", errUnknownInstance
	}
	return values[0], nil
}

func (a *App) instance(id string) (Instance, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, instance := range a.inventory.Instances {
		if instance.ID == id {
			return instance, true
		}
	}
	return Instance{}, false
}

func runParam(r *http.Request) (string, error) {
	values, present := r.URL.Query()["run"]
	if !present || len(values) != 1 || values[0] == "" || validateRouteIdentifier(values[0], "run id") != nil {
		if !present || len(values) == 0 || (len(values) == 1 && values[0] == "") {
			return "", errMissingRun
		}
		return "", errBadRun
	}
	return values[0], nil
}

func isUnknownRunError(err error) bool {
	var cliErr *CLIError
	return errors.As(err, &cliErr) && cliErr != nil && cliErr.Exit == 4
}
var (
	keyPattern = regexp.MustCompile(`(?i)(keys/|key[_-]?id[=:]|token[=:]|bearer\s+|api[_-]?key[=:])([a-zA-Z0-9_\-\.]{12,})`)
	bearerPattern = regexp.MustCompile(`(?i)sk-[a-zA-Z0-9_\-]{20,}`)
)

func sanitizeLogText(text string) string {
	if text == "" {
		return ""
	}
	res := keyPattern.ReplaceAllString(text, "${1}[REDACTED]")
	res = bearerPattern.ReplaceAllString(res, "[REDACTED]")
	return res
}


func requireGet(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet {
		return true
	}
	w.Header().Set("Allow", http.MethodGet)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}

func handleParamError(w http.ResponseWriter, _ *http.Request, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, errUnknownInstance) {
		status = http.StatusNotFound
	}
	http.Error(w, err.Error(), status)
}

func (a *App) render(w http.ResponseWriter, _ *http.Request, names []string, data any) {
	if a.templates == nil {
		http.Error(w, "templates are not configured", http.StatusInternalServerError)
		return
	}
	var selected *template.Template
	for _, name := range names {
		if candidate := a.templates.Lookup(name); candidate != nil {
			selected = candidate
			break
		}
	}
	if selected == nil {
		http.Error(w, fmt.Sprintf("template %q is not configured", names[0]), http.StatusInternalServerError)
		return
	}
	var body bytes.Buffer
	if err := selected.Execute(&body, data); err != nil {
		http.Error(w, "render template", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body.Bytes())
}
