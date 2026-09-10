# Canopy

Canopy is a read-only work and evidence view across independent Iron Forest instances. A searchable work index opens an evidence reader for the current decision, exact-revision review, observed merge, provider subtotal and Run attempts. Desktop inspection keeps the index beside the evidence; mobile gives the selected work its own reading view. Runtime details remain available without dominating the page. One Go binary embeds the HTML, styles, scripts and fonts; there is no frontend application server or second business ledger.

## Boundary

Canopy treats every Forest as an external service. It invokes only the versioned `forest.cli.v2` JSON interface, locally, through `ssh`, or through an authenticated private observation endpoint. It does not import Iron Forest code, read `.iron-forest/runtime` files, open the Ledger database, or expose mutation routes. Optional Habitat, Tach and GitHub-compatible forge APIs supply independent read-only ticket and provider evidence; Canopy keeps no money ledger.

A failed refresh never becomes an empty healthy state. Canopy retains the last successful snapshot and marks it stale. An instance without a successful snapshot is unknown. Trigger polling and Run outcomes remain separate signals.

## Inspect work

The overview distinguishes **Needs you**, **In progress**, and observed **Merged**
counts. Open a work item to inspect its request, candidate, revision-bound review,
merge and tracker state independently. A merge does not imply deployment.

The instance switcher uses ordinary page navigation. Each fleet refresh carries
the page's instance identity, so another viewer's activity cannot relabel it.
Search matches already-rendered work keys, titles and immutable IDs. The **All
work**, **Needs you**, **In progress** and **Merged** filters never expand source
scope. `q` and `filter` URL parameters preserve this context through work links,
reload and browser Back/Forward; they do not become collector queries.

Press `/` to focus a visible search field. Escape clears the focused query while
keeping its category, or dismisses the open instance switcher. All work and
evidence remain readable without JavaScript; search and filtering appear only
when their handlers are available.

Work links use `/?instance=<id>&system=<namespace>&work=<immutable-id>#work-evidence`.
They survive reload and refresh without widening a source query. An unknown work
identity remains unknown; a URL is not permission to fetch or execute it.
Fleet and work disclosures retain their state independently. Keyboard focus,
search caret, page position and declared scroll regions survive replacement.
Long review receipts retain their reading position for the same reviewed
revision and start at the top for a new revision. Selected receipt text is
restored only when both the revision and exact text are unchanged. The evidence
itself is refreshed, not frozen.

Run logs load on demand within the selected work. Their ordinary GET links also
open a complete page with a return link. Known evicted logs remain distinct from
unknown Runs or failed reads. Diagnostics label the Ledger percentage as an
**exit-zero rate**, never agent quality. New Forest `outcome`, `process_exit`
and `completion` fields are independent; legacy missing facts remain unknown.

Provider cost is a subtotal with explicit complete/partial/unknown coverage.
The index rounds for reading; work details retain full precision and the
first-delivery allocation caveats. Human effort and infrastructure are not part
of this provider subtotal.

## Read the mandate

The instance view and fleet switcher project optional `intent` from the effective
`forest config show --json` response. Its fields are `purpose` (string),
`outcomes` (string array), `constraints` (string array), and `release_policy`
(string). This is **declared policy**, not proof of enforcement or a new Canopy
configuration source. The mandate disclosure reuses the existing native
disclosure and refresh behavior.

An unobserved configuration, an absent intent object, explicitly empty fields
and a stale retained declaration remain distinct. Canopy never infers mandate
or release authority from a repository name, role prompt, lack of activity or
missing metadata. Changing intent still happens outside this read-only surface.

## Design authority

