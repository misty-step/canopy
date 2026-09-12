package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// completeCost and partialCost build the native provider charge a Run record
// carries: complete means the provider closed its accounting for that Run.
func completeCost(usd float64) *ProviderCost {
	return &ProviderCost{Provider: providerOpenRouter, CostUSD: new(usd), Complete: true}
}

func partialCost(usd float64) *ProviderCost {
	return &ProviderCost{Provider: providerOpenRouter, CostUSD: new(usd)}
}

func deliveryFixture() Snapshot {
	observed := time.Date(2026, 9, 8, 11, 0, 0, 0, time.UTC)
	state := SourceObservation{ObservedAt: observed}
	work := &WorkRef{System: "https://habitat.example", ID: "ticket-a", Key: "VE-1", URL: "https://habitat.example/work/VE-1"}
	first := RunData{RunID: "run-1", Agent: "builder", RequestID: "request-1", Work: work, Started: "2026-09-08T09:00:00Z", Duration: 60, Exit: 1,
		TokensIn: 10, TokensOut: 20, CacheRead: 100, ProviderCost: completeCost(0.01)}
	second := RunData{RunID: "run-2", Agent: "builder", RequestID: "request-2", Work: work, Started: "2026-09-08T09:05:00Z", Duration: 180, Exit: 130, Error: "cancelled",
		TokensIn: 20, TokensOut: 30, CacheRead: 100, ProviderCost: partialCost(0.02)}
	created := RunData{RunID: "run-3", Agent: "critic", Started: "2026-09-08T09:15:00Z", Duration: 120, ProviderCost: completeCost(5.0)}
	oldPR, newPR := "https://github.com/org/repo/pull/1", "https://github.com/org/repo/pull/2"
	recent := first
	recent.Work, recent.RequestID = nil, ""
	return Snapshot{
		Instance: Instance{ID: "vector", Label: "Vector", Root: "/data/vector", Forest: "/data/vector/.iron-forest/bin/forest", Sources: TicketSources{
			Habitat: &HabitatSource{System: work.System, ReadSource: ReadSource{Endpoint: work.System, TokenEnv: "HABITAT_READ"}},
			Forge:   &ForgeSource{ReadSource: ReadSource{Endpoint: "https://api.github.com", TokenEnv: "FORGE_READ"}, WebURL: "https://github.com"},
		}},
		Config:                  ConfigData{Repo: "org/repo", Primary: "refs/heads/main"},
		ConfigObservation:       state,
		DeclarationsObservation: state,
		History:                 RunHistory{SourceObservation: state, Runs: []RunData{first, second, created}},
		Status:                  StatusData{Recent: []RunData{recent, second}, LiveRuns: []LiveRunData{{RunID: first.RunID, Work: work}}},
		Delivery: DeliverySources{
			Habitat: HabitatObservation{SourceObservation: state, Links: []HabitatLink{
				{RunID: first.RunID, WorkItemID: work.ID, Relationship: "served"},
				{RunID: first.RunID, WorkItemID: work.ID, Relationship: "served"},
				{RunID: created.RunID, WorkItemID: "ticket-b", Relationship: "created"},
			}, Items: map[string]HabitatItem{
				work.ID:    {SourceObservation: state, ID: work.ID, Key: work.Key, Status: "in_progress", PRURLs: []string{oldPR, newPR}},
				"ticket-b": {SourceObservation: state, ID: "ticket-b", Key: "VE-2", Status: "backlog"},
			}},
			Forge: ForgeObservation{SourceObservation: state, Pulls: map[string]PullEvidence{
				oldPR: {SourceObservation: state, URL: oldPR, BaseRef: "main", Merged: true, MergedAt: new(observed.Add(-110 * time.Minute)), SHA: "merge-one", MergedBy: "reviewer", MergerType: "User"},
				newPR: {SourceObservation: state, URL: newPR, BaseRef: "main", Merged: true, MergedAt: new(observed.Add(-time.Hour)), SHA: "merge-two", MergedBy: "reviewer", MergerType: "User"},
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
	if ticket.InputTokens != "30" || ticket.OutputTokens != "50" || ticket.CacheRead != "200" || ticket.Coverage != "partial" || ticket.CostReported != 2 || ticket.CostUnknown != 0 {
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

func TestReopenedWorkDoesNotIncreaseFirstDeliveryCost(t *testing.T) {
	snapshot := deliveryFixture()
	snapshot.History.Runs[1].Started = "2026-09-08T09:20:00Z"
	snapshot.Status.Recent = nil
	view := ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute).Tickets[0]
	if view.USD != "$0.03000000" || view.FirstDeliveryUSD != "$0.01000000" {
		t.Fatalf("reopened work changed first-delivery spend: %+v", view)
	}
}

func TestRunSpanningMergeCannotBeAllocatedToFirstDelivery(t *testing.T) {
	snapshot := onlyServedFixture()
	snapshot.History.Runs[0].Duration = 20 * 60
	view := ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute).Tickets[0]
	if view.USD != "$0.01000000" || view.FirstDeliveryUSD != "Unknown" {
		t.Fatalf("a Run spanning merge was allocated without per-generation timing: %+v", view)
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
			t.Fatal("no-work selection was submitted for source evidence queries")
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

func TestNativeProviderCostKeepsUnknownZeroAndPartialDistinct(t *testing.T) {
	snapshot := onlyServedFixture()
	snapshot.History.Runs[0].TokensIn = 900
	snapshot.History.Runs[0].ProviderCost = nil
	view := ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute).Tickets[0]
	if view.USD != "Unknown" || view.Coverage != "unknown" || view.CostReported != 0 || view.CostUnknown != 1 {
		t.Fatalf("a Run without a reported charge became zero or an estimate: %+v", view)
	}
	if view.InputTokens != "900" {
		t.Fatalf("native token counts were discarded with the missing charge: %+v", view)
	}
	snapshot.History.Runs[0].ProviderCost = completeCost(0.0)
	view = ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute).Tickets[0]
	if view.USD != "$0.00000000" || view.Coverage != "complete" || view.CostReported != 1 || view.CostUnknown != 0 {
		t.Fatalf("explicit provider zero was lost: %+v", view)
	}
	snapshot.History.Runs[0].ProviderCost = partialCost(0.25)
	view = ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute).Tickets[0]
	if view.USD != "$0.25000000" || view.Coverage != "partial" || view.Runs[0].Coverage != "partial" {
		t.Fatalf("an open provider subtotal was reported as final: %+v", view)
	}
}

func TestMixedRunCoverageNeverImpliesACompleteSubtotal(t *testing.T) {
	snapshot := deliveryFixture()
	snapshot.Status.Recent = nil
	snapshot.History.Runs[1].ProviderCost = nil
	view := ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute).Tickets[0]
	if view.USD != "$0.01000000" || view.Coverage != "partial" || view.CostUnknown != 1 || view.FirstDeliveryUSD != "Unknown" {
		t.Fatalf("a known subtotal beside an unreported Run claimed completion: %+v", view)
	}
	snapshot.History.Runs[1].ProviderCost = completeCost(0.02)
	view = ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute).Tickets[0]
	if view.USD != "$0.03000000" || view.Coverage != "complete" || view.CostUnknown != 0 || view.FirstDeliveryUSD != "$0.03000000" {
		t.Fatalf("fully reported Runs did not complete the subtotal: %+v", view)
	}
	// A stale history clock can hide later Runs, so an accounting subtotal is
	// never promoted to complete from a window that may already be incomplete.
	stale := time.Now()
	snapshot.History.ObservedAt = stale.Add(-10 * time.Minute)
	projected := ticketDeliveryView(snapshot, false, stale, time.Minute)
	if projected.HistoryNotice == "" || projected.Tickets[0].USD != "$0.03000000" || projected.Tickets[0].Coverage != "partial" {
		t.Fatalf("expired history still claimed a complete charge subtotal: %+v", projected)
	}
}

func TestTrackerDoneAndNonPrimaryMergeDoNotEstablishObservedPrimaryMerge(t *testing.T) {
	snapshot := onlyServedFixture()
	item := snapshot.Delivery.Habitat.Items["ticket-a"]
	item.Status, item.PRURLs = "done", nil
	snapshot.Delivery.Habitat.Items["ticket-a"] = item
	view := ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute).Tickets[0]
	if view.Delivered || view.Latency != "Unknown" || view.MergeSHA != "" || view.Stage != "Evidence unavailable" {
		t.Fatalf("Done became independent merge/completion evidence: %+v", view)
	}
	for prURL, pull := range snapshot.Delivery.Forge.Pulls {
		item.PRURLs = append(item.PRURLs, prURL)
		pull.BaseRef = "release"
		snapshot.Delivery.Forge.Pulls[prURL] = pull
	}
	snapshot.Delivery.Habitat.Items["ticket-a"] = item
	view = ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute).Tickets[0]
	if view.Delivered || view.Latency != "Unknown" {
		t.Fatalf("wrong-primary merge became observed primary delivery: %+v", view)
	}
	prURL := "https://github.com/org/repo/pull/1"
	pull := snapshot.Delivery.Forge.Pulls[prURL]
	pull.BaseRef, pull.MergerType = "main", "Bot"
	snapshot.Delivery.Forge.Pulls[prURL] = pull
	view = ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute).Tickets[0]
	if !view.Delivered || view.MergeSHA != "merge-one" || view.MergerType != "Bot" || view.Stage == "Complete" {
		t.Fatalf("observed merge was either lost or mistaken for enforced human approval/completion: %+v", view)
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
	app.states[snapshot.Instance.ID] = &InstanceState{Snapshot: &snapshot, LastSuccess: time.Now()}
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

func reviewedFixture(decision string) Snapshot {
	snapshot := onlyServedFixture()
	state := snapshot.History.SourceObservation
	revision := strings.Repeat("a", 40)
	prURL := "https://github.com/org/repo/pull/2"
	item := snapshot.Delivery.Habitat.Items["ticket-a"]
	item.PRURL = prURL
	snapshot.Delivery.Habitat.Items[item.ID] = item
	verdictAt := time.Date(2026, 9, 8, 10, 10, 20, 0, time.UTC)
	identity := EvidenceIdentity{Name: "Forest Verifier", Email: "verifier@example.test", Time: verdictAt}
	review := ReviewEvidence{RunID: "run-1", VerifierRunID: "verify-1", Work: snapshot.History.Runs[0].Work, Revision: revision,
		Branch: "forest/VE-1/candidate", Decision: decision, Summary: "Exact candidate inspected",
		RequestRef: "refs/forest/v1/request/" + revision, ChecksRef: "refs/forest/v1/checks/" + revision, VerdictRef: "refs/forest/v1/verdict/" + revision,
		RequestState: "readable", ChecksState: "readable", VerdictState: "readable",
		RequestCommit: &EvidenceCommit{SHA: strings.Repeat("c", 40), Author: identity, Committer: identity},
		ChecksCommit:  &EvidenceCommit{SHA: strings.Repeat("d", 40), Author: identity, Committer: identity},
		VerdictCommit: &EvidenceCommit{SHA: strings.Repeat("e", 40), Author: identity, Committer: identity}}
	snapshot.Reviews = ReviewObservation{SourceObservation: state, Reviews: []ReviewEvidence{review}}
	snapshot.History.Runs = append(snapshot.History.Runs, RunData{RunID: "verify-1", Agent: "verifier",
		RequestID: item.ID + ":verifier:verify-1", Work: snapshot.History.Runs[0].Work,
		Started: "2026-09-08T10:10:00Z", Duration: 60, Outcome: "completed", ProcessExit: new(0),
		Completion: &CompletionData{Schema: "forest.completion.v1", Status: "completed", Evidence: review.VerdictRef}})
	snapshot.Reviews.Reviews[0].Runs = append([]RunData(nil), snapshot.History.Runs...)
	snapshot.Delivery.Forge.Pulls[prURL] = PullEvidence{SourceObservation: state, URL: prURL, State: "open", BaseRef: "main",
		HeadSHA: review.Revision, HeadRef: review.Branch, ApprovalSource: state}
	return snapshot
}

func TestCurrentReviewRejectsConflictingProvenanceAndWrongRevision(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Snapshot, *PullEvidence)
	}{
		{"wrong role", func(snapshot *Snapshot, _ *PullEvidence) { snapshot.History.Runs[1].Agent = "builder" }},
		{"no-work selection", func(snapshot *Snapshot, _ *PullEvidence) { snapshot.History.Runs[1].NoWork = true }},
		{"wrong work", func(snapshot *Snapshot, _ *PullEvidence) {
			snapshot.History.Runs[1].Work = &WorkRef{System: "https://habitat.example", ID: "ticket-b", Key: "VE-2"}
		}},
		{"wrong work system", func(snapshot *Snapshot, _ *PullEvidence) {
			work := *snapshot.History.Runs[1].Work
			work.System = "https://other-tracker.example"
			snapshot.History.Runs[1].Work = &work
		}},
		{"conflicting run provenance", func(snapshot *Snapshot, _ *PullEvidence) {
			run := snapshot.History.Runs[1]
			run.Work = &WorkRef{System: "https://habitat.example", ID: "ticket-b", Key: "VE-2"}
			snapshot.Status.Recent = []RunData{run}
		}},
		{"stale revision", func(_ *Snapshot, pull *PullEvidence) { pull.HeadSHA = strings.Repeat("b", 40) }},
		{"missing review", func(snapshot *Snapshot, _ *PullEvidence) { snapshot.Reviews.Reviews = nil }},
		{"missing verdict", func(snapshot *Snapshot, _ *PullEvidence) {
			snapshot.Reviews.Reviews[0].VerdictState = "missing"
			snapshot.Reviews.Reviews[0].VerdictCommit = nil
		}},
		{"malformed verdict", func(snapshot *Snapshot, _ *PullEvidence) {
			snapshot.Reviews.Reviews[0].VerdictState = "unreadable"
			snapshot.Reviews.Reviews[0].Errors = map[string]string{"verdict": "invalid verdict JSON"}
		}},
		{"unpublished verdict", func(snapshot *Snapshot, _ *PullEvidence) {
			snapshot.Reviews.Reviews[0].VerdictCommit = nil
		}},
		{"binding names builder", func(snapshot *Snapshot, _ *PullEvidence) {
			snapshot.Reviews.Reviews[0].VerifierRunID = "run-1"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := reviewedFixture("approve")
			prURL := snapshot.Delivery.Habitat.Items["ticket-a"].PRURL
			pull := snapshot.Delivery.Forge.Pulls[prURL]
			test.change(&snapshot, &pull)
			for i := range snapshot.Reviews.Reviews {
				snapshot.Reviews.Reviews[i].Runs = append([]RunData(nil), snapshot.History.Runs...)
			}
			snapshot.Delivery.Forge.Pulls[prURL] = pull
			ticket := ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute).Tickets[0]
			if ticket.Stage != "Awaiting verification" || ticket.ReviewWarning == "" {
				t.Fatalf("rejected or absent current review became approval or an unobserved source: %+v", ticket)
			}
		})
	}
}

