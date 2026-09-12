package main

import (
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"
	"time"
)

// WorkRef is opaque Forest provenance, not a title/branch heuristic.
type WorkRef struct {
	System string `json:"system"`
	ID     string `json:"id"`
	Key    string `json:"key,omitempty"`
	URL    string `json:"url,omitempty"`
}

type SourceObservation struct {
	ObservedAt time.Time `json:"observed_at"`
	Error      string    `json:"error,omitempty"`
}

type RunHistory struct {
	SourceObservation
	Runs []RunData `json:"runs"`
}

type HabitatItem struct {
	SourceObservation
	ID             string   `json:"id"`
	Key            string   `json:"external_id"`
	Title          string   `json:"title"`
	Status         string   `json:"status"`
	URL            string   `json:"url"`
	PRURL          string   `json:"pr_url"`
	PRURLs         []string `json:"observed_pr_urls,omitempty"`
	HistoryWarning string   `json:"history_warning,omitempty"`
}

type HabitatLink struct {
	RunID        string       `json:"run_id"`
	WorkItemID   string       `json:"work_item_id"`
	Relationship string       `json:"relationship"`
	Item         *HabitatItem `json:"work_item"`
}

type HabitatObservation struct {
	SourceObservation
	Scope string                 `json:"scope"`
	Links []HabitatLink          `json:"links"`
	Items map[string]HabitatItem `json:"items"`
}

type PullEvidence struct {
	SourceObservation
	URL             string            `json:"url"`
	State           string            `json:"state"`
	BaseRef         string            `json:"base_ref"`
	HeadSHA         string            `json:"head_sha"`
	HeadRef         string            `json:"head_ref"`
	Merged          bool              `json:"merged"`
	MergedAt        *time.Time        `json:"merged_at"`
	SHA             string            `json:"sha"`
	MergedBy        string            `json:"merged_by"`
	MergerType      string            `json:"merger_type"`
	ApprovalSource  SourceObservation `json:"approval_source"`
	Approvals       []ForgeApproval   `json:"approvals"`
	ApprovalWarning string            `json:"approval_warning,omitempty"`
}

// ReviewObservation is the published immutable evidence read by Forest.
// Missing and unreadable pieces remain distinct; neither implies approval.
type ReviewObservation struct {
	SourceObservation
	Reviews []ReviewEvidence `json:"reviews"`
}

type EvidenceIdentity struct {
	Name  string    `json:"name"`
	Email string    `json:"email"`
	Time  time.Time `json:"time"`
}

type EvidenceCommit struct {
	SHA       string           `json:"sha"`
	Author    EvidenceIdentity `json:"author"`
	Committer EvidenceIdentity `json:"committer"`
}

type ReviewEvidence struct {
	Revision      string            `json:"revision"`
	Branch        string            `json:"branch"`
	Work          *WorkRef          `json:"work"`
	Decision      string            `json:"decision"`
	Summary       string            `json:"summary"`
	RunID         string            `json:"run_id"`
	RequestRef    string            `json:"request_ref"`
	ChecksRef     string            `json:"checks_ref"`
	VerdictRef    string            `json:"verdict_ref"`
	RequestCommit *EvidenceCommit   `json:"request_commit"`
	ChecksCommit  *EvidenceCommit   `json:"checks_commit"`
	VerdictCommit *EvidenceCommit   `json:"verdict_commit"`
	RequestState  string            `json:"request_state"`
	ChecksState   string            `json:"checks_state"`
	VerdictState  string            `json:"verdict_state"`
	Errors        map[string]string `json:"errors"`
	Runs          []RunData         `json:"runs"`
}

// ForgeApproval is an observed account action, not proof that a capability-
// enforced human gate exists or that the worker lacks the same account.
type ForgeApproval struct {
	Author      string    `json:"author"`
	AuthorType  string    `json:"author_type"`
	Revision    string    `json:"revision"`
	URL         string    `json:"url"`
	SubmittedAt time.Time `json:"submitted_at"`
}

// PrimaryEvidence proves reachability, not a merge event, actor or timestamp.
type PrimaryEvidence struct {
	SourceObservation
	Contained bool `json:"contained"`
}

type ForgeObservation struct {
	SourceObservation
	Pulls   map[string]PullEvidence    `json:"pulls"`
	Primary map[string]PrimaryEvidence `json:"primary"`
}

type DeliverySources struct {
	AttemptedAt time.Time          `json:"attempted_at"`
	Habitat     HabitatObservation `json:"habitat"`
	Forge       ForgeObservation   `json:"forge"`
}

func retainRunHistory(next, previous RunHistory) RunHistory {
	if next.Error != "" && !previous.ObservedAt.IsZero() {
		err := next.Error
		next = previous
		next.Error = err
	}
	return next
}

func retainDeliverySources(next, previous DeliverySources) DeliverySources {
	if next.Habitat.Error != "" {
		err := next.Habitat.Error
		next.Habitat = previous.Habitat
		next.Habitat.Error = err
	}
	return next
}

type attributedRun struct {
	ID, Agent, RequestID string
	Started              string
	Elapsed              string
	Duration             *float64
	Exit                 *int
	Error                string
	NoWork               bool
	ProcessExit          *int
	Outcome              string
	Completion           *CompletionData
	Recovery             *RecoveryData
	Native               *RunData
}

