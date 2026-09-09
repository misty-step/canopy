# Canopy

Canopy is a read-only operator view across one or more Iron Forest instances. It renders ticket delivery, live Kernel, audit, trigger, Run, Ledger, declaration, and log evidence in one Go binary with server-rendered HTML and HTMX updates.

## Boundary

Canopy treats every Forest as an external service. It invokes only the versioned `forest.cli.v2` JSON interface, locally, through `ssh`, or through an authenticated private observation endpoint. It does not import Iron Forest code, read `.iron-forest/runtime` files, open the Ledger database, or expose mutation routes. Optional Habitat, Tach and GitHub-compatible forge APIs supply independent read-only ticket and provider evidence; Canopy keeps no money ledger.

A failed refresh never becomes an empty healthy state. Canopy retains the last successful snapshot and marks it stale. An instance without a successful snapshot is unknown. Trigger polling and Run outcomes remain separate signals.

## Run

Requirements:

- Go 1.26 or newer
- An installed `.iron-forest/bin/forest` binary that emits `forest.cli.v2` JSON envelopes, including additive Run `request_id`/`work` provenance
- `ssh` in `PATH` for SSH instances, or an authenticated HTTP observation service for private Fly instances

```sh
cp canopy.example.json canopy.json
# Edit the Forest paths and instances.
go run . -config canopy.json
```

Open <http://127.0.0.1:8080>. The default listener is loopback-only. For the R90 pilot, all browser routes (including fragments, logs, static files and health) belong behind the ops-owned HTTPS Basic-auth boundary with named viewer identities; never expose the raw Canopy port publicly. Source credentials are separate from viewer authentication and from worker write/ingest credentials.

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
      "forest": "/absolute/path/to/iron-forest/.iron-forest/bin/forest"
    },
    {
      "id": "remote",
      "label": "Remote Forest",
      "host": "operator@example.org",
      "root": "/srv/iron-forest",
      "forest": "/srv/iron-forest/.iron-forest/bin/forest"
    }
  ]
}
```

Remote roots must be absolute. SSH destinations, executable paths, route identifiers, and command operands are validated before execution. Canopy passes each local command directly to the process runner and shell-quotes each remote token before handing one command to the SSH login shell.

Local discovery recognizes only `.iron-forest/config.yaml` plus an executable `.iron-forest/bin/forest` under the same checkout, including matching systemd user services. It does not adopt root-level `forest.yaml`, `agents`, `.forest` or another checkout's/PATH binary. This repository's own profile is `.iron-forest/config.yaml` with declarations under `.iron-forest/agents`; generated runtime and installed binaries are ignored.

### Optional delivery sources

Each instance can add `sources`. Omitting an entry disables that external source explicitly; the UI says unavailable/unknown, not zero. Inventory accepts endpoint URLs and credential **environment-variable names**, never secret values. Every configured credential is required independently: there is no fallback to Habitat write, Tach ingest, OpenRouter, or worker credentials.

```json
{
  "habitat": {
    "endpoint": "https://habitat-r90tools.vercel.app",
    "system": "https://habitat-r90tools.vercel.app",
    "token_env": "HABITAT_READ_TOKEN"
  },
  "forge": {
    "endpoint": "https://api.github.com",
    "web_url": "https://github.com",
    "token_env": "GITHUB_READ_TOKEN"
  }
}
```

Add `tach` with `endpoint` set to the deployed Tach ingest function **base URL** (not the query path) and `token_env` set to `TACH_QUERY_TOKEN`. Provision that name with a scoped Tach **query** credential. The exact server-side request is `POST <endpoint>/v1/provider-usage/query`, header `x-query-key`, body `{"source":"iron-forest","session_ids":["exact-run-id"]}` in batches of at most 50 unique IDs. Canopy accepts only `tach.provider-usage.v1`; it never estimates provider prices or queries a provider-management API.

Habitat uses a complete Bearer token from its named environment variable (including `habitat:` when applicable), authorized for read access to the pilot module/work items. It reads `/api/work/run-links` by exact Run IDs with all pages, `/api/work/items/<immutable-id>`, and `/api/work/items/<immutable-id>/history`. Set `system` to the **exact** namespace in Forest's `work.system`; identity is never normalized from a ticket title, branch or status. Habitat link scope covers current nondeleted items in the credential's authorized modules, not the whole tracker.

Forge credentials need read access to the declared repository's pull requests and reviews. Only explicit current/historical `pr_url` values on the same configured forge and Forest repository are queried. A merged PR must have a source timestamp/SHA and target the declared primary. A human merge actor or a recorded human approval establishes pilot delivery; bot merges without observed human approval remain merged-only. Tracker Done is not merge evidence, and a reopened item retains its immutable identity and earlier observed merge.

When a separately scoped forge credential is unavailable, the R90 worker's authenticated observer can provide the narrow GitHub metadata read capability instead. Set `forge.endpoint` to the **same origin as `observer_url`** with path `/v1/github`, retain `web_url: https://github.com`, and use the same `FOREST_OBSERVATION_TOKEN` environment-variable name. The observer must permit only GET `/repos/r90group/vector/pulls/<positive-id>` and its `/reviews?per_page=100&page=<positive-page>` collection, construct its own upstream HTTPS requests, strip incoming headers and bodies, and refuse redirects. Its worker GitHub credential never enters Canopy. A private HTTP forge source with any other origin, path or credential name is rejected.