func TestVerdictBindingDoesNotRequireLedgerRowsOrTimeCorrelation(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Snapshot)
	}{
		{"no ledger row", func(snapshot *Snapshot) {
			snapshot.Reviews.Reviews[0].Runs = nil
			snapshot.History.Runs = snapshot.History.Runs[:1]
		}},
		{"overlapping verifier", func(snapshot *Snapshot) {
			other := snapshot.History.Runs[1]
			other.RunID = "verify-2"
			snapshot.History.Runs = append(snapshot.History.Runs, other)
			snapshot.Reviews.Reviews[0].Runs = snapshot.History.Runs
		}},
		{"unrelated clock", func(snapshot *Snapshot) {
			snapshot.Reviews.Reviews[0].VerdictCommit.Committer.Time = time.Date(2026, 9, 8, 10, 15, 0, 0, time.UTC)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := reviewedFixture("approve")
			test.change(&snapshot)
			ticket := ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute).Tickets[0]
			if ticket.Stage != "Verified, awaiting merge" || ticket.ReviewDecision != "approve" || ticket.ReviewRunID != "verify-1" || ticket.ReviewWarning != "" {
				t.Fatalf("durable binding lost to optional Ledger correlation: %+v", ticket)
			}
		})
	}
}

func TestLegacyVerdictRemainsUnboundDespiteMatchingVerifier(t *testing.T) {
	snapshot := reviewedFixture("approve")
	// Decode a legacy surface with the request Run but no verifier_run_id field.
	raw, err := json.Marshal(snapshot.Reviews.Reviews[0])
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	delete(fields, "verifier_run_id")
	raw, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Reviews.Reviews[0] = ReviewEvidence{}
	if err := json.Unmarshal(raw, &snapshot.Reviews.Reviews[0]); err != nil {
		t.Fatal(err)
	}
	ticket := ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute).Tickets[0]
	if !strings.Contains(ticket.Stage, "unbound") || !strings.Contains(ticket.ReviewWarning, "unbound (legacy)") || ticket.ReviewRunID != "" || ticket.ReviewDecision != "approve" {
		t.Fatalf("legacy publication was lost or implicitly bound: %+v", ticket)
	}
}

