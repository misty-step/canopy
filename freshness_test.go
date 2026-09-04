package main

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"
)

func freshnessTestSnapshot(instance Instance) Snapshot {
	return Snapshot{
		Instance:    instance,
		CollectedAt: time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC),
		Version:     VersionData{BuildSHA: "abc"},
		Config:      ConfigData{Repo: "misty-step/canopy", Primary: "master"},
		Status:      StatusData{Repo: "misty-step/canopy"},
	}
}

func seedFreshnessSuccess(app *App, id string, at time.Time) {
	app.mu.Lock()
	defer app.mu.Unlock()
	state := app.states[id]
	if state == nil {
		state = &InstanceState{}
		app.states[id] = state
	}
	var instance Instance
	for _, candidate := range app.inventory.Instances {
		if candidate.ID == id {
			instance = candidate
			break
		}
	}
	state.Snapshot = &Snapshot{}
	snapshot := freshnessTestSnapshot(instance)
	state.Snapshot = &snapshot
	state.LastSuccess = at
	state.LastAttempt = at
	state.Err = nil
}

func renderInstanceFreshness(t *testing.T, view InstanceView) string {
	t.Helper()
	templates, err := loadTemplates()
	if err != nil {
		t.Fatalf("loadTemplates: %v", err)
	}
	fragment := templates.Lookup("instance.html")
	if fragment == nil {
		t.Fatal("instance.html template not found")
	}
	var body bytes.Buffer
	if err := fragment.Execute(&body, view); err != nil {
		t.Fatalf("render instance: %v", err)
	}
	return body.String()
}

func renderFleetFreshness(t *testing.T, view PageView) string {
	t.Helper()
	templates, err := loadTemplates()
	if err != nil {
		t.Fatalf("loadTemplates: %v", err)
	}
	fragment := templates.Lookup("fleet.html")
	if fragment == nil {
		t.Fatal("fleet.html template not found")
	}
	var body bytes.Buffer
	if err := fragment.Execute(&body, view); err != nil {
		t.Fatalf("render fleet: %v", err)
	}
	return body.String()
}

func TestFreshnessCycleBudgetSelectedWindow(t *testing.T) {
	inventory := Inventory{
		FleetIntervalSeconds:    10,
		SelectedIntervalSeconds: 2,
		Instances:               []Instance{{ID: "one", Label: "One", Root: "/tmp/one", Forest: "forest"}},
	}
	base := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	selectedWindow := 2*time.Second + refreshTimeout + freshnessSchedulingSlack
	if selectedWindow != 33*time.Second {
		t.Fatalf("selected window=%v, want 33s (2s interval + 30s collection bound + 1s slack)", selectedWindow)
	}

	app := NewApp(inventory, &testCollector{}, nil)
	seedFreshnessSuccess(app, "one", base)

	// A healthy selected instance must stay fresh beyond the obsolete 6s
	// (3x2s) threshold while inside one legitimate collection cycle.
	for _, age := range []time.Duration{7 * time.Second, 10 * time.Second, 30 * time.Second, selectedWindow - time.Millisecond} {
		view := app.pageView("one", base.Add(age))
		if view.Selected.Freshness != string(Fresh) {
			t.Fatalf("age=%v freshness=%q, want fresh (window=%v)", age, view.Selected.Freshness, selectedWindow)
		}
		if view.Selected.LastObserved != formatTime(base) {
			t.Fatalf("age=%v last observed=%q, want %q", age, view.Selected.LastObserved, formatTime(base))
		}
	}

	freshBody := renderInstanceFreshness(t, app.pageView("one", base.Add(10*time.Second)).Selected)
	if !bytes.Contains([]byte(freshBody), []byte(`status-badge fresh`)) {
		t.Fatalf("fresh instance render missing fresh badge: %s", freshBody)
	}
	fleetBody := renderFleetFreshness(t, app.pageView("one", base.Add(10*time.Second)))
	if !bytes.Contains([]byte(fleetBody), []byte(`state-dot fresh`)) {
		t.Fatalf("fresh fleet render missing fresh dot: %s", fleetBody)
	}

	// At the new expiry boundary a silent worker is stale even without an
	// explicit error.
	for _, age := range []time.Duration{selectedWindow, selectedWindow + 10*time.Second} {
		view := app.pageView("one", base.Add(age))
		if view.Selected.Freshness != string(Stale) {
			t.Fatalf("age=%v freshness=%q, want stale (window=%v)", age, view.Selected.Freshness, selectedWindow)
		}
	}
	staleBody := renderInstanceFreshness(t, app.pageView("one", base.Add(selectedWindow)).Selected)
	if !bytes.Contains([]byte(staleBody), []byte(`status-badge stale`)) {
		t.Fatalf("stale instance render missing stale badge: %s", staleBody)
	}
	staleFleet := renderFleetFreshness(t, app.pageView("one", base.Add(selectedWindow)))
	if !bytes.Contains([]byte(staleFleet), []byte(`state-dot stale`)) {
		t.Fatalf("stale fleet render missing stale dot: %s", staleFleet)
	}

	// An explicit collection error is stale immediately, without waiting for
	// the missed-refresh budget.
	app.mu.Lock()
	app.states["one"].Err = context.DeadlineExceeded
	app.mu.Unlock()
	immediate := app.pageView("one", base.Add(time.Second))
	if immediate.Selected.Freshness != string(Stale) {
		t.Fatalf("explicit error freshness=%q, want stale", immediate.Selected.Freshness)
	}
	errorBody := renderInstanceFreshness(t, immediate.Selected)
	if !bytes.Contains([]byte(errorBody), []byte(`status-badge stale`)) {
		t.Fatalf("error instance render missing stale badge: %s", errorBody)
	}

	// No successful snapshot is unknown, never fresh or stale.
	empty := NewApp(inventory, &testCollector{}, nil)
	unknown := empty.pageView("one", base.Add(time.Second))
	if unknown.Selected.Freshness != string(Unknown) {
		t.Fatalf("no-success freshness=%q, want unknown", unknown.Selected.Freshness)
	}
}

