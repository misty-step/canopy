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

// These are nullable provider facts, not model-catalog estimates. The token
// subcategories overlap input/output and must never be added to their totals.
type ProviderUsage struct {
	SessionID        string   `json:"session_id"`
	InputTokens      *int64   `json:"input_tokens"`
	OutputTokens     *int64   `json:"output_tokens"`
	CacheReadTokens  *int64   `json:"cache_read_tokens"`
	CacheWriteTokens *int64   `json:"cache_write_tokens"`
	ReasoningTokens  *int64   `json:"reasoning_tokens"`
	CostUSD          *float64 `json:"cost_usd"`
	GenerationCount  *int     `json:"generation_count"`
	PendingCount     *int     `json:"pending_count"`
	Coverage         string   `json:"coverage"`
	Models           []string `json:"models"`
}

type UsageObservation struct {
	SourceObservation
	AsOf     time.Time                `json:"as_of"`
	Sessions map[string]ProviderUsage `json:"sessions"`
}

func (usage UsageObservation) freshnessObservation() SourceObservation {
	observation := usage.SourceObservation
	if !observation.ObservedAt.IsZero() && !usage.AsOf.IsZero() && usage.AsOf.Before(observation.ObservedAt) {
		observation.ObservedAt = usage.AsOf
	}
	return observation
}

type PullEvidence struct {
	SourceObservation
	URL             string          `json:"url"`
	State           string          `json:"state"`
	BaseRef         string          `json:"base_ref"`
	HeadSHA         string          `json:"head_sha"`
	Merged          bool            `json:"merged"`
	MergedAt        *time.Time      `json:"merged_at"`
	SHA             string          `json:"sha"`
	MergedBy        string          `json:"merged_by"`
	MergerType      string          `json:"merger_type"`
	ReviewSource    SourceObservation `json:"review_source"`
	ReviewReceipts  []ReviewReceipt `json:"review_receipts"`
	ApprovalSource SourceObservation `json:"approval_source"`
	Approvals       []ForgeApproval `json:"approvals"`
	ApprovalWarning string          `json:"approval_warning,omitempty"`
}

