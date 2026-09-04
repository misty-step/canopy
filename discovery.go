package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DiscoverLocalInstances scans the host for all available Iron Forest instances
// strictly within the Misty Step boundary and active systemd units.
func DiscoverLocalInstances(ctx context.Context) ([]Instance, error) {
	candidates := make(map[string]Instance) // keyed by normalized root path

	// 1. Scan systemd user units (forest@<name>.service)
	scanSystemdUnits(ctx, candidates)

	// 2. Scan Misty Step development checkouts
	scanDevelopmentRoots(candidates)

	instances := make([]Instance, 0, len(candidates))
	for _, inst := range candidates {
		if err := validateInstance(inst); err == nil {
			instances = append(instances, inst)
		}
	}

	sort.Slice(instances, func(i, j int) bool {
		return instances[i].ID < instances[j].ID
	})

	return instances, nil
}

func scanSystemdUnits(ctx context.Context, out map[string]Instance) {
	cmdCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "systemctl", "--user", "list-units", "forest@*.service", "--all", "--no-legend", "--plain")
	output, err := cmd.Output()
	if err != nil {
		return
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		unitName := fields[0]
		if !strings.HasPrefix(unitName, "forest@") || !strings.HasSuffix(unitName, ".service") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(unitName, "forest@"), ".service")
		if name == "" {
			continue
		}

		propCtx, propCancel := context.WithTimeout(ctx, 1*time.Second)
		showCmd := exec.CommandContext(propCtx, "systemctl", "--user", "show", unitName, "-p", "WorkingDirectory", "-p", "ExecStart")
		showOut, showErr := showCmd.Output()
		propCancel()
		if showErr != nil {
			continue
		}

		var workDir, execPath string
		for _, prop := range strings.Split(string(showOut), "\n") {
			prop = strings.TrimSpace(prop)
			if strings.HasPrefix(prop, "WorkingDirectory=") {
				workDir = strings.TrimPrefix(prop, "WorkingDirectory=")
			} else if strings.HasPrefix(prop, "ExecStart=") {
				val := strings.TrimPrefix(prop, "ExecStart=")
				val = strings.TrimPrefix(val, "{")
				val = strings.TrimSuffix(val, "}")
				parts := strings.Split(val, ";")
				for _, part := range parts {
					part = strings.TrimSpace(part)
					if strings.HasPrefix(part, "path=") {
						execPath = strings.TrimSpace(strings.TrimPrefix(part, "path="))
					}
				}
			}
		}

		if workDir != "" && execPath != "" {
			// Ensure it falls under Misty Step Development boundary
			if !strings.Contains(workDir, "/misty-step/") {
				continue
			}
			if isExecutable(execPath) {
				id := sanitizeID(name)
				out[workDir] = Instance{
					ID:     id,
					Label:  formatLabel(id),
					Root:   workDir,
					Forest: execPath,
				}
			}
		}
	}
}

func scanDevelopmentRoots(out map[string]Instance) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return
	}

	scanDevelopmentRoot(filepath.Join(homeDir, "Development", "misty-step"), out)
}

// scanDevelopmentRoot adds one discoverable instance per directory under
// parent that contains both a .forest directory and an executable ./forest
// binary. It is separated from scanDevelopmentRoots so unit tests can supply
// a temporary directory with mocked checkouts.
func scanDevelopmentRoot(parent string, out map[string]Instance) {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		repoPath := filepath.Join(parent, entry.Name())
		forestDir := filepath.Join(repoPath, ".forest")
		forestBin := filepath.Join(repoPath, "forest")

		if fi, err := os.Stat(forestDir); err == nil && fi.IsDir() {
			if isExecutable(forestBin) {
				if _, exists := out[repoPath]; !exists {
					id := sanitizeID(entry.Name())
					out[repoPath] = Instance{
						ID:     id,
						Label:  formatLabel(id),
						Root:   repoPath,
						Forest: forestBin,
					}
				}
			}
		}
	}
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0111 != 0
}

func sanitizeID(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else if r == '_' || r == ' ' || r == '.' {
			b.WriteRune('-')
		}
	}
	res := strings.Trim(b.String(), "-")
	if res == "" {
		return "instance"
	}
	return res
}

func formatLabel(id string) string {
	parts := strings.Split(id, "-")
	for i, part := range parts {
		if len(part) > 0 {
			parts[i] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return strings.Join(parts, " ")
}
