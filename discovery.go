package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
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

	instances = resolveDiscoveryCollisions(instances, nil)

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
// parent that declares itself a Forest checkout. A checkout is recognized
// either by a forest.yaml declaration or by the legacy combination of a
// .forest directory and an executable repo-local ./forest binary. When the
// repo-local binary is missing or not executable, the Forest binary is
// resolved from PATH and then from the self-host Iron Forest factory
// checkout. It is separated from scanDevelopmentRoots so unit tests can
// supply a temporary directory with mocked checkouts.
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
		forestYAML := filepath.Join(repoPath, "forest.yaml")

		if !isRegularFile(forestYAML) {
			fi, err := os.Stat(forestDir)
			if err != nil || !fi.IsDir() || !isExecutable(forestBin) {
				continue
			}
		}

		binary := resolveForestBinary(repoPath, parent)
		if binary == "" {
			continue
		}

		if _, exists := out[repoPath]; exists {
			continue
		}
		id := sanitizeID(entry.Name())
		out[repoPath] = Instance{
			ID:     id,
			Label:  formatLabel(id),
			Root:   repoPath,
			Forest: binary,
		}
	}
}

// resolveForestBinary returns the Forest executable to use for a checkout.
// It prefers a repo-local ./forest binary, then one on PATH, then the
// compiled binary in the self-host Iron Forest factory checkout.
func resolveForestBinary(repoPath, parent string) string {
	local := filepath.Join(repoPath, "forest")
	if isExecutable(local) {
		return local
	}
	if path, err := exec.LookPath("forest"); err == nil && path != "" {
		return path
	}
	factory := filepath.Join(parent, "iron-forest", "forest")
	if isExecutable(factory) {
		return factory
	}
	return ""
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
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

// resolveDiscoveryCollisions gives every discovered instance a distinct valid
// identity without touching explicit inventory entries. Discovered roots that
// already have an explicit entry for the same normalized root are omitted:
// the explicit route is authoritative and the checkout must not appear as an
// independently observed instance. Remaining discovered IDs that collide with
// an explicit ID or with each other are suffixed with the smallest
// discriminating counter ("-2", "-3", ...). Ordering is by normalized
// root so repeated scans and input reorderings keep the same route-to-root
// binding, and ordinary noncolliding IDs are preserved unchanged.
func resolveDiscoveryCollisions(discovered []Instance, explicit []Instance) []Instance {
	explicitIDs := make(map[string]struct{}, len(explicit))
	explicitRoots := make(map[string]struct{}, len(explicit))
	for _, inst := range explicit {
		explicitIDs[inst.ID] = struct{}{}
		if inst.Root != "" {
			explicitRoots[filepath.Clean(inst.Root)] = struct{}{}
		}
	}

	ordered := append([]Instance(nil), discovered...)
	sort.Slice(ordered, func(i, j int) bool {
		ri, rj := ordered[i].Root, ordered[j].Root
		if ri != rj {
			return ri < rj
		}
		if ordered[i].ID != ordered[j].ID {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].Forest < ordered[j].Forest
	})

	used := make(map[string]struct{}, len(explicitIDs)+len(ordered))
	for id := range explicitIDs {
		used[id] = struct{}{}
	}
	seenRoots := make(map[string]struct{}, len(ordered))
	resolved := make([]Instance, 0, len(ordered))
	for _, inst := range ordered {
		cleanRoot := inst.Root
		if cleanRoot != "" {
			cleanRoot = filepath.Clean(cleanRoot)
		}
		if _, conflict := explicitRoots[cleanRoot]; conflict {
			continue
		}
		if _, duplicate := seenRoots[cleanRoot]; duplicate {
			continue
		}
		base := inst.ID
		if base == "" {
			base = "instance"
		}
		candidate := base
		if _, taken := used[candidate]; taken {
			candidate = disambiguateID(base, used)
		}
		if candidate != inst.ID {
			inst.ID = candidate
			inst.Label = formatLabel(candidate)
		}
		used[candidate] = struct{}{}
		seenRoots[cleanRoot] = struct{}{}
		resolved = append(resolved, inst)
	}
	return resolved
}

// disambiguateID returns the smallest "base-N" variant that is unused and
// remains a valid route identifier.
func disambiguateID(base string, used map[string]struct{}) string {
	for n := 2; ; n++ {
		candidate := base + "-" + strconv.Itoa(n)
		if _, taken := used[candidate]; taken {
			continue
		}
		if validateRouteIdentifier(candidate, "instance id") != nil {
			continue
		}
		return candidate
	}
}
