package main

import (
	"context"
	"errors"
	"html/template"
	"reflect"
	"sync"
	"time"
)

const (
	defaultFleetInterval    = 10 * time.Second
	defaultSelectedInterval = 2 * time.Second
	// A collector should never hold a worker forever. The CLI collector also
	// applies its own command timeout; this bound protects custom collectors.
	refreshTimeout = 30 * time.Second
	// freshnessSchedulingSlack is the small scheduling allowance included in
	// the missed-refresh budget for timer and render drift. A refresh loop
	// waits the configured interval after each collection completes, so one
	// legitimate cycle can take the interval plus the full collection bound.
	freshnessSchedulingSlack = time.Second
)

// InstanceState retains independent status and optional observations. Every
// published Snapshot is immutable; only status success renews LastSuccess.
type InstanceState struct {
	Snapshot          *Snapshot
	LastSuccess       time.Time
	LastAttempt       time.Time
	Err               error
	refreshing        bool
	refreshingDetails bool
	refreshingSources bool
}

type instanceWorker struct {
	wake        chan struct{}
	sourcesWake chan struct{}
	cancel      context.CancelFunc
	started     bool
}

// App owns the in-memory projection and the bounded refresh workers. The
// collector is an external read-only boundary; no filesystem state is read by
// this package.
type App struct {
	inventory Inventory
	collector Collector
	templates *template.Template
	reader    sourceReader

	mu      sync.RWMutex
	states  map[string]*InstanceState
	workers map[string]*instanceWorker
	// discovered records instances added by auto-discovery so reconciliation
	// never removes an explicitly configured inventory entry.
	discovered map[string]struct{}
	selected   string
	start      sync.Once
}

func NewApp(inventory Inventory, collector Collector, templates *template.Template) *App {
	states := make(map[string]*InstanceState, len(inventory.Instances))
	workers := make(map[string]*instanceWorker, len(inventory.Instances))
	for i := range inventory.Instances {
		instance := inventory.Instances[i]
		// Inventory validation normally guarantees unique IDs. Keeping the first
		// state makes a malformed hand-built Inventory deterministic as well.
		if _, exists := states[instance.ID]; exists {
			continue
		}
		states[instance.ID] = &InstanceState{}
		workers[instance.ID] = newInstanceWorker()
	}
	selected := ""
	if len(inventory.Instances) > 0 {
		selected = inventory.Instances[0].ID
	}
	return &App{
		inventory:  inventory,
		collector:  collector,
		templates:  templates,
		reader:     newSourceReader(),
		states:     states,
		workers:    workers,
		discovered: make(map[string]struct{}),
		selected:   selected,
	}
}

// Each instance has serialized status, stable-detail, and external-source
// lanes. Optional work cannot occupy or renew the current-status lane.
func (a *App) Start(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	a.start.Do(func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		seen := make(map[string]struct{}, len(a.inventory.Instances))
		for i := range a.inventory.Instances {
			instance := a.inventory.Instances[i]
			if _, duplicate := seen[instance.ID]; duplicate {
				continue
			}
			seen[instance.ID] = struct{}{}
			worker := a.workers[instance.ID]
			if worker == nil || worker.started {
				continue
			}
			a.startWorker(ctx, instance, worker)
		}
		// Start background periodic auto-discovery
		go a.discoveryLoop(ctx)
	})
}
func (a *App) discoveryLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			discovered, err := DiscoverLocalInstances(ctx)
			if err == nil {
				a.syncDiscoveredInstances(ctx, discovered)
			}
		}
	}
}

func newInstanceWorker() *instanceWorker {
	return &instanceWorker{wake: make(chan struct{}, 1), sourcesWake: make(chan struct{}, 1)}
}

// Caller holds a.mu; all lanes share one cancellation lifetime.
func (a *App) startWorker(ctx context.Context, instance Instance, worker *instanceWorker) {
	worker.started = true
	ctx, worker.cancel = context.WithCancel(ctx)
	go a.refreshLoop(ctx, instance, worker)
	go a.detailsLoop(ctx, instance, worker)
	go a.sourcesLoop(ctx, instance, worker)
}

func (a *App) detailsLoop(ctx context.Context, instance Instance, worker *instanceWorker) {
	timer := time.NewTimer(0)
	first := true
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			a.refreshDetailsOnce(ctx, instance)
			if first {
				first = false
				select {
				case worker.sourcesWake <- struct{}{}:
				default:
				}
			}
		}
		timer.Reset(sourceRefreshInterval)
	}
}

func (a *App) sourcesLoop(ctx context.Context, instance Instance, worker *instanceWorker) {
	timer := time.NewTimer(sourceRefreshInterval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-worker.sourcesWake:
		case <-timer.C:
		}
		a.refreshSourcesOnce(ctx, instance)
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(sourceRefreshInterval)
	}
}

