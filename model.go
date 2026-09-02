package main

import (
	"context"
	"fmt"
	"time"
)

// Instance identifies one independent Iron Forest checkout. Host is empty for
// a local checkout; otherwise it is the SSH destination used to execute Forest.
type Instance struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Host   string `json:"host,omitempty"`
	Root   string `json:"root"`
	Forest string `json:"forest"`
}

// Inventory is Canopy's process configuration. Intervals are seconds and are
// applied by the server's refresh loops.
type Inventory struct {
	Listen                  string     `json:"listen"`
	FleetIntervalSeconds    int        `json:"fleet_interval_seconds"`
	SelectedIntervalSeconds int        `json:"selected_interval_seconds"`
	Instances               []Instance `json:"instances"`
}

// CLIError describes a Forest command that could not provide a valid answer.
// Exit is the Forest process/envelope exit status. An Exit of -1 means the
// process could not be started or did not provide a status.
type CLIError struct {
	Command string `json:"command"`
	Exit    int    `json:"exit"`
	Message string `json:"message"`
}

func (e *CLIError) Error() string {
	if e == nil {
		return "forest command failed"
	}
	if e.Command == "" {
		if e.Message == "" {
			return fmt.Sprintf("forest command failed (exit %d)", e.Exit)
		}
		return e.Message
	}
	if e.Message == "" {
		return fmt.Sprintf("forest %s failed (exit %d)", e.Command, e.Exit)
	}
	return fmt.Sprintf("forest %s failed (exit %d): %s", e.Command, e.Exit, e.Message)
}

// Snapshot is one complete, point-in-time projection of an instance. A
// collection either supplies all fields or returns an error; callers retain
// their previous successful snapshot when a later collection fails.
type Snapshot struct {
	Instance     Instance          `json:"instance"`
	CollectedAt  time.Time         `json:"collected_at"`
	Version      VersionData       `json:"version"`
	Config       ConfigData        `json:"config"`
	Status       StatusData        `json:"status"`
	Declarations []DeclarationData `json:"declarations"`
}

// LogResult is the machine-readable result of `forest run logs --json`.
// Retained=false and a successful call is a known, evicted log—not an error.
// Exit is nil while the Run has not completed.
type LogResult struct {
	RunID    string `json:"run_id"`
	Retained bool   `json:"retained"`
	Complete bool   `json:"complete"`
	Exit     *int   `json:"exit"`
	Text     string `json:"text"`
}

// Collector is the read-only boundary between Canopy and an Iron Forest
// instance.
type Collector interface {
	Collect(context.Context, Instance) (Snapshot, error)
	Logs(context.Context, Instance, string, bool) (LogResult, error)
}

// VersionData mirrors the stable fields emitted by `forest version --json`.
// Unknown additive CLI fields are intentionally ignored while decoding.
type VersionData struct {
	BuildSHA   string `json:"build_sha"`
	CommitTime string `json:"commit_time,omitempty"`
	Dirty      bool   `json:"dirty"`
}

// ScopeData mirrors the configured subject scope.
type ScopeData struct {
	Label        string   `json:"label,omitempty"`
	Subjects     []string `json:"subjects,omitempty"`
	BranchPrefix string   `json:"branch_prefix,omitempty"`
}

// AgentConfigData mirrors one configured agent's polling declaration.
type AgentConfigData struct {
	Poll        string `json:"poll"`
	Interval    int    `json:"interval"`
	MaxDuration int    `json:"max_duration,omitempty"`
}

// CheckData mirrors one configured verification check.
type CheckData struct {
	Name string `json:"name"`
	Run  string `json:"run"`
}

// ConfigData mirrors the loaded result of `forest config show --json`.
type ConfigData struct {
	Repo          string                     `json:"repo"`
	Primary       string                     `json:"primary"`
	PrimarySource string                     `json:"primary_source"`
	Scope         *ScopeData                 `json:"scope,omitempty"`
	Agents        map[string]AgentConfigData `json:"agents"`
	Checks        []CheckData                `json:"checks"`
}

// DeclarationData mirrors the fully resolved result of `forest declaration
// show <name> --json`.
type DeclarationData struct {
	Name          string   `json:"name"`
	Model         string   `json:"model"`
	Tools         []string `json:"tools"`
	Thinking      string   `json:"thinking"`
	SystemPrompt  string   `json:"system_prompt"`
	TaskPrompt    string   `json:"task_prompt"`
	ModelSource   string   `json:"model_source,omitempty"`
	SkillPaths    []string `json:"skills"`
	DefinitionSHA string   `json:"definition_sha,omitempty"`
	MaxDuration   int      `json:"max_duration,omitempty"`
}