func TestConflictingVerdictBindingDisplaysRejectedRunID(t *testing.T) {
	snapshot := reviewedFixture("approve")
	snapshot.Reviews.Reviews[0].VerifierRunID = "run-1"
	ticket := ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute).Tickets[0]
	if ticket.Stage != "Awaiting verification" || ticket.ReviewRunID != "run-1" || !strings.Contains(ticket.ReviewWarning, "not a Forest Verifier") {
		t.Fatalf("wrong bound identity was hidden or accepted: %+v", ticket)
	}
}

func TestChangesVerdictCompletesVerifierExecutionWithoutApprovingCandidate(t *testing.T) {
	snapshot := reviewedFixture("changes")
	view := ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute)
	ticket := view.Tickets[0]
	if ticket.Stage != "Changes requested" || ticket.ActionOwner != "Fixer" || !ticket.NeedsAttention || ticket.ReviewDecision != "changes" || ticket.ReviewWarning != "" {
		t.Fatalf("valid changes verdict was lost or promoted to approval: %+v", ticket)
	}
	if ticket.Runs[1].Completion != "completed" || ticket.Runs[1].CompletionEvidence != snapshot.Reviews.Reviews[0].VerdictRef || ticket.Runs[1].USD != "Unknown" ||
		ticket.Runs[1].Coverage != "unknown" || ticket.USDCompact != "$0.01" || ticket.Coverage != "partial" || ticket.CostUnknown != 1 {
		t.Fatalf("Verifier completion became missing work, quality failure, or free usage: %+v", ticket)
	}
}

