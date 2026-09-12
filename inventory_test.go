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

func TestPrivateForgeProxyCannotEscapeTheObservationCapability(t *testing.T) {
	instance := Instance{
		ID: "vector", Label: "Vector", Root: "/data/vector", Forest: "/data/vector/.iron-forest/bin/forest",
		ObserverURL: "http://[fdaa::1]:9090/v1/forest/observe", ObserverTokenEnv: "FOREST_OBSERVATION_TOKEN",
		Sources: TicketSources{Forge: &ForgeSource{
			ReadSource: ReadSource{Endpoint: "http://[fdaa::1]:9090/v1/github", TokenEnv: "FOREST_OBSERVATION_TOKEN"},
			WebURL:     "https://github.com",
		}},
	}
	if err := validateInstance(instance); err != nil {
		t.Fatalf("private read proxy rejected: %v", err)
	}
	for _, source := range []ReadSource{
		{Endpoint: "http://[fdaa::2]:9090/v1/github", TokenEnv: "FOREST_OBSERVATION_TOKEN"},
		{Endpoint: "http://[fdaa::1]:9090/other-api", TokenEnv: "FOREST_OBSERVATION_TOKEN"},
		{Endpoint: "http://[fdaa::1]:9090/v1/github", TokenEnv: "WORKER_WRITE_TOKEN"},
	} {
		instance.Sources.Forge.ReadSource = source
		if err := validateInstance(instance); err == nil {
			t.Fatalf("private forge origin/path/credential escaped the read-only observer: %+v", source)
		}
	}
}

func TestConfiguredWorkInventoryStaysBounded(t *testing.T) {
	path := writeInventoryFixture(t, `{
		"instances": [{"id": "vector", "label": "Vector", "root": "/data/vector", "forest": "/data/vector/.iron-forest/bin/forest",
			"sources": {
				"habitat": {"endpoint": "https://habitat.example", "token_env": "HABITAT_READ", "system": "https://habitat.example", "work_item_ids": ["ticket-a", "ticket-c"]},
				"forge": {"endpoint": "https://api.github.com", "token_env": "FORGE_READ", "web_url": "https://github.com"}
			}}]
	}`)
	inventory, err := LoadInventory(path)
	if err != nil {
		t.Fatalf("explicit bounded inventory rejected: %v", err)
	}
	source := inventory.Instances[0].Sources
	if len(source.Habitat.WorkItemIDs) != 2 {
		t.Fatalf("configured work inventory was dropped: %+v", source)
	}
	for _, sources := range []string{
		`{"habitat": {"endpoint": "https://habitat.example", "token_env": "HABITAT_READ", "system": "https://habitat.example", "work_item_ids": ["../../etc/passwd"]}}`,
		`{"habitat": {"endpoint": "https://habitat.example", "token_env": "HABITAT_READ", "system": "https://habitat.example", "work_item_ids": ["ticket-a", "ticket-a"]}}`,
		`{"forge": {"endpoint": "https://api.github.com", "token_env": "FORGE_READ", "web_url": "https://github.com", "automation_login": "retired"}}`,
		`{"forge": {"endpoint": "https://api.github.com", "token_env": "FORGE_READ", "web_url": "https://github.com", "candidates": []}}`,
	} {
		unsafe := writeInventoryFixture(t, `{"instances": [{"id": "vector", "label": "Vector", "root": "/data/vector", "forest": "/usr/bin/forest", "sources": `+sources+`}]}`)
		if _, err := LoadInventory(unsafe); err == nil {
			t.Fatalf("unsafe or ambiguous configured identity accepted: %s", sources)
		}
	}
}

func TestAppBotSuffixRemainsInvalidForRouteIdentifiers(t *testing.T) {
	for _, instance := range []string{
		`{"id": "iron-forest[bot]", "label": "Bot", "root": "/data/seedbed", "forest": "/usr/bin/forest"}`,
		`{"id": "seedbed", "label": "Seedbed", "root": "/data/seedbed", "forest": "/usr/bin/forest",
			"sources": {"habitat": {"endpoint": "https://habitat.example", "token_env": "HABITAT_READ",
				"system": "https://habitat.example", "work_item_ids": ["iron-forest[bot]"]}}}`,
	} {
		path := writeInventoryFixture(t, `{"instances": [`+instance+`]}`)
		if _, err := LoadInventory(path); err == nil {
			t.Fatalf("App bot suffix escaped automation login into a route identity: %s", instance)
		}
	}
}