func attributedRuns(snapshot Snapshot) map[string]attributedRun {
	runs := make(map[string]attributedRun, len(snapshot.History.Runs)+len(snapshot.Status.Recent)+len(snapshot.Status.LiveRuns))
	addCompleted := func(run RunData, history bool) {
		if run.RunID == "" {
			return
		}
		if retained, exists := runs[run.RunID]; exists {
			if retained.RequestID == "" {
				retained.RequestID = run.RequestID
			}
			retained.NoWork = retained.NoWork || run.NoWork
			if retained.Outcome == "" {
				retained.Outcome = run.Outcome
			}
			if retained.ProcessExit == nil {
				retained.ProcessExit = run.ProcessExit
			}
			if retained.Completion == nil {
				retained.Completion = run.Completion
			}
			if retained.Recovery == nil {
				retained.Recovery = run.Recovery
			}
			runs[run.RunID] = retained
			return
		}
		runs[run.RunID] = attributedRun{ID: run.RunID, Agent: run.Agent, RequestID: run.RequestID,
			Started: run.Started, Duration: &run.Duration, Exit: &run.Exit, Error: run.Error, NoWork: run.NoWork,
			ProcessExit: run.ProcessExit, Outcome: run.Outcome, Completion: run.Completion, Recovery: run.Recovery}
		if history {
			retained := runs[run.RunID]
			retained.Native = &run
			runs[run.RunID] = retained
		}
	}
	for _, run := range snapshot.History.Runs {
		addCompleted(run, true)
	}
	for _, run := range snapshot.Status.Recent {
		addCompleted(run, false)
	}
	for _, run := range snapshot.Status.LiveRuns {
		if run.RunID == "" {
			continue
		}
		if completed, exists := runs[run.RunID]; exists {
			if completed.RequestID == "" {
				completed.RequestID = run.RequestID
				runs[run.RunID] = completed
			}
			continue
		}
		runs[run.RunID] = attributedRun{ID: run.RunID, Agent: run.Agent, RequestID: run.RequestID, Started: run.StartedAt,
			Elapsed: run.Elapsed, ProcessExit: run.ProcessExit, Outcome: run.Outcome, Completion: run.Completion, Recovery: run.Recovery}
	}
	for id, run := range runs {
		if run.NoWork || run.Outcome == "no_work" {
			delete(runs, id)
		}
	}
	return runs
}

// Visit every projection rather than letting a recent/live row erase retained
// provenance. Contradictory identities remain candidates and fail attribution.
func visitRunWork(snapshot Snapshot, visit func(string, WorkRef)) {
	add := func(id string, work *WorkRef) {
		if id != "" && work != nil && work.System != "" && work.ID != "" {
			visit(id, *work)
		}
	}
	for _, run := range snapshot.History.Runs {
		add(run.RunID, run.Work)
	}
	for _, run := range snapshot.Status.Recent {
		add(run.RunID, run.Work)
	}
	for _, run := range snapshot.Status.LiveRuns {
		add(run.RunID, run.Work)
	}
}