[`DESIGN.md`](DESIGN.md) preserves the approved visual identity and is the single
normative token source. Pinned Google alpha-format tooling generates the committed
plain CSS sheet; no Tailwind, React or runtime Node dependency is introduced.
Follow its generation and conformance commands when changing tokens or components.
Browser chrome and the standalone favicon keep checked mirrors of the canvas and
primary colors because those assets cannot inherit the page's custom properties.

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
    "token_env": "HABITAT_READ_TOKEN",
    "work_item_ids": ["immutable-authorized-work-id"]
  },
  "forge": {
    "endpoint": "https://api.github.com",
    "web_url": "https://github.com",
    "token_env": "GITHUB_READ_TOKEN",
    "automation_login": "repository-worker"
  }
}
```

Add `tach` with `endpoint` set to the deployed Tach ingest function **base URL** (not the query path) and `token_env` set to `TACH_QUERY_TOKEN`. Provision that name with a scoped Tach **query** credential. The exact server-side request is `POST <endpoint>/v1/provider-usage/query`, header `x-query-key`, body `{"source":"iron-forest","session_ids":["exact-run-id"]}` in batches of at most 50 unique IDs. Canopy accepts only `tach.provider-usage.v1`; it never estimates provider prices or queries a provider-management API.

Habitat uses a complete Bearer token from its named environment variable (including `habitat:` when applicable), authorized for read access to the pilot module/work items. It reads `/api/work/run-links` by exact Run IDs with all pages, `/api/work/items/<immutable-id>`, and `/api/work/items/<immutable-id>/history`. Set `system` to the **exact** namespace in Forest's `work.system`; identity is never normalized from a ticket title, branch or status. Habitat link scope covers current nondeleted items in the credential's authorized modules, not the whole tracker.

Optional `work_item_ids` declares the exact bounded work inventory independently
of Run history. These identities appear before their first Run, with unknown
usage rather than zero cost. An omitted list retains Run-derived discovery;
Canopy does not enumerate a whole module or infer authorization from its contents.

Forge credentials need read access to the declared repository's pull requests and receipt comments. Only explicit current/historical `pr_url` values on the configured forge and Forest repository are queried. A merge must have its own timestamp/SHA and target the declared primary. `Merged` counts that observation, not an inferred human act. A GitHub `User` account is not by itself proof of human intent or protected merge authority.

Set `automation_login` to the dedicated principal expected to publish review receipts. It qualifies author trust; it does not invalidate historical receipts from other accountable accounts. A usable `forest.review.v1` receipt requires a positive account ID, a `User` or `Bot` author with `OWNER`, `MEMBER` or `COLLABORATOR` repository association, the exact current candidate, matching immutable work provenance, a known Verifier Run, its lifetime window and a canonical comment URL. Request IDs are opaque; no profile-specific naming convention is required.

Use the exact GitHub user login (for example, `repository-worker`) or GitHub App
bot login (for example, `iron-forest[bot]`); keep the literal `[bot]` suffix.
Canopy preserves the complete configured identity. Login safety validation permits
that one terminal suffix while still rejecting an empty name, other brackets,
path separators, whitespace, controls and unsafe punctuation; it does not attempt
to reproduce GitHub's account or App registration policies. This exception is
only for `forge.automation_login`; instance, work-item and Run route identifiers
still reject brackets.

A different accountable author or an unconfigured `automation_login` produces a named caveat without suppressing an otherwise valid review or completed delivery. Freshly observed missing, invalid or ambiguous receipts leave an open candidate `Awaiting verification` with the specific reason; unavailable or stale sources read `Evidence unavailable`. Review, current tracker state and historical delivery are separate; reopening work does not erase earlier observed merges or costs. Neither an author match nor a GitHub `User` merger proves credential isolation or human intent. Host/forge policy, not Canopy, enforces separation between worker and human authority.

When a separately scoped forge credential is unavailable, the R90 worker's authenticated observer can provide the narrow GitHub metadata read capability instead. Set `forge.endpoint` to the **same origin as `observer_url`** with path `/v1/github`, retain `web_url: https://github.com`, and use the same `FOREST_OBSERVATION_TOKEN` environment-variable name. The observer permits bounded GET metadata for the declared repository: `pulls/<id>`, paginated `pulls/<id>/reviews`, and paginated `issues/<id>/comments`. It projects required receipt/identity fields, constructs its own upstream HTTPS requests, strips incoming headers and bodies, and refuses redirects. Its worker GitHub credential never enters Canopy. A private HTTP forge source with another origin, path or credential name is rejected.

