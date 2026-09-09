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
