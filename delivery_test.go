package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func deliveryFixture() Snapshot {
	observed := time.Date(2026, 9, 8, 11, 0, 0, 0, time.UTC)
	state := SourceObservation{ObservedAt: observed}
	work := &WorkRef{System: "https://habitat.example", ID: "ticket-a", Key: "VE-1", URL: "https://habitat.example/work/VE-1"}
	first := RunData{RunID: "run-1", Agent: "builder", RequestID: "request-1", Work: work, Started: "2026-09-08T09:00:00Z", Duration: 60, Exit: 1}
	second := RunData{RunID: "run-2", Agent: "builder", RequestID: "request-2", Work: work, Started: "2026-09-08T09:05:00Z", Duration: 180, Exit: 130, Error: "cancelled"}
	created := RunData{RunID: "run-3", Agent: "critic", Started: "2026-09-08T09:15:00Z", Duration: 120}
	oldPR, newPR := "https://github.com/org/repo/pull/1", "https://github.com/org/repo/pull/2"
	recent := first
	recent.Work, recent.RequestID = nil, ""
	return Snapshot{
		Instance: Instance{ID: "vector", Label: "Vector", Root: "/data/vector", Forest: "/data/vector/.iron-forest/bin/forest", Sources: TicketSources{
			Habitat: &HabitatSource{System: work.System, ReadSource: ReadSource{Endpoint: work.System, TokenEnv: "HABITAT_READ"}},
			Tach:    &ReadSource{Endpoint: "https://tach.example/ingest", TokenEnv: "TACH_READ"},
			Forge:   &ForgeSource{ReadSource: ReadSource{Endpoint: "https://api.github.com", TokenEnv: "FORGE_READ"}, WebURL: "https://github.com"},
		}},
		Config:  ConfigData{Repo: "org/repo", Primary: "refs/heads/main"},
		History: RunHistory{SourceObservation: state, Runs: []RunData{first, second, created}},
		Status:  StatusData{Recent: []RunData{recent, second}, LiveRuns: []LiveRunData{{RunID: first.RunID, Work: work}}},
		Delivery: DeliverySources{
			Habitat: HabitatObservation{SourceObservation: state, Links: []HabitatLink{
				{RunID: first.RunID, WorkItemID: work.ID, Relationship: "served"},
				{RunID: first.RunID, WorkItemID: work.ID, Relationship: "served"},
				{RunID: created.RunID, WorkItemID: "ticket-b", Relationship: "created"},
			}, Items: map[string]HabitatItem{
				work.ID:    {SourceObservation: state, ID: work.ID, Key: work.Key, Status: "in_progress", PRURLs: []string{oldPR, newPR}},
				"ticket-b": {SourceObservation: state, ID: "ticket-b", Key: "VE-2", Status: "backlog"},
			}},
			Usage: UsageObservation{SourceObservation: state, AsOf: observed, Sessions: map[string]ProviderUsage{
				first.RunID:   {SessionID: first.RunID, InputTokens: new(int64(10)), OutputTokens: new(int64(20)), CacheReadTokens: new(int64(100)), CostUSD: new(0.01), GenerationCount: new(1), PendingCount: new(0), Coverage: "complete"},
				second.RunID:  {SessionID: second.RunID, InputTokens: new(int64(20)), OutputTokens: new(int64(30)), CacheReadTokens: new(int64(100)), CostUSD: new(0.02), GenerationCount: new(1), PendingCount: new(1), Coverage: "partial"},
				created.RunID: {SessionID: created.RunID, CostUSD: new(5.0), GenerationCount: new(1), PendingCount: new(0), Coverage: "complete"},
			}},
			Forge: ForgeObservation{SourceObservation: state, Pulls: map[string]PullEvidence{
				oldPR: {SourceObservation: state, URL: oldPR, BaseRef: "main", Merged: true, HumanApproved: true, MergedAt: new(observed.Add(-110 * time.Minute)), SHA: "merge-one", MergedBy: "reviewer"},
				newPR: {SourceObservation: state, URL: newPR, BaseRef: "main", Merged: true, HumanApproved: true, MergedAt: new(observed.Add(-time.Hour)), SHA: "merge-two", MergedBy: "reviewer"},
			}},
		},
	}
}

func onlyServedFixture() Snapshot {
	snapshot := deliveryFixture()
	snapshot.History.Runs = snapshot.History.Runs[:1]
	snapshot.Status.Recent, snapshot.Status.LiveRuns = nil, nil
	snapshot.Delivery.Habitat.Links = nil
	return snapshot
}

