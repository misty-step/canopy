package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestPollingTriggersPauseWhenDocumentHidden(t *testing.T) {
	templates, err := loadTemplates()
	if err != nil {
		t.Fatalf("loadTemplates: %v", err)
	}

	fleet := templates.Lookup("fleet.html")
	if fleet == nil {
		t.Fatal("fleet.html template not found")
	}
	var fleetBody bytes.Buffer
	if err := fleet.Execute(&fleetBody, PageView{}); err != nil {
		t.Fatalf("render fleet: %v", err)
	}
	if !strings.Contains(fleetBody.String(), `every 10s [document.hidden === false]`) {
		t.Fatalf("fleet polling trigger is not visibility-gated: %s", fleetBody.String())
	}

	instance := templates.Lookup("instance.html")
	if instance == nil {
		t.Fatal("instance.html template not found")
	}
	var instanceBody bytes.Buffer
	if err := instance.Execute(&instanceBody, InstanceView{ID: "one"}); err != nil {
		t.Fatalf("render instance: %v", err)
	}
	if !strings.Contains(instanceBody.String(), `every 2s [document.hidden === false]`) {
		t.Fatalf("instance polling trigger is not visibility-gated: %s", instanceBody.String())
	}
}