Habitat currently returns at most 50 item-history changes. When that history is full or unavailable, or other needed evidence is incomplete, **first-delivery latency stays unknown**. The UI separately labels the interval to the earliest **observed** qualifying merge; it does not promote that observation to proven lifetime first delivery. Previously observed PR references and successful source responses remain in the same volatile refresh snapshot, not a new persisted ledger. A restart cannot recover PR references no longer exposed by Habitat.

The core snapshot/Run history follows existing instance refresh cadence. Optional external sources refresh within that same worker about once a minute, with bounded requests. Failures retain the last successful source data with its original observation time and a stale/unavailable warning. Ticket totals deduplicate exact Run IDs across history/recent/live projections and exact served links. Created links do not assign served cost; conflicting primary associations assign cost to neither ticket and are visible. Failed/cancelled Run usage stays included. Token subcategories are shown separately, never blindly added to input/output. Null metrics, incomplete/pending coverage, missing Runs and explicit zero remain distinct; known values are subtotals until coverage is complete.

### Private HTTP observation

Instead of `host`, an instance can set `observer_url` to the full private `/v1/forest/observe` endpoint and `observer_token_env` to `FOREST_OBSERVATION_TOKEN`. Keep `root` and `forest` set to the worker's actual paths, for example `/data/vector` and `/data/vector/.iron-forest/bin/forest`. HTTP observation is allowed only on loopback, `.internal` hosts, or private RFC1918/ULA addresses. Other remote sources require HTTPS except the exact observer-bound forge proxy described above. Redirects never receive credentials.

The adapter sends `POST` with Bearer authentication and `{"args":[...]}` and consumes `{"stdout":"...","stderr":"...","exit_code":0}`. The ops-owned observer must allow only `version`, `config show`, `declaration list/show`, `status`, `run list --limit 1000 [--after ID]`, and `run logs ID`, with `--json --root <configured-root>`. This read-query POST does not create a Canopy mutation route.

After ops supplies the named read credentials and writes the generated inventory:

```sh
go build -o canopy .
./canopy -config canopy.json -listen 127.0.0.1:8080
```

For the real browser journey, select the Vector instance, inspect each source's observation time, follow the tracker and independently linked PR, expand Run evidence, and open a failed/cancelled Run log. Keep the evidence disclosure open across refreshes; content should update without losing keyboard focus. With a source credential absent, confirm that source is unavailable while fleet/Run/log views continue. Reopen work in the tracker only through its normal authorized workflow, never Canopy; the ticket must remain one row with the previous observed merge and newly attributed Run costs.

## HTTP surface

Canopy serves only GET routes:

- `/` — complete page
- `/fragments/fleet` — fleet rail update
- `/fragments/instance` — selected instance update
- `/logs` — retained, evicted, unknown, or failed log state
- `/healthz` — process liveness
- `/static/` — embedded CSS and HTMX

HTMX 2.0.9 is vendored from the official distribution under the Zero-Clause BSD license. See `THIRD_PARTY_LICENSES`.