func snapshotRunIDs(snapshot Snapshot) []string {
	seen := make(map[string]bool, len(snapshot.History.Runs)+len(snapshot.Status.LiveRuns))
	for _, run := range snapshot.History.Runs {
		if run.RunID != "" {
			seen[run.RunID] = seen[run.RunID] || run.NoWork || run.Outcome == "no_work"
		}
	}
	for _, run := range snapshot.Status.Recent {
		if run.RunID != "" {
			seen[run.RunID] = seen[run.RunID] || run.NoWork || run.Outcome == "no_work"
		}
	}
	for _, run := range snapshot.Status.LiveRuns {
		if _, exists := seen[run.RunID]; !exists && run.RunID != "" {
			seen[run.RunID] = run.Outcome == "no_work"
		}
	}
	ids := make([]string, 0, len(seen))
	for id, noWork := range seen {
		if !noWork {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

type EvidenceSourceView struct {
	Name, State, Observed, Message string
}

type TicketRunView struct {
	ID, Agent, RequestID, Outcome, Duration, Started              string
	ProcessOutcome, Completion, CompletionEvidence, USD, Coverage string
	Cancelled                                                     bool
	Recovery                                                      *RecoveryData
}

type TicketView struct {
	ID, System, Key, URL, Title                                                     string
	Tracker, TrackerFreshness, TrackerObserved                                      string
	Delivery, DeliveryClass, PRURL, MergeTime, MergeSHA, Merger, MergeFreshness     string
	Started, Latency, ObservedLatency, Duration, Coverage                           string
	InputTokens, OutputTokens, CacheRead, CacheWrite, Reasoning, USD                string
	FirstDeliveryUSD                                                                string
	Runs                                                                            []TicketRunView
	CurrentRuns                                                                     []TicketRunView
	CurrentKnown                                                                    bool
	CreatedRuns                                                                     []string
	RunCount, FailedRuns, LiveRuns, CostReported, CostUnknown                       int
	Notes                                                                           []string
	Delivered                                                                       bool
	Stage, StageClass, NextAction, ActionOwner                                      string
	ReviewDecision, ReviewSHA, ReviewRunID, ReviewURL, ReviewSummary, ReviewWarning string
	ReviewAuthor, ReviewAuthorType, ReviewAuthorAssociation                         string
	CurrentPRURL, HeadSHA, USDCompact, MergerType                                   string
	PRState, HeadRef                                                                string
	AccountApprovals                                                                []ForgeApproval
	ApprovalFreshness                                                               string
	NeedsAttention                                                                  bool
}

type TicketDeliveryView struct {
	Tickets                                          []TicketView
	Sources                                          []EvidenceSourceView
	UnattributedRuns, ConflictingRuns, Delivered     int
	NeedsAttention, InProgress                       int
	HistoryNotice                                    string
	UnassignedUSD, UnassignedInput, UnassignedOutput string
}

type ticketIdentity struct{ System, ID string }

type ticketAggregate struct {
	Work        WorkRef
	Runs        map[string]attributedRun
	Created     map[string]bool
	Notes       []string
	Conflicting bool
}

func ticketDeliveryView(snapshot Snapshot, parentStale bool, now time.Time, maxAge time.Duration) TicketDeliveryView {
	view := TicketDeliveryView{}
	sources := snapshot.Instance.Sources
	observation := snapshot.Delivery
	view.Sources = append(view.Sources, evidenceSourceView("Run history", true, snapshot.History.SourceObservation, parentStale, now, sourceRefreshInterval+refreshTimeout+freshnessSchedulingSlack, "All Run pages, including nonzero exits and any provider charge the Run reports"))
	view.Sources = append(view.Sources, evidenceSourceView("Habitat", sources.Habitat != nil, observation.Habitat.SourceObservation, parentStale, now, sourceRefreshInterval+refreshTimeout+freshnessSchedulingSlack, observation.Habitat.Scope))
	view.Sources = append(view.Sources, evidenceSourceView("Forge", sources.Forge != nil, observation.Forge.SourceObservation, parentStale, now, sourceRefreshInterval+refreshTimeout+freshnessSchedulingSlack, "Exact published candidate heads; merge to declared primary is not deployment"))
	view.Sources = append(view.Sources, evidenceSourceView("Forest reviews", true, snapshot.Reviews.SourceObservation, parentStale, now, sourceRefreshInterval+refreshTimeout+freshnessSchedulingSlack, "Published request, checks and verdict refs; never PR-comment authority"))
	if view.Sources[0].State != "fresh" {
		view.HistoryNotice = "Run history is incomplete or stale. Counts, first start, duration and cost cover observed Runs only."
	}
	runs := attributedRuns(snapshot)
	tickets := make(map[ticketIdentity]*ticketAggregate)
	ensure := func(work WorkRef) *ticketAggregate {
		key := ticketIdentity{System: work.System, ID: work.ID}
		aggregate := tickets[key]
		if aggregate == nil {
			aggregate = &ticketAggregate{Work: work, Runs: make(map[string]attributedRun), Created: make(map[string]bool)}
			tickets[key] = aggregate
		}
		if aggregate.Work.Key == "" {
			aggregate.Work.Key = work.Key
		}
		if aggregate.Work.URL == "" {
			aggregate.Work.URL = work.URL
		}
		return aggregate
	}
	for _, review := range snapshot.Reviews.Reviews {
		if review.Work != nil && review.Work.System != "" && review.Work.ID != "" {
			ensure(*review.Work)
		}
	}
	candidates := make(map[string]map[ticketIdentity]bool)
	addCandidate := func(runID string, work WorkRef) {
		ensure(work)
		if candidates[runID] == nil {
			candidates[runID] = make(map[ticketIdentity]bool)
		}
		candidates[runID][ticketIdentity{work.System, work.ID}] = true
	}
	visitRunWork(snapshot, addCandidate)
	if sources.Habitat != nil {
		for _, id := range sources.Habitat.WorkItemIDs {
			item := observation.Habitat.Items[id]
			ensure(WorkRef{System: sources.Habitat.System, ID: id, Key: item.Key, URL: item.URL})
		}
		for _, link := range observation.Habitat.Links {
			if _, observed := runs[link.RunID]; !observed || link.WorkItemID == "" {
				continue
			}
			item := observation.Habitat.Items[link.WorkItemID]
			work := WorkRef{System: sources.Habitat.System, ID: link.WorkItemID, Key: item.Key, URL: item.URL}
			if link.Relationship == "served" {
				addCandidate(link.RunID, work)
			} else if link.Relationship == "created" {
				ensure(work).Created[link.RunID] = true
			}
		}
	}
	var unassignedInput, unassignedOutput *int64
	var unassignedCost *float64
	for id, run := range runs {
		switch len(candidates[id]) {
		case 0:
			view.UnattributedRuns++
		case 1:
			for key := range candidates[id] {
				tickets[key].Runs[id] = run
			}
		default:
			view.ConflictingRuns++
			for key := range candidates[id] {
				tickets[key].Conflicting = true
				tickets[key].Notes = append(tickets[key].Notes, "Conflicting primary association for Run "+id+"; its duration and usage are not charged to either ticket")
			}
		}
		if len(candidates[id]) != 1 {
			if native := run.Native; native != nil {
				unassignedInput = addKnownTokens(unassignedInput, &native.TokensIn)
				unassignedOutput = addKnownTokens(unassignedOutput, &native.TokensOut)
				if native.ProviderCost.coverage() != "unknown" {
					if unassignedCost == nil {
						unassignedCost = new(0.0)
					}
					*unassignedCost += *native.ProviderCost.CostUSD
				}
			}
		}
	}
	view.UnassignedInput, view.UnassignedOutput = formatKnownTokens(unassignedInput), formatKnownTokens(unassignedOutput)
	view.UnassignedUSD = "Unknown"
	if unassignedCost != nil {
		view.UnassignedUSD = fmt.Sprintf("$%.8f", *unassignedCost)
	}
	for _, aggregate := range tickets {
		ticket := projectTicket(*aggregate, snapshot, parentStale, now, maxAge)
		if ticket.Delivered {
			view.Delivered++
		}
		if ticket.NeedsAttention {
			view.NeedsAttention++
		}
		if ticket.Stage == "In progress" {
			view.InProgress++
		}
		view.Tickets = append(view.Tickets, ticket)
	}
	sort.Slice(view.Tickets, func(i, j int) bool {
		a, b := view.Tickets[i], view.Tickets[j]
		if a.System != b.System {
			return a.System < b.System
		}
		if a.Key != b.Key {
			return a.Key < b.Key
		}
		return a.ID < b.ID
	})
	return view
}

func evidenceSourceView(name string, configured bool, observation SourceObservation, parentStale bool, now time.Time, maxAge time.Duration, message string) EvidenceSourceView {
	view := EvidenceSourceView{Name: name, State: "unknown", Message: message}
	if !configured {
		view.Message = "Optional source not configured"
		return view
	}
	if observation.Error != "" {
		view.Message = observation.Error
	}
	if observation.ObservedAt.IsZero() {
		if observation.Error == "" {
			view.Message = "Waiting for source observation"
		}
		return view
	}
	view.Observed = formatTime(observation.ObservedAt)
	view.State = "fresh"
	if parentStale || observation.Error != "" || !now.Before(observation.ObservedAt.Add(maxAge)) {
		view.State = "stale"
	}
	return view
}

func safeExternalURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return ""
	}
	return raw
}

func projectTicket(aggregate ticketAggregate, snapshot Snapshot, parentStale bool, now time.Time, maxAge time.Duration) TicketView {
	view := TicketView{ID: aggregate.Work.ID, System: aggregate.Work.System, Key: aggregate.Work.Key,
		URL: safeExternalURL(aggregate.Work.URL), Tracker: "Unknown", TrackerFreshness: "unknown", Delivery: "Unknown — no independently observed PR", DeliveryClass: "unknown",
		FirstDeliveryUSD: "Unknown",
		Started:          "Unknown", Latency: "Unknown", ObservedLatency: "Unknown", Duration: "Unknown", Coverage: "unknown", MergeTime: "Unknown", Notes: aggregate.Notes}
	view.CurrentKnown = !parentStale && snapshot.Status.LiveRunError == ""
	if view.Key == "" {
		view.Key = view.ID
	}
	var item HabitatItem
	if source := snapshot.Instance.Sources.Habitat; source != nil && source.System == aggregate.Work.System {
		item = snapshot.Delivery.Habitat.Items[view.ID]
		if item.Key != "" {
			view.Key = item.Key
		}
		if item.URL != "" {
			view.URL = safeExternalURL(item.URL)
		}
		view.Title = item.Title
		if item.Status != "" {
			view.Tracker = item.Status
		}
		state := evidenceSourceView("", true, item.SourceObservation, parentStale || snapshot.Delivery.Habitat.Error != "", now, sourceRefreshInterval+refreshTimeout+freshnessSchedulingSlack, "")
		view.TrackerFreshness, view.TrackerObserved = state.State, state.Observed
		if item.Error != "" {
			view.Notes = append(view.Notes, "Tracker: "+item.Error)
		}
		if item.HistoryWarning != "" {
			view.Notes = append(view.Notes, item.HistoryWarning)
		}
	}
	// Published work identity scopes the join. A branch match with a changed
	// SHA is useful stale evidence, never approval of the new PR head.
	item.PRURLs = append([]string(nil), item.PRURLs...)
	var current []string
	for prURL, pull := range snapshot.Delivery.Forge.Pulls {
		for _, candidate := range snapshot.Reviews.Reviews {
			if candidate.Work == nil || candidate.Work.System != aggregate.Work.System || candidate.Work.ID != aggregate.Work.ID ||
				candidate.RequestState != "readable" || pull.ObservedAt.IsZero() {
				continue
			}
			if pull.HeadSHA == candidate.Revision {
				item.PRURLs = append(item.PRURLs, prURL)
				current = append(current, prURL)
				break
			}
			if pull.HeadRef == strings.TrimPrefix(candidate.Branch, "refs/heads/") {
				view.Notes = append(view.Notes, "Published candidate is stale; the PR head changed: "+prURL)
			}
		}
	}
	sort.Strings(current)
	if len(current) == 1 {
		item.PRURL = current[0]
	} else if len(current) > 1 {
		item.PRURL = ""
		view.Notes = append(view.Notes, "Multiple published candidates match this work; current PR is ambiguous")
	}
	var firstStart *time.Time
	startTimesComplete := true
	var duration float64
	completed := 0
	var input, output, cacheRead, cacheWrite, reasoning *int64
	var cost *float64
	completeUsage := 0
	ids := make([]string, 0, len(aggregate.Runs))
	for id := range aggregate.Runs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		run := aggregate.Runs[id]
		row := TicketRunView{ID: id, Agent: run.Agent, RequestID: run.RequestID, Outcome: "live", Duration: "in progress", Started: run.Started,
			ProcessOutcome: processOutcome(run.Outcome, run.ProcessExit), USD: "Unknown", Coverage: "unknown"}
		row.Completion, row.CompletionEvidence = completionView(run.Completion)
		row.Cancelled, row.Recovery = run.Outcome == "cancelled", run.Recovery
		if started, err := time.Parse(time.RFC3339Nano, run.Started); err == nil {
			if firstStart == nil || started.Before(*firstStart) {
				firstStart = &started
			}
		} else {
			startTimesComplete = false
			view.Notes = append(view.Notes, "Run "+id+" has no valid start time; first start may be incomplete")
		}
		if run.Exit != nil {
			row.Outcome = fmt.Sprintf("exit %d", *run.Exit)
			if *run.Exit != 0 {
				view.FailedRuns++
			}
			if run.Duration != nil && *run.Duration >= 0 && !math.IsNaN(*run.Duration) && !math.IsInf(*run.Duration, 0) {
				duration += *run.Duration
				completed++
				row.Duration = formatDuration(*run.Duration)
			} else {
				row.Duration = "unknown"
			}
		} else {
			view.LiveRuns++
			row.Duration = run.Elapsed
			if row.Duration == "" {
				row.Duration = "unknown"
			}
			if run.Outcome != "" {
				// The model attempt finished while Kernel finalization continues;
				// that is neither a completed execution nor observed delivery.
				row.Outcome = "finalizing"
			}
		}
		if run.Error != "" {
			row.Outcome += ": " + run.Error
		}
		if native := run.Native; native != nil {
			row.Coverage = native.ProviderCost.coverage()
			if row.Coverage != "unknown" {
				row.USD = fmt.Sprintf("$%.8f", *native.ProviderCost.CostUSD)
			}
		}
		if run.Exit == nil {
			view.CurrentRuns = append(view.CurrentRuns, row)
		}
		view.Runs = append(view.Runs, row)
		if row.Coverage == "unknown" {
			view.CostUnknown++
		} else {
			view.CostReported++
		}
		native := run.Native
		if native == nil {
			continue
		}
		if row.Coverage == "complete" {
			completeUsage++
		}
		input = addKnownTokens(input, &native.TokensIn)
		output = addKnownTokens(output, &native.TokensOut)
		cacheRead = addKnownTokens(cacheRead, &native.CacheRead)
		cacheWrite = addKnownTokens(cacheWrite, &native.CacheWrite)
		reasoning = addKnownTokens(reasoning, &native.Reasoning)
		if row.Coverage != "unknown" {
			if cost == nil {
				cost = new(0.0)
			}
			*cost += *native.ProviderCost.CostUSD
		}
	}
	view.RunCount = len(ids)
	if completed > 0 {
		view.Duration = formatDuration(duration)
	}
	if completed != view.RunCount && view.RunCount > 0 {
		view.Duration += " (completed subtotal)"
	}
	if firstStart != nil {
		view.Started = formatTime(*firstStart)
	}
	historyFresh := evidenceSourceView("", true, snapshot.History.SourceObservation, parentStale, now, sourceRefreshInterval+refreshTimeout+freshnessSchedulingSlack, "").State == "fresh"
	if completeUsage == view.RunCount && view.RunCount > 0 && !aggregate.Conflicting && historyFresh {
		view.Coverage = "complete"
	} else if cost != nil {
		view.Coverage = "partial"
	}
	view.InputTokens, view.OutputTokens = formatKnownTokens(input), formatKnownTokens(output)
	view.CacheRead, view.CacheWrite, view.Reasoning = formatKnownTokens(cacheRead), formatKnownTokens(cacheWrite), formatKnownTokens(reasoning)
	view.USD = "Unknown"
	view.USDCompact = "Unknown"
	if cost != nil {
		view.USD = fmt.Sprintf("$%.8f", *cost)
		view.USDCompact = compactUSD(*cost)
	}
	for id := range aggregate.Created {
		view.CreatedRuns = append(view.CreatedRuns, id)
	}
	sort.Strings(view.CreatedRuns)
	if len(view.CreatedRuns) > 0 {
		view.Notes = append(view.Notes, "Created links are provenance only; they do not assign served duration or cost")
	}
	if view.RunCount == 0 {
		view.Notes = append(view.Notes, "No unambiguous served Run is attributed to this ticket")
	}
	var firstMerged *PullEvidence
	firstDeliveryKnown := startTimesComplete && !aggregate.Conflicting && historyFresh && view.TrackerFreshness == "fresh" && item.HistoryWarning == ""
	urls := make(map[string]bool, len(item.PRURLs)+1)
	for _, prURL := range item.PRURLs {
		urls[prURL] = true
	}
	if item.PRURL != "" {
		urls[item.PRURL] = true
	}
	for prURL := range urls {
		pull, exists := snapshot.Delivery.Forge.Pulls[prURL]
		if !exists {
			firstDeliveryKnown = false
			continue
		}
		if evidenceSourceView("", true, pull.SourceObservation, parentStale, now, sourceRefreshInterval+refreshTimeout+freshnessSchedulingSlack, "").State != "fresh" {
			firstDeliveryKnown = false
		}
		if pull.Error != "" {
			view.Notes = append(view.Notes, "Forge: "+pull.Error)
		}
		if pull.ApprovalWarning != "" {
			view.Notes = append(view.Notes, pull.ApprovalWarning)
		}
		if pull.ObservedAt.IsZero() {
			continue
		}
		if pull.BaseRef != strings.TrimPrefix(snapshot.Config.Primary, "refs/heads/") {
			view.Notes = append(view.Notes, "PR does not target the declared primary; not counted as an observed primary merge")
			continue
		}
		if pull.Merged && pull.MergedAt != nil && pull.SHA != "" && (firstMerged == nil || pull.MergedAt.Before(*firstMerged.MergedAt)) {
			firstMerged = new(pull)
		}
	}
	if selected := firstMerged; selected != nil {
		view.Delivery, view.DeliveryClass, view.Delivered = "Observed merge to primary", "ok", true
		view.PRURL, view.MergeSHA, view.Merger, view.MergerType = safeExternalURL(selected.URL), selected.SHA, selected.MergedBy, selected.MergerType
		view.MergeTime = formatTime(*selected.MergedAt)
		view.MergeFreshness = evidenceSourceView("", true, selected.SourceObservation, parentStale, now, sourceRefreshInterval+refreshTimeout+freshnessSchedulingSlack, "").State
		if firstStart != nil {
			if selected.MergedAt.Before(*firstStart) {
				view.Notes = append(view.Notes, "Merge predates the first attributed Run; no delivery latency can be established")
			} else {
				view.ObservedLatency = formatDuration(selected.MergedAt.Sub(*firstStart).Seconds())
				if firstDeliveryKnown {
					view.Latency = view.ObservedLatency
					view.FirstDeliveryUSD = firstDeliveryCost(aggregate.Runs, *selected.MergedAt)
				} else {
					view.Notes = append(view.Notes, "Earliest observed merge is shown; incomplete evidence leaves first-delivery latency and cost unknown")
				}
			}
		}
	}
	projectCurrentStage(&view, aggregate, snapshot, item, historyFresh, parentStale, now)
	sort.Strings(view.Notes)
	return view
}