// KernelData preserves the distinction between a stopped Kernel and one whose
// liveness could not be determined.
type KernelData struct {
	Running      bool   `json:"running"`
	RunningKnown bool   `json:"running_known"`
	LockError    string `json:"lock_error,omitempty"`
}

// TriggerData preserves known/unknown state independently for trigger state
// and Kernel-derived running state. Error strings are operator-authored and
// must remain visible to the external projection.
type TriggerData struct {
	Name              string `json:"name"`
	StateKnown        bool   `json:"state_known"`
	ConsecutiveErrors int    `json:"consecutive_errors"`
	LastCode          int    `json:"last_code"`
	Running           bool   `json:"running"`
	RunningKnown      bool   `json:"running_known"`
	Stale             bool   `json:"stale"`
	LastRun           string `json:"last_run,omitempty"`
	PollError         string `json:"poll_error,omitempty"`
	RunError          string `json:"run_error,omitempty"`
	AuditError        string `json:"audit_error,omitempty"`
}

// AuditData mirrors the last persisted Auditor state.
type AuditData struct {
	Baseline      string   `json:"baseline"`
	LastMaster    string   `json:"last_master"`
	AuditedMaster string   `json:"audited_master"`
	LastAt        string   `json:"last_at"`
	LastResult    string   `json:"last_result"`
	Violations    []string `json:"violations"`
}

// RunData mirrors one historical Ledger row, including all five retained token
// classes and the optional recorded failure reason.
type RunData struct {
	RunID         string  `json:"run_id"`
	Agent         string  `json:"agent"`
	Started       string  `json:"started"`
	Duration      float64 `json:"duration"`
	Exit          int     `json:"exit"`
	TokensIn      int64   `json:"tokens_in"`
	TokensOut     int64   `json:"tokens_out"`
	CacheRead     int64   `json:"cache_read"`
	CacheWrite    int64   `json:"cache_write"`
	Reasoning     int64   `json:"reasoning"`
	Error         string  `json:"error,omitempty"`
	DefinitionSHA string  `json:"definition_sha,omitempty"`
}

// LiveRunData mirrors one currently running Run.
type LiveRunData struct {
	RunID     string `json:"run_id"`
	Agent     string `json:"agent"`
	StartedAt string `json:"started_at"`
	Elapsed   string `json:"elapsed"`
	Cancel    string `json:"cancel"`
}

// AgentLedgerData is the whole-Ledger aggregate for one historical agent. An
// empty or historical agent identity is retained exactly as emitted.
type AgentLedgerData struct {
	Agent       string  `json:"agent"`
	Runs        int     `json:"runs"`
	PassRate    float64 `json:"pass_rate"`
	DurationP50 float64 `json:"duration_p50"`
	DurationP95 float64 `json:"duration_p95"`
	TokensIn    int64   `json:"tokens_in"`
	TokensOut   int64   `json:"tokens_out"`
	CacheRead   int64   `json:"cache_read"`
	CacheWrite  int64   `json:"cache_write"`
	Reasoning   int64   `json:"reasoning"`
}

// RunFailureData identifies one recent non-zero Ledger row.
type RunFailureData struct {
	RunID string `json:"run_id"`
	Agent string `json:"agent"`
	Exit  int    `json:"exit"`
	Error string `json:"error,omitempty"`
}

// LedgerData contains aggregates computed over every Ledger row, not only the
// recent status tail.
type LedgerData struct {
	Runs           int               `json:"runs"`
	PassRate       float64           `json:"pass_rate"`
	Agents         []AgentLedgerData `json:"agents"`
	RecentFailures []RunFailureData  `json:"recent_failures"`
}

// StatusData mirrors the complete status command payload.
type StatusData struct {
	Repo              string        `json:"repo"`
	Kernel            KernelData    `json:"kernel"`
	Triggers          []TriggerData `json:"triggers"`
	TriggerStateError string        `json:"trigger_state_error,omitempty"`
	LiveRunError      string        `json:"live_run_error,omitempty"`
	Audit             AuditData     `json:"audit"`
	Recent            []RunData     `json:"recent"`
	LiveRuns          []LiveRunData `json:"live_runs"`
	Ledger            *LedgerData   `json:"ledger,omitempty"`
}