func (a *App) refreshDetailsOnce(parent context.Context, instance Instance) {
	if parent == nil {
		parent = context.Background()
	}
	a.mu.Lock()
	state := a.states[instance.ID]
	if state == nil || state.refreshingDetails || parent.Err() != nil {
		a.mu.Unlock()
		return
	}
	state.refreshingDetails = true
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(parent, refreshTimeout)
	next := Snapshot{}
	if a.collector != nil {
		next = a.collector.CollectDetails(ctx, instance)
	}
	cancel()
	a.mu.Lock()
	defer a.mu.Unlock()
	state.refreshingDetails = false
	if a.states[instance.ID] != state || parent.Err() != nil {
		return
	}
	snapshot := Snapshot{Instance: instance}
	if state.Snapshot != nil {
		snapshot = *state.Snapshot
	}
	retainDetail(&snapshot.Version, &snapshot.VersionObservation, next.Version, next.VersionObservation)
	retainDetail(&snapshot.Config, &snapshot.ConfigObservation, next.Config, next.ConfigObservation)
	retainDetail(&snapshot.Declarations, &snapshot.DeclarationsObservation, next.Declarations, next.DeclarationsObservation)
	if next.History.ObservedAt.IsZero() && next.History.Error == "" {
		next.History.Error = "Run history has not been observed"
	}
	snapshot.History = retainRunHistory(next.History, snapshot.History)
	state.Snapshot = &snapshot
}

func retainDetail[T any](value *T, observation *SourceObservation, next T, observed SourceObservation) {
	if observed.ObservedAt.IsZero() && observed.Error == "" {
		observed.Error = "No successful observation"
	}
	if observed.Error != "" {
		observation.Error = observed.Error
		return
	}
	*value, *observation = next, observed
}

func (a *App) refreshSourcesOnce(parent context.Context, instance Instance) {
	if parent == nil {
		parent = context.Background()
	}
	a.mu.Lock()
	state := a.states[instance.ID]
	if state == nil || state.Snapshot == nil || state.refreshingSources || parent.Err() != nil {
		a.mu.Unlock()
		return
	}
	input := *state.Snapshot
	if !input.Delivery.AttemptedAt.IsZero() && time.Now().Before(input.Delivery.AttemptedAt.Add(sourceRefreshInterval)) {
		a.mu.Unlock()
		return
	}
	state.refreshingSources = true
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(parent, refreshTimeout)
	next := a.reader.collect(ctx, input, input.Delivery)
	next.AttemptedAt = time.Now().UTC()
	cancel()
	a.mu.Lock()
	defer a.mu.Unlock()
	state.refreshingSources = false
	if a.states[instance.ID] != state || parent.Err() != nil {
		return
	}
	// Merge onto the latest status, not the status captured before HTTP reads.
	snapshot := *state.Snapshot
	snapshot.Delivery = next
	state.Snapshot = &snapshot
}

func (a *App) refreshLoop(ctx context.Context, instance Instance, worker *instanceWorker) {
	// An immediate first attempt makes the first page useful without waiting
	// for the fleet interval. The timer is recreated after each wake so a
	// selection change takes effect promptly.
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-worker.wake:
			a.refreshOnce(ctx, instance)
		case <-timer.C:
			a.refreshOnce(ctx, instance)
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(a.refreshInterval(instance.ID))
	}
}

func (a *App) refreshInterval(id string) time.Duration {
	a.mu.RLock()
	selected := id != "" && id == a.selected
	seconds := a.inventory.FleetIntervalSeconds
	if selected {
		seconds = a.inventory.SelectedIntervalSeconds
	}
	a.mu.RUnlock()
	if seconds <= 0 {
		if selected {
			return defaultSelectedInterval
		}
		return defaultFleetInterval
	}
	return time.Duration(seconds) * time.Second
}

// freshnessWindow is the authoritative missed-refresh budget for one
// instance: the configured wait between collections plus the worst-case
// collection bound plus a small scheduling allowance. A silent worker that
// exceeds one legitimate cycle without a success is stale, even when its
// last attempt reported no explicit error. LastAttempt never extends this
// window.
func (a *App) freshnessWindow(id string) time.Duration {
	return a.refreshInterval(id) + refreshTimeout + freshnessSchedulingSlack
}