func processOutcome(outcome string, processExit *int) string {
	switch outcome {
	case "completed":
		outcome = "execution completed"
	case "no_work", "setup_failed", "execution_failed", "provider_failed", "cancelled", "timed_out", "interrupted", "internal_error":
		outcome = strings.ReplaceAll(outcome, "_", " ")
	default:
		outcome = "unknown (outcome not recorded)"
	}
	if processExit != nil {
		outcome += fmt.Sprintf("; Pi exit %d", *processExit)
	}
	return outcome
}

func completionView(completion *CompletionData) (string, string) {
	if completion == nil {
		return "not observed", ""
	}
	if completion.Schema != "forest.completion.v1" {
		return "unknown", "Invalid completion observation schema"
	}
	switch completion.Status {
	case "completed":
		if strings.TrimSpace(completion.Evidence) != "" {
			return "completed", completion.Evidence
		}
	case "incomplete", "unknown":
		if strings.TrimSpace(completion.Reason) != "" {
			return completion.Status, completion.Reason
		}
	}
	return "unknown", "Completion observation lacks the required evidence or reason"
}

func compactUSD(cost float64) string {
	if cost > 0 && cost < 0.01 {
		return "<$0.01"
	}
	return fmt.Sprintf("$%.2f", cost)
}

type reviewSelection struct {
	Evidence *ReviewEvidence
	RunID    string
	State    string
	Warning  string
}

