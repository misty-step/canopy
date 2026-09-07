package main

import (
	"context"
	"errors"
	"html/template"
	"sync"
	"testing"
	"time"
)

type testCollector struct {
	mu      sync.Mutex
	collect func(context.Context, Instance) (Snapshot, error)
	logs    func(context.Context, Instance, string, bool) (LogResult, error)
	active  int
	max     int
	calls   int
	started chan struct{}
}

func (c *testCollector) Collect(ctx context.Context, instance Instance) (Snapshot, error) {
	c.mu.Lock()
	c.calls++
	c.active++
	if c.active > c.max {
		c.max = c.active
	}
	if c.started != nil {
		select {
		case c.started <- struct{}{}:
		default:
		}
	}
	fn := c.collect
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.active--
		c.mu.Unlock()
	}()
	if fn == nil {
		return Snapshot{Instance: instance}, nil
	}
	return fn(ctx, instance)
}

func (c *testCollector) Logs(ctx context.Context, instance Instance, runID string, follow bool) (LogResult, error) {
	if c.logs == nil {
		return LogResult{RunID: runID, Complete: true}, nil
	}
	return c.logs(ctx, instance, runID, follow)
}

type blockingCollector struct {
	mu      sync.Mutex
	active  map[string]int
	max     map[string]int
	release chan struct{}
}

func newBlockingCollector() *blockingCollector {
	return &blockingCollector{
		active:  make(map[string]int),
		max:     make(map[string]int),
		release: make(chan struct{}),
	}
}

func (c *blockingCollector) Collect(ctx context.Context, instance Instance) (Snapshot, error) {
	c.mu.Lock()
	c.active[instance.ID]++
	if c.active[instance.ID] > c.max[instance.ID] {
		c.max[instance.ID] = c.active[instance.ID]
	}
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.active[instance.ID]--
		c.mu.Unlock()
	}()

	select {
	case <-c.release:
		return Snapshot{Instance: instance}, nil
	case <-ctx.Done():
		return Snapshot{}, ctx.Err()
	}
}

func (c *blockingCollector) Logs(context.Context, Instance, string, bool) (LogResult, error) {
	return LogResult{}, nil
}

func (c *blockingCollector) maxFor(id string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.max[id]
}

func (c *blockingCollector) activeFor(id string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active[id]
}

func testInventory() Inventory {
	return Inventory{
		FleetIntervalSeconds:    30,
		SelectedIntervalSeconds: 30,
		Instances:               []Instance{{ID: "one", Label: "One", Root: "/tmp/one", Forest: "forest"}},
	}
}

func TestRefreshRetainsLastSuccessfulSnapshotOnFailure(t *testing.T) {
	first := Snapshot{Instance: testInventory().Instances[0], Version: VersionData{BuildSHA: "abc"}}
	collector := &testCollector{}
	collector.collect = func(context.Context, Instance) (Snapshot, error) { return first, nil }
	app := NewApp(testInventory(), collector, nil)
	app.refreshOnce(context.Background(), first.Instance)
	before, ok := app.state("one")
	if !ok || before.Snapshot == nil {
		t.Fatalf("first refresh state=%+v, want snapshot", before)
	}
	lastSuccess := before.LastSuccess
	collector.collect = func(context.Context, Instance) (Snapshot, error) { return Snapshot{}, errors.New("offline") }
	app.refreshOnce(context.Background(), first.Instance)
	after, _ := app.state("one")
	if after.Snapshot == nil || after.Snapshot.Version.BuildSHA != "abc" {
		t.Fatalf("failure cleared snapshot: %+v", after.Snapshot)
	}
	if !after.LastSuccess.Equal(lastSuccess) {
		t.Fatalf("last success changed from %v to %v", lastSuccess, after.LastSuccess)
	}
	if after.Err == nil || after.Err.Error() != "offline" {
		t.Fatalf("error=%v, want offline", after.Err)
	}
	if !after.LastAttempt.After(lastSuccess) {
		t.Fatalf("last attempt=%v, want after last success=%v", after.LastAttempt, lastSuccess)
	}
	view := instanceView(first.Instance, after, true, time.Now().UTC(), time.Minute)
	if view.Freshness != string(Stale) || view.Reachable {
		t.Fatalf("view freshness=%q reachable=%t, want stale and unreachable", view.Freshness, view.Reachable)
	}
}