// In-flight guards also cover direct refresh calls. A removed/readded instance
// has a different state identity, so late results cannot revive or overwrite it.
func (a *App) refreshOnce(parent context.Context, instance Instance) {
	if parent == nil {
		parent = context.Background()
	}
	a.mu.Lock()
	state := a.states[instance.ID]
	if state == nil || state.refreshing || parent.Err() != nil {
		a.mu.Unlock()
		return
	}
	state.refreshing = true
	state.LastAttempt = time.Now().UTC()
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(parent, refreshTimeout)
	var next Snapshot
	err := errors.New("collector is not configured")
	if a.collector != nil {
		next, err = a.collector.Collect(ctx, instance)
		if err == nil {
			err = ctx.Err()
		}
	}
	cancel()
	a.mu.Lock()
	defer a.mu.Unlock()
	state.refreshing = false
	if a.states[instance.ID] != state || parent.Err() != nil {
		return
	}
	state.Err = err
	if err != nil {
		return
	}
	snapshot := Snapshot{Instance: instance}
	if state.Snapshot != nil {
		snapshot = *state.Snapshot
	}
	snapshot.Status, snapshot.CollectedAt = next.Status, next.CollectedAt
	state.Snapshot = &snapshot
	state.LastSuccess = time.Now().UTC()
}

func (a *App) state(id string) (InstanceState, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	state, ok := a.states[id]
	if !ok || state == nil {
		return InstanceState{}, false
	}
	copy := *state
	return copy, true
}

func (a *App) instances() []Instance {
	a.mu.RLock()
	defer a.mu.RUnlock()
	instances := make([]Instance, len(a.inventory.Instances))
	copy(instances, a.inventory.Instances)
	return instances
}

func (a *App) syncDiscoveredInstances(ctx context.Context, discovered []Instance) {
	a.mu.Lock()
	defer a.mu.Unlock()

	discMap := make(map[string]Instance, len(discovered))
	for _, disc := range discovered {
		discMap[disc.ID] = disc
	}

	// 1. Reconcile the effective list. Explicit inventory entries are never
	// removed; only instances previously added by auto-discovery are pruned
	// when they stop appearing.
	newInstances := make([]Instance, 0, len(a.inventory.Instances)+len(discovered))
	for _, existing := range a.inventory.Instances {
		if _, wasDiscovered := a.discovered[existing.ID]; wasDiscovered {
			if updated, stillPresent := discMap[existing.ID]; stillPresent {
				if !reflect.DeepEqual(updated, existing) {
					if worker := a.workers[existing.ID]; worker != nil && worker.cancel != nil {
						worker.cancel()
					}
					a.states[existing.ID] = &InstanceState{}
					worker := newInstanceWorker()
					a.workers[existing.ID] = worker
					a.startWorker(ctx, updated, worker)
				}
				newInstances = append(newInstances, updated)
			} else {
				// Obsolete discovered instance: cancel and clean up worker
				if worker, ok := a.workers[existing.ID]; ok && worker != nil && worker.cancel != nil {
					worker.cancel()
				}
				delete(a.workers, existing.ID)
				delete(a.states, existing.ID)
				delete(a.discovered, existing.ID)
			}
			continue
		}
		newInstances = append(newInstances, existing)
	}

	// 2. Add newly discovered instances
	existingMap := make(map[string]bool, len(newInstances))
	for _, inst := range newInstances {
		existingMap[inst.ID] = true
	}

	var added []Instance
	for _, disc := range discovered {
		if existingMap[disc.ID] {
			continue
		}
		existingMap[disc.ID] = true
		newInstances = append(newInstances, disc)
		a.discovered[disc.ID] = struct{}{}
		a.states[disc.ID] = &InstanceState{}
		worker := newInstanceWorker()
		a.workers[disc.ID] = worker
		added = append(added, disc)
	}
	a.inventory.Instances = newInstances

	// 3. Reconcile selection if previous selection was removed
	if _, selectedExists := a.states[a.selected]; !selectedExists {
		if len(a.inventory.Instances) > 0 {
			a.selected = a.inventory.Instances[0].ID
		} else {
			a.selected = ""
		}
	}

	// 4. Start workers for newly added instances with individual cancellation
	for _, inst := range added {
		worker := a.workers[inst.ID]
		if worker != nil && !worker.started {
			a.startWorker(ctx, inst, worker)
		}
	}
}

func (a *App) inventoryCopy() Inventory {
	a.mu.RLock()
	defer a.mu.RUnlock()
	inventory := a.inventory
	inventory.Instances = append([]Instance(nil), a.inventory.Instances...)
	return inventory
}

func (a *App) selectInstance(id string) {
	a.mu.Lock()
	if _, ok := a.states[id]; !ok {
		a.mu.Unlock()
		return
	}
	changed := a.selected != id
	a.selected = id
	worker := a.workers[id]
	a.mu.Unlock()
	if !changed {
		return
	}
	if worker != nil {
		select {
		case worker.wake <- struct{}{}:
		default:
		}
	}
}

func (a *App) selectedID() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.selected
}

func (a *App) requestRefresh(id string) {
	a.mu.RLock()
	worker := a.workers[id]
	a.mu.RUnlock()
	if worker == nil {
		return
	}
	select {
	case worker.wake <- struct{}{}:
	default:
		// A worker is already queued or collecting. The next loop iteration
		// will perform at most one additional refresh.
	}
}