func selectReview(pull PullEvidence, snapshot Snapshot, work WorkRef, now time.Time) reviewSelection {
	selected := reviewSelection{State: "missing", Warning: "No published verdict ref observed for the exact revision"}
	var stale *ReviewEvidence
	for i := range snapshot.Reviews.Reviews {
		evidence := &snapshot.Reviews.Reviews[i]
		if evidence.Work == nil || evidence.Work.System != work.System || evidence.Work.ID != work.ID {
			continue
		}
		if evidence.Revision != pull.HeadSHA {
			if selected.Evidence == nil && evidence.Branch != "" && strings.TrimPrefix(evidence.Branch, "refs/heads/") == pull.HeadRef {
				selected.State, selected.Warning = "stale", "Published verdict is for a stale revision; the current PR head requires verification"
				stale = evidence
			}
			continue
		}
		if selected.Evidence != nil {
			return reviewSelection{State: "invalid", Warning: "Duplicate published evidence for the current revision is ambiguous"}
		}
		selected.State, selected.Warning = "missing", "No published verdict ref observed for the exact revision"
		selected.Evidence = evidence
		if evidence.VerdictState == "missing" || (evidence.VerdictState == "" && evidence.VerdictCommit == nil) {
			continue
		}
		selected.State = "invalid"
		if evidence.RequestState != "readable" || evidence.VerdictState != "readable" ||
			evidence.VerdictRef != "refs/forest/v1/verdict/"+evidence.Revision ||
			!revisionSHA.MatchString(evidence.Revision) || evidence.VerdictCommit == nil ||
			!revisionSHA.MatchString(evidence.VerdictCommit.SHA) ||
			(evidence.Decision != "approve" && evidence.Decision != "changes") || strings.TrimSpace(evidence.Summary) == "" {
			selected.Warning = "Published verdict payload or ref identity is unreadable"
			continue
		}
		at := evidence.VerdictCommit.Committer.Time
		if at.IsZero() || at.After(now) {
			selected.Warning = "Published verdict commit time is unavailable or in the future"
			continue
		}
		matches := make(map[string]bool)
		for _, run := range evidence.Runs {
			if run.Agent == "verifier" && run.RunID != "" && run.Work != nil &&
				run.Work.System == work.System && run.Work.ID == work.ID && !run.NoWork && run.Outcome != "no_work" &&
				verdictRunWarning(*evidence, run.RunID, pull, snapshot, work, now) == "" {
				matches[run.RunID] = true
			}
		}
		if len(matches) != 1 {
			selected.Warning = "Verdict commit time must fall inside exactly one known Verifier Run lifetime with exact work provenance"
			continue
		}
		for id := range matches {
			selected.RunID = id
		}
		selected.State, selected.Warning = "current", ""
	}
	if selected.Evidence == nil && stale != nil {
		selected.Evidence = stale
	}
	return selected
}

