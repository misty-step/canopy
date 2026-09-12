# Canopy

Canopy is a read-only view of one software change and its evidence across independent Iron Forest instances. A sole observed work item opens directly; a bounded, searchable work disclosure selects among multiple items. The selected change keeps the full reading width for the request, actual execution, candidate, revision-bound review, blocker or operator action, elapsed time, and provider subtotal with coverage. Declarations, infrastructure and aggregate history stay behind native disclosures. One Go binary embeds the HTML, styles, scripts and fonts; there is no frontend application server or second business ledger.

## Boundary

Canopy treats every Forest as an external service. It invokes only the versioned `forest.cli.v2` JSON interface, locally, through `ssh`, or through an authenticated private observation endpoint. It does not import Iron Forest code, read `.iron-forest/runtime` files, open the Ledger database, or expose mutation routes. Optional Habitat and GitHub-compatible forge APIs supply independent read-only ticket and pull-request evidence. Provider cost is read from the native Run history the collector already fetches: Canopy keeps no money ledger, holds no accounting API credential and polls no accounting service.

Current status and optional details have independent clocks. A failed status read retains the last successful status and marks it stale; no first status success is unknown. A version, configuration, declaration, history or external-source failure cannot hold or renew current status. Each failed optional section retains only its own last-good observation with its original timestamp; first failures remain unknown.

## Inspect work

Open a work item to inspect its request, candidate, revision-bound review,
merge and tracker state independently. Current execution comes from exact Run
provenance in status, even when optional history or external sources are slow.
No live execution is not success, rejection or cancellation. An agent exit is
not a verifier verdict, and a merge does not imply deployment.

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
Long review evidence retains its reading position for the same reviewed
revision and starts at the top for a new revision. Selected review text is
restored only when both the revision and exact text are unchanged. The evidence
itself is refreshed, not frozen.

Run logs load on demand within the selected work. Their ordinary GET links also
open a complete page with a return link. Known evicted logs remain distinct from
unknown Runs or failed reads. Diagnostics label the Ledger percentage as an
**exit-zero rate**, never agent quality. New Forest `outcome`, `process_exit`,
`completion` and `provider_cost` fields are independent; legacy missing facts
remain unknown, and an unreadable charge never rewrites them.

For explicitly recorded cancelled executions, Run attempts can disclose optional
`recovery` with a private repo-relative native Git worktree path and optional base
revision. "Worktree retained" is a historical observation, subject to manual
disposal—not an archive, download, restore instruction, candidate revision or
permission to resume. Missing recovery stays unknown, not clean. Canopy never
reads or serves worktree contents and does not manage expiry.

Provider cost is the direct charge each Run reports, with explicit
complete/partial/unknown coverage. The index rounds for reading; work details
retain full precision and the first-delivery allocation caveats. Human effort and
infrastructure are not part of this provider subtotal.

## Read the mandate

Inside Runtime details, the instance view projects optional `intent` from the
effective `forest config show --json` response. Its fields are `purpose` (string),
`outcomes` (string array), `constraints` (string array), and `release_policy`
(string). This is **declared policy**, not proof of enforcement or a new Canopy
configuration source. Its configuration observation time is independent of
current status.

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
- An installed `.iron-forest/bin/forest` binary that emits `forest.cli.v2` JSON envelopes, including additive Run `request_id`/`work` provenance and the optional `provider_cost` object
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

Each instance can add `sources`. Omitting an entry disables that external source explicitly; the UI says unavailable/unknown, not zero. Inventory accepts endpoint URLs and credential **environment-variable names**, never secret values. Every configured credential is required independently: there is no fallback to Habitat write, observer, forge or worker credentials. Provider cost needs no credential at all, because it arrives inside the Run records Forest already returns.

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