func TestRefreshWorkersDoNotOverlap(t *testing.T) {
	inventory := testInventory()
	inventory.SelectedIntervalSeconds = 1
	inventory.FleetIntervalSeconds = 1
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	collector := &testCollector{started: started}
	collector.collect = func(ctx context.Context, _ Instance) (Snapshot, error) {
		select {
		case <-release:
			return Snapshot{}, nil
		case <-ctx.Done():
			return Snapshot{}, ctx.Err()
		}
	}
	app := NewApp(inventory, collector, template.New("unused"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app.Start(ctx)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	for range 4 {
		app.requestRefresh("one")
	}
	// The collector remains blocked while all refresh requests queue at most one
	// follow-up. A second collector would make max exceed one.
	time.Sleep(20 * time.Millisecond)
	collector.mu.Lock()
	max := collector.max
	collector.mu.Unlock()
	if max != 1 {
		t.Fatalf("maximum concurrent collectors=%d, want 1", max)
	}
	close(release)
}

func TestStartupDiscoveredInstanceLaunchesSingleWorker(t *testing.T) {
	inventory := testInventory()
	inventory.SelectedIntervalSeconds = 1
	inventory.FleetIntervalSeconds = 1
	collector := newBlockingCollector()
	app := NewApp(inventory, collector, template.New("unused"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer close(collector.release)

	app.syncDiscoveredInstances(ctx, []Instance{
		{ID: "discovered", Label: "Discovered", Root: "/tmp/discovered", Forest: "forest"},
	})
	app.Start(ctx)

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if collector.maxFor("discovered") == 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := collector.maxFor("discovered"); got != 1 {
		t.Fatalf("maximum concurrent collectors for startup-discovered instance=%d, want 1", got)
	}
}

func TestStartupDiscoveredInstanceWorkerStopsAfterPrune(t *testing.T) {
	inventory := testInventory()
	inventory.SelectedIntervalSeconds = 1
	inventory.FleetIntervalSeconds = 1
	collector := newBlockingCollector()
	app := NewApp(inventory, collector, template.New("unused"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer close(collector.release)

	app.syncDiscoveredInstances(ctx, []Instance{
		{ID: "discovered", Label: "Discovered", Root: "/tmp/discovered", Forest: "forest"},
	})
	app.Start(ctx)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if collector.activeFor("discovered") > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := collector.activeFor("discovered"); got != 1 {
		t.Fatalf("active startup-discovered collectors before prune=%d, want 1", got)
	}

	app.syncDiscoveredInstances(ctx, nil)

	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if collector.activeFor("discovered") == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := collector.activeFor("discovered"); got != 0 {
		t.Fatalf("active startup-discovered collectors after prune=%d, want 0", got)
	}
}

func TestSyncDiscoveredInstancesPreservesExplicitInventory(t *testing.T) {
	app := NewApp(testInventory(), &testCollector{}, nil)

	discovered := []Instance{
		{ID: "discovered", Label: "Discovered", Root: "/tmp/discovered", Forest: "forest"},
	}
	app.syncDiscoveredInstances(context.Background(), discovered)

	got := app.instances()
	if len(got) != 2 {
		t.Fatalf("instances after first sync=%+v, want explicit plus discovered", got)
	}

	// A later discovery pass that no longer reports the discovered instance
	// must remove it while retaining the explicitly configured entry.
	app.syncDiscoveredInstances(context.Background(), nil)
	got = app.instances()
	if len(got) != 1 || got[0].ID != "one" {
		t.Fatalf("instances after empty sync=%+v, want only explicit instance one", got)
	}
}

func TestSyncDiscoveredInstancesResolvesCollidingIDs(t *testing.T) {
	collector := &testCollector{}
	collector.collect = func(_ context.Context, inst Instance) (Snapshot, error) {
		return Snapshot{Instance: inst}, nil
	}
	app := NewApp(testInventory(), collector, nil)

	rootUnderscore := "/tmp/agent_test"
	rootDash := "/tmp/agent-test"
	discovered := []Instance{
		{ID: "agent-test", Label: "Agent Test", Root: rootUnderscore, Forest: "forest"},
		{ID: "agent-test", Label: "Agent Test", Root: rootDash, Forest: "forest"},
	}
	app.syncDiscoveredInstances(context.Background(), discovered)

	got := app.instances()
	if len(got) != 3 {
		t.Fatalf("instances after colliding sync=%+v, want explicit plus two resolved", got)
	}
	byRoot := make(map[string]Instance)
	for _, inst := range got {
		byRoot[inst.Root] = inst
	}
	underscore, ok := byRoot[rootUnderscore]
	if !ok {
		t.Fatalf("missing instance for root %s: %+v", rootUnderscore, got)
	}
	dash, ok := byRoot[rootDash]
	if !ok {
		t.Fatalf("missing instance for root %s: %+v", rootDash, got)
	}
	if underscore.ID == dash.ID {
		t.Fatalf("colliding roots share ID %q", underscore.ID)
	}
	if err := validateRouteIdentifier(underscore.ID, "instance id"); err != nil {
		t.Errorf("resolved ID %q is invalid: %v", underscore.ID, err)
	}
	if err := validateRouteIdentifier(dash.ID, "instance id"); err != nil {
		t.Errorf("resolved ID %q is invalid: %v", dash.ID, err)
	}

	// Each resolved instance must keep its own worker/state binding: a
	// refresh for one root must not be attributed to the other.
	for _, inst := range []Instance{underscore, dash} {
		app.refreshOnce(context.Background(), inst)
		state, ok := app.state(inst.ID)
		if !ok || state.Snapshot == nil {
			t.Fatalf("no snapshot for resolved instance %q", inst.ID)
		}
		if state.Snapshot.Instance.Root != inst.Root {
			t.Fatalf("snapshot for %q has root %q, want %q", inst.ID, state.Snapshot.Instance.Root, inst.Root)
		}
	}

	// Reordered scans must keep the same route-to-root binding.
	reordered := []Instance{discovered[1], discovered[0]}
	fresh := NewApp(testInventory(), collector, nil)
	fresh.syncDiscoveredInstances(context.Background(), reordered)
	freshByRoot := make(map[string]string)
	for _, inst := range fresh.instances() {
		freshByRoot[inst.Root] = inst.ID
	}
	for _, inst := range got {
		if inst.ID == "one" {
			continue
		}
		if freshByRoot[inst.Root] != inst.ID {
			t.Fatalf("reordered scan changed binding for %s: %q vs %q", inst.Root, freshByRoot[inst.Root], inst.ID)
		}
	}

	// Fleet selection must reach each resolved instance.
	for _, inst := range []Instance{underscore, dash} {
		if _, ok := app.state(inst.ID); !ok {
			t.Fatalf("missing state for resolved instance %q", inst.ID)
		}
		app.selectInstance(underscore.ID)
		app.selectInstance(inst.ID)
		if got := app.selectedID(); got != inst.ID {
			t.Fatalf("selected=%q after selecting %q", got, inst.ID)
		}
	}
}

func TestSyncDiscoveredInstancesPreservesExplicitOnIDCollision(t *testing.T) {
	inventory := testInventory()
	inventory.Instances = []Instance{{ID: "agent-test", Label: "Explicit", Root: "/explicit/agent", Forest: "forest"}}
	app := NewApp(inventory, &testCollector{}, nil)

	discovered := []Instance{
		{ID: "agent-test", Label: "Agent Test", Root: "/tmp/agent-test", Forest: "forest"},
	}
	app.syncDiscoveredInstances(context.Background(), discovered)

	got := app.instances()
	if len(got) != 2 {
		t.Fatalf("instances after explicit collision=%+v, want explicit plus renamed", got)
	}
	if got[0].ID != "agent-test" || got[0].Root != "/explicit/agent" || got[0].Label != "Explicit" {
		t.Fatalf("explicit entry was overwritten: %+v", got[0])
	}
	if got[1].ID == "agent-test" {
		t.Fatalf("discovered collision was not renamed: %+v", got[1])
	}
	if got[1].Root != "/tmp/agent-test" {
		t.Fatalf("renamed discovered root=%q, want /tmp/agent-test", got[1].Root)
	}
	if _, ok := app.state(got[1].ID); !ok {
		t.Fatalf("missing state for renamed discovered %q", got[1].ID)
	}
	app.selectInstance(got[1].ID)
	if selected := app.selectedID(); selected != got[1].ID {
		t.Fatalf("selected=%q, want renamed discovered %q", selected, got[1].ID)
	}
}

func TestSyncDiscoveredInstancesOmitsExplicitRootDuplicate(t *testing.T) {
	inventory := testInventory()
	inventory.Instances = []Instance{{ID: "explicit", Label: "Explicit", Root: "/tmp/shared", Forest: "forest"}}
	app := NewApp(inventory, &testCollector{}, nil)

	discovered := []Instance{
		{ID: "shared", Label: "Shared", Root: "/tmp/shared", Forest: "forest"},
	}
	app.syncDiscoveredInstances(context.Background(), discovered)

	got := app.instances()
	if len(got) != 1 || got[0].ID != "explicit" {
		t.Fatalf("instances after root duplicate=%+v, want only explicit entry", got)
	}
	if _, exists := app.state("shared"); exists {
		t.Fatalf("duplicate root was presented as independently observed instance")
	}
}