func verdictRunWarning(evidence ReviewEvidence, runID string, pull PullEvidence, snapshot Snapshot, work WorkRef, now time.Time) string {
	at := evidence.VerdictCommit.Committer.Time
	known := false
	var started, ended time.Time
	check := func(id, agent, start string, identity *WorkRef, duration *float64, noWork bool) string {
		if id != runID {
			return ""
		}
		if noWork || (agent != "" && agent != "verifier") {
			return "Verdict Run is not a Forest Verifier execution"
		}
		if identity != nil && (identity.System != work.System || identity.ID != work.ID || (work.Key != "" && identity.Key != "" && identity.Key != work.Key)) {
			return "Verdict Run has conflicting immutable work provenance"
		}
		if start != "" {
			value, err := time.Parse(time.RFC3339Nano, start)
			if err != nil || (!started.IsZero() && !started.Equal(value)) {
				return "Verdict Run start evidence is invalid or conflicting"
			}
			started = value
			if duration != nil {
				if *duration < 0 || math.IsNaN(*duration) || math.IsInf(*duration, 0) || *duration > 365*24*60*60 {
					return "Verdict Run duration evidence is invalid"
				}
				end := value.Add(time.Duration(*duration * float64(time.Second)))
				if !ended.IsZero() && !ended.Equal(end) {
					return "Verdict Run lifetime evidence is conflicting"
				}
				ended = end
			}
		}
		known = known || (agent == "verifier" && identity != nil && start != "")
		return ""
	}
	for _, run := range evidence.Runs {
		if warning := check(run.RunID, run.Agent, run.Started, run.Work, &run.Duration, run.NoWork || run.Outcome == "no_work"); warning != "" {
			return warning
		}
	}
	for _, run := range snapshot.History.Runs {
		if warning := check(run.RunID, run.Agent, run.Started, run.Work, &run.Duration, run.NoWork || run.Outcome == "no_work"); warning != "" {
			return warning
		}
	}
	for _, run := range snapshot.Status.Recent {
		if warning := check(run.RunID, run.Agent, run.Started, run.Work, &run.Duration, run.NoWork || run.Outcome == "no_work"); warning != "" {
			return warning
		}
	}
	for _, run := range snapshot.Status.LiveRuns {
		if warning := check(run.RunID, run.Agent, run.StartedAt, run.Work, nil, run.Outcome == "no_work"); warning != "" {
			return warning
		}
	}
	if !known || started.IsZero() || ended.IsZero() {
		return "Verdict does not identify a completed Forest Verifier Run with exact work provenance"
	}
	// Git commit clocks have second precision; accept their containing second,
	// not an arbitrary grace period outside the observed execution.
	if at.Before(started.Truncate(time.Second)) || !at.Before(ended.Truncate(time.Second).Add(time.Second)) {
		return "Verdict was published outside the known Verifier Run lifetime"
	}
	if pull.MergedAt != nil && at.After(*pull.MergedAt) {
		return "Verdict was published after the observed merge"
	}
	return ""
}

