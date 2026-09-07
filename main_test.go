package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeDefaultCanopy writes canopy.json into a fresh directory and returns the
// directory. The caller is expected to t.Chdir into it so loadInventory's
// default-path lookup observes exactly that directory.
func writeDefaultCanopy(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "canopy.json"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadInventoryZeroConfigWhenDefaultAbsent(t *testing.T) {
	t.Chdir(t.TempDir())
	inventory, err := loadInventory("")
	if err != nil {
		t.Fatalf("loadInventory with absent canopy.json: %v", err)
	}
	if len(inventory.Instances) != 0 {
		t.Fatalf("inventory instances=%d, want zero-config discovery", len(inventory.Instances))
	}
}

func TestLoadInventoryDefaultDirectoryIsNotConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "canopy.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	inventory, err := loadInventory("")
	if err != nil {
		t.Fatalf("loadInventory with canopy.json directory: %v", err)
	}
	if len(inventory.Instances) != 0 {
		t.Fatalf("inventory instances=%d, want zero-config discovery", len(inventory.Instances))
	}
}

func TestLoadInventoryMalformedDefaultFailsLoudly(t *testing.T) {
	dir := writeDefaultCanopy(t, `{"instances": [`)
	t.Chdir(dir)
	if _, err := loadInventory(""); err == nil {
		t.Fatal("loadInventory with malformed default canopy.json succeeded, want loud failure")
	} else if !strings.Contains(err.Error(), "load inventory:") {
		t.Fatalf("loadInventory error=%q, want shared `load inventory:` boundary", err)
	}
}

func TestLoadInventoryTrailingDefaultFailsLoudly(t *testing.T) {
	dir := writeDefaultCanopy(t, `{"instances": []} {}`)
	t.Chdir(dir)
	if _, err := loadInventory(""); err == nil {
		t.Fatal("loadInventory with trailing JSON default succeeded, want loud failure")
	} else if !strings.Contains(err.Error(), "load inventory:") {
		t.Fatalf("loadInventory error=%q, want shared `load inventory:` boundary", err)
	}
}

func TestLoadInventoryValidDefaultLoads(t *testing.T) {
	dir := writeDefaultCanopy(t, `{
		"instances": [{"id": "local", "label": "Local", "root": "/tmp/forest", "forest": "/usr/bin/forest"}]
	}`)
	t.Chdir(dir)
	inventory, err := loadInventory("")
	if err != nil {
		t.Fatalf("loadInventory with valid default: %v", err)
	}
	if len(inventory.Instances) != 1 || inventory.Instances[0].ID != "local" {
		t.Fatalf("inventory=%+v, want the single `local` instance from default canopy.json", inventory)
	}
}

func TestLoadInventoryExplicitConfigStillLoud(t *testing.T) {
	path := writeInventoryFixture(t, `{"instances": [`)
	if _, err := loadInventory(path); err == nil {
		t.Fatal("loadInventory with malformed explicit config succeeded, want loud failure")
	} else if !strings.Contains(err.Error(), "load inventory:") {
		t.Fatalf("loadInventory error=%q, want shared `load inventory:` boundary", err)
	}
}
