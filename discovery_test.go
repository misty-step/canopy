package main

import (
	"context"
	"testing"
)

func TestDiscoverLocalInstances(t *testing.T) {
	instances, err := DiscoverLocalInstances(context.Background())
	if err != nil {
		t.Fatalf("DiscoverLocalInstances failed: %v", err)
	}
	if len(instances) == 0 {
		t.Fatalf("expected at least 1 instance, got 0")
	}

	foundMap := make(map[string]Instance)
	for _, inst := range instances {
		foundMap[inst.ID] = inst
		t.Logf("Discovered instance: id=%s root=%s forest=%s", inst.ID, inst.Root, inst.Forest)
	}

	if _, ok := foundMap["iron-forest"]; !ok {
		t.Errorf("expected iron-forest to be discovered")
	}
	if _, ok := foundMap["powder"]; !ok {
		t.Errorf("expected powder to be discovered")
	}
}
