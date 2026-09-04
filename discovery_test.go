package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoveryRequiresAnInstalledProfile(t *testing.T) {
	parent := t.TempDir()
	pathBin := t.TempDir()
	t.Setenv("PATH", pathBin)
	if err := os.WriteFile(filepath.Join(pathBin, "forest"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	makeCheckout := func(name string, config, executable bool) string {
		t.Helper()
		repo := filepath.Join(parent, name)
		profile := filepath.Join(repo, ".iron-forest")
		if err := os.MkdirAll(filepath.Join(profile, "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if config {
			if err := os.WriteFile(filepath.Join(profile, "config.yaml"), []byte("repo: org/"+name+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		mode := os.FileMode(0o644)
		if executable {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(profile, "bin", "forest"), []byte("#!/bin/sh\nexit 0\n"), mode); err != nil {
			t.Fatal(err)
		}
		return repo
	}
	installed := makeCheckout("installed", true, true)
	makeCheckout("without-config", false, true)
	makeCheckout("without-executable", true, false)
	legacy := filepath.Join(parent, "legacy")
	if err := os.MkdirAll(filepath.Join(legacy, ".forest"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "forest.yaml"), []byte("repo: org/legacy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "forest"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := make(map[string]Instance)
	scanDevelopmentRoot(parent, out)
	if len(out) != 1 || out[installed].Forest != filepath.Join(installed, ".iron-forest", "bin", "forest") {
		t.Fatalf("discovery must use only a complete pinned instance profile, got %+v", out)
	}
}

func TestDiscoverySkipsLinkedWorktrees(t *testing.T) {
	parent := t.TempDir()
	makeProfile := func(name string) string {
		t.Helper()
		repo := filepath.Join(parent, name)
		profile := filepath.Join(repo, ".iron-forest", "bin")
		if err := os.MkdirAll(profile, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, ".iron-forest", "config.yaml"), []byte("repo: org/"+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(profile, "forest"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return repo
	}

	primary := makeProfile("iron-forest")
	if err := os.MkdirAll(filepath.Join(primary, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	worktree := makeProfile("iron-forest-mis-64")
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: /tmp/fake.git/worktrees/mis-64\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := make(map[string]Instance)
	scanDevelopmentRoot(parent, out)

	if _, ok := out[primary]; !ok {
		t.Fatalf("expected primary checkout %s to be discovered: %v", primary, out)
	}
	if _, ok := out[worktree]; ok {
		t.Fatalf("linked worktree %s must not be discovered: %v", worktree, out)
	}
	if len(out) != 1 {
		t.Fatalf("discovered %d instances, want 1: %v", len(out), out)
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
