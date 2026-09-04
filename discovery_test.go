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
