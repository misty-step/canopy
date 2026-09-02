package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Freshness describes how much confidence an operator should place in a
// rendered instance state.
type Freshness string

const (
	Fresh   Freshness = "fresh"
	Stale   Freshness = "stale"
	Unknown Freshness = "unknown"
)

type PageView struct {
	Instances []InstanceView
	Selected  InstanceView
}

type InstanceView struct {
	ID, Label, Repo, Host, Version, Freshness, LastObserved string
	IsSelected, Reachable, KernelKnown, KernelRunning       bool
	ActiveRuns                                              int
	Audit                                                   AuditView
	Triggers                                                []TriggerViewModel
	LiveRuns                                                []LiveRunViewModel
	RecentRuns                                              []RunViewModel
	Agents                                                  []AgentViewModel
	Config                                                  ConfigViewModel
	Errors                                                  []string
}

type AuditView struct {
	Baseline, LastMaster, AuditedMaster, LastAt, LastResult string
	Trust                                                   string
	TrustClass                                              string
	Violations                                              []string
}
type TriggerViewModel struct {
	Name              string
	StateKnown        bool
	ConsecutiveErrors int
	LastCode          int
	Running           bool
	RunningKnown      bool
	Stale             bool
	LastRun           string
	PollError         string
	RunError          string
	AuditError        string
	PollStatus        string
	RunStatus         string
	AuditStatus       string
}

type LiveRunViewModel struct {
	RunID     string
	Agent     string
	StartedAt string
	Elapsed   string
	Status    string
}

type RunViewModel struct {
	RunID, Agent, Started string
	Duration              string
	DurationSeconds       float64
	Exit                  int
	ExitLabel             string
	Status                string
	Error                 string
	TokensIn, TokensOut   int64
	CacheRead, CacheWrite int64
	Reasoning             int64
	DefinitionSHA         string
}

type AgentViewModel struct {
	Agent                 string
	Runs                  int
	PassRate              float64
	PassRateLabel         string
	DurationP50           float64
	DurationP50Label      string
	DurationP95           float64
	DurationP95Label      string
	TokensIn, TokensOut   int64
	CacheRead, CacheWrite int64
	Reasoning             int64
}

type ConfigViewModel struct {
	Repo, Primary, PrimarySource string
	Scope                        ScopeViewModel
	Agents                       []AgentConfigViewModel
	Checks                       []CheckViewModel
	Declarations                 []DeclarationViewModel
}

type ScopeViewModel struct {
	Label, BranchPrefix string
	Subjects            []string
}

type AgentConfigViewModel struct {
	Agent       string
	Poll        string
	Interval    int
	MaxDuration int
}

type CheckViewModel struct {
	Name, Run string
}

type DeclarationViewModel struct {
	Name, Model, ModelSource string
	Tools                    []string
	Thinking, SystemPrompt   string
	TaskPrompt               string
	SkillPaths               []string
	DefinitionSHA            string
	MaxDuration              int
}

type LogView struct {
	RunID    string
	State    string
	Retained bool
	Complete bool
	Exit     *int
	Text     string
	Error    string
}

// classifyFreshness keeps a successful snapshot visible while making an
// unsuccessful attempt visibly stale. A state without a first success is
// unknown rather than healthy. maxAge is the missed-refresh window after which
// a silent worker is stale.
func classifyFreshness(state InstanceState, now time.Time, maxAge time.Duration) Freshness {
	if state.Snapshot == nil || state.LastSuccess.IsZero() {
		return Unknown
	}
	if state.Err != nil {
		return Stale
	}
	if maxAge > 0 && !now.Before(state.LastSuccess.Add(maxAge)) {
		return Stale
	}
	return Fresh
}

func (a *App) pageView(selectedID string, now time.Time) PageView {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	instances := a.instances()
	if selectedID == "" {
		selectedID = a.selectedID()
	}
	views := make([]InstanceView, 0, len(instances))
	for _, instance := range instances {
		state, _ := a.state(instance.ID)
		views = append(views, instanceView(instance, state, instance.ID == selectedID, now, 3*a.refreshInterval(instance.ID)))
	}
	selected := InstanceView{}
	for _, view := range views {
		if view.ID == selectedID {
			selected = view
			break
		}
	}
	if selected.ID == "" && len(views) > 0 {
		views[0].IsSelected = true
		selected = views[0]
	}
	return PageView{Instances: views, Selected: selected}
}

