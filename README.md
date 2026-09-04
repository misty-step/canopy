# Canopy

Canopy is a read-only operator view across one or more Iron Forest instances. It renders live Kernel, audit, trigger, Run, Ledger, declaration, and log data in one Go binary with server-rendered HTML and HTMX updates.

## Boundary

Canopy treats every Forest as an external service. It invokes only the versioned `forest.cli.v2` JSON interface, either as a local process or through `ssh`. It does not import Iron Forest code, read `.forest` files, open the Ledger database, or expose mutation routes.

A failed refresh never becomes an empty healthy state. Canopy retains the last successful snapshot and marks it stale. An instance without a successful snapshot is unknown. Trigger polling and Run outcomes remain separate signals.

## Run

Requirements:

- Go 1.26 or newer
- An Iron Forest binary that emits `forest.cli.v2` JSON envelopes
- `ssh` in `PATH` for remote instances

```sh
cp canopy.example.json canopy.json
# Edit the Forest paths and instances.
go run . -config canopy.json
```

`-config` is optional. Run `go run .` without it to start from zero configuration and use local discovery (see [Discovery](#discovery)).

Open <http://127.0.0.1:8080>. The default listener is loopback-only. If you bind Canopy to another interface, protect it at the network or reverse-proxy boundary because Run logs can contain sensitive repository output.

Build and verify:

```sh
go test ./...
go vet ./...
go build -o canopy .
```

## Discovery

Without an inventory file, Canopy still starts and discovers local Iron Forest instances from two sources:

- **systemd user units** named `forest@<name>.service`. Canopy reads each unit's `WorkingDirectory` and `ExecStart` and accepts the unit only when the working directory path contains a `/misty-step/` segment and the `ExecStart` binary is executable.
- **Development checkouts** under `~/Development/misty-step`. A checkout is discovered when it contains both a `.forest` directory and an executable `./forest` binary.

Each discovered instance derives a sanitized id and a title-case label from the unit or checkout name, uses the working directory as its root, and uses the resolved `forest` binary as its executable. Instances are sorted by id.

## Configuration precedence

Canopy resolves configuration in this order:

1. `-config <path>` — load exactly that inventory file. A missing or invalid file is a fatal startup error.
2. `./canopy.json` — loaded when `-config` is not set and the file exists. A present but invalid default file is skipped.
3. Zero-config discovery — when no inventory file is loaded, Canopy starts with an empty inventory and fills it from [Discovery](#discovery).

Discovered instances are appended to any configured inventory and deduplicated by instance id, so a configured id wins over a discovered instance with the same id. `-listen <host:port>` overrides the listen address from any source. Defaults are `127.0.0.1:8080` for the listener, 10 seconds for the fleet interval, and 2 seconds for the selected-instance interval.

## Empty state

Canopy never renders a failed refresh as a healthy empty view. Before an instance produces its first successful snapshot it is `unknown` and the panel shows a waiting-for-first-observation message instead of healthy data. After a successful snapshot, a later failed or timed-out refresh retains that snapshot and marks the instance `stale`.

When no instances are configured or discovered, Canopy still serves the page: the fleet rail reports "No instances configured." and the main panel prompts to add an instance. `/healthz` reports process liveness only and remains `ok` when no instance is reachable.

## Inventory

```json
{
  "listen": "127.0.0.1:8080",
  "fleet_interval_seconds": 10,
  "selected_interval_seconds": 2,
  "instances": [
    {
      "id": "local",
      "label": "Local Forest",
      "root": "/absolute/path/to/iron-forest",
      "forest": "/absolute/path/to/iron-forest/forest"
    },
    {
      "id": "remote",
      "label": "Remote Forest",
      "host": "operator@example.org",
      "root": "/srv/iron-forest",
      "forest": "/usr/local/bin/forest"
    }
  ]
}
```

Remote roots must be absolute. SSH destinations, executable paths, route identifiers, and command operands are validated before execution. Canopy passes each local command directly to the process runner and shell-quotes each remote token before handing one command to the SSH login shell.

## HTTP surface

Canopy serves only GET routes:

- `/` — complete page
- `/fragments/fleet` — fleet rail update
- `/fragments/instance` — selected instance update
- `/logs` — retained, evicted, unknown, or failed log state
- `/healthz` — process liveness
- `/static/` — embedded CSS and HTMX

HTMX 2.0.9 is vendored from the official distribution under the Zero-Clause BSD license. See `THIRD_PARTY_LICENSES`.