func TestFreshnessCoversFleetAndIntervalChanges(t *testing.T) {
	base := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	inventory := Inventory{
		FleetIntervalSeconds:    10,
		SelectedIntervalSeconds: 2,
		Instances: []Instance{
			{ID: "one", Label: "One", Root: "/tmp/one", Forest: "forest"},
			{ID: "two", Label: "Two", Root: "/tmp/two", Forest: "forest"},
		},
	}
	app := NewApp(inventory, &testCollector{}, nil)
	seedFreshnessSuccess(app, "one", base)
	seedFreshnessSuccess(app, "two", base)

	fleetWindow := 10*time.Second + refreshTimeout + freshnessSchedulingSlack
	if fleetWindow != 41*time.Second {
		t.Fatalf("fleet window=%v, want 41s", fleetWindow)
	}

	// Fleet instance "two" is not selected, so its budget uses the fleet
	// interval. 35s exceeds the obsolete 30s (3x10s) fleet threshold but is
	// inside one legitimate fleet cycle.
	view := app.pageView("one", base.Add(35*time.Second))
	var fleetView InstanceView
	for _, candidate := range view.Instances {
		if candidate.ID == "two" {
			fleetView = candidate
		}
	}
	if fleetView.Freshness != string(Fresh) {
		t.Fatalf("fleet age=35s freshness=%q, want fresh (window=%v)", fleetView.Freshness, fleetWindow)
	}
	if view.Selected.Freshness != string(Stale) {
		t.Fatalf("selected age=35s freshness=%q, want stale (selected window=33s)", view.Selected.Freshness)
	}

	expired := app.pageView("one", base.Add(fleetWindow))
	for _, candidate := range expired.Instances {
		if candidate.ID == "two" && candidate.Freshness != string(Stale) {
			t.Fatalf("fleet at boundary freshness=%q, want stale", candidate.Freshness)
		}
	}

	// Changing the configured intervals changes the authoritative budget
	// without any additional configuration.
	changed := Inventory{
		FleetIntervalSeconds:    20,
		SelectedIntervalSeconds: 5,
		Instances:               inventory.Instances,
	}
	changedApp := NewApp(changed, &testCollector{}, nil)
	seedFreshnessSuccess(changedApp, "one", base)
	seedFreshnessSuccess(changedApp, "two", base)
	newSelectedWindow := 5*time.Second + refreshTimeout + freshnessSchedulingSlack
	newFleetWindow := 20*time.Second + refreshTimeout + freshnessSchedulingSlack
	mid := changedApp.pageView("one", base.Add(35*time.Second))
	if mid.Selected.Freshness != string(Fresh) {
		t.Fatalf("changed selected age=35s freshness=%q, want fresh (window=%v)", mid.Selected.Freshness, newSelectedWindow)
	}
	for _, candidate := range mid.Instances {
		if candidate.ID == "two" && candidate.Freshness != string(Fresh) {
			t.Fatalf("changed fleet age=35s freshness=%q, want fresh (window=%v)", candidate.Freshness, newFleetWindow)
		}
	}
	atNewSelectedExpiry := changedApp.pageView("one", base.Add(newSelectedWindow))
	if atNewSelectedExpiry.Selected.Freshness != string(Stale) {
		t.Fatalf("changed selected at boundary freshness=%q, want stale", atNewSelectedExpiry.Selected.Freshness)
	}
}

