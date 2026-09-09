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
	URL             string     `json:"url"`
	State           string     `json:"state"`
	BaseRef         string     `json:"base_ref"`
	Merged          bool       `json:"merged"`
	HumanApproved   bool       `json:"human_approved"`
	MergedAt        *time.Time `json:"merged_at"`
	SHA             string     `json:"sha"`
	MergedBy        string     `json:"merged_by"`
	ApprovalWarning string     `json:"approval_warning,omitempty"`
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
			runs[run.RunID] = retained
			return
		}
		runs[run.RunID] = attributedRun{ID: run.RunID, Agent: run.Agent, RequestID: run.RequestID,
			Started: run.Started, Duration: &run.Duration, Exit: &run.Exit, Error: run.Error, NoWork: run.NoWork}
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
		runs[run.RunID] = attributedRun{ID: run.RunID, Agent: run.Agent, RequestID: run.RequestID, Started: run.StartedAt}
	}
	for id, run := range runs {
		if run.NoWork {
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
			seen[run.RunID] = seen[run.RunID] || run.NoWork
		}
	}
	for _, run := range snapshot.Status.Recent {
		if run.RunID != "" {
			seen[run.RunID] = seen[run.RunID] || run.NoWork
		}
	}
	for _, run := range snapshot.Status.LiveRuns {
		if _, exists := seen[run.RunID]; !exists && run.RunID != "" {
			seen[run.RunID] = false
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
}

type TicketView struct {
	ID, System, Key, URL, Title                                                       string
	Tracker, TrackerFreshness, TrackerObserved                                        string
	Delivery, DeliveryClass, PRURL, MergeTime, MergeSHA, Merger, MergeFreshness       string
	Started, Latency, ObservedLatency, Duration, Coverage, UsageFreshness             string
	InputTokens, OutputTokens, CacheRead, CacheWrite, Reasoning, USD                  string
	Runs                                                                              []TicketRunView
	CreatedRuns                                                                       []string
	RunCount, FailedRuns, LiveRuns, MissingUsage, Pending, Generations, UsageSessions int
	Notes                                                                             []string
	Delivered                                                                         bool
}

type TicketDeliveryView struct {
	Tickets                                          []TicketView
	Sources                                          []EvidenceSourceView
	UnattributedRuns, ConflictingRuns, Delivered     int
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
	if snapshot.History.ObservedAt.IsZero() || snapshot.History.Error != "" {
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
		ticket := projectTicket(*aggregate, snapshot, parentStale, now)
		if ticket.Delivered {
			view.Delivered++
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

func projectTicket(aggregate ticketAggregate, snapshot Snapshot, parentStale bool, now time.Time) TicketView {
	view := TicketView{ID: aggregate.Work.ID, System: aggregate.Work.System, Key: aggregate.Work.Key,
		URL: safeExternalURL(aggregate.Work.URL), Tracker: "Unknown", TrackerFreshness: "unknown", Delivery: "Unknown — no independently observed PR", DeliveryClass: "unknown",
		Started: "Unknown", Latency: "Unknown", ObservedLatency: "Unknown", Duration: "Unknown", Coverage: "unknown", MergeTime: "Unknown", Notes: aggregate.Notes}
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
		row := TicketRunView{ID: id, Agent: run.Agent, RequestID: run.RequestID, Outcome: "live", Duration: "in progress", Started: run.Started}
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
		}
		if run.Error != "" {
			row.Outcome += ": " + run.Error
		}
		view.Runs = append(view.Runs, row)
		usage, known := snapshot.Delivery.Usage.Sessions[id]
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
	if completeUsage == view.RunCount && view.RunCount > 0 {
		view.Coverage = "complete"
	} else if view.Generations > 0 || view.Pending > 0 || input != nil || output != nil || cost != nil {
		view.Coverage = "partial"
	}
	view.InputTokens, view.OutputTokens = formatKnownTokens(input), formatKnownTokens(output)
	view.CacheRead, view.CacheWrite, view.Reasoning = formatKnownTokens(cacheRead), formatKnownTokens(cacheWrite), formatKnownTokens(reasoning)
	view.USD = "Unknown"
	if cost != nil {
		view.USD = fmt.Sprintf("$%.8f", *cost)
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
	var earliest *PullEvidence
	var firstMerged *PullEvidence
	firstDeliveryKnown := startTimesComplete && !parentStale && !item.ObservedAt.IsZero() && item.Error == "" && item.HistoryWarning == "" &&
		snapshot.Delivery.Habitat.Error == "" && !snapshot.History.ObservedAt.IsZero() && snapshot.History.Error == ""
	urls := append([]string(nil), item.PRURLs...)
	if len(urls) == 0 && item.PRURL != "" {
		urls = append(urls, item.PRURL)
	}
	for _, prURL := range urls {
		pull, exists := snapshot.Delivery.Forge.Pulls[prURL]
		if !exists {
			firstDeliveryKnown = false
			continue
		}
		if pull.Error != "" || pull.ApprovalWarning != "" || pull.ObservedAt.IsZero() || (pull.Merged && !pull.HumanApproved) {
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
			view.Notes = append(view.Notes, "PR does not target the declared primary; not counted as delivery")
			continue
		}
		if !pull.Merged || pull.MergedAt == nil {
			if earliest == nil && firstMerged == nil {
				view.Delivery = "PR " + pull.State + "; not merged to primary"
				view.PRURL = safeExternalURL(pull.URL)
			}
			continue
		}
		if firstMerged == nil || pull.MergedAt.Before(*firstMerged.MergedAt) {
			firstMerged = new(pull)
		}
		if pull.HumanApproved && (earliest == nil || pull.MergedAt.Before(*earliest.MergedAt)) {
			earliest = new(pull)
		}
	}
	selected := earliest
	if selected == nil {
		selected = firstMerged
	}
	if selected != nil {
		view.Delivery = "Merged to primary; approval unknown"
		if earliest != nil {
			view.Delivery, view.DeliveryClass, view.Delivered = "Human-approved merge to primary", "ok", true
		}
		view.PRURL, view.MergeSHA, view.Merger = safeExternalURL(selected.URL), selected.SHA, selected.MergedBy
		view.MergeTime = formatTime(*selected.MergedAt)
		view.MergeFreshness = evidenceSourceView("", true, selected.SourceObservation, parentStale, now, sourceRefreshInterval+refreshTimeout+freshnessSchedulingSlack, "").State
		if view.Delivered && firstStart != nil {
			if selected.MergedAt.Before(*firstStart) {
				view.Notes = append(view.Notes, "Merge predates the first attributed Run; no delivery latency can be established")
			} else {
				view.ObservedLatency = formatDuration(selected.MergedAt.Sub(*firstStart).Seconds())
				if firstDeliveryKnown {
					view.Latency = view.ObservedLatency
				} else {
					view.Notes = append(view.Notes, "Earliest observed merge is shown; incomplete evidence leaves first-delivery latency unknown")
				}
			}
		}
	}
	sort.Strings(view.Notes)
	return view
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
