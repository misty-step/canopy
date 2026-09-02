package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

const (
	defaultListen                  = "127.0.0.1:8080"
	defaultFleetIntervalSeconds    = 10
	defaultSelectedIntervalSeconds = 2
)

// LoadInventory reads and validates one JSON inventory. Unknown fields are
// rejected so a misspelled operational setting cannot silently select a
// default; Forest command envelopes have a separate additive-field policy.
func LoadInventory(path string) (Inventory, error) {
	if strings.TrimSpace(path) == "" {
		return Inventory{}, fmt.Errorf("inventory path is empty")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return Inventory{}, fmt.Errorf("read inventory %q: %w", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var inventory Inventory
	if err := decoder.Decode(&inventory); err != nil {
		return Inventory{}, fmt.Errorf("decode inventory %q: %w", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Inventory{}, fmt.Errorf("decode inventory %q: multiple JSON values", path)
		}
		return Inventory{}, fmt.Errorf("decode inventory %q: trailing data: %w", path, err)
	}
	if err := validateInventory(&inventory); err != nil {
		return Inventory{}, fmt.Errorf("validate inventory %q: %w", path, err)
	}
	return inventory, nil
}

func validateInventory(inventory *Inventory) error {
	if inventory == nil {
		return fmt.Errorf("inventory is nil")
	}
	if inventory.Listen == "" {
		inventory.Listen = defaultListen
	}
	if err := validateListen(inventory.Listen); err != nil {
		return err
	}
	if inventory.FleetIntervalSeconds == 0 {
		inventory.FleetIntervalSeconds = defaultFleetIntervalSeconds
	}
	if inventory.SelectedIntervalSeconds == 0 {
		inventory.SelectedIntervalSeconds = defaultSelectedIntervalSeconds
	}
	if inventory.FleetIntervalSeconds < 1 {
		return fmt.Errorf("fleet_interval_seconds must be positive")
	}
	if inventory.SelectedIntervalSeconds < 1 {
		return fmt.Errorf("selected_interval_seconds must be positive")
	}
	if len(inventory.Instances) == 0 {
		return fmt.Errorf("instances must contain at least one instance")
	}
	seen := make(map[string]struct{}, len(inventory.Instances))
	for index := range inventory.Instances {
		instance := &inventory.Instances[index]
		if err := validateInstance(*instance); err != nil {
			return fmt.Errorf("instances[%d]: %w", index, err)
		}
		if _, found := seen[instance.ID]; found {
			return fmt.Errorf("instances[%d]: duplicate id %q", index, instance.ID)
		}
		seen[instance.ID] = struct{}{}
	}
	return nil
}

func validateListen(listen string) error {
	if strings.TrimSpace(listen) != listen || strings.ContainsAny(listen, "\r\n\x00") {
		return fmt.Errorf("listen address %q contains whitespace or control characters", listen)
	}
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return fmt.Errorf("listen must be host:port: %w", err)
	}
	if host == "" {
		// :port is a valid net.Listen address and means all local interfaces.
	} else if strings.ContainsAny(host, " \t\r\n") {
		return fmt.Errorf("listen host %q contains whitespace", host)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("listen port %q is not in 1..65535", port)
	}
	return nil
}

func validateInstance(instance Instance) error {
	if err := validateRouteIdentifier(instance.ID, "instance id"); err != nil {
		return err
	}
	if strings.TrimSpace(instance.Label) == "" {
		return fmt.Errorf("instance %q label is empty", instance.ID)
	}
	if strings.ContainsRune(instance.Root, '\x00') {
		return fmt.Errorf("instance %q root contains NUL", instance.ID)
	}
	if instance.Root == "" {
		return fmt.Errorf("instance %q root is empty", instance.ID)
	}
	if strings.ContainsRune(instance.Forest, '\x00') {
		return fmt.Errorf("instance %q forest contains NUL", instance.ID)
	}
	if instance.Forest == "" {
		return fmt.Errorf("instance %q forest is empty", instance.ID)
	}
	if instance.Host != "" {
		if err := validateSSHHost(instance.Host); err != nil {
			return fmt.Errorf("instance %q host: %w", instance.ID, err)
		}
		if err := validateSSHPath(instance.Root, "root"); err != nil {
			return fmt.Errorf("instance %q: %w", instance.ID, err)
		}
		if err := validateSSHExecutable(instance.Forest); err != nil {
			return fmt.Errorf("instance %q: %w", instance.ID, err)
		}
	}
	return nil
}

// validateRouteIdentifier is deliberately narrower than a shell token. These
// values become URL/query route identities as well as Forest operands, so path
// separators, controls, and shell punctuation are never accepted.
func validateRouteIdentifier(value, what string) error {
	if value == "" {
		return fmt.Errorf("%s is empty", what)
	}
	if value == "." || value == ".." {
		return fmt.Errorf("%s %q is not an identifier", what, value)
	}
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("%s %q contains whitespace or control characters", what, value)
		}
		switch r {
		case '/', '\\', '?', '#', '&', ';', '|', '`', '$', '\'', '"', '<', '>', '*', '(', ')', '[', ']', '{', '}', '!', '=':
			return fmt.Errorf("%s %q contains unsafe character %q", what, value, r)
		}
	}
	return nil
}

