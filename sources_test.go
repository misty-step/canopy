package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHabitatReadsEveryPageAndRejectsOutOfScopeRunLinks(t *testing.T) {
	var outside atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer read-only" {
			t.Errorf("Habitat received a non-read or wrong-credential request")
			w.WriteHeader(403)
			return
		}
		switch r.URL.Path {
		case "/api/work/run-links":
			if r.URL.Query().Get("run_ids") != "run-1" {
				t.Errorf("unbounded/imprecise Run query: %s", r.URL.RawQuery)
			}
			runID, item, relationship := "run-1", "ticket-a", "served"
			more := r.URL.Query().Get("offset") == "0"
			if !more {
				item, relationship = "ticket-b", "created"
			}
			if outside.Load() {
				runID = "unrequested-run"
			}
			var next any
			if more {
				next = 1
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"links": []HabitatLink{{RunID: runID, WorkItemID: item, Relationship: relationship, Item: &HabitatItem{ID: item}}},
				"count": 2, "has_more": more, "next_offset": next,
				"scope": map[string]any{"kind": "authorized_modules", "module_ids": []string{"pilot-module"}, "includes_deleted": false},
			})
		case "/api/work/items/ticket-a", "/api/work/items/ticket-b":
			id := strings.TrimPrefix(r.URL.Path, "/api/work/items/")
			_ = json.NewEncoder(w).Encode(map[string]any{"item": HabitatItem{ID: id, Key: id, Status: "in_progress"}})
		case "/api/work/items/ticket-a/history", "/api/work/items/ticket-b/history":
			fmt.Fprint(w, `{"history":[]}`)
		default:
			t.Errorf("unexpected Habitat read path: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	reader := newSourceReader()
	reader.lookup = func(name string) (string, bool) { return "read-only", name == "HABITAT_READ" }
	snapshot := onlyServedFixture()
	snapshot.Instance.Sources = TicketSources{Habitat: &HabitatSource{System: "https://habitat.example", ReadSource: ReadSource{Endpoint: server.URL, TokenEnv: "HABITAT_READ"}}}
	sources := reader.collect(context.Background(), snapshot, DeliverySources{})
	snapshot.Delivery = sources
	view := ticketDeliveryView(snapshot, false, time.Now(), time.Minute)
	if sources.Habitat.Error != "" || len(view.Tickets) != 2 || view.Tickets[0].RunCount != 1 || len(view.Tickets[1].CreatedRuns) != 1 {
		t.Fatalf("paged served/created evidence lost: source=%+v view=%+v", sources.Habitat, view)
	}
	outside.Store(true)
	sources = reader.collect(context.Background(), snapshot, DeliverySources{})
	if sources.Habitat.Error == "" || !sources.Habitat.ObservedAt.IsZero() || len(sources.Habitat.Links) != 0 {
		t.Fatalf("out-of-scope result became an authoritative association: %+v", sources.Habitat)
	}
}