func TestObservedMergeRequiresSeparateExactApprovalAndTrackerReconciliation(t *testing.T) {
	snapshot := reviewedFixture("approve")
	prURL := snapshot.Delivery.Habitat.Items["ticket-a"].PRURL
	pull := snapshot.Delivery.Forge.Pulls[prURL]
	pull.Merged, pull.State, pull.SHA = true, "closed", "current-merge"
	pull.MergedAt = new(time.Date(2026, 9, 8, 10, 20, 0, 0, time.UTC))
	pull.MergedBy, pull.MergerType = "shared-worker-account", "User"
	snapshot.Delivery.Forge.Pulls[prURL] = pull
	ticket := ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute).Tickets[0]
	if ticket.Stage != "Merged; reconciliation required" || !ticket.NeedsAttention || ticket.Tracker != "in_progress" || ticket.ReviewDecision != "approve" {
		t.Fatalf("merge incorrectly completed tracker work: %+v", ticket)
	}
	item := snapshot.Delivery.Habitat.Items["ticket-a"]
	item.Status = "done"
	snapshot.Delivery.Habitat.Items[item.ID] = item
	ticket = ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute).Tickets[0]
	if ticket.Stage != "Complete" || ticket.NeedsAttention {
		t.Fatalf("independent exact review, merge and Done evidence not recognized: %+v", ticket)
	}
	snapshot.Reviews.Reviews = nil
	ticket = ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute).Tickets[0]
	if ticket.Stage != "Merged; reconciliation required" || ticket.ReviewWarning == "" {
		t.Fatalf("User-type merger plus Done fabricated valid approval: %+v", ticket)
	}
}

