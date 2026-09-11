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
	t.Setenv("CANOPY_UNRELATED_WRITE_KEY", "worker-write-credential")
	t.Setenv("CANOPY_MISSING_READ_TOKEN", "")
	reader := newSourceReader()
	var response any
	err := reader.read(context.Background(), http.MethodPost, server.URL, "CANOPY_MISSING_READ_TOKEN", "x-query-key", map[string]any{"query": "configured-source"}, &response)
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
	snapshot := NewCLICollector(refreshTimeout).CollectDetails(context.Background(), instance)
	view := ticketDeliveryView(snapshot, false, time.Now(), time.Minute).Tickets[0]
	if snapshot.History.Error != "" || view.RunCount != 2 || view.FailedRuns != 1 || view.Duration != "1m 0s" || view.Started != "2026-09-08 09:00:00Z" {
		t.Fatalf("status tail hid older failed work from HTTP observation: %+v; history=%+v", view, snapshot.History)
	}
}

func TestForgeProtocolFailureRetainsObservedMergeWithoutRenewingIt(t *testing.T) {
	const prURL = "https://github.com/org/repo/pull/1"
	var malformed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/comments") || strings.HasSuffix(r.URL.Path, "/reviews") {
			fmt.Fprint(w, `[]`)
			return
		}
		var mergedAt any = "2026-09-08T09:10:00Z"
		if malformed.Load() {
			mergedAt = nil
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"merged": true, "state": "closed", "merged_at": mergedAt, "merge_commit_sha": "verified-merge", "html_url": prURL,
			"head":      map[string]string{"sha": strings.Repeat("a", 40)},
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
	if !pull.Merged || pull.MergerType != "User" || pull.SHA != "verified-merge" || pull.Error == "" || pull.ObservedAt != previous.Pulls[prURL].ObservedAt {
		t.Fatalf("a failed refresh erased or renewed independent merge evidence: %+v", pull)
	}
}

func TestExplicitHabitatInventoryIncludesZeroRunItemsWithoutBroadeningScope(t *testing.T) {
	var unavailable atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer read-only" {
			t.Errorf("inventory read crossed the read-only source boundary")
			w.WriteHeader(403)
			return
		}
		switch r.URL.Path {
		case "/api/work/items/ticket-a", "/api/work/items/ticket-c":
			if unavailable.Load() {
				w.WriteHeader(503)
				return
			}
			id := strings.TrimPrefix(r.URL.Path, "/api/work/items/")
			_ = json.NewEncoder(w).Encode(map[string]any{"item": HabitatItem{ID: id, Key: id, Title: "Explicitly configured work", Status: "backlog"}})
		case "/api/work/items/ticket-a/history", "/api/work/items/ticket-c/history":
			fmt.Fprint(w, `{"history":[]}`)
		default:
			t.Errorf("zero-run inventory broadened its query: %s", r.URL)
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	reader := newSourceReader()
	reader.lookup = func(string) (string, bool) { return "read-only", true }
	snapshot := Snapshot{Instance: Instance{Sources: TicketSources{Habitat: &HabitatSource{
		ReadSource: ReadSource{Endpoint: server.URL, TokenEnv: "READ"}, System: "https://habitat.example", WorkItemIDs: []string{"ticket-a", "ticket-c"},
	}}}, History: RunHistory{SourceObservation: SourceObservation{ObservedAt: time.Now().UTC()}}}
	snapshot.Delivery = reader.collect(context.Background(), snapshot, DeliverySources{})
	view := ticketDeliveryView(snapshot, false, time.Now(), time.Minute)
	if len(view.Tickets) != 2 || view.UnattributedRuns != 0 {
		t.Fatalf("explicit zero-run inventory disappeared: %+v", view)
	}
	for _, ticket := range view.Tickets {
		if ticket.RunCount != 0 || ticket.USD != "Unknown" || ticket.USDCompact != "Unknown" || ticket.Coverage != "unknown" ||
			ticket.LiveRuns != 0 || ticket.ActionOwner != "Operator" || ticket.TrackerFreshness != "fresh" {
			t.Fatalf("zero-run inventory invented cost, activity or authorization: %+v", ticket)
		}
	}
	previous := snapshot.Delivery
	unavailable.Store(true)
	snapshot.Delivery = reader.collect(context.Background(), snapshot, previous)
	view = ticketDeliveryView(snapshot, false, time.Now(), time.Minute)
	for _, ticket := range view.Tickets {
		if ticket.Stage != "Evidence unavailable" || ticket.TrackerFreshness != "stale" || ticket.Title != "Explicitly configured work" ||
			snapshot.Delivery.Habitat.Items[ticket.ID].ObservedAt != previous.Habitat.Items[ticket.ID].ObservedAt {
			t.Fatalf("failed item observation lost or renewed last-good inventory: %+v", ticket)
		}
	}
}

