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

Open <http://127.0.0.1:8080>. The default listener is loopback-only. If you bind Canopy to another interface, protect it at the network or reverse-proxy boundary because Run logs can contain sensitive repository output.

Build and verify:

```sh
go test ./...
go vet ./...
go build -o canopy .
```

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