func TestHistoricalMergeDoesNotHideReopenedCurrentCandidate(t *testing.T) {
	snapshot := reviewedFixture("approve")
	ticket := ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute).Tickets[0]
	if ticket.NeedsAttention || ticket.ActionOwner != "" || !ticket.Delivered || ticket.PRURL != "https://github.com/org/repo/pull/1" ||
		ticket.CurrentPRURL != "https://github.com/org/repo/pull/2" || ticket.HeadSHA != strings.Repeat("a", 40) ||
		ticket.MergeSHA != "merge-one" || ticket.FirstDeliveryUSD != "$0.01000000" {
		t.Fatalf("historical merge/cost erased or completed the reopened current candidate: %+v", ticket)
	}
	snapshot.Reviews.Reviews = nil
	ticket = ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute).Tickets[0]
	if ticket.Stage != "Awaiting verification" || !ticket.Delivered || ticket.FirstDeliveryUSD != "$0.01000000" {
		t.Fatalf("missing current review borrowed historic approval or erased historic cost: %+v", ticket)
	}
}

func TestStaleSourcesCannotKeepCurrentReadinessWhileFactsRemainVisible(t *testing.T) {
	for _, source := range []string{"tracker", "history", "pull", "reviews", "parent", "config", "declarations"} {
		t.Run(source, func(t *testing.T) {
			snapshot := reviewedFixture("approve")
			old := snapshot.History.ObservedAt.Add(-10 * time.Minute)
			item := snapshot.Delivery.Habitat.Items["ticket-a"]
			pull := snapshot.Delivery.Forge.Pulls[item.PRURL]
			switch source {
			case "tracker":
				item.ObservedAt = old
			case "history":
				snapshot.History.ObservedAt = old
			case "pull":
				pull.ObservedAt = old
			case "reviews":
				snapshot.Reviews.ObservedAt = old
			case "config":
				snapshot.ConfigObservation.ObservedAt = old
			case "declarations":
				snapshot.DeclarationsObservation.ObservedAt = old
			}
			snapshot.Delivery.Habitat.Items[item.ID] = item
			snapshot.Delivery.Forge.Pulls[item.PRURL] = pull
			ticket := ticketDeliveryView(snapshot, source == "parent", time.Date(2026, 9, 8, 11, 0, 0, 0, time.UTC), time.Minute).Tickets[0]
			if ticket.Stage != "Evidence unavailable" || !ticket.NeedsAttention || ticket.ReviewSHA != strings.Repeat("a", 40) || ticket.USD != "$0.01000000" {
				t.Fatalf("stale source retained readiness or erased last-good facts: %+v", ticket)
			}
		})
	}
}