Provider cost is not a configured source. A Run record may carry an optional `provider_cost` object — `{"provider":"openrouter","cost_usd":0.0125,"complete":true}` — and Canopy then displays that direct charged amount. An absent, unreadable or foreign-provider object is **unknown**, never zero; an explicit `0` is a real charge of nothing; `complete:false` means charged responses were still open, so the amount is a subtotal. Because the field is optional, malformed accounting cannot fail the Run record, suppress its software outcome, or gate a first observation. Canopy never derives a price from catalog metadata and never calls a provider-management API to reconcile it.

Habitat uses a complete Bearer token from its named environment variable (including `habitat:` when applicable), authorized for read access to the pilot module/work items. It reads `/api/work/run-links` by exact Run IDs with all pages, `/api/work/items/<immutable-id>`, and `/api/work/items/<immutable-id>/history`. Set `system` to the **exact** namespace in Forest's `work.system`; identity is never normalized from a ticket title, branch or status. Habitat link scope covers current nondeleted items in the credential's authorized modules, not the whole tracker.

Optional `work_item_ids` declares the exact bounded work inventory independently
of Run history. These identities appear before their first Run, with unknown
usage rather than zero cost. An omitted list retains Run-derived discovery;
Canopy does not enumerate a whole module or infer authorization from its contents.

Forge credentials need read access to the declared repository's pull requests and commit comparisons. Canopy enumerates bounded pages of open PRs, joins exact published candidate heads, and retains exact-branch/changed-head observations as stale rather than a match. Explicit current/historical Habitat `pr_url` values remain independently observable. A PR merge must have its own timestamp/SHA and target the declared primary; a GitHub `User` account does not prove human intent or protected merge authority.

Review is not pinned in inventory. Canopy collects `forest review list --json` through the same `forest.cli.v2` inspect path used for other details (locally, over SSH, or the private observer) and joins each published row to already observed work by exact revision. Inventory has no `forge.candidates` array and no per-ticket branch, SHA, or URL override. Canopy never infers a candidate from a pull-request title, a `forest/` branch-name prefix, or a repository-wide PR search.

A current review requires the listed revision to equal the observed head, a readable published request and verdict ref, and the verdict-commit time to fall within exactly one known Verifier Run with matching immutable work provenance. The review read surface supplies matching Ledger Runs; Canopy also rejects conflicting history/status projections. Zero or multiple matching Runs remain unverified. A changed head makes the previous join stale until the new revision publishes its own refs. Missing verdicts stay absent; malformed verdicts stay unreadable. Checks are separately published evidence, not a replacement for a verdict. GitHub comments are optional human sugar and are not read or required by Canopy.

Candidates without an observed PR are compared with the declared primary through GitHub's commit comparison API. Exact ancestry establishes that the candidate is present on primary, including git-native landings before the forge source existed. It does not invent a PR, merge event, actor, timestamp, tracker completion, or first-delivery cost.

Freshly observed missing, invalid or unreadable reviews remain unverified with the specific reason; unavailable or stale sources retain their independent warning. Review, current tracker state and historical delivery are separate; reopening work does not erase earlier observed merges or costs. Neither Git author metadata nor a GitHub `User` merger proves credential isolation or human intent. Host/forge policy, not Canopy, enforces separation between worker and human authority. The obsolete `automation_login` and `candidates` inventory fields are rejected by strict configuration decoding.

An authenticated private observer can provide the narrow GitHub metadata read capability instead of a separately scoped forge credential. Set `forge.endpoint` to the **same origin as `observer_url`** with path `/v1/github`, retain `web_url: https://github.com`, and use the same observer-token environment-variable name. Automatic discovery requires the observer to permit bounded open `pulls` pages, `pulls/<id>`, paginated `pulls/<id>/reviews`, and exact candidate-to-primary `compare` reads for the declared repository, as well as `review list` on its Forest read boundary. An older observer without these capabilities reports unavailable evidence; this local change does not update remote observers. A private HTTP forge source with another origin, path or credential name is rejected.