func instanceView(instance Instance, state InstanceState, selected bool, now time.Time, maxAge time.Duration) InstanceView {
	view := InstanceView{
		ID:         instance.ID,
		Label:      instance.Label,
		Host:       instance.Host,
		IsSelected: selected,
		Freshness:  string(classifyFreshness(state, now, maxAge)),
		Reachable:  state.Snapshot != nil && state.Err == nil,
		Errors:     []string{},
		Triggers:   []TriggerViewModel{},
		LiveRuns:   []LiveRunViewModel{},
		RecentRuns: []RunViewModel{},
		Agents:     []AgentViewModel{},
	}
	if !state.LastSuccess.IsZero() {
		view.LastObserved = formatTime(state.LastSuccess)
	}
	if state.Err != nil {
		view.Errors = append(view.Errors, state.Err.Error())
	}
	if state.Snapshot == nil {
		if !state.LastAttempt.IsZero() && view.LastObserved == "" {
			view.LastObserved = "attempted " + formatTime(state.LastAttempt)
		}
		return view
	}
	snapshot := state.Snapshot
	view.Repo = snapshot.Config.Repo
	if view.Repo == "" {
		view.Repo = snapshot.Status.Repo
	}
	view.Version = snapshot.Version.BuildSHA
	if view.Version == "" {
		view.Version = "unknown"
	}
	if snapshot.Version.Dirty && view.Version != "unknown" {
		view.Version += " (dirty)"
	}
	view.Config = configView(snapshot)
	if view.Config.Repo == "" {
		view.Config.Repo = view.Repo
	}
	view.Audit = auditView(snapshot.Status.Audit)
	view.Triggers = triggerViews(snapshot.Status.Triggers)
	view.LiveRuns = liveRunViews(snapshot.Status.LiveRuns)
	view.ActiveRuns = len(view.LiveRuns)
	view.RecentRuns = runViews(snapshot.Status.Recent)
	view.Agents = agentViews(snapshot.Status.Ledger.Agents)
	if snapshot.Status.Kernel.RunningKnown {
		view.KernelKnown = true
		view.KernelRunning = snapshot.Status.Kernel.Running
	}
	if snapshot.Status.Kernel.LockError != "" {
		appendViewError(&view.Errors, "kernel: "+snapshot.Status.Kernel.LockError)
	}
	if snapshot.Status.TriggerStateError != "" {
		appendViewError(&view.Errors, "trigger state: "+snapshot.Status.TriggerStateError)
	}
	if snapshot.Status.LiveRunError != "" {
		appendViewError(&view.Errors, "live runs: "+snapshot.Status.LiveRunError)
	}
	for _, trigger := range view.Triggers {
		for _, err := range []string{trigger.PollError, trigger.RunError, trigger.AuditError} {
			if err != "" {
				appendViewError(&view.Errors, fmt.Sprintf("trigger %s: %s", trigger.Name, err))
			}
		}
	}
	return view
}

func configView(snapshot *Snapshot) ConfigViewModel {
	cfg := snapshot.Config
	view := ConfigViewModel{
		Repo:          cfg.Repo,
		Primary:       cfg.Primary,
		PrimarySource: cfg.PrimarySource,
		Agents:        make([]AgentConfigViewModel, 0, len(cfg.Agents)),
		Checks:        make([]CheckViewModel, 0, len(cfg.Checks)),
		Declarations:  make([]DeclarationViewModel, 0, len(snapshot.Declarations)),
	}
	if cfg.Scope != nil {
		view.Scope = ScopeViewModel{
			Label: cfg.Scope.Label, BranchPrefix: cfg.Scope.BranchPrefix,
			Subjects: append([]string(nil), cfg.Scope.Subjects...),
		}
	}
	names := make([]string, 0, len(cfg.Agents))
	for name := range cfg.Agents {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		agent := cfg.Agents[name]
		view.Agents = append(view.Agents, AgentConfigViewModel{
			Agent: name, Poll: agent.Poll, Interval: agent.Interval, MaxDuration: agent.MaxDuration,
		})
	}
	for _, check := range cfg.Checks {
		view.Checks = append(view.Checks, CheckViewModel{Name: check.Name, Run: check.Run})
	}
	for _, declaration := range snapshot.Declarations {
		view.Declarations = append(view.Declarations, DeclarationViewModel{
			Name: declaration.Name, Model: declaration.Model, ModelSource: declaration.ModelSource,
			Tools: append([]string(nil), declaration.Tools...), Thinking: declaration.Thinking,
			SystemPrompt: declaration.SystemPrompt, TaskPrompt: declaration.TaskPrompt,
			SkillPaths: append([]string(nil), declaration.SkillPaths...), DefinitionSHA: declaration.DefinitionSHA,
			MaxDuration: declaration.MaxDuration,
		})
	}
	return view
}