func TestPaginatedReviewReceiptsRetainSourceFreshnessIndependentlyOfNewHead(t *testing.T) {
	snapshot := reviewedFixture("approve")
	prURL := snapshot.Delivery.Habitat.Items["ticket-a"].PRURL
	receipt := snapshot.Delivery.Forge.Pulls[prURL].ReviewReceipts[0]
	payload, err := json.Marshal(map[string]string{"schema": receipt.Schema, "run_id": receipt.RunID, "work_id": receipt.WorkID,
		"revision": receipt.Revision, "decision": receipt.Decision, "summary": receipt.Summary})
	if err != nil {
		t.Fatal(err)
	}
	var failComments, changedHead atomic.Bool
	var secondPage atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer read-only" {
			t.Errorf("forge evidence crossed the read-only proxy boundary")
			w.WriteHeader(403)
			return
		}
		switch r.URL.Path {
		case "/v1/github/repos/org/repo/pulls/2":
			head := receipt.Revision
			if changedHead.Load() {
				head = strings.Repeat("b", 40)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"merged": false, "state": "open", "html_url": prURL,
				"base": map[string]any{"ref": "main", "repo": map[string]string{"full_name": "org/repo"}}, "head": map[string]string{"sha": head}})
		case "/v1/github/repos/org/repo/pulls/2/reviews":
			_ = json.NewEncoder(w).Encode([]any{map[string]any{"state": "APPROVED", "commit_id": receipt.Revision,
				"submitted_at": receipt.CreatedAt, "html_url": prURL + "#pullrequestreview-1",
				"user": map[string]string{"login": "operator-account", "type": "User"}}})
		case "/v1/github/repos/org/repo/issues/2/comments":
			if r.URL.Query().Get("per_page") != "100" {
				t.Errorf("receipt pagination lost its bounded page size: %s", r.URL.RawQuery)
			}
			if failComments.Load() {
				w.WriteHeader(503)
				return
			}
			switch r.URL.Query().Get("page") {
			case "1":
				placeholders := make([]map[string]any, 100)
				for i := range placeholders {
					placeholders[i] = map[string]any{}
				}
				_ = json.NewEncoder(w).Encode(placeholders)
			case "2":
				secondPage.Store(true)
				_ = json.NewEncoder(w).Encode([]any{map[string]any{"id": receipt.ID, "body": "<!-- forest.review.v1 -->\n" + string(payload),
					"html_url": receipt.URL, "created_at": receipt.CreatedAt, "updated_at": receipt.UpdatedAt,
					"author_association": receipt.Association, "user": map[string]any{"id": receipt.AuthorID, "login": receipt.Author, "type": receipt.AuthorType}}})
			default:
				t.Errorf("unexpected receipt page: %s", r.URL.RawQuery)
				w.WriteHeader(400)
			}
		default:
			t.Errorf("unexpected forge read: %s", r.URL)
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	reader := newSourceReader()
	reader.lookup = func(string) (string, bool) { return "read-only", true }
	snapshot.Instance.Sources.Forge.Endpoint = server.URL + "/v1/github"
	state := SourceObservation{ObservedAt: time.Now().UTC()}
	snapshot.History.SourceObservation = state
	snapshot.ConfigObservation, snapshot.DeclarationsObservation = state, state
	snapshot.Delivery.Habitat.SourceObservation = state
	item := snapshot.Delivery.Habitat.Items["ticket-a"]
	item.SourceObservation, item.PRURLs = state, []string{prURL}
	snapshot.Delivery.Habitat.Items[item.ID] = item
	snapshot.Delivery.Forge = reader.forge(context.Background(), *snapshot.Instance.Sources.Forge, snapshot.Config, snapshot.Delivery.Habitat, ForgeObservation{})
	ticket := ticketDeliveryView(snapshot, false, time.Now(), time.Minute).Tickets[0]
	if !secondPage.Load() || ticket.ReviewURL != receipt.URL || ticket.ReviewAuthor != receipt.Author ||
		len(ticket.AccountApprovals) != 1 || ticket.AccountApprovals[0].Revision != receipt.Revision {
		t.Fatalf("paged exact receipt/account-action evidence was lost: %+v", ticket)
	}
	previous := snapshot.Delivery.Forge
	changedHead.Store(true)
	failComments.Store(true)
	snapshot.Delivery.Forge = reader.forge(context.Background(), *snapshot.Instance.Sources.Forge, snapshot.Config, snapshot.Delivery.Habitat, previous)
	ticket = ticketDeliveryView(snapshot, false, time.Now(), time.Minute).Tickets[0]
	if ticket.Stage != "Evidence unavailable" || ticket.HeadSHA != strings.Repeat("b", 40) || ticket.ReviewSHA != receipt.Revision ||
		!strings.Contains(ticket.ReviewWarning, "observation failed") || len(ticket.AccountApprovals) != 0 ||
		snapshot.Delivery.Forge.Pulls[prURL].ReviewSource.ObservedAt != previous.Pulls[prURL].ReviewSource.ObservedAt {
		t.Fatalf("new PR observation renewed retained old review evidence or lost new head: %+v", ticket)
	}
	failComments.Store(false)
	snapshot.Delivery.Forge = reader.forge(context.Background(), *snapshot.Instance.Sources.Forge, snapshot.Config, snapshot.Delivery.Habitat, snapshot.Delivery.Forge)
	ticket = ticketDeliveryView(snapshot, false, time.Now(), time.Minute).Tickets[0]
	if ticket.Stage != "Awaiting verification" || !strings.Contains(ticket.ReviewWarning, "stale revision") {
		t.Fatalf("successful stale-revision observation was conflated with source failure: %+v", ticket)
	}
}
