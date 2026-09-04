package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanDevelopmentRootDiscoversMockedCheckouts(t *testing.T) {
	parent := t.TempDir()

	makeCheckout := func(name string, withForestDir, withForestBin bool) string {
		t.Helper()
		repo := filepath.Join(parent, name)
		if err := os.MkdirAll(repo, 0o755); err != nil {
			t.Fatal(err)
		}
		if withForestDir {
			if err := os.MkdirAll(filepath.Join(repo, ".forest"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if withForestBin {
			if err := os.WriteFile(filepath.Join(repo, "forest"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		return repo
	}

	ironForest := makeCheckout("iron-forest", true, true)
	powder := makeCheckout("powder", true, true)
	makeCheckout("no-binary", true, false)
	makeCheckout("no-forest-dir", false, true)

	out := make(map[string]Instance)
	scanDevelopmentRoot(parent, out)

	if len(out) != 2 {
		t.Fatalf("discovered %d instances, want 2: %v", len(out), out)
	}

	iron, ok := out[ironForest]
	if !ok {
		t.Fatalf("expected %s to be discovered", ironForest)
	}
	if iron.ID != "iron-forest" || iron.Label != "Iron Forest" || iron.Root != ironForest || iron.Forest != filepath.Join(ironForest, "forest") {
		t.Errorf("iron-forest instance = %+v", iron)
	}

	powderInstance, ok := out[powder]
	if !ok {
		t.Fatalf("expected %s to be discovered", powder)
	}
	if powderInstance.ID != "powder" || powderInstance.Label != "Powder" || powderInstance.Root != powder || powderInstance.Forest != filepath.Join(powder, "forest") {
		t.Errorf("powder instance = %+v", powderInstance)
	}
}

func TestScanDevelopmentRootDiscoversForestYAMLDeclaredCheckouts(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // ensure no forest binary is available on PATH

	parent := t.TempDir()

	writeExecutable := func(path string) {
		t.Helper()
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	factory := filepath.Join(parent, "iron-forest")
	if err := os.MkdirAll(factory, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(filepath.Join(factory, "forest"))

	compiled := filepath.Join(parent, "canopy")
	if err := os.MkdirAll(compiled, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(compiled, "forest.yaml"), []byte("repo: misty-step/canopy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeExecutable(filepath.Join(compiled, "forest"))

	managed := filepath.Join(parent, "cantrip")
	if err := os.MkdirAll(managed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(managed, "forest.yaml"), []byte("repo: misty-step/cantrip\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	uncompiled := filepath.Join(parent, "powder")
	if err := os.MkdirAll(uncompiled, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(uncompiled, "forest.yaml"), []byte("repo: misty-step/powder\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(uncompiled, "forest"), []byte("#!/bin/sh\nexit 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := make(map[string]Instance)
	scanDevelopmentRoot(parent, out)

	compiledInst, ok := out[compiled]
	if !ok {
		t.Fatalf("expected %s to be discovered: %v", compiled, out)
	}
	if compiledInst.Forest != filepath.Join(compiled, "forest") {
		t.Errorf("compiled checkout Forest = %q, want its repo-local binary", compiledInst.Forest)
	}

	factoryBin := filepath.Join(factory, "forest")
	for _, repo := range []string{managed, uncompiled} {
		inst, ok := out[repo]
		if !ok {
			t.Fatalf("expected %s to be discovered: %v", repo, out)
		}
		if inst.Forest != factoryBin {
			t.Errorf("Forest for %s = %q, want factory binary %q", repo, inst.Forest, factoryBin)
		}
		if inst.Root != repo {
			t.Errorf("Root for %s = %q, want %q", repo, inst.Root, repo)
		}
	}
}

func TestScanDevelopmentRootPrefersPathForestBinary(t *testing.T) {
	parent := t.TempDir()
	binDir := t.TempDir()
	t.Setenv("PATH", binDir)

	pathForest := filepath.Join(binDir, "forest")
	if err := os.WriteFile(pathForest, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	factory := filepath.Join(parent, "iron-forest")
	if err := os.MkdirAll(factory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(factory, "forest"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	managed := filepath.Join(parent, "cantrip")
	if err := os.MkdirAll(managed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(managed, "forest.yaml"), []byte("repo: misty-step/cantrip\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := make(map[string]Instance)
	scanDevelopmentRoot(parent, out)

	inst, ok := out[managed]
	if !ok {
		t.Fatalf("expected %s to be discovered: %v", managed, out)
	}
	if inst.Forest != pathForest {
		t.Errorf("Forest = %q, want PATH binary %q", inst.Forest, pathForest)
	}
}

func TestResolveDiscoveryCollisionsRetainsCollidingSiblings(t *testing.T) {
	if sanitizeID("agent_test") != sanitizeID("agent-test") {
		t.Fatalf("sanitizeID(agent_test)=%q, sanitizeID(agent-test)=%q, want collision", sanitizeID("agent_test"), sanitizeID("agent-test"))
	}
	if sanitizeID("agent_test") == sanitizeID("agent-beta") {
		t.Fatalf("sanitizeID(agent_test)=%q must not collide with agent-beta", sanitizeID("agent_test"))
	}

	// Use sibling roots under one parent so the lexicographic route order is
	// determined by the colliding leaf names ("-" sorts before "_").
	parent := t.TempDir()
	rootUnderscore := filepath.Join(parent, "agent_test")
	rootDash := filepath.Join(parent, "agent-test")
	colliding := []Instance{
		{ID: sanitizeID("agent_test"), Label: formatLabel(sanitizeID("agent_test")), Root: rootUnderscore, Forest: "forest"},
		{ID: sanitizeID("agent-test"), Label: formatLabel(sanitizeID("agent-test")), Root: rootDash, Forest: "forest"},
	}

	resolved := resolveDiscoveryCollisions(colliding, nil)
	if len(resolved) != 2 {
		t.Fatalf("resolved %d instances, want 2: %+v", len(resolved), resolved)
	}
	byRoot := make(map[string]Instance, 2)
	for _, inst := range resolved {
		byRoot[inst.Root] = inst
		if err := validateRouteIdentifier(inst.ID, "instance id"); err != nil {
			t.Errorf("resolved ID %q is not a valid route identifier: %v", inst.ID, err)
		}
	}
	if byRoot[rootUnderscore].ID == byRoot[rootDash].ID {
		t.Fatalf("colliding roots share ID %q: %+v", byRoot[rootUnderscore].ID, resolved)
	}
	// The lexicographically smaller root keeps the base ID; the other takes
	// the smallest discriminating counter.
	if byRoot[rootDash].ID != "agent-test" {
		t.Errorf("root agent-test ID=%q, want base agent-test", byRoot[rootDash].ID)
	}
	if byRoot[rootUnderscore].ID != "agent-test-2" {
		t.Errorf("root agent_test ID=%q, want agent-test-2", byRoot[rootUnderscore].ID)
	}

	reversed := []Instance{colliding[1], colliding[0]}
	resolvedReversed := resolveDiscoveryCollisions(reversed, nil)
	if len(resolvedReversed) != 2 {
		t.Fatalf("reordered resolved %d instances, want 2", len(resolvedReversed))
	}
	byRootReordered := make(map[string]string, 2)
	for _, inst := range resolvedReversed {
		byRootReordered[inst.Root] = inst.ID
	}
	for root, id := range byRoot {
		if byRootReordered[root] != id.ID {
			t.Fatalf("reordered scan changed binding for %s: %q vs %q", root, byRootReordered[root], id.ID)
		}
	}
}

func TestScanDevelopmentRootCollidingCheckoutsResolveToDistinctIDs(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	parent := t.TempDir()
	factory := filepath.Join(parent, "iron-forest")
	if err := os.MkdirAll(factory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(factory, "forest"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"agent_test", "agent-test"} {
		repo := filepath.Join(parent, name)
		if err := os.MkdirAll(repo, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, "forest.yaml"), []byte("repo: misty-step/"+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	out := make(map[string]Instance)
	scanDevelopmentRoot(parent, out)
	if len(out) != 2 {
		t.Fatalf("discovered %d checkouts, want 2 colliding siblings: %v", len(out), out)
	}
	var colliding []Instance
	for _, inst := range out {
		if inst.ID == "agent-test" {
			colliding = append(colliding, inst)
		}
	}
	if len(colliding) != 2 {
		t.Fatalf("colliding checkout IDs=%+v, want two agent-test entries before resolution", out)
	}
	resolved := resolveDiscoveryCollisions(colliding, nil)
	if len(resolved) != 2 || resolved[0].ID == resolved[1].ID {
		t.Fatalf("resolved colliding checkouts=%+v, want two distinct IDs", resolved)
	}
}
