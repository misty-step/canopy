package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type fakeCollectorRunner struct {
	responses map[string]CommandResult
	calls     [][]string
}

func (f *fakeCollectorRunner) Run(_ context.Context, _ Instance, args []string) (CommandResult, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	key := commandRoute(args)
	response, ok := f.responses[key]
	if !ok {
		return CommandResult{Exit: -1}, errors.New("unexpected fake command " + key)
	}
	return response, nil
}

func commandRoute(args []string) string {
	if len(args) >= 3 && (args[0] == "declaration" && args[1] == "show" || args[0] == "run" && args[1] == "logs") {
		return args[0] + " " + args[1] + ":" + args[2]
	}
	if len(args) >= 2 && (args[0] == "config" || args[0] == "declaration" || args[0] == "run") {
		return args[0] + " " + args[1]
	}
	if len(args) > 0 {
		return args[0]
	}
	return ""
}

func fakeEnvelope(command string, exit int, data any, message *string) CommandResult {
	envelope := struct {
		Schema  string   `json:"schema"`
		Command string   `json:"command"`
		Args    []string `json:"args"`
		Exit    int      `json:"exit"`
		Data    any      `json:"data"`
		Error   *string  `json:"error"`
	}{
		Schema:  "forest.cli.v2",
		Command: command,
		Args:    []string{},
		Exit:    exit,
		Data:    data,
		Error:   message,
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		panic(err)
	}
	return CommandResult{Stdout: encoded, Exit: exit}
}

func validTestInstance(root string) Instance {
	return Instance{ID: "local", Label: "Local", Root: root, Forest: "/usr/bin/forest"}
}

func TestCLICollectorRejectsEnvelopeCommandMismatch(t *testing.T) {
	runner := &fakeCollectorRunner{responses: map[string]CommandResult{
		"status": fakeEnvelope("version", 0, VersionData{}, nil),
	}}
	_, err := NewCLICollectorWithRunner(0, runner).Collect(context.Background(), validTestInstance(t.TempDir()))
	var cliErr *CLIError
	if !errors.As(err, &cliErr) || cliErr.Command != "status" {
		t.Fatalf("mismatched envelope accepted: %v", err)
	}
}

func TestCLICollectorReturnsCommandFailure(t *testing.T) {
	message := "status unavailable"
	runner := &fakeCollectorRunner{responses: map[string]CommandResult{
		"status": fakeEnvelope("status", 2, nil, &message),
	}}
	_, err := NewCLICollectorWithRunner(0, runner).Collect(context.Background(), validTestInstance(t.TempDir()))
	var cliErr *CLIError
	if !errors.As(err, &cliErr) || cliErr.Command != "status" || cliErr.Exit != 2 || cliErr.Message != message {
		t.Fatalf("command failure not retained: %v", err)
	}
}

func TestCLICollectorRejectsMalformedEnvelopes(t *testing.T) {
	valid := `{"schema":"forest.cli.v2","command":"status","args":[],"exit":0,"data":{},"error":null}`
	cases := map[string]string{
		"wrong schema":    strings.Replace(valid, "forest.cli.v2", "forest.cli.v1", 1),
		"malformed":       `{"schema":`,
		"non-object":      `[]`,
		"trailing value":  valid + `{}`,
		"missing schema":  strings.Replace(valid, `"schema":"forest.cli.v2",`, "", 1),
		"missing command": strings.Replace(valid, `"command":"status",`, "", 1),
		"missing args":    strings.Replace(valid, `"args":[],`, "", 1),
		"missing exit":    strings.Replace(valid, `"exit":0,`, "", 1),
		"missing data":    strings.Replace(valid, `"data":{},`, "", 1),
		"missing error":   strings.Replace(valid, `,"error":null`, "", 1),
		"null args":       strings.Replace(valid, `"args":[]`, `"args":null`, 1),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			runner := &fakeCollectorRunner{responses: map[string]CommandResult{"status": {Stdout: []byte(raw)}}}
			_, err := NewCLICollectorWithRunner(0, runner).Collect(context.Background(), validTestInstance(t.TempDir()))
			var cliErr *CLIError
			if !errors.As(err, &cliErr) || cliErr.Command != "status" {
				t.Fatalf("invalid envelope accepted: %v", err)
			}
		})
	}
}