func TestTachDuplicateSessionsCannotBecomeDoubleCharges(t *testing.T) {
	var duplicate atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/ingest/v1/provider-usage/query" || r.Header.Get("x-query-key") != "query-only" || r.Header.Get("Authorization") != "" || r.Header.Get("x-ingest-key") != "" {
			t.Errorf("Tach request crossed its query-only boundary")
			w.WriteHeader(403)
			return
		}
		var body struct {
			Source string   `json:"source"`
			IDs    []string `json:"session_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Source != "iron-forest" || len(body.IDs) != 1 || body.IDs[0] != "run-1" {
			t.Errorf("Tach query must carry the exact source and unique Run IDs")
			w.WriteHeader(400)
			return
		}
		sessions := []ProviderUsage{{SessionID: "run-1", CostUSD: new(0.125), InputTokens: new(int64(10)), GenerationCount: new(1), PendingCount: new(0), Coverage: "complete"}}
		if duplicate.Load() {
			sessions = append(sessions, sessions[0])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"schema": "tach.provider-usage.v1", "as_of": "2026-09-08T11:00:00Z", "sessions": sessions})
	}))
	defer server.Close()
	snapshot := onlyServedFixture()
	snapshot.Instance.Sources = TicketSources{Tach: &ReadSource{Endpoint: server.URL + "/ingest", TokenEnv: "TACH_QUERY"}}
	reader := newSourceReader()
	reader.lookup = func(name string) (string, bool) { return "query-only", name == "TACH_QUERY" }
	snapshot.Delivery = reader.collect(context.Background(), snapshot, DeliverySources{})
	view := ticketDeliveryView(snapshot, false, time.Now(), time.Minute).Tickets[0]
	if view.USD != "$0.12500000" || view.Coverage != "complete" {
		t.Fatalf("valid provider observation unavailable: %+v", view)
	}
	duplicate.Store(true)
	snapshot.Delivery = reader.collect(context.Background(), snapshot, DeliverySources{})
	view = ticketDeliveryView(snapshot, false, time.Now(), time.Minute).Tickets[0]
	if snapshot.Delivery.Usage.Error == "" || view.USD != "Unknown" {
		t.Fatalf("duplicate sessions became charges: %+v", view)
	}
}

func TestTachMissingCoverageCountsStayUnknown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"schema":"tach.provider-usage.v1","as_of":"2026-09-08T11:00:00Z","sessions":[{"session_id":"run-1","cost_usd":0,"coverage":"complete"}]}`)
	}))
	defer server.Close()
	snapshot := onlyServedFixture()
	snapshot.Instance.Sources = TicketSources{Tach: &ReadSource{Endpoint: server.URL, TokenEnv: "READ"}}
	reader := newSourceReader()
	reader.lookup = func(string) (string, bool) { return "read-only", true }
	snapshot.Delivery = reader.collect(context.Background(), snapshot, DeliverySources{})
	view := ticketDeliveryView(snapshot, false, time.Now(), time.Minute).Tickets[0]
	if snapshot.Delivery.Usage.Error == "" || view.USD != "Unknown" || view.UsageSessions != 0 {
		t.Fatalf("missing receipt/pending fields were silently accepted as zero: %+v", view)
	}
}

func TestSourceRedirectCannotForwardReadCredentials(t *testing.T) {
	var reached atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Store(true)
		fmt.Fprint(w, `{}`)
	}))
	defer destination.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	defer redirect.Close()
	reader := newSourceReader()
	reader.lookup = func(string) (string, bool) { return "private-query-credential", true }
	var response any
	err := reader.read(context.Background(), http.MethodGet, redirect.URL, "READ_TOKEN", "Authorization", nil, &response)
	if err == nil || reached.Load() || strings.Contains(err.Error(), "private-query-credential") {
		t.Fatalf("redirect crossed credential boundary: reached=%v error=%v", reached.Load(), err)
	}
}

func TestAbsentReadCredentialNeverBorrowsAnIngestOrWorkerCredential(t *testing.T) {
	var reached atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Store(true); fmt.Fprint(w, `{}`) }))
	defer server.Close()
	t.Setenv("TACH_INGEST_KEY", "worker-write-credential")
	t.Setenv("CANOPY_MISSING_READ_TOKEN", "")
	reader := newSourceReader()
	var response any
	err := reader.read(context.Background(), http.MethodPost, server.URL, "CANOPY_MISSING_READ_TOKEN", "x-query-key", map[string]any{"source": "iron-forest"}, &response)
	if err == nil || reached.Load() || strings.Contains(err.Error(), "worker-write-credential") {
		t.Fatalf("read credential absence crossed source boundary: reached=%v error=%v", reached.Load(), err)
	}
}