func TestConflictingCostCannotClaimCompleteFirstDeliverySubtotal(t *testing.T) {
	snapshot := onlyServedFixture()
	conflict := snapshot.History.Runs[0]
	conflict.RunID, conflict.ProviderCost = "ambiguous-run", completeCost(10.0)
	snapshot.History.Runs = append(snapshot.History.Runs, conflict)
	snapshot.Delivery.Habitat.Links = []HabitatLink{{RunID: conflict.RunID, WorkItemID: "ticket-b", Relationship: "served"}}
	ticket := ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute).Tickets[0]
	if ticket.USD != "$0.01000000" || ticket.Coverage != "partial" || ticket.FirstDeliveryUSD != "Unknown" || ticket.Stage != "Evidence unavailable" {
		t.Fatalf("conflicting excluded Run produced a falsely complete first-delivery cost: %+v", ticket)
	}
}

func TestAttributedExecutionOutcomeAndCompletionRemainIndependent(t *testing.T) {
	snapshot := onlyServedFixture()
	var run RunData
	if err := json.Unmarshal([]byte(`{"run_id":"run-1","agent":"builder","started":"2026-09-08T09:00:00Z","duration":60,"exit":1,"process_exit":0,"outcome":"provider_failed","completion":{"schema":"forest.completion.v1","status":"incomplete","reason":"Candidate PR was not published"}}`), &run); err != nil {
		t.Fatal(err)
	}
	run.Work = snapshot.History.Runs[0].Work
	snapshot.History.Runs[0] = run
	ticket := ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute).Tickets[0]
	row := ticket.Runs[0]
	if !strings.Contains(row.ProcessOutcome, "provider failed") || !strings.Contains(row.ProcessOutcome, "Pi exit 0") ||
		row.Completion != "incomplete" || row.CompletionEvidence != "Candidate PR was not published" || ticket.FailedRuns != 1 {
		t.Fatalf("raw Pi exit replaced attributed execution/completion failure: %+v", ticket)
	}
	run.Outcome, run.ProcessExit, run.Completion = "", nil, nil
	snapshot.History.Runs[0] = run
	row = ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute).Tickets[0].Runs[0]
	if !strings.Contains(row.ProcessOutcome, "unknown") || row.Completion != "not observed" {
		t.Fatalf("legacy absence invented a failure cause or completion: %+v", row)
	}
}

