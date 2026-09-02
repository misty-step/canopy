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
		"version": fakeEnvelope("status", 0, VersionData{}, nil),
	}}
	collector := NewCLICollectorWithRunner(0, runner)
	_, err := collector.Collect(context.Background(), validTestInstance(t.TempDir()))
	if err == nil {
		t.Fatal("Collect succeeded for an envelope with the wrong command")
	}
	var cliErr *CLIError
	if !errors.As(err, &cliErr) {
		t.Fatalf("Collect error type=%T, want *CLIError", err)
	}
	if cliErr.Command != "version" || !strings.Contains(cliErr.Message, "does not match") {
		t.Fatalf("CLI error=%+v, want version command mismatch", cliErr)
	}
}

func TestCLICollectorReturnsCommandFailure(t *testing.T) {
	message := "configuration unreadable"
	runner := &fakeCollectorRunner{responses: map[string]CommandResult{
		"version": fakeEnvelope("version", 2, nil, &message),
	}}
	collector := NewCLICollectorWithRunner(0, runner)
	_, err := collector.Collect(context.Background(), validTestInstance(t.TempDir()))
	if err == nil {
		t.Fatal("Collect succeeded for a failing command")
	}
	var cliErr *CLIError
	if !errors.As(err, &cliErr) {
		t.Fatalf("Collect error type=%T, want *CLIError", err)
	}
	if cliErr.Command != "version" || cliErr.Exit != 2 || cliErr.Message != message {
		t.Fatalf("CLI error=%+v, want command failure", cliErr)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("calls=%d, want collection to stop after first failure", len(runner.calls))
	}
}

func TestCLICollectorCollectsAllCommandsAndDeclarationsLocally(t *testing.T) {
	status := StatusData{Repo: "org/repo", Kernel: KernelData{RunningKnown: true}}
	list := struct {
		Declarations []DeclarationData `json:"declarations"`
	}{Declarations: []DeclarationData{{Name: "builder"}, {Name: "legacy"}}}
	runner := &fakeCollectorRunner{responses: map[string]CommandResult{
		"version":                  fakeEnvelope("version", 0, VersionData{BuildSHA: "abc", Dirty: true}, nil),
		"config show":              fakeEnvelope("config show", 0, ConfigData{Repo: "org/repo"}, nil),
		"declaration list":         fakeEnvelope("declaration list", 0, list, nil),
		"declaration show:builder": fakeEnvelope("declaration show", 0, DeclarationData{Name: "builder", Model: "m"}, nil),
		"declaration show:legacy":  fakeEnvelope("declaration show", 0, DeclarationData{Name: "legacy", Model: "m"}, nil),
		"status":                   fakeEnvelope("status", 0, status, nil),
	}}
	collector := NewCLICollectorWithRunner(0, runner)
	snapshot, err := collector.Collect(context.Background(), validTestInstance(t.TempDir()))
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if snapshot.Version.BuildSHA != "abc" || !snapshot.Version.Dirty {
		t.Fatalf("version=%+v, want decoded version", snapshot.Version)
	}
	if len(snapshot.Declarations) != 2 || snapshot.Declarations[0].Name != "builder" || snapshot.Declarations[1].Name != "legacy" {
		t.Fatalf("declarations=%+v, want list order and both shows", snapshot.Declarations)
	}
	if snapshot.Status.Repo != "org/repo" {
		t.Fatalf("status=%+v, want decoded status", snapshot.Status)
	}
	if len(runner.calls) != 6 {
		t.Fatalf("calls=%d, want version/config/list/2 shows/status", len(runner.calls))
	}
	for _, call := range runner.calls {
		if len(call) < 4 || call[len(call)-3] != "--json" || call[len(call)-2] != "--root" {
			t.Fatalf("call=%q, want --json --root suffix", call)
		}
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