Habitat currently returns at most 50 item-history changes. When that history is full or unavailable, or other needed evidence is incomplete, **first-delivery latency stays unknown**. The UI separately labels the interval to the earliest **observed** qualifying merge; it does not promote that observation to proven lifetime first delivery. Previously observed PR references and successful source responses remain in the same volatile refresh snapshot, not a new persisted ledger. A restart cannot recover PR references no longer exposed by Habitat.

Status invokes only `forest status --json --root ...` at the selected/fleet interval. Stable details and external sources each have a separately serialized, cancellable lane per instance; neither runs inside status collection. Detail collection has a 30-second total bound: version and configuration get up to 5 seconds each, declarations and paginated history up to 10 seconds each, and reviews up to 15 seconds within the remaining total budget. Declarations are limited to 256 and history to 100,000 rows; exceeding a limit is an incomplete observation, never a complete ledger. Optional lanes wait about a minute between attempts; each external collection is bounded to 30 seconds. Detail freshness expires after 91 seconds, independently of status. Per-source success clocks are never renewed by a failed read. State is volatile and removed with a discovered instance; late cancelled results cannot overwrite a readded instance.

Ticket totals deduplicate exact Run IDs across history/recent/live projections and exact served links. Created links do not assign served cost; conflicting primary associations assign cost to neither ticket and are visible. Failed/cancelled Run charges stay included. Token subcategories are shown separately, never blindly added to input/output. Missing Runs, unreported charges and explicit zero remain distinct: a ticket reads `complete` only when every attributed Run reports a complete charge and its history window is still fresh, `partial` when some charges are known or the window may have moved on, and `unknown` when no charge is reported at all.

Lifetime provider USD is a known subtotal across all attributed Runs, including
reopened work. First-delivery USD is separate: it requires proven first-merge
history and a complete reported charge for every pre-merge Run. A live Run or a
Run spanning that merge makes the allocation unknown; Canopy never prorates it.

### Private HTTP observation

Instead of `host`, an instance can set `observer_url` to the full private `/v1/forest/observe` endpoint and `observer_token_env` to `FOREST_OBSERVATION_TOKEN`. Keep `root` and `forest` set to the worker's actual paths, for example `/data/vector` and `/data/vector/.iron-forest/bin/forest`. HTTP observation is allowed only on loopback, `.internal` hosts, or private RFC1918/ULA addresses. Other remote sources require HTTPS except the exact observer-bound forge proxy described above. Redirects never receive credentials.

The adapter sends `POST` with Bearer authentication and `{"args":[...]}` and consumes `{"stdout":"...","stderr":"...","exit_code":0}`. The ops-owned observer must allow only `version`, `config show`, `declaration list/show`, `status`, `run list --limit 1000 [--after ID]`, `run logs ID`, and `review list`, with `--json --root <configured-root>`. This read-query POST does not create a Canopy mutation route.

After ops supplies the named read credentials and writes the generated inventory:

```sh
go build -o canopy .
./canopy -config canopy.json -listen 127.0.0.1:8080
```

For the browser acceptance journey, inspect complete work, verified work awaiting merge, changes requested, a merged item awaiting reconciliation, an incomplete review, active work and a zero-Run item. Search and combine categories, recover from no matches, follow a deep link and use Back/Forward. Leave disclosures open, type with a selected search range, and scroll/select long review evidence across consecutive refreshes. A new reviewed revision must start the review at the top; changed text must not inherit an old selection. Open the instance switcher during overlapping fleet/work refreshes, and verify that independent viewers retain their own instance context.

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

## Explicit-request profile

The active `.iron-forest` profile inherits one model from `defaults.yaml`:
`openrouter/deepseek/deepseek-v4.1-flash`. All five declarations load the checked-in,
credential-free `extensions/models.ts` registry; their existing tools, thinking
and intervals remain unchanged. Registry prices are catalog metadata, not the
provider billing authority displayed by Canopy. Credentials remain outside the
profile in the protected runtime environment.

Builder, Verifier and Fixer polls return exit 1 (no work); Critic and Tester keep
their existing no-intake read-only polls. A timer or old backlog is not authority
to start work. This source profile change does not start a Kernel or deploy it.