func TestUnobservedCompletionMakesLatestRelevantAttemptOperatorOwned(t *testing.T) {
	snapshot := reviewedFixture("approve")
	snapshot.Reviews.Reviews = nil
	verifier := &snapshot.History.Runs[len(snapshot.History.Runs)-1]
	verifier.Completion = &CompletionData{Schema: "forest.completion.v1", Status: "unknown", Reason: "Completion observer failed"}
	ticket := ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute).Tickets[0]
	if ticket.Stage != "Awaiting verification" || ticket.ActionOwner != "Operator" || !ticket.NeedsAttention ||
		ticket.Runs[1].Completion != "unknown" {
		t.Fatalf("unobserved completion did not request operator inspection: %+v", ticket)
	}
	verifier.Completion = &CompletionData{Schema: "forest.completion.v1", Status: "completed", Evidence: "refs/forest/v1/verdict/" + strings.Repeat("a", 40)}
	ticket = ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute).Tickets[0]
	if ticket.Stage != "Awaiting verification" || ticket.ActionOwner != "Verifier" || ticket.NeedsAttention {
		t.Fatalf("an observed-complete attempt was reported as paused: %+v", ticket)
	}
	verifier.Completion = nil
	ticket = ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute).Tickets[0]
	if ticket.ActionOwner != "Verifier" || ticket.NeedsAttention || ticket.Runs[1].Completion != "not observed" {
		t.Fatalf("absence without a configured observer was reported as a known pause: %+v", ticket)
	}
	snapshot.Declarations = []DeclarationData{{Name: "verifier", Completion: ".iron-forest/bin/completion"}}
	ticket = ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute).Tickets[0]
	if ticket.ActionOwner != "Operator" || !ticket.NeedsAttention || ticket.Runs[1].Completion != "not observed" {
		t.Fatalf("a configured observer producing no observation did not surface the paused attempt: %+v", ticket)
	}
	latest := *verifier
	latest.RunID, latest.RequestID, latest.Started = "verify-2", "ticket-a:verifier:verify-2", "2026-09-08T10:20:00Z"
	latest.Completion = &CompletionData{Schema: "forest.completion.v1", Status: "completed", Evidence: "Current candidate reviewed"}
	snapshot.History.Runs = append(snapshot.History.Runs, latest)
	snapshot.History.Runs[0].Outcome = "provider_failed"
	snapshot.History.Runs[0].Completion = &CompletionData{Schema: "forest.completion.v1", Status: "unknown", Reason: "Historical Builder observation unavailable"}
	ticket = ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute).Tickets[0]
	if ticket.ActionOwner != "Verifier" || ticket.NeedsAttention || ticket.Stage != "Awaiting verification" {
		t.Fatalf("a superseded Verifier or unrelated Builder failure overrode the latest completed Verifier: %+v", ticket)
	}
}