func auditView(audit AuditData) AuditView {
	result := strings.TrimSpace(audit.LastResult)
	trustClass := "unknown"
	if result != "" {
		switch strings.ToLower(result) {
		case "pass", "passed", "ok", "clean", "trusted":
			trustClass = "ok"
		case "fail", "failed", "error", "violations", "untrusted":
			trustClass = "error"
		default:
			trustClass = "unknown"
		}
	}
	if audit.AuditedMaster != "" && audit.LastMaster != "" && audit.AuditedMaster != audit.LastMaster {
		trustClass = "stale"
	}
	trust := result
	if trust == "" {
		trust = "unknown"
	}
	return AuditView{
		Baseline: audit.Baseline, LastMaster: audit.LastMaster, AuditedMaster: audit.AuditedMaster,
		LastAt: audit.LastAt, LastResult: audit.LastResult, Trust: trust, TrustClass: trustClass,
		Violations: append([]string(nil), audit.Violations...),
	}
}

func triggerViews(triggers []TriggerData) []TriggerViewModel {
	views := make([]TriggerViewModel, 0, len(triggers))
	for _, trigger := range triggers {
		view := TriggerViewModel{
			Name: trigger.Name, StateKnown: trigger.StateKnown, ConsecutiveErrors: trigger.ConsecutiveErrors,
			LastCode: trigger.LastCode, Running: trigger.Running, RunningKnown: trigger.RunningKnown,
			Stale: trigger.Stale, LastRun: trigger.LastRun, PollError: trigger.PollError,
			RunError: trigger.RunError, AuditError: trigger.AuditError,
		}
		view.PollStatus = triggerStatus(trigger.StateKnown, trigger.PollError)
		view.RunStatus = triggerStatus(trigger.RunningKnown, trigger.RunError)
		view.AuditStatus = triggerStatus(trigger.StateKnown, trigger.AuditError)
		if trigger.Stale && view.PollStatus == "ok" {
			view.PollStatus = "stale"
		}
		views = append(views, view)
	}
	return views
}

func triggerStatus(known bool, err string) string {
	if err != "" {
		return "error"
	}
	if !known {
		return "unknown"
	}
	return "ok"
}

func liveRunViews(runs []LiveRunData) []LiveRunViewModel {
	views := make([]LiveRunViewModel, 0, len(runs))
	for _, run := range runs {
		views = append(views, LiveRunViewModel{
			RunID: run.RunID, Agent: run.Agent, StartedAt: run.StartedAt, Elapsed: run.Elapsed, Status: "running",
		})
	}
	return views
}

func runViews(runs []RunData) []RunViewModel {
	views := make([]RunViewModel, 0, len(runs))
	for _, run := range runs {
		views = append(views, runView(run))
	}
	return views
}

func runView(run RunData) RunViewModel {
	status := "passed"
	if run.Exit != 0 {
		status = "failed"
	}
	return RunViewModel{
		RunID: run.RunID, Agent: run.Agent, Started: run.Started,
		DurationSeconds: run.Duration, Duration: formatDuration(run.Duration), Exit: run.Exit,
		ExitLabel: fmt.Sprintf("exit %d", run.Exit), Status: status, Error: run.Error,
		TokensIn: run.TokensIn, TokensOut: run.TokensOut, CacheRead: run.CacheRead,
		CacheWrite: run.CacheWrite, Reasoning: run.Reasoning, DefinitionSHA: run.DefinitionSHA,
	}
}

func agentViews(agents []AgentLedgerData) []AgentViewModel {
	views := make([]AgentViewModel, 0, len(agents))
	for _, agent := range agents {
		views = append(views, AgentViewModel{
			Agent: agent.Agent, Runs: agent.Runs, PassRate: agent.PassRate,
			PassRateLabel: fmt.Sprintf("%.0f%%", agent.PassRate*100), DurationP50: agent.DurationP50,
			DurationP50Label: formatDuration(agent.DurationP50), DurationP95: agent.DurationP95,
			DurationP95Label: formatDuration(agent.DurationP95), TokensIn: agent.TokensIn,
			TokensOut: agent.TokensOut, CacheRead: agent.CacheRead, CacheWrite: agent.CacheWrite,
			Reasoning: agent.Reasoning,
		})
	}
	return views
}

func formatDuration(seconds float64) string {
	if seconds <= 0 {
		return "0s"
	}
	if seconds < 1 {
		return fmt.Sprintf("%.0fms", seconds*1000)
	}
	if seconds < 60 {
		return fmt.Sprintf("%.1fs", seconds)
	}
	minutes := int(seconds / 60)
	remaining := seconds - float64(minutes*60)
	if minutes < 60 {
		return fmt.Sprintf("%dm %.0fs", minutes, remaining)
	}
	hours := minutes / 60
	minutes %= 60
	return fmt.Sprintf("%dh %dm", hours, minutes)
}

func formatTime(value time.Time) string {
	return value.UTC().Format("2006-01-02 15:04:05Z")
}

func appendViewError(errors *[]string, value string) {
	if value == "" {
		return
	}
	for _, existing := range *errors {
		if existing == value {
			return
		}
	}
	*errors = append(*errors, value)
}