func projectCurrentStage(view *TicketView, aggregate ticketAggregate, snapshot Snapshot, item HabitatItem, historyFresh, parentStale bool, now time.Time) {
	setStage := func(stage, class, owner, next string, attention bool) {
		view.Stage, view.StageClass, view.ActionOwner, view.NextAction, view.NeedsAttention = stage, class, owner, next, attention
	}
	setStage("Evidence unavailable", "unknown", "Operator", "Restore fresh observations before deciding the next action", true)
	view.CurrentPRURL = safeExternalURL(item.PRURL)
	pull, found := snapshot.Delivery.Forge.Pulls[item.PRURL]
	primaryContained := false
	staleHead := false
	if !found && item.PRURL == "" {
		var candidate *ReviewEvidence
		for i := range snapshot.Reviews.Reviews {
			row := &snapshot.Reviews.Reviews[i]
			if row.Work == nil || row.Work.System != aggregate.Work.System || row.Work.ID != aggregate.Work.ID || row.RequestState != "readable" {
				continue
			}
			for _, observed := range snapshot.Delivery.Forge.Pulls {
				if observed.HeadRef == strings.TrimPrefix(row.Branch, "refs/heads/") && observed.HeadSHA != row.Revision {
					staleHead = true
				}
			}
			if candidate == nil || (row.RequestCommit != nil && (candidate.RequestCommit == nil || row.RequestCommit.Committer.Time.After(candidate.RequestCommit.Committer.Time))) {
				candidate = row
			}
		}
		if candidate != nil && !staleHead {
			pull.HeadSHA, pull.HeadRef = candidate.Revision, strings.TrimPrefix(candidate.Branch, "refs/heads/")
			pull.SourceObservation = snapshot.Reviews.SourceObservation
			found = true
			primary := snapshot.Delivery.Forge.Primary[candidate.Revision]
			if primary.Error != "" {
				view.Notes = append(view.Notes, primary.Error)
			}
			primaryContained = primary.Contained && evidenceSourceView("", true, primary.SourceObservation, parentStale, now, sourceRefreshInterval+refreshTimeout+freshnessSchedulingSlack, "").State == "fresh"
			if primaryContained {
				view.Delivery, view.DeliveryClass, view.Delivered = "Present on primary; no PR observed", "ok", true
				view.Notes = append(view.Notes, "GitHub confirms the exact candidate is reachable from primary. No PR, merge event, actor or merge time was observed.")
			}
		}
	}
	review := reviewSelection{State: "missing", Warning: "No current PR is linked on the work item"}
	if staleHead {
		review.State, review.Warning = "stale", "Published candidate is stale; the current PR head requires verification"
	}
	reviewFresh := false
	if found {
		view.HeadSHA = pull.HeadSHA
		view.PRState, view.HeadRef = pull.State, pull.HeadRef
		work := aggregate.Work
		if item.Key != "" {
			work.Key = item.Key
		}
		review = selectReview(pull, snapshot, work, now)
		reviewFresh = evidenceSourceView("", true, snapshot.Reviews.SourceObservation, parentStale, now, sourceRefreshInterval+refreshTimeout+freshnessSchedulingSlack, "").State == "fresh"
		view.AccountApprovals = pull.Approvals
		view.ApprovalFreshness = evidenceSourceView("", true, pull.ApprovalSource, parentStale, now, sourceRefreshInterval+refreshTimeout+freshnessSchedulingSlack, "").State
		if evidence := review.Evidence; evidence != nil {
			view.ReviewSHA, view.ReviewRunID, view.ReviewSummary = evidence.Revision, review.RunID, evidence.Summary
			if review.State == "current" {
				view.ReviewDecision = evidence.Decision
			}
			if evidence.VerdictCommit != nil {
				view.ReviewAuthor = evidence.VerdictCommit.Committer.Name
				if source := snapshot.Instance.Sources.Forge; source != nil {
					view.ReviewURL = safeExternalURL(strings.TrimRight(source.WebURL, "/") + "/" + snapshot.Config.Repo + "/commit/" + evidence.VerdictCommit.SHA)
				}
			}
		}
		if !reviewFresh {
			review.Warning = "Forest review observation is unavailable or stale"
			if snapshot.Reviews.Error != "" {
				review.Warning = snapshot.Reviews.Error
			}
		}
		if evidenceSourceView("", true, pull.SourceObservation, parentStale, now, sourceRefreshInterval+refreshTimeout+freshnessSchedulingSlack, "").State != "fresh" {
			review.Warning = "Current PR observation is unavailable or stale; verdict cannot establish the current head"
		}
	}
	view.ReviewWarning = review.Warning
	if primaryContained {
		setStage("Merged to primary; no PR observed", "ok", "", "Candidate reachability is observed; merge time, actor and tracker completion are not established", false)
		return
	}
	if parentStale || snapshot.Status.LiveRunError != "" {
		return
	}
	if aggregate.Conflicting {
		if found && pull.State == "open" && reviewFresh && revisionSHA.MatchString(pull.HeadSHA) &&
			evidenceSourceView("", true, pull.SourceObservation, parentStale, now, sourceRefreshInterval+refreshTimeout+freshnessSchedulingSlack, "").State == "fresh" {
			setStage("Awaiting verification", "warn", "Operator", "Resolve conflicting Run/work provenance before accepting verification or complete cost", true)
			return
		}
		view.NextAction = "Resolve conflicting Run/work attribution before claiming progress or complete cost"
		return
	}
	if view.LiveRuns > 0 {
		setStage("In progress", "warn", "Running agent", "Observe the active Run; do not start a duplicate", false)
		return
	}
	// Only label an observed execution, never cancel the work item by inference.
	// The status tail supplies the current clock; optional history is not needed.
	var latest time.Time
	lastOutcome := ""
	for _, run := range snapshot.Status.Recent {
		if _, attributed := aggregate.Runs[run.RunID]; !attributed {
			continue
		}
		started, err := time.Parse(time.RFC3339Nano, run.Started)
		if err != nil {
			lastOutcome = ""
			break
		}
		if started.After(latest) {
			latest, lastOutcome = started, run.Outcome
		} else if started.Equal(latest) && lastOutcome != run.Outcome {
			lastOutcome = ""
		}
	}
	if lastOutcome == "cancelled" {
		setStage("Last execution cancelled", "warn", "Operator", "Inspect the cancelled Run and any retained worktree before authorizing new work; nothing resumes automatically", true)
		return
	}
	if !historyFresh || view.TrackerFreshness != "fresh" {
		if snapshot.Instance.Sources.Habitat == nil && historyFresh && found && evidenceSourceView("", true, pull.SourceObservation, parentStale, now, sourceRefreshInterval+refreshTimeout+freshnessSchedulingSlack, "").State == "fresh" {
			if pull.State == "open" {
				setStage("Open PR; tracker unknown", "warn", "Operator", "Inspect the exact candidate and review evidence; tracker state is unavailable", true)
				if reviewFresh && review.State == "current" {
					if view.ReviewDecision == "approve" {
						setStage("Verified, awaiting merge", "ok", "", "Exact-revision approval is recorded; tracker state is unknown and merge has not been observed", false)
					} else {
						setStage("Changes requested", "warn", "Fixer", "Address the published findings; tracker state is unknown", true)
					}
				}
			} else if view.Delivered {
				setStage("Merged; reconciliation required", "warn", "Operator", "Merge is observed; tracker state and completion still require reconciliation", true)
			}
		}
		return
	}
	for _, observation := range []SourceObservation{snapshot.ConfigObservation, snapshot.DeclarationsObservation} {
		if evidenceSourceView("", true, observation, false, now, sourceRefreshInterval+refreshTimeout+freshnessSchedulingSlack, "").State != "fresh" {
			view.NextAction = "Restore configuration and declaration observations before deciding the next action"
			return
		}
	}
	if item.PRURL == "" {
		if item.Status == "done" {
			view.NextAction = "Restore the exact current PR link; tracker Done alone is not completion evidence"
		} else if view.RunCount == 0 {
			// No observed Run and no candidate. Do not read another tracker's
			// status vocabulary to claim progress that was never observed.
			setStage("No execution observed", "unknown", "Operator", "Confirm admission authority before authorizing a Run; inventory is read-only scope", false)
		} else {
			setStage("Candidate not observed", "unknown", "Operator", "Inspect the latest execution and candidate evidence before authorizing another Run", true)
			if latestAttemptNeedsInspection(aggregate.Runs, snapshot.Declarations, "builder") {
				view.ActionOwner, view.NextAction, view.NeedsAttention = "Operator", "Inspect the latest Builder completion and admission evidence before authorizing another Run", true
			}
		}
		return
	}
	if !found || evidenceSourceView("", true, pull.SourceObservation, parentStale, now, sourceRefreshInterval+refreshTimeout+freshnessSchedulingSlack, "").State != "fresh" {
		view.NextAction = "Restore current PR observation; historical merges cannot establish the current attempt"
		return
	}
	if pull.BaseRef != strings.TrimPrefix(snapshot.Config.Primary, "refs/heads/") {
		view.NextAction = "Resolve the candidate base branch mismatch with the declared primary"
		return
	}
	if pull.Merged {
		if pull.MergedAt == nil || pull.SHA == "" {
			view.NextAction = "Restore the observed merge timestamp and revision"
			return
		}
		if item.Status != "done" {
			for _, run := range aggregate.Runs {
				started, err := time.Parse(time.RFC3339Nano, run.Started)
				if err == nil && started.After(*pull.MergedAt) {
					setStage("Current candidate not observed", "unknown", "Operator", "Link the reopened attempt's current PR; the historical merge is retained separately", true)
					return
				}
			}
		}
		if item.Status == "done" && reviewFresh && review.State == "current" && view.ReviewDecision == "approve" {
			setStage("Complete", "ok", "None", "Observe only; merge, exact-revision review and tracker Done are independently recorded", false)
		} else {
			setStage("Merged; reconciliation required", "warn", "Operator", "Inspect the recorded merge, exact-revision approval and tracker state; observation does not authorize further action", true)
		}
		return
	}
	if item.Status == "done" {
		view.NextAction = "Resolve tracker Done versus the unmerged current PR"
		return
	}
	if pull.State != "open" {
		setStage("Candidate closed", "warn", "Operator", "Inspect the closed, unmerged candidate before authorizing another Run", true)
		return
	}
	if !revisionSHA.MatchString(pull.HeadSHA) {
		view.ReviewWarning, view.NextAction = "Current PR head SHA is unavailable", "Restore exact candidate revision observation"
		return
	}
	if !reviewFresh {
		view.NextAction = "Restore or inspect published verdict evidence for the exact current work/revision"
		return
	}
	switch review.State {
	case "current":
		if view.ReviewDecision == "changes" {
			setStage("Changes requested", "warn", "Fixer", "Address the recorded findings and request verification of the new exact revision", true)
		} else {
			setStage("Verified, awaiting merge", "ok", "", "Exact-revision approval is recorded; merge has not been observed", false)
		}
	default:
		setStage("Awaiting verification", "warn", "Verifier", "Verify the current head and publish its immutable verdict ref", false)
		if review.State == "invalid" {
			view.ActionOwner, view.NextAction, view.NeedsAttention = "Operator", "Inspect the rejected verdict evidence and obtain valid verification of the current revision", true
		}
		if latestAttemptNeedsInspection(aggregate.Runs, snapshot.Declarations, "verifier") {
			view.ActionOwner, view.NextAction, view.NeedsAttention = "Operator", "Inspect the latest Verifier completion and admission evidence before authorizing another Run", true
		}
	}
}

