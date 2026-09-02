package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeInventoryFixture(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "inventory.json")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadInventoryAppliesSafeDefaults(t *testing.T) {
	path := writeInventoryFixture(t, `{
		"instances": [{"id": "local", "label": "Local", "root": "/tmp/forest", "forest": "/usr/bin/forest"}]
	}`)
	inventory, err := LoadInventory(path)
	if err != nil {
		t.Fatalf("LoadInventory: %v", err)
	}
	if inventory.Listen != defaultListen || inventory.FleetIntervalSeconds != defaultFleetIntervalSeconds || inventory.SelectedIntervalSeconds != defaultSelectedIntervalSeconds {
		t.Fatalf("inventory defaults=%+v, want listen and intervals defaults", inventory)
	}
}

func TestLoadInventoryRejectsDuplicateOrUnsafeInstances(t *testing.T) {
	duplicate := writeInventoryFixture(t, `{
		"instances": [
			{"id": "same", "label": "One", "root": "/tmp/one", "forest": "/usr/bin/forest"},
			{"id": "same", "label": "Two", "root": "/tmp/two", "forest": "/usr/bin/forest"}
		]
	}`)
	if _, err := LoadInventory(duplicate); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate inventory error=%v, want duplicate id rejection", err)
	}

	unsafeSSH := writeInventoryFixture(t, `{
		"instances": [{"id": "remote", "label": "Remote", "host": "bad host", "root": "/srv/forest", "forest": "/usr/bin/forest"}]
	}`)
	if _, err := LoadInventory(unsafeSSH); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("unsafe SSH inventory error=%v, want host validation", err)
	}
}

func TestLoadInventoryRejectsUnknownAndTrailingJSON(t *testing.T) {
	unknown := writeInventoryFixture(t, `{"instances": [], "unexpected": true}`)
	if _, err := LoadInventory(unknown); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown inventory error=%v, want strict field rejection", err)
	}
	trailing := writeInventoryFixture(t, `{"instances": [{"id":"one","label":"One","root":"/tmp","forest":"forest"}]} {}`)
	if _, err := LoadInventory(trailing); err == nil || !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("trailing inventory error=%v, want multiple-value rejection", err)
	}
}