func TestObserverPagedHistoryIncludesOlderFailedWork(t *testing.T) {
	t.Setenv("OBSERVER_READ", "observer-read-only")
	work := &WorkRef{System: "https://habitat.example", ID: "ticket-a", Key: "VE-1"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer observer-read-only" {
			w.WriteHeader(403)
			return
		}
		var request struct {
			Args []string `json:"args"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		command := commandRoute(request.Args)
		var data any
		switch command {
		case "version":
			data = VersionData{BuildSHA: "pinned-build"}
		case "config show":
			data = ConfigData{Repo: "org/repo", Primary: "refs/heads/main"}
		case "declaration list":
			data = map[string]any{"declarations": []DeclarationData{}}
		case "status":
			data = StatusData{Recent: []RunData{{RunID: "recent", Work: work, Started: "2026-09-08T10:00:00Z", Duration: 10}}}
		case "run list":
			if strings.Contains(strings.Join(request.Args, " "), "--after recent") {
				data = map[string]any{"runs": []RunData{{RunID: "older", Work: work, Started: "2026-09-08T09:00:00Z", Duration: 50, Exit: 130, Error: "cancelled"}}, "next_after": ""}
			} else {
				data = map[string]any{"runs": []RunData{{RunID: "recent", Work: work, Started: "2026-09-08T10:00:00Z", Duration: 10}}, "next_after": "recent"}
			}
		default:
			t.Errorf("unexpected observation command %s", command)
			w.WriteHeader(400)
			return
		}
		envelope := fakeEnvelope(command, 0, data, nil)
		_ = json.NewEncoder(w).Encode(map[string]any{"stdout": string(envelope.Stdout), "stderr": "", "exit_code": 0})
	}))
	defer server.Close()
	instance := Instance{ID: "vector", Label: "Vector", Root: "/data/vector", Forest: "/data/vector/.iron-forest/bin/forest", ObserverURL: server.URL, ObserverTokenEnv: "OBSERVER_READ"}
	snapshot, err := NewCLICollector(refreshTimeout).Collect(context.Background(), instance)
	if err != nil {
		t.Fatal(err)
	}
	view := ticketDeliveryView(snapshot, false, time.Now(), time.Minute).Tickets[0]
	if snapshot.History.Error != "" || view.RunCount != 2 || view.FailedRuns != 1 || view.Duration != "1m 0s" || view.Started != "2026-09-08 09:00:00Z" {
		t.Fatalf("status tail hid older failed work from HTTP observation: %+v; history=%+v", view, snapshot.History)
	}
}

func TestForgeProtocolFailureRetainsObservedMergeWithoutRenewingIt(t *testing.T) {
	const prURL = "https://github.com/org/repo/pull/1"
	var malformed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var mergedAt any = "2026-09-08T09:10:00Z"
		if malformed.Load() {
			mergedAt = nil
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"merged": true, "state": "closed", "merged_at": mergedAt, "merge_commit_sha": "verified-merge", "html_url": prURL,
			"base":      map[string]any{"ref": "main", "repo": map[string]string{"full_name": "org/repo"}},
			"merged_by": map[string]string{"login": "human-reviewer", "type": "User"},
		})
	}))
	defer server.Close()
	source := ForgeSource{ReadSource: ReadSource{Endpoint: server.URL, TokenEnv: "READ"}, WebURL: "https://github.com"}
	habitat := HabitatObservation{Items: map[string]HabitatItem{"ticket-a": {PRURLs: []string{prURL}}}}
	reader := newSourceReader()
	reader.lookup = func(string) (string, bool) { return "read-only", true }
	previous := reader.forge(context.Background(), source, ConfigData{Repo: "org/repo", Primary: "main"}, habitat, ForgeObservation{})
	malformed.Store(true)
	next := reader.forge(context.Background(), source, ConfigData{Repo: "org/repo", Primary: "main"}, habitat, previous)
	pull := next.Pulls[prURL]
	if !pull.Merged || !pull.HumanApproved || pull.SHA != "verified-merge" || pull.Error == "" || pull.ObservedAt != previous.Pulls[prURL].ObservedAt {
		t.Fatalf("a failed refresh erased or renewed independent merge evidence: %+v", pull)
	}
}