// A historical failure is not the current attempt, and completion evidence is
// not admission state. Flag only the latest attempt of the next role for
// operator inspection; do not infer whether an operator has already resumed.
func latestAttemptNeedsInspection(runs map[string]attributedRun, declarations []DeclarationData, role string) bool {
	var latest attributedRun
	var latestStart time.Time
	for _, run := range runs {
		if run.Agent != role {
			continue
		}
		started, err := time.Parse(time.RFC3339Nano, run.Started)
		if err != nil {
			return true
		}
		if latest.ID == "" || started.After(latestStart) || (started.Equal(latestStart) && run.ID > latest.ID) {
			latest, latestStart = run, started
		}
	}
	if latest.ID == "" || latest.Exit == nil {
		return false
	}
	if latest.Outcome != "" && latest.Outcome != "completed" {
		return true
	}
	if latest.Completion != nil {
		completion, _ := completionView(latest.Completion)
		return completion != "completed"
	}
	for _, declaration := range declarations {
		if declaration.Name == role {
			return strings.TrimSpace(declaration.Completion) != ""
		}
	}
	return false
}

func firstDeliveryCost(runs map[string]attributedRun, merged time.Time) string {
	var total float64
	known := false
	for _, run := range runs {
		started, err := time.Parse(time.RFC3339Nano, run.Started)
		if err != nil {
			return "Unknown"
		}
		if !started.Before(merged) {
			continue
		}
		if run.Exit == nil || run.Duration == nil || *run.Duration < 0 || math.IsNaN(*run.Duration) || math.IsInf(*run.Duration, 0) || *run.Duration > merged.Sub(started).Seconds() {
			// Per-Run receipts cannot allocate a Run spanning the merge.
			return "Unknown"
		}
		if run.Native == nil || run.Native.ProviderCost.coverage() != "complete" {
			return "Unknown"
		}
		total += *run.Native.ProviderCost.CostUSD
		known = true
	}
	if !known {
		return "Unknown"
	}
	return fmt.Sprintf("$%.8f", total)
}

func addKnownTokens(total, value *int64) *int64 {
	if value == nil {
		return total
	}
	if total == nil {
		return new(*value)
	}
	*total += *value
	return total
}

func formatKnownTokens(value *int64) string {
	if value == nil {
		return "Unknown"
	}
	return fmt.Sprintf("%d", *value)
}