func TestDeliveryDeduplicatesReopenedServedWorkWithoutChargingCreatedLinks(t *testing.T) {
	snapshot := deliveryFixture()
	view := ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute)
	if len(view.Tickets) != 2 || view.Delivered != 1 || view.UnattributedRuns != 1 {
		t.Fatalf("ticket identity/served join failed: %+v", view)
	}
	ticket, created := view.Tickets[0], view.Tickets[1]
	if ticket.RunCount != 2 || ticket.FailedRuns != 2 || ticket.LiveRuns != 0 || ticket.Duration != "4m 0s" || ticket.USD != "$0.03000000" {
		t.Fatalf("duplicate/live/failed/cancelled Runs changed served totals: %+v", ticket)
	}
	if ticket.InputTokens != "30" || ticket.OutputTokens != "50" || ticket.CacheRead != "200" || ticket.Pending != 1 || ticket.Coverage != "partial" {
		t.Fatalf("provider subtotals or overlapping token categories were misrepresented: %+v", ticket)
	}
	if ticket.Tracker != "in_progress" || ticket.MergeSHA != "merge-one" || ticket.Latency != "10m 0s" || ticket.Runs[0].RequestID != "request-1" {
		t.Fatalf("reopening/projection overlap erased original provenance or first merge: %+v", ticket)
	}
	if created.RunCount != 0 || created.USD != "Unknown" || len(created.CreatedRuns) != 1 || view.UnassignedUSD != "$5.00000000" {
		t.Fatalf("created relationship must not assign served cost: %+v", created)
	}
	again := ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute).Tickets[0]
	if again.USD != ticket.USD || again.InputTokens != ticket.InputTokens || again.Duration != ticket.Duration {
		t.Fatalf("refresh mutated the provider/Run snapshot and accumulated a second charge: %+v", again)
	}
}

func TestNoWorkReceiptIsNotAnUnattributedExecution(t *testing.T) {
	snapshot := onlyServedFixture()
	var selection RunData
	if err := json.Unmarshal([]byte(`{"run_id":"selection","agent":"builder","started":"2026-09-08T10:00:00Z","duration":1,"exit":1,"no_work":true}`), &selection); err != nil {
		t.Fatal(err)
	}
	snapshot.History.Runs = append(snapshot.History.Runs, selection)
	snapshot.Status.LiveRuns = []LiveRunData{{RunID: selection.RunID, Agent: selection.Agent, StartedAt: selection.Started}}
	view := ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute)
	if view.UnattributedRuns != 0 || view.Tickets[0].RunCount != 1 || view.Tickets[0].FailedRuns != 1 {
		t.Fatalf("no-work selection changed execution accounting: %+v", view)
	}
	for _, id := range snapshotRunIDs(snapshot) {
		if id == selection.RunID {
			t.Fatal("no-work selection was submitted for provider usage accounting")
		}
	}
}

func TestConflictingExactPrimaryAssociationsDoNotSplitOrDuplicateCharges(t *testing.T) {
	snapshot := onlyServedFixture()
	snapshot.Delivery.Habitat.Links = []HabitatLink{{RunID: "run-1", WorkItemID: "ticket-b", Relationship: "served"}}
	view := ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute)
	if view.ConflictingRuns != 1 || len(view.Tickets) != 2 {
		t.Fatalf("missing conflict: %+v", view)
	}
	for _, ticket := range view.Tickets {
		if ticket.RunCount != 0 || ticket.USD != "Unknown" {
			t.Fatalf("ambiguous Run was charged: %+v", ticket)
		}
	}
	// Conflicting Forest projections have the same fail-closed attribution,
	// even when no Habitat source is available to discover the inconsistency.
	snapshot.Delivery.Habitat.Links = nil
	other := snapshot.History.Runs[0]
	other.Work = &WorkRef{System: "https://habitat.example", ID: "ticket-b", Key: "VE-2"}
	snapshot.Status.Recent = []RunData{other}
	view = ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute)
	if view.ConflictingRuns != 1 {
		t.Fatalf("contradictory retained/recent provenance was overwritten: %+v", view)
	}
}

func TestUnknownUsageCannotBecomeZeroOrForestEstimatedCost(t *testing.T) {
	snapshot := onlyServedFixture()
	snapshot.History.Runs[0].TokensIn = 900
	snapshot.Delivery.Usage.Sessions = nil
	view := ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute).Tickets[0]
	if view.USD != "Unknown" || view.InputTokens != "Unknown" || view.Coverage != "unknown" || view.UsageSessions != 0 {
		t.Fatalf("missing provider evidence became a zero/Forest estimate: %+v", view)
	}
	snapshot.Delivery.Usage.Sessions = map[string]ProviderUsage{"run-1": {SessionID: "run-1", InputTokens: new(int64(0)), CostUSD: new(0.0), GenerationCount: new(0), PendingCount: new(0), Coverage: "complete"}}
	view = ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute).Tickets[0]
	if view.USD != "$0.00000000" || view.InputTokens != "0" || view.Coverage != "complete" {
		t.Fatalf("explicit provider zero was lost: %+v", view)
	}
}