func validateSSHHost(host string) error {
	if host == "" {
		return fmt.Errorf("host is empty")
	}
	if strings.HasPrefix(host, "-") {
		return fmt.Errorf("host %q may not begin with '-'", host)
	}
	if containsUnsafeShellCharacters(host) || strings.IndexFunc(host, unicode.IsSpace) >= 0 {
		return fmt.Errorf("host %q contains unsafe characters", host)
	}
	// Validate the optional user@ prefix without trying to resolve DNS here.
	if strings.Count(host, "@") > 1 {
		return fmt.Errorf("host %q has more than one user separator", host)
	}
	target := host
	if at := strings.LastIndexByte(target, '@'); at >= 0 {
		if err := validateSSHAtom(target[:at], "SSH user"); err != nil {
			return err
		}
		target = target[at+1:]
	}
	if target == "" {
		return fmt.Errorf("host target is empty")
	}
	if strings.HasPrefix(target, "[") {
		closing := strings.IndexByte(target, ']')
		if closing <= 1 {
			return fmt.Errorf("host %q has an invalid bracketed address", host)
		}
		if rest := target[closing+1:]; rest != "" {
			if !strings.HasPrefix(rest, ":") || !validPort(rest[1:]) {
				return fmt.Errorf("host %q has an invalid port", host)
			}
		}
		return nil
	}
	if strings.Count(target, ":") > 1 {
		return fmt.Errorf("host %q must bracket an IPv6 address", host)
	}
	if colon := strings.LastIndexByte(target, ':'); colon >= 0 {
		if !validPort(target[colon+1:]) {
			return fmt.Errorf("host %q has an invalid port", host)
		}
		target = target[:colon]
	}
	if target == "" {
		return fmt.Errorf("host target is empty")
	}
	return validateSSHAtom(target, "SSH host")
}

func validPort(raw string) bool {
	port, err := strconv.Atoi(raw)
	return err == nil && port >= 1 && port <= 65535
}

func validateSSHAtom(value, what string) error {
	if value == "" {
		return fmt.Errorf("%s is empty", what)
	}
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) || containsUnsafeShellRune(r) {
			return fmt.Errorf("%s contains unsafe characters", what)
		}
	}
	return nil
}

func validateSSHPath(path, what string) error {
	if path == "" {
		return fmt.Errorf("%s is empty", what)
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%s %q must be absolute for SSH", what, path)
	}
	if filepath.Clean(path) != path {
		return fmt.Errorf("%s %q is not clean", what, path)
	}
	if strings.IndexFunc(path, unicode.IsSpace) >= 0 || strings.ContainsRune(path, '\x00') || containsUnsafeShellCharacters(path) {
		return fmt.Errorf("%s %q contains unsafe characters", what, path)
	}
	return nil
}

func validateSSHExecutable(executable string) error {
	if executable == "" {
		return fmt.Errorf("forest executable is empty")
	}
	if filepath.IsAbs(executable) {
		return validateSSHPath(executable, "forest executable")
	}
	if filepath.Clean(executable) != executable || strings.HasPrefix(executable, "-") {
		return fmt.Errorf("forest executable %q is not a clean command path", executable)
	}
	if strings.IndexFunc(executable, unicode.IsSpace) >= 0 || containsUnsafeShellCharacters(executable) {
		return fmt.Errorf("forest executable %q contains unsafe characters", executable)
	}
	return nil
}

func containsUnsafeShellCharacters(value string) bool {
	return strings.IndexFunc(value, containsUnsafeShellRune) >= 0
}

func containsUnsafeShellRune(r rune) bool {
	switch r {
	case ';', '|', '&', '`', '$', '\'', '"', '<', '>', '*', '?', '(', ')', '[', ']', '{', '}', '!', '\\':
		return true
	default:
		return false
	}
}