func TestCLICollectorStatusDoesNotRequireOptionalCommands(t *testing.T) {
	runner := &fakeCollectorRunner{responses: map[string]CommandResult{
		"status": fakeEnvelope("status", 0, StatusData{LiveRuns: []LiveRunData{{RunID: "new-run", RequestID: "request"}}}, nil),
	}}
	snapshot, err := NewCLICollectorWithRunner(0, runner).Collect(context.Background(), validTestInstance(t.TempDir()))
	if err != nil || len(snapshot.Status.LiveRuns) != 1 || snapshot.Status.LiveRuns[0].RequestID != "request" {
		t.Fatalf("optional command prevented status observation: %+v, %v", snapshot, err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("status invoked optional work: %v", runner.calls)
	}
}

func TestCLICollectorDetailsFailIndependently(t *testing.T) {
	message := "unknown version command"
	runner := &fakeCollectorRunner{responses: map[string]CommandResult{
		"version":                  fakeEnvelope("version", 6, nil, &message),
		"config show":              fakeEnvelope("config show", 0, ConfigData{Repo: "org/repo"}, nil),
		"declaration list":         fakeEnvelope("declaration list", 0, map[string]any{"declarations": []DeclarationData{{Name: "builder"}}}, nil),
		"declaration show:builder": fakeEnvelope("declaration show", 2, nil, &message),
		"run list":                 fakeEnvelope("run list", 0, map[string]any{"runs": []RunData{}, "next_after": ""}, nil),
	}}
	snapshot := NewCLICollectorWithRunner(0, runner).CollectDetails(context.Background(), validTestInstance(t.TempDir()))
	if snapshot.VersionObservation.Error == "" || !snapshot.VersionObservation.ObservedAt.IsZero() || snapshot.Version.BuildSHA != "" {
		t.Fatalf("unsupported version became a successful observation: %+v", snapshot)
	}
	if snapshot.Config.Repo != "org/repo" || snapshot.ConfigObservation.ObservedAt.IsZero() || snapshot.ConfigObservation.Error != "" {
		t.Fatalf("version failure blocked configuration: %+v", snapshot)
	}
	if snapshot.DeclarationsObservation.Error == "" || !snapshot.DeclarationsObservation.ObservedAt.IsZero() || snapshot.History.ObservedAt.IsZero() {
		t.Fatalf("declaration failure corrupted independent history observation: %+v", snapshot)
	}
}

func TestCLICollectorRejectsInvalidSSHBeforeExecution(t *testing.T) {
	runner := &fakeCollectorRunner{responses: map[string]CommandResult{}}
	collector := NewCLICollectorWithRunner(0, runner)
	instance := Instance{ID: "remote", Label: "Remote", Host: "host;touch", Root: "/srv/forest", Forest: "/usr/bin/forest"}
	_, err := collector.Collect(context.Background(), instance)
	if err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("Collect error=%v, want SSH validation failure", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner calls=%d, want no command construction for invalid SSH inventory", len(runner.calls))
	}
}

func TestCLICollectorLogsRetainedFalseIsKnown(t *testing.T) {
	runner := &fakeCollectorRunner{responses: map[string]CommandResult{
		"run logs:run-evicted": fakeEnvelope("run logs", 0, LogResult{RunID: "run-evicted", Retained: false, Complete: true}, nil),
	}}
	collector := NewCLICollectorWithRunner(0, runner)
	result, err := collector.Logs(context.Background(), validTestInstance(t.TempDir()), "run-evicted", false)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if result.RunID != "run-evicted" || result.Retained || !result.Complete || result.Text != "" {
		t.Fatalf("result=%+v, want known evicted log", result)
	}
}

func TestCLICollectorLogsExitFourIdentifiesUnknownRun(t *testing.T) {
	message := "run \"missing\" not found"
	runner := &fakeCollectorRunner{responses: map[string]CommandResult{
		"run logs:missing": fakeEnvelope("run logs", 4, nil, &message),
	}}
	collector := NewCLICollectorWithRunner(0, runner)
	_, err := collector.Logs(context.Background(), validTestInstance(t.TempDir()), "missing", false)
	if err == nil {
		t.Fatal("Logs succeeded for an unknown Run")
	}
	var cliErr *CLIError
	if !errors.As(err, &cliErr) {
		t.Fatalf("Logs error type=%T, want *CLIError", err)
	}
	if cliErr.Exit != 4 || cliErr.Command != "run logs" || cliErr.Message != message {
		t.Fatalf("CLI error=%+v, want exit-4 unknown Run", cliErr)
	}
}