func TestTrackerDoneAndUnapprovedOrNonPrimaryMergesAreNotDelivery(t *testing.T) {
	snapshot := onlyServedFixture()
	item := snapshot.Delivery.Habitat.Items["ticket-a"]
	item.Status, item.PRURLs = "done", nil
	snapshot.Delivery.Habitat.Items["ticket-a"] = item
	view := ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute).Tickets[0]
	if view.Delivered || view.Latency != "Unknown" || view.MergeSHA != "" {
		t.Fatalf("Done became merge evidence: %+v", view)
	}
	for prURL, pull := range snapshot.Delivery.Forge.Pulls {
		item.PRURLs = append(item.PRURLs, prURL)
		if pull.SHA == "merge-one" {
			pull.BaseRef = "release"
		} else {
			pull.HumanApproved = false
		}
		snapshot.Delivery.Forge.Pulls[prURL] = pull
	}
	snapshot.Delivery.Habitat.Items["ticket-a"] = item
	view = ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute).Tickets[0]
	if view.Delivered || view.Latency != "Unknown" {
		t.Fatalf("wrong-primary/unapproved merge became delivery: %+v", view)
	}
}

func TestSourceFailureRetainsUsageWithoutRenewingItsFreshness(t *testing.T) {
	snapshot := onlyServedFixture()
	snapshot.Instance.Sources.Habitat, snapshot.Instance.Sources.Forge = nil, nil
	previous := snapshot.Delivery
	reader := newSourceReader()
	reader.lookup = func(string) (string, bool) { return "", false }
	snapshot.Delivery = reader.collect(context.Background(), snapshot, previous)
	view := ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt.Add(time.Second), time.Minute).Tickets[0]
	if snapshot.Delivery.Usage.ObservedAt != previous.Usage.ObservedAt || view.USD != "$0.01000000" || view.UsageFreshness != "stale" {
		t.Fatalf("failed read erased or renewed provider evidence: %+v", view)
	}
	now := snapshot.History.ObservedAt.Add(2 * time.Minute)
	snapshot.Delivery.Usage.Error = ""
	snapshot.Delivery.Usage.ObservedAt = now
	view = ticketDeliveryView(snapshot, false, now, time.Minute).Tickets[0]
	if view.UsageFreshness != "stale" {
		t.Fatalf("old Tach as_of became fresh when fetched again: %+v", view)
	}
}

func TestTruncatedHistoryLeavesFirstDeliveryUnknown(t *testing.T) {
	snapshot := onlyServedFixture()
	item := snapshot.Delivery.Habitat.Items["ticket-a"]
	item.HistoryWarning = "History limit reached"
	snapshot.Delivery.Habitat.Items["ticket-a"] = item
	view := ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute).Tickets[0]
	if !view.Delivered || view.Latency != "Unknown" || view.ObservedLatency != "10m 0s" || view.MergeSHA != "merge-one" {
		t.Fatalf("observed merge was lost or falsely promoted to proven first delivery: %+v", view)
	}
}

func TestTicketFragmentKeepsCredentialsServerSideAndEscapesTrackerContent(t *testing.T) {
	snapshot := onlyServedFixture()
	const secret = "never-render-source-credential"
	t.Setenv("HABITAT_READ", secret)
	t.Setenv("TACH_READ", secret)
	t.Setenv("FORGE_READ", secret)
	item := snapshot.Delivery.Habitat.Items["ticket-a"]
	item.Title = "<script>alert('tracker')</script>"
	item.URL = "javascript:alert('tracker')"
	snapshot.Delivery.Habitat.Items["ticket-a"] = item
	templates, err := loadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	app := NewApp(Inventory{Instances: []Instance{snapshot.Instance}}, nil, templates)
	app.recordRefresh(snapshot.Instance.ID, &snapshot, nil)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/fragments/instance?instance=vector", nil))
	if response.Code != 200 {
		t.Fatalf("fragment status = %d", response.Code)
	}
	body := response.Body.String()
	if strings.Contains(body, secret) || strings.Contains(body, "<script>alert") || strings.Contains(body, `href="javascript:`) {
		t.Fatalf("source data crossed credential/script boundary: %s", body)
	}
	mutation := httptest.NewRecorder()
	app.Handler().ServeHTTP(mutation, httptest.NewRequest("POST", "/fragments/instance?instance=vector", nil))
	if mutation.Code != 405 {
		t.Fatalf("delivery fragment accepted mutation method: %d", mutation.Code)
	}
}
