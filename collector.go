package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	defaultCollectorTimeout = 30 * time.Second
	logsPollInterval        = 100 * time.Millisecond
)

// CommandResult is the process-level result returned by a CommandRunner.
// Stdout and Stderr are kept separate because only stdout is a CLI envelope;
// stderr remains useful when a process dies before it can emit JSON.
type CommandResult struct {
	Stdout []byte
	Stderr []byte
	Exit   int
}

// CommandRunner executes one already-tokenized Forest command. Implementations
// must not invoke a shell. The interface exists both for the local/SSH runners
// and for deterministic collector tests.
type CommandRunner interface {
	Run(context.Context, Instance, []string) (CommandResult, error)
}

// CommandRunnerFunc adapts a function to CommandRunner.
type CommandRunnerFunc func(context.Context, Instance, []string) (CommandResult, error)

func (f CommandRunnerFunc) Run(ctx context.Context, instance Instance, args []string) (CommandResult, error) {
	return f(ctx, instance, args)
}

type cliCollector struct {
	timeout time.Duration
	runner  CommandRunner
}

// NewCLICollector creates a collector that executes the configured Forest
// binary locally or over SSH. A non-positive timeout selects a bounded default
// so an unavailable instance cannot pin a refresh loop forever.
func NewCLICollector(timeout time.Duration) Collector {
	return NewCLICollectorWithRunner(timeout, processCommandRunner{})
}

// NewCLICollectorWithRunner is the injectable form used by deterministic tests.
// Production callers should normally use NewCLICollector.
func NewCLICollectorWithRunner(timeout time.Duration, runner CommandRunner) Collector {
	if timeout <= 0 {
		timeout = defaultCollectorTimeout
	}
	if runner == nil {
		runner = processCommandRunner{}
	}
	return &cliCollector{timeout: timeout, runner: runner}
}

func newCLICollector(timeout time.Duration, runner CommandRunner) *cliCollector {
	collector := NewCLICollectorWithRunner(timeout, runner)
	return collector.(*cliCollector)
}

// processCommandRunner invokes a local binary directly or passes a quoted,
// single remote command through ssh. No command string supplied by inventory or
// a route is interpreted by a local shell.
type processCommandRunner struct{}

func (processCommandRunner) Run(ctx context.Context, instance Instance, args []string) (CommandResult, error) {
	if err := validateInstance(instance); err != nil {
		return CommandResult{Exit: -1}, err
	}
	var command *exec.Cmd
	if instance.Host == "" {
		command = exec.CommandContext(ctx, instance.Forest, args...)
	} else {
		if err := validateSSHInvocation(instance, args); err != nil {
			return CommandResult{Exit: -1}, err
		}
		remote := make([]string, 0, len(args)+1)
		remote = append(remote, instance.Forest)
		remote = append(remote, args...)
		quoted := make([]string, len(remote))
		for index, value := range remote {
			quoted[index] = shellQuote(value)
		}
		// `--` prevents a validated destination from being interpreted as an
		// option. The remote command is one shell-quoted argument; ssh itself
		// necessarily uses the remote login shell, but none of our tokens can
		// alter its syntax.
		command = exec.CommandContext(ctx, "ssh", "--", instance.Host, strings.Join(quoted, " "))
	}
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	result := CommandResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), Exit: 0}
	if err != nil {
		result.Exit = -1
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			result.Exit = exitError.ExitCode()
		}
	}
	return result, err
}

func validateSSHInvocation(instance Instance, args []string) error {
	if err := validateSSHHost(instance.Host); err != nil {
		return err
	}
	if err := validateSSHPath(instance.Root, "root"); err != nil {
		return err
	}
	if err := validateSSHExecutable(instance.Forest); err != nil {
		return err
	}
	for index, arg := range args {
		if strings.ContainsRune(arg, '\x00') {
			return fmt.Errorf("SSH argument %d contains NUL", index)
		}
		if strings.IndexFunc(arg, unicodeSpace) >= 0 {
			return fmt.Errorf("SSH argument %d contains whitespace", index)
		}
	}
	return nil
}

func unicodeSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\r' || r == '\n' || r == '\v' || r == '\f'
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func (c *cliCollector) Collect(ctx context.Context, instance Instance) (Snapshot, error) {
	if ctx == nil {
		return Snapshot{}, fmt.Errorf("collect context is nil")
	}
	if err := validateInstance(instance); err != nil {
		return Snapshot{}, err
	}
	snapshot := Snapshot{Instance: instance, CollectedAt: time.Now().UTC()}
	versionRaw, err := c.runJSON(ctx, instance, "version", []string{"version"})
	if err != nil {
		return Snapshot{}, err
	}
	if err := decodeCommandData(versionRaw, "version", &snapshot.Version); err != nil {
		return Snapshot{}, err
	}

	configRaw, err := c.runJSON(ctx, instance, "config show", []string{"config", "show"})
	if err != nil {
		return Snapshot{}, err
	}
	if err := decodeCommandData(configRaw, "config show", &snapshot.Config); err != nil {
		return Snapshot{}, err
	}

	listRaw, err := c.runJSON(ctx, instance, "declaration list", []string{"declaration", "list"})
	if err != nil {
		return Snapshot{}, err
	}
	var list struct {
		Declarations []DeclarationData `json:"declarations"`
	}
	if err := decodeCommandData(listRaw, "declaration list", &list); err != nil {
		return Snapshot{}, err
	}
	if list.Declarations == nil {
		return Snapshot{}, commandDataError("declaration list", "data.declarations is null or missing")
	}
	snapshot.Declarations = make([]DeclarationData, 0, len(list.Declarations))
	seenDeclarations := make(map[string]struct{}, len(list.Declarations))
	for index, listed := range list.Declarations {
		if err := validateRouteIdentifier(listed.Name, "declaration name"); err != nil {
			return Snapshot{}, commandDataError("declaration list", fmt.Sprintf("declarations[%d]: %s", index, err))
		}
		if _, duplicate := seenDeclarations[listed.Name]; duplicate {
			return Snapshot{}, commandDataError("declaration list", fmt.Sprintf("duplicate declaration %q", listed.Name))
		}
		seenDeclarations[listed.Name] = struct{}{}
		declarationRaw, showErr := c.runJSON(ctx, instance, "declaration show", []string{"declaration", "show", listed.Name})
		if showErr != nil {
			return Snapshot{}, showErr
		}
		var declaration DeclarationData
		if err := decodeCommandData(declarationRaw, "declaration show", &declaration); err != nil {
			return Snapshot{}, err
		}
		if declaration.Name == "" {
			// Older Forest builds omitted the repeated name from the show payload;
			// the list identity is authoritative while all supplied fields remain.
			declaration.Name = listed.Name
		} else if declaration.Name != listed.Name {
			return Snapshot{}, commandDataError("declaration show", fmt.Sprintf("returned name %q for %q", declaration.Name, listed.Name))
		}
		snapshot.Declarations = append(snapshot.Declarations, declaration)
	}

	statusRaw, err := c.runJSON(ctx, instance, "status", []string{"status"})
	if err != nil {
		return Snapshot{}, err
	}
	if err := decodeCommandData(statusRaw, "status", &snapshot.Status); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func (c *cliCollector) Logs(ctx context.Context, instance Instance, runID string, follow bool) (LogResult, error) {
	if ctx == nil {
		return LogResult{}, fmt.Errorf("logs context is nil")
	}
	if err := validateInstance(instance); err != nil {
		return LogResult{}, err
	}
	if err := validateRouteIdentifier(runID, "run id"); err != nil {
		return LogResult{}, err
	}
	for {
		raw, err := c.runJSON(ctx, instance, "run logs", []string{"run", "logs", runID})
		if err != nil {
			return LogResult{}, err
		}
		var result LogResult
		if err := decodeCommandData(raw, "run logs", &result); err != nil {
			return LogResult{}, err
		}
		if result.RunID != "" && result.RunID != runID {
			return LogResult{}, commandDataError("run logs", fmt.Sprintf("returned run_id %q for %q", result.RunID, runID))
		}
		if result.RunID == "" {
			result.RunID = runID
		}
		if !follow || result.Complete {
			return result, nil
		}
		timer := time.NewTimer(logsPollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return LogResult{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func (c *cliCollector) runJSON(ctx context.Context, instance Instance, command string, args []string) (json.RawMessage, error) {
	if err := validateInstance(instance); err != nil {
		return nil, commandError(command, -1, err.Error())
	}
	if instance.Host != "" {
		if err := validateSSHInvocation(instance, args); err != nil {
			return nil, commandError(command, -1, err.Error())
		}
	}
	child, cancel := context.WithCancel(ctx)
	if c.timeout > 0 {
		child, cancel = context.WithTimeout(ctx, c.timeout)
	}
	defer cancel()
	result, runErr := c.runner.Run(child, instance, argsWithJSONRoot(args, instance.Root))
	if len(bytes.TrimSpace(result.Stdout)) == 0 {
		if runErr != nil {
			return nil, commandError(command, resultExit(result, runErr), commandFailureMessage(result, runErr))
		}
		if result.Exit != 0 {
			return nil, commandError(command, result.Exit, commandFailureMessage(result, nil))
		}
		return nil, commandError(command, -1, "command emitted no JSON envelope")
	}
	envelope, envelopeErr := decodeCLIEnvelope(result.Stdout)
	if envelopeErr != nil {
		if runErr != nil || result.Exit != 0 {
			return nil, commandError(command, resultExit(result, runErr), fmt.Sprintf("invalid CLI envelope: %v; %s", envelopeErr, commandFailureMessage(result, runErr)))
		}
		return nil, commandError(command, 0, fmt.Sprintf("invalid CLI envelope: %v", envelopeErr))
	}
	if envelope.Schema != "forest.cli.v2" {
		return nil, commandError(command, envelope.Exit, fmt.Sprintf("invalid schema %q (want %q)", envelope.Schema, "forest.cli.v2"))
	}
	if envelope.Command != command {
		return nil, commandError(command, envelope.Exit, fmt.Sprintf("envelope command %q does not match %q", envelope.Command, command))
	}
	if result.Exit != 0 && envelope.Exit != result.Exit {
		return nil, commandError(command, result.Exit, fmt.Sprintf("process exit %d does not match envelope exit %d", result.Exit, envelope.Exit))
	}
	if envelope.Exit != 0 {
		message := envelopeErrorMessage(envelope.Error)
		if message == "" {
			message = commandFailureMessage(result, runErr)
		}
		return nil, commandError(command, envelope.Exit, message)
	}
	if runErr != nil {
		return nil, commandError(command, resultExit(result, runErr), commandFailureMessage(result, runErr))
	}
	if result.Exit != 0 {
		return nil, commandError(command, result.Exit, commandFailureMessage(result, nil))
	}
	if !rawPresent(envelope.Data) || bytes.Equal(bytes.TrimSpace(envelope.Data), []byte("null")) {
		return nil, commandError(command, 0, "successful envelope has null or missing data")
	}
	if !rawPresent(envelope.Error) || !bytes.Equal(bytes.TrimSpace(envelope.Error), []byte("null")) {
		return nil, commandError(command, 0, "successful envelope error is not null")
	}
	return envelope.Data, nil
}

func argsWithJSONRoot(args []string, root string) []string {
	result := make([]string, 0, len(args)+3)
	result = append(result, args...)
	result = append(result, "--json", "--root", root)
	return result
}

type cliEnvelope struct {
	Schema  string
	Command string
	Args    []string
	Exit    int
	Data    json.RawMessage
	Error   json.RawMessage
}

func decodeCLIEnvelope(contents []byte) (cliEnvelope, error) {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	var fields map[string]json.RawMessage
	if err := decoder.Decode(&fields); err != nil {
		return cliEnvelope{}, err
	}
	if fields == nil {
		return cliEnvelope{}, fmt.Errorf("envelope is not an object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return cliEnvelope{}, fmt.Errorf("multiple JSON values")
		}
		return cliEnvelope{}, fmt.Errorf("trailing JSON: %w", err)
	}
	for _, name := range []string{"schema", "command", "args", "exit", "data", "error"} {
		if _, ok := fields[name]; !ok {
			return cliEnvelope{}, fmt.Errorf("missing %q", name)
		}
	}
	var envelope cliEnvelope
	if err := unmarshalEnvelopeField(fields, "schema", &envelope.Schema); err != nil {
		return cliEnvelope{}, err
	}
	if err := unmarshalEnvelopeField(fields, "command", &envelope.Command); err != nil {
		return cliEnvelope{}, err
	}
	if err := unmarshalEnvelopeField(fields, "args", &envelope.Args); err != nil {
		return cliEnvelope{}, err
	}
	if envelope.Args == nil {
		return cliEnvelope{}, fmt.Errorf("args is null")
	}
	if err := unmarshalEnvelopeField(fields, "exit", &envelope.Exit); err != nil {
		return cliEnvelope{}, err
	}
	envelope.Data = fields["data"]
	envelope.Error = fields["error"]
	return envelope, nil
}

func unmarshalEnvelopeField(fields map[string]json.RawMessage, name string, target any) error {
	if err := json.Unmarshal(fields[name], target); err != nil {
		return fmt.Errorf("invalid %s: %w", name, err)
	}
	return nil
}

func decodeCommandData(raw json.RawMessage, command string, target any) error {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return commandDataError(command, "data is null")
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return commandDataError(command, fmt.Sprintf("invalid data: %v", err))
	}
	return nil
}

func commandDataError(command, message string) error {
	return commandError(command, 0, message)
}

func commandError(command string, exit int, message string) error {
	return &CLIError{Command: command, Exit: exit, Message: message}
}

func resultExit(result CommandResult, runErr error) int {
	if result.Exit != 0 {
		return result.Exit
	}
	if runErr != nil {
		return -1
	}
	return 0
}

func commandFailureMessage(result CommandResult, runErr error) string {
	if text := strings.TrimSpace(string(result.Stderr)); text != "" {
		return text
	}
	if runErr != nil {
		return runErr.Error()
	}
	if result.Exit != 0 {
		return "command exited " + strconv.Itoa(result.Exit)
	}
	return "command failed"
}

func envelopeErrorMessage(raw json.RawMessage) string {
	if !rawPresent(raw) || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return ""
	}
	var message string
	if err := json.Unmarshal(raw, &message); err != nil {
		return fmt.Sprintf("invalid envelope error: %v", err)
	}
	return message
}

func rawPresent(raw json.RawMessage) bool {
	return len(bytes.TrimSpace(raw)) > 0
}
