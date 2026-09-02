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