type slowSuccessCollector struct {
	mu       sync.Mutex
	active   int
	max      int
	entered  chan struct{}
	release  chan struct{}
	snapshot Snapshot
}

func (c *slowSuccessCollector) Collect(ctx context.Context, instance Instance) (Snapshot, error) {
	c.mu.Lock()
	c.active++
	if c.active > c.max {
		c.max = c.active
	}
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.active--
		c.mu.Unlock()
	}()
	select {
	case c.entered <- struct{}{}:
	default:
	}
	select {
	case <-c.release:
		return c.snapshot, nil
	case <-ctx.Done():
		return Snapshot{}, ctx.Err()
	}
}

func (c *slowSuccessCollector) Logs(context.Context, Instance, string, bool) (LogResult, error) {
	return LogResult{}, nil
}

func (c *slowSuccessCollector) maximum() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.max
}

func TestSlowSuccessRefreshRecoversWithoutOverlap(t *testing.T) {
	inventory := Inventory{
		FleetIntervalSeconds:    300,
		SelectedIntervalSeconds: 300,
		Instances:               []Instance{{ID: "one", Label: "One", Root: "/tmp/one", Forest: "forest"}},
	}
	collector := &slowSuccessCollector{
		entered: make(chan struct{}, 8),
		release: make(chan struct{}),
		snapshot: Snapshot{
			Instance: inventory.Instances[0],
			Version:  VersionData{BuildSHA: "slow-ok"},
			Config:   ConfigData{Repo: "misty-step/canopy", Primary: "master"},
			Status:   StatusData{Repo: "misty-step/canopy"},
		},
	}
	app := NewApp(inventory, collector, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app.Start(ctx)

	// Wait for the immediate first collection to block inside the slow
	// collector. No sleeps: channel synchronization only.
	select {
	case <-collector.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("slow collector did not start")
	}

	// Queue follow-up refreshes while the first collection is still running.
	// The loop runs collections synchronously, so at most one follow-up may
	// run after the release.
	for range 4 {
		app.requestRefresh("one")
	}
	app.mu.RLock()
	attemptBefore := app.states["one"].LastAttempt
	app.mu.RUnlock()
	if attemptBefore.IsZero() {
		t.Fatal("first attempt was not recorded before release")
	}
	collector.mu.Lock()
	active := collector.active
	collector.mu.Unlock()
	if active != 1 {
		t.Fatalf("concurrent collectors while blocked=%d, want 1", active)
	}

	close(collector.release)

	// The queued wake drives exactly one follow-up collection after the first
	// succeeds. Waiting for the second entry proves the first completed and
	// no second collection overlapped it.
	select {
	case <-collector.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("follow-up collection did not start after release")
	}
	// Allow the follow-up (release already closed) to record before asserting.
	// The second Collect returns immediately; wait for observable success via
	// a bounded channel wait on state rather than a sleep.
	deadline := time.Now().Add(5 * time.Second)
	for {
		state, _ := app.state("one")
		if state.Snapshot != nil && state.Err == nil && !state.LastSuccess.IsZero() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("slow collector never recorded a successful refresh")
		}
		// Deterministic yield without sleeping: block on the next timer tick
		// is avoided; spin on runtime scheduling only.
		select {
		case <-ctx.Done():
			t.Fatal("context cancelled while waiting for success")
		default:
		}
	}

	if got := collector.maximum(); got != 1 {
		t.Fatalf("maximum concurrent collectors=%d, want 1", got)
	}
	fresh := app.pageView("one", time.Now().UTC())
	if fresh.Selected.Freshness != string(Fresh) {
		t.Fatalf("after slow success freshness=%q, want fresh", fresh.Selected.Freshness)
	}
	if fresh.Selected.LastObserved == "" {
		t.Fatal("after slow success last observed is empty, want visible timestamp")
	}
	state, _ := app.state("one")
	if state.Snapshot == nil || state.Snapshot.Version.BuildSHA != "slow-ok" {
		t.Fatalf("snapshot=%+v, want slow-ok success retained", state.Snapshot)
	}
	if !state.LastAttempt.After(state.LastSuccess.Add(-time.Hour)) {
		t.Fatalf("attempt=%v success=%v, want attempt recorded", state.LastAttempt, state.LastSuccess)
	}
}