// ReviewReceipt combines the profile payload with independently read forge
// metadata. It is not trusted until correlated with a known Forest Verifier Run.
type ReviewReceipt struct {
	Schema          string    `json:"schema"`
	RunID           string    `json:"run_id"`
	WorkID          string    `json:"work_id"`
	Revision        string    `json:"revision"`
	Decision        string    `json:"decision"`
	Summary         string    `json:"summary"`
	ID              int64     `json:"id"`
	URL             string    `json:"url"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	Author          string    `json:"author"`
	AuthorID        int64     `json:"author_id"`
	AuthorType      string    `json:"author_type"`
	Association     string    `json:"author_association"`
	ValidationWarning string  `json:"validation_warning,omitempty"`
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

type ForgeObservation struct {
	SourceObservation
	Pulls map[string]PullEvidence `json:"pulls"`
}

type DeliverySources struct {
	AttemptedAt time.Time          `json:"attempted_at"`
	Habitat     HabitatObservation `json:"habitat"`
	Usage       UsageObservation   `json:"usage"`
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
	if next.Usage.Error != "" {
		err := next.Usage.Error
		next.Usage = previous.Usage
		next.Usage.Error = err
	}
	return next
}

type attributedRun struct {
	ID, Agent, RequestID string
	Started              string
	Duration             *float64
	Exit                 *int
	Error                string
	NoWork               bool
	ProcessExit *int
	Outcome     string
	Completion  *CompletionData
}

func attributedRuns(snapshot Snapshot) map[string]attributedRun {
	runs := make(map[string]attributedRun, len(snapshot.History.Runs)+len(snapshot.Status.Recent)+len(snapshot.Status.LiveRuns))
	addCompleted := func(run RunData) {
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
			runs[run.RunID] = retained
			return
		}
		runs[run.RunID] = attributedRun{ID: run.RunID, Agent: run.Agent, RequestID: run.RequestID,
			Started: run.Started, Duration: &run.Duration, Exit: &run.Exit, Error: run.Error, NoWork: run.NoWork,
			ProcessExit: run.ProcessExit, Outcome: run.Outcome, Completion: run.Completion}
	}
	for _, run := range snapshot.History.Runs {
		addCompleted(run)
	}
	for _, run := range snapshot.Status.Recent {
		addCompleted(run)
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
			ProcessExit: run.ProcessExit, Outcome: run.Outcome, Completion: run.Completion}
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

func validProviderUsage(usage ProviderUsage) bool {
	if usage.Coverage != "unknown" && usage.Coverage != "partial" && usage.Coverage != "complete" {
		return false
	}
	if usage.GenerationCount == nil || usage.PendingCount == nil || *usage.GenerationCount < 0 || *usage.PendingCount < 0 ||
		(usage.Coverage == "complete" && *usage.PendingCount > 0) {
		return false
	}
	for _, value := range []*int64{usage.InputTokens, usage.OutputTokens, usage.CacheReadTokens, usage.CacheWriteTokens, usage.ReasoningTokens} {
		if value != nil && *value < 0 {
			return false
		}
	}
	return usage.CostUSD == nil || (*usage.CostUSD >= 0 && !math.IsNaN(*usage.CostUSD) && !math.IsInf(*usage.CostUSD, 0))
}

type EvidenceSourceView struct {
	Name, State, Observed, Message string
}

type TicketRunView struct {
	ID, Agent, RequestID, Outcome, Duration, Started string
	ProcessOutcome, Completion, CompletionEvidence, USD, Coverage string
}

type TicketView struct {
	ID, System, Key, URL, Title                                                       string
	Tracker, TrackerFreshness, TrackerObserved                                        string
	Delivery, DeliveryClass, PRURL, MergeTime, MergeSHA, Merger, MergeFreshness       string
	Started, Latency, ObservedLatency, Duration, Coverage, UsageFreshness             string
	InputTokens, OutputTokens, CacheRead, CacheWrite, Reasoning, USD                  string
	FirstDeliveryUSD                                                                  string
	Runs                                                                              []TicketRunView
	CreatedRuns                                                                       []string
	RunCount, FailedRuns, LiveRuns, MissingUsage, Pending, Generations, UsageSessions int
	Notes                                                                             []string
	Delivered                                                                         bool
	Stage, StageClass, NextAction, ActionOwner                                          string
	ReviewDecision, ReviewSHA, ReviewRunID, ReviewURL, ReviewSummary, ReviewWarning     string
	ReviewAuthor, ReviewAuthorType, ReviewAuthorAssociation                            string
	CurrentPRURL, HeadSHA, USDCompact, MergerType                                       string
	AccountApprovals                                                                  []ForgeApproval
	ApprovalFreshness                                                                 string
	NeedsAttention                                                                    bool
}

type TicketDeliveryView struct {
	Tickets                                          []TicketView
	Sources                                          []EvidenceSourceView
	UnattributedRuns, ConflictingRuns, Delivered     int
	NeedsAttention, InProgress                        int
	HistoryNotice                                    string
	UnassignedUSD, UnassignedInput, UnassignedOutput string
	UnassignedUsageSessions, UnassignedPending       int
}

type ticketIdentity struct{ System, ID string }

type ticketAggregate struct {
	Work    WorkRef
	Runs    map[string]attributedRun
	Created map[string]bool
	Notes   []string
	Conflicting bool
}

func ticketDeliveryView(snapshot Snapshot, parentStale bool, now time.Time, maxAge time.Duration) TicketDeliveryView {
	view := TicketDeliveryView{}
	sources := snapshot.Instance.Sources
	observation := snapshot.Delivery
	view.Sources = append(view.Sources, evidenceSourceView("Run history", true, snapshot.History.SourceObservation, parentStale, now, maxAge, "All Run pages, including nonzero exits"))
	view.Sources = append(view.Sources, evidenceSourceView("Habitat", sources.Habitat != nil, observation.Habitat.SourceObservation, parentStale, now, sourceRefreshInterval+refreshTimeout+freshnessSchedulingSlack, observation.Habitat.Scope))
	usageMessage := "Provider receipts only; missing is not zero"
	if !observation.Usage.AsOf.IsZero() {
		usageMessage += "; provider query as of " + formatTime(observation.Usage.AsOf)
	}
	view.Sources = append(view.Sources, evidenceSourceView("Tach", sources.Tach != nil, observation.Usage.freshnessObservation(), parentStale, now, sourceRefreshInterval+refreshTimeout+freshnessSchedulingSlack, usageMessage))
	view.Sources = append(view.Sources, evidenceSourceView("Forge", sources.Forge != nil, observation.Forge.SourceObservation, parentStale, now, sourceRefreshInterval+refreshTimeout+freshnessSchedulingSlack, "Explicit PR links; merge to declared primary is not deployment"))
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
			if usage, known := observation.Usage.Sessions[id]; known {
				view.UnassignedUsageSessions++
				unassignedInput = addKnownTokens(unassignedInput, usage.InputTokens)
				unassignedOutput = addKnownTokens(unassignedOutput, usage.OutputTokens)
				if usage.CostUSD != nil {
					if unassignedCost == nil {
						unassignedCost = new(0.0)
					}
					*unassignedCost += *usage.CostUSD
				}
				if usage.PendingCount != nil {
					view.UnassignedPending += *usage.PendingCount
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
			if run.Outcome != "" {
				// The model attempt finished while Kernel finalization continues;
				// that is neither a completed execution nor observed delivery.
				row.Outcome = "finalizing"
			}
		}
		if run.Error != "" {
			row.Outcome += ": " + run.Error
		}
		usage, known := snapshot.Delivery.Usage.Sessions[id]
		if known {
			row.Coverage = usage.Coverage
			if usage.CostUSD != nil {
				row.USD = fmt.Sprintf("$%.8f", *usage.CostUSD)
			}
		}
		view.Runs = append(view.Runs, row)
		if !known || usage.Coverage == "unknown" {
			view.MissingUsage++
		}
		if !known {
			continue
		}
		view.UsageSessions++
		if usage.Coverage == "complete" {
			completeUsage++
		}
		input = addKnownTokens(input, usage.InputTokens)
		output = addKnownTokens(output, usage.OutputTokens)
		cacheRead = addKnownTokens(cacheRead, usage.CacheReadTokens)
		cacheWrite = addKnownTokens(cacheWrite, usage.CacheWriteTokens)
		reasoning = addKnownTokens(reasoning, usage.ReasoningTokens)
		if usage.CostUSD != nil {
			if cost == nil {
				cost = new(0.0)
			}
			*cost += *usage.CostUSD
		}
		if usage.PendingCount != nil {
			view.Pending += *usage.PendingCount
		}
		if usage.GenerationCount != nil {
			view.Generations += *usage.GenerationCount
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
	if completeUsage == view.RunCount && view.RunCount > 0 && !aggregate.Conflicting {
		view.Coverage = "complete"
	} else if view.Generations > 0 || view.Pending > 0 || input != nil || output != nil || cost != nil {
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
	view.UsageFreshness = evidenceSourceView("", snapshot.Instance.Sources.Tach != nil, snapshot.Delivery.Usage.freshnessObservation(), parentStale, now, sourceRefreshInterval+refreshTimeout+freshnessSchedulingSlack, "").State
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
	historyFresh := evidenceSourceView("", true, snapshot.History.SourceObservation, parentStale, now, maxAge, "").State == "fresh"
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
					if view.UsageFreshness == "fresh" {
						view.FirstDeliveryUSD = firstDeliveryCost(aggregate.Runs, snapshot.Delivery.Usage.Sessions, *selected.MergedAt)
					}
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
	Receipt *ReviewReceipt
	State   string
	Warning string
}

func selectReview(pull PullEvidence, snapshot Snapshot, work WorkRef, now time.Time) reviewSelection {
	selected := reviewSelection{State: "missing", Warning: "No forest.review.v1 Verifier receipt observed"}
	current := 0
	var stale, invalid *ReviewReceipt
	var invalidWarning string
	newer := func(a ReviewReceipt, b *ReviewReceipt) bool {
		return b == nil || a.CreatedAt.After(b.CreatedAt) || (a.CreatedAt.Equal(b.CreatedAt) && a.ID > b.ID)
	}
	for _, receipt := range pull.ReviewReceipts {
		warning := receiptWarning(receipt, pull, snapshot, work, now)
		if warning != "" {
			if newer(receipt, invalid) {
				invalid, invalidWarning = new(receipt), warning
			}
			continue
		}
		if receipt.Revision != pull.HeadSHA {
			if newer(receipt, stale) {
				stale = new(receipt)
			}
			continue
		}
		current++
		if newer(receipt, selected.Receipt) {
			selected = reviewSelection{Receipt: new(receipt), State: "current"}
		}
	}
	if current > 1 {
		selected.State, selected.Warning = "invalid", "Multiple valid Verifier receipts for the current revision are ambiguous"
	} else if current == 0 {
		if stale != nil {
			selected = reviewSelection{Receipt: stale, State: "stale", Warning: "Verifier receipt is for a stale revision; the current PR head requires verification"}
		} else if invalid != nil {
			selected = reviewSelection{Receipt: invalid, State: "invalid", Warning: invalidWarning}
		}
	}
	if selected.Receipt != nil && selected.State != "invalid" {
		source := snapshot.Instance.Sources.Forge
		if source == nil || source.AutomationLogin == "" || !strings.EqualFold(selected.Receipt.Author, source.AutomationLogin) {
			caveat := fmt.Sprintf("Receipt author %s is not the configured automation identity; treat as observed evidence, not capability-enforced automation", selected.Receipt.Author)
			if source == nil || source.AutomationLogin == "" {
				caveat = fmt.Sprintf("Receipt author %s is observed, but no automation identity is configured; this is not capability-enforced automation evidence", selected.Receipt.Author)
			}
			if selected.Warning != "" {
				selected.Warning += "; "
			}
			selected.Warning += caveat
		}
	}
	return selected
}

func receiptWarning(receipt ReviewReceipt, pull PullEvidence, snapshot Snapshot, work WorkRef, now time.Time) string {
	if receipt.ValidationWarning != "" {
		return receipt.ValidationWarning
	}
	if receipt.Schema != "forest.review.v1" || receipt.RunID == "" || !revisionSHA.MatchString(receipt.Revision) ||
		(receipt.Decision != "approve" && receipt.Decision != "changes") || strings.TrimSpace(receipt.Summary) == "" {
		return "Malformed forest.review.v1 receipt"
	}
	if receipt.WorkID != work.ID {
		return "Verifier receipt work identity does not match this immutable work item"
	}
	if strings.TrimSpace(receipt.Author) == "" || receipt.AuthorID <= 0 ||
		(receipt.AuthorType != "User" && receipt.AuthorType != "Bot") ||
		(receipt.Association != "OWNER" && receipt.Association != "MEMBER" && receipt.Association != "COLLABORATOR") {
		return "Receipt author lacks an accountable identity or trusted repository association"
	}
	if receipt.ID <= 0 || receipt.URL != fmt.Sprintf("%s#issuecomment-%d", pull.URL, receipt.ID) ||
		receipt.CreatedAt.IsZero() || receipt.UpdatedAt.IsZero() || receipt.UpdatedAt.Before(receipt.CreatedAt) || receipt.UpdatedAt.After(now) {
		return "Receipt source URL, identity or timestamps are unavailable or mismatched"
	}
	known := false
	var started, ended time.Time
	check := func(id, agent, start string, identity *WorkRef, duration *float64, noWork bool) string {
		if id != receipt.RunID {
			return ""
		}
		if noWork || (agent != "" && agent != "verifier") {
			return "Receipt Run is not a Forest Verifier execution"
		}
		if identity != nil && (identity.System != work.System || identity.ID != work.ID || (work.Key != "" && identity.Key != "" && identity.Key != work.Key)) {
			return "Receipt Run has conflicting immutable work provenance"
		}
		if start != "" {
			value, err := time.Parse(time.RFC3339Nano, start)
			if err != nil || (!started.IsZero() && !started.Equal(value)) {
				return "Receipt Run start evidence is invalid or conflicting"
			}
			started = value
			if duration != nil {
				if *duration < 0 || math.IsNaN(*duration) || math.IsInf(*duration, 0) || *duration > 365*24*60*60 {
					return "Receipt Run duration evidence is invalid"
				}
				end := value.Add(time.Duration(*duration * float64(time.Second)))
				if !ended.IsZero() && !ended.Equal(end) {
					return "Receipt Run lifetime evidence is conflicting"
				}
				ended = end
			}
		}
		known = known || (agent == "verifier" && identity != nil && start != "")
		return ""
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
	if !known || started.IsZero() {
		return "Receipt does not identify a known Forest Verifier Run with exact work provenance"
	}
	// GitHub timestamps have second precision; do not reject an in-Run comment
	// solely because Forest recorded a subsecond start or finish.
	if receipt.CreatedAt.Before(started.Truncate(time.Second)) ||
		(!ended.IsZero() && !receipt.UpdatedAt.Before(ended.Truncate(time.Second).Add(time.Second))) {
		return "Receipt was created or edited outside the known Verifier Run lifetime"
	}
	if pull.MergedAt != nil && receipt.UpdatedAt.After(*pull.MergedAt) {
		return "Verifier receipt was created or edited after the observed merge"
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
	review := reviewSelection{State: "missing", Warning: "No current PR is linked on the work item"}
	reviewFresh := false
	if found {
		view.HeadSHA = pull.HeadSHA
		work := aggregate.Work
		work.Key = item.Key
		review = selectReview(pull, snapshot, work, now)
		reviewFresh = evidenceSourceView("", true, pull.ReviewSource, parentStale, now, sourceRefreshInterval+refreshTimeout+freshnessSchedulingSlack, "").State == "fresh"
		view.AccountApprovals = pull.Approvals
		view.ApprovalFreshness = evidenceSourceView("", true, pull.ApprovalSource, parentStale, now, sourceRefreshInterval+refreshTimeout+freshnessSchedulingSlack, "").State
		if receipt := review.Receipt; receipt != nil {
			view.ReviewDecision, view.ReviewSHA, view.ReviewRunID = receipt.Decision, receipt.Revision, receipt.RunID
			view.ReviewURL, view.ReviewSummary = safeExternalURL(receipt.URL), receipt.Summary
			view.ReviewAuthor, view.ReviewAuthorType, view.ReviewAuthorAssociation = receipt.Author, receipt.AuthorType, receipt.Association
		}
		if !reviewFresh {
			review.Warning = "Verifier receipt observation is unavailable or stale"
			if pull.ReviewSource.Error != "" {
				review.Warning = pull.ReviewSource.Error
			}
		}
		if evidenceSourceView("", true, pull.SourceObservation, parentStale, now, sourceRefreshInterval+refreshTimeout+freshnessSchedulingSlack, "").State != "fresh" {
			review.Warning = "Current PR observation is unavailable or stale; receipt cannot establish the current head"
		}
	}
	view.ReviewWarning = review.Warning
	if parentStale || !historyFresh || view.TrackerFreshness != "fresh" || snapshot.Status.LiveRunError != "" {
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
	if item.PRURL == "" {
		if item.Status == "done" {
			view.NextAction = "Restore the exact current PR link; tracker Done alone is not completion evidence"
		} else if view.RunCount == 0 {
			// No observed Run and no candidate. Do not read another tracker's
			// status vocabulary to claim progress that was never observed.
			setStage("Not started", "unknown", "Operator", "Confirm admission authority before authorizing a Run; inventory is read-only scope", false)
		} else {
			setStage("In progress", "warn", "Builder", "Publish and link the current candidate PR", false)
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
					setStage("In progress", "warn", "Builder", "Link the reopened attempt's current PR; the historical merge is retained separately", true)
					return
				}
			}
		}
		if item.Status == "done" && reviewFresh && review.State == "current" && view.ReviewDecision == "approve" {
			setStage("Complete", "ok", "None", "Observe only; merge, exact-revision review and tracker Done are independently recorded", false)
		} else {
			setStage("Merged; reconciliation required", "warn", "Human operator", "Inspect exact-revision approval and run explicit reconciliation; do not merge again or auto-resume", true)
		}
		return
	}
	if item.Status == "done" {
		view.NextAction = "Resolve tracker Done versus the unmerged current PR"
		return
	}
	if pull.State != "open" {
		setStage("In progress", "warn", "Builder", "Replace or reopen the closed, unmerged candidate PR", true)
		return
	}
	if !revisionSHA.MatchString(pull.HeadSHA) {
		view.ReviewWarning, view.NextAction = "Current PR head SHA is unavailable", "Restore exact candidate revision observation"
		return
	}
	if !reviewFresh {
		view.NextAction = "Restore or inspect Verifier receipt evidence for the exact current Run/work/revision"
		return
	}
	switch review.State {
	case "current":
		if view.ReviewDecision == "changes" {
			setStage("Changes requested", "warn", "Fixer", "Address the recorded findings and request verification of the new exact revision", true)
		} else {
			setStage("Ready for human review", "ok", "Human operator", "Review the exact approved SHA, merge explicitly, then reconcile the tracker", true)
		}
	default:
		setStage("Awaiting verification", "warn", "Verifier", "Verify the current head and publish a Run-linked forest.review.v1 receipt", false)
		if review.State == "invalid" {
			view.ActionOwner, view.NextAction, view.NeedsAttention = "Operator", "Inspect the rejected receipt and obtain valid verification of the current revision", true
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

func firstDeliveryCost(runs map[string]attributedRun, sessions map[string]ProviderUsage, merged time.Time) string {
	var total float64
	known := false
	for id, run := range runs {
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
		usage, exists := sessions[id]
		if !exists || usage.Coverage != "complete" || usage.CostUSD == nil {
			return "Unknown"
		}
		total += *usage.CostUSD
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