func TestReviewTrustUsesKernelIdentityRatherThanRequestNamingConvention(t *testing.T) {
	snapshot := reviewedFixture("approve")
	snapshot.History.Runs[1].RequestID = "another-profile/verification/batch-7"
	ticket := ticketDeliveryView(snapshot, false, snapshot.History.ObservedAt, time.Minute).Tickets[0]
	if ticket.ReviewWarning != "" || ticket.ReviewRunID != snapshot.History.Runs[1].RunID {
		t.Fatalf("opaque Kernel request id blocked otherwise valid exact Run/work/revision evidence: %+v", ticket)
	}
}

func TestCurrentExecutionSurvivesUnavailableOptionalEvidence(t *testing.T) {
	now := time.Now().UTC()
	work := &WorkRef{System: "https://habitat.example", ID: "change", Key: "CHANGE-1"}
	snapshot := Snapshot{Status: StatusData{LiveRuns: []LiveRunData{{
		RunID: "attempt", Agent: "builder", RequestID: "explicit-request", Work: work,
		StartedAt: now.Format(time.RFC3339Nano), Elapsed: "12s",
	}}}}
	ticket := ticketDeliveryView(snapshot, false, now, time.Minute).Tickets[0]
	if !ticket.CurrentKnown || len(ticket.CurrentRuns) != 1 || ticket.CurrentRuns[0].RequestID != "explicit-request" || ticket.CurrentRuns[0].Duration != "12s" {
		t.Fatalf("missing optional details hid actual current execution: %+v", ticket)
	}
	if ticket.Stage != "In progress" || ticket.HeadSHA != "" || ticket.ReviewDecision != "" || ticket.Coverage != "unknown" {
		t.Fatalf("current execution invented optional evidence: %+v", ticket)
	}
	snapshot.Status.LiveRuns = nil
	snapshot.Status.Recent = []RunData{{RunID: "attempt", Agent: "builder", RequestID: "explicit-request",
		Work: work, Started: now.Format(time.RFC3339Nano), Exit: 130, Error: "cancelled"}}
	ticket = ticketDeliveryView(snapshot, false, now, time.Minute).Tickets[0]
	if len(ticket.CurrentRuns) != 0 || ticket.Runs[0].Cancelled || ticket.Stage == "Complete" || ticket.ReviewDecision != "" {
		t.Fatalf("legacy exit or error text became a work verdict: %+v", ticket)
	}
	snapshot.Status.Recent[0].Outcome = "cancelled"
	snapshot.Status.Recent[0].Recovery = &RecoveryData{Path: ".iron-forest/runtime/worktrees/attempt", BaseRevision: strings.Repeat("b", 40)}
	ticket = ticketDeliveryView(snapshot, false, now, time.Minute).Tickets[0]
	if !ticket.Runs[0].Cancelled || ticket.Runs[0].Recovery == nil || !ticket.NeedsAttention || ticket.ActionOwner != "Operator" || ticket.HeadSHA != "" {
		t.Fatalf("explicit cancellation lost recovery or promoted its base to a candidate: %+v", ticket)
	}
	snapshot.Status.LiveRunError = "live observation unavailable"
	ticket = ticketDeliveryView(snapshot, false, now, time.Minute).Tickets[0]
	if ticket.CurrentKnown {
		t.Fatal("failed live observation became known inactivity")
	}
}

func TestSingleObservedChangeOpensWithoutBroadeningUnknownSelection(t *testing.T) {
	now := time.Now()
	snapshot := Snapshot{Status: StatusData{LiveRuns: []LiveRunData{{
		RunID: "run", Work: &WorkRef{System: "test-system", ID: "only-change"},
	}}}}
	view := instanceView(Instance{ID: "one"}, InstanceState{Snapshot: &snapshot, LastSuccess: now}, true, now, time.Minute)
	if view.SelectedTicket == nil || view.SelectedWork != "only-change" {
		t.Fatal("sole observed change did not open")
	}
	request := httptest.NewRequest("GET", "/?system=test-system&work=not-observed", nil)
	if err := selectWork(&view, request); err != nil || view.SelectedTicket != nil || view.SelectedWork != "not-observed" {
		t.Fatalf("unknown explicit selection borrowed the default's evidence: %+v, %v", view, err)
	}
}