Habitat currently returns at most 50 item-history changes. When that history is full or unavailable, or other needed evidence is incomplete, **first-delivery latency stays unknown**. The UI separately labels the interval to the earliest **observed** qualifying merge; it does not promote that observation to proven lifetime first delivery. Previously observed PR references and successful source responses remain in the same volatile refresh snapshot, not a new persisted ledger. A restart cannot recover PR references no longer exposed by Habitat.

The core snapshot/Run history follows existing instance refresh cadence. Optional external sources refresh within that same worker about once a minute, with bounded requests. Failures retain the last successful source data with its original observation time and a stale/unavailable warning. Ticket totals deduplicate exact Run IDs across history/recent/live projections and exact served links. Created links do not assign served cost; conflicting primary associations assign cost to neither ticket and are visible. Failed/cancelled Run usage stays included. Token subcategories are shown separately, never blindly added to input/output. Null metrics, incomplete/pending coverage, missing Runs and explicit zero remain distinct; known values are subtotals until coverage is complete.

Lifetime provider USD is a known subtotal across all attributed Runs, including
reopened work. First-delivery USD is separate: it requires proven first-merge
history and complete provider receipts for all pre-merge Runs. A live Run or a
Run spanning that merge makes the allocation unknown; Canopy never prorates it.

### Private HTTP observation

Instead of `host`, an instance can set `observer_url` to the full private `/v1/forest/observe` endpoint and `observer_token_env` to `FOREST_OBSERVATION_TOKEN`. Keep `root` and `forest` set to the worker's actual paths, for example `/data/vector` and `/data/vector/.iron-forest/bin/forest`. HTTP observation is allowed only on loopback, `.internal` hosts, or private RFC1918/ULA addresses. Other remote sources require HTTPS except the exact observer-bound forge proxy described above. Redirects never receive credentials.

The adapter sends `POST` with Bearer authentication and `{"args":[...]}` and consumes `{"stdout":"...","stderr":"...","exit_code":0}`. The ops-owned observer must allow only `version`, `config show`, `declaration list/show`, `status`, `run list --limit 1000 [--after ID]`, and `run logs ID`, with `--json --root <configured-root>`. This read-query POST does not create a Canopy mutation route.

After ops supplies the named read credentials and writes the generated inventory:

```sh
go build -o canopy .
./canopy -config canopy.json -listen 127.0.0.1:8080
```

For the browser acceptance journey, inspect complete work, human review, changes requested, a merged item awaiting reconciliation, an incomplete review, active work and a zero-Run item. Search and combine categories, recover from no matches, follow a deep link and use Back/Forward. Leave disclosures open, type with a selected search range, and scroll/select a long receipt across consecutive refreshes. A new reviewed revision must start the receipt at the top; changed text must not inherit an old selection. Open the instance switcher during overlapping fleet/work refreshes, and verify that independent viewers retain their own instance context.

At mobile width, primary work content must be readable without horizontal scrolling; diagnostic tables retain their own scroll regions. Exercise keyboard focus, the dedicated evidence view, retained and evicted logs, and navigation back to work. A failed source keeps last-good evidence visibly stale; no first observation remains unknown, and an empty inventory is distinct from a filter with no matches. Use isolated synthetic observations for these states; this exercise does not authorize tracker mutations or new paid work.

## HTTP surface

Canopy serves only GET routes:

- `/` — complete work page; optional instance/system/work selection
- `/fragments/fleet` — fleet switcher update with the page's explicit instance context
- `/fragments/instance` — selected instance update
- `/logs` — full retained/evicted/unknown/failed log page, or a fragment for HTMX
- `/healthz` — process liveness
- `/static/` — embedded styles, scripts, identity mark and self-hosted fonts

HTMX 2.0.9 is vendored from the official distribution under the Zero-Clause BSD license. See `THIRD_PARTY_LICENSES`.

The interface uses Barlow Semi Condensed for identity/headings and IBM Plex Sans
for UI and review prose. Monospace is reserved for exact identifiers and logs.
The fonts are served locally under the SIL Open Font License; browsing Canopy
does not contact a font CDN. Their notices are in `THIRD_PARTY_LICENSES`.
