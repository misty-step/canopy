---
version: alpha
name: Canopy
description: A read-only work and evidence view across independent Iron Forest instances.
colors:
  primary: "#2447d8"
  canvas: "#f4f7fb"
  surface: "#ffffff"
  ink: "#213047"
  muted: "#5f6d84"
  blue-wash: "#dde5ff"
  green: "#247a62"
  green-wash: "#e6f2ed"
  oxide: "#a8442c"
  oxide-wash: "#fff0e9"
  line: "#d6deed"
  line-strong: "#8290a5"
  nav-shadow: "#2130471a"
  filter-hover: "#e9eefc"
  work-hover: "#eaf0ff"
  signal-wash: "#eaf0f6"
  scrollbar: "#a6b3cf"
  log-text: "#f1f5fc"
  log-line: "#4a5a72"
  log-muted: "#c4cfe1"
  log-focus: "#a7b8ff"
  filter-idle: "#eaf0fa"
typography:
  # Keep unitless lineHeight values quoted: the pinned 0.4.0 resolver drops numbers.
  display:
    fontFamily: '"Barlow Semi Condensed", "Arial Narrow", sans-serif'
    fontWeight: 600
    lineHeight: "1.1"
  text:
    fontFamily: '"IBM Plex Sans", system-ui, sans-serif'
    fontSize: 16px
    fontWeight: 400
    lineHeight: "1.55"
  code:
    fontFamily: 'ui-monospace, "SFMono-Regular", Consolas, "Liberation Mono", monospace'
    fontSize: 0.82em
  h1:
    fontSize: 4.5rem
    fontWeight: 700
    letterSpacing: -0.025em
  h1-compact:
    fontSize: 3rem
  h1-mobile:
    fontSize: 3.1rem
  h2:
    fontSize: 1.875rem
    letterSpacing: -0.012em
  h3:
    fontSize: 1rem
    fontWeight: 600
    lineHeight: "1.4"
  note:
    fontSize: 0.875rem
    lineHeight: "1.65"
  label:
    fontSize: 0.75rem
    fontWeight: 500
rounded:
  signal: 0.2rem
  small: 0.25rem
  control: 0.3rem
  detail-mobile: 0.35rem
  drawer: 0.4rem
  panel: 0.45rem
  detail: 0.5rem
spacing:
  xs: 0.25rem
  sm: 0.5rem
  md: 1rem
  lg: 1.5rem
  xl: 2rem
  xxl: 3rem
  page-width: 1560px
  gutter-min: 1rem
  gutter-max: 3.25rem
components:
  page:
    backgroundColor: "{colors.canvas}"
    textColor: "{colors.ink}"
    typography: "{typography.text}"
  panel:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.panel}"
  brand:
    textColor: "{colors.primary}"
    typography: "{typography.display}"
  selection:
    backgroundColor: "{colors.blue-wash}"
    textColor: "{colors.primary}"
  filter-selected:
    backgroundColor: "{colors.primary}"
    textColor: "{colors.surface}"
    rounded: "{rounded.control}"
    height: 2.75rem
  filter-hover:
    backgroundColor: "{colors.filter-hover}"
    textColor: "{colors.primary}"
  work-hover:
    backgroundColor: "{colors.work-hover}"
    textColor: "{colors.ink}"
  filter-idle:
    backgroundColor: "{colors.filter-idle}"
    textColor: "{colors.muted}"
  signal:
    backgroundColor: "{colors.signal-wash}"
    textColor: "{colors.muted}"
    rounded: "{rounded.signal}"
    typography: "{typography.label}"
  signal-fresh:
    backgroundColor: "{colors.green-wash}"
    textColor: "{colors.green}"
  signal-warning:
    backgroundColor: "{colors.oxide-wash}"
    textColor: "{colors.oxide}"
  mandate:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.panel}"
    padding: "{spacing.lg}"
  log:
    backgroundColor: "{colors.ink}"
    textColor: "{colors.log-text}"
    rounded: "{rounded.drawer}"
  log-caption:
    backgroundColor: "{colors.ink}"
    textColor: "{colors.log-muted}"
---

## Overview

Canopy is an observation instrument, not a factory control desk. Preserve the
approved MIS-43 identity from release `c73bd0056a21f80e7a2a06763582488659bf6141`:
a cool daylight canvas, cobalt navigation, condensed headings and readable work
evidence. The mandate explains why an instance exists; source-backed activity,
delivery, runtime identity and provider coverage explain what has been observed.
Do not imply that reading a policy proves its enforcement.

**Token authority:** this file's YAML is the only normative palette, font-stack,
type-foundation, radius and named-spacing source. `static/design-tokens.css` is a
committed generated projection, never a second place to choose values.
`static/canopy.css` owns responsive composition and component geometry. Templates
own semantic HTML and evidence wording. There is no frontend framework migration.

The format is Google's **alpha** specification. The development-only dependency
is pinned to **@google/design.md 0.4.0**, published from
[`9bf8eae67128b6cc55ad9bf86665767deb4c11cd`](https://github.com/google-labs-code/design.md/tree/9bf8eae67128b6cc55ad9bf86665767deb4c11cd).
Primary references: [format specification](https://github.com/google-labs-code/design.md/blob/9bf8eae67128b6cc55ad9bf86665767deb4c11cd/docs/spec.md),
[tooling and public API](https://github.com/google-labs-code/design.md/blob/9bf8eae67128b6cc55ad9bf86665767deb4c11cd/README.md),
and [package manifest](https://github.com/google-labs-code/design.md/blob/9bf8eae67128b6cc55ad9bf86665767deb4c11cd/packages/cli/package.json).
Read those versioned sources before upgrading; do not substitute an unpinned
`npx` invocation or assume the alpha schema is stable.

## Colors

The core six are canvas, surface, ink, muted, primary and line. Cobalt identifies
navigation, selection and reported running activity; green is a fresh or positive
observation; oxide indicates stale evidence, errors or partial coverage. Every
state must also have text, not color alone. Pale washes group related evidence.
The retained log reader uses ink with its own light text, caption and focus colors.

Line, line-strong, scrollbar and nav-shadow are non-text structural colors. They
are not foreground text tokens; an upstream orphan-token finding does not license
inventing decorative components just to reference them. Component contrast pairs
are checked by the pinned official linter. Rendered text, focus and actual
background combinations still require browser verification.

## Typography

Self-hosted **Barlow Semi Condensed** (600, 700) establishes the brand and heading
hierarchy. **IBM Plex Sans** carries body text, controls and evidence. The system
monospace stack is reserved for immutable IDs, revisions, command declarations and
numeric details; do not replace ordinary prose with terminal styling.

The text foundation is 16px with 1.55 leading. Headings are compact; notes use more
leading. H1 interpolates between h1-compact and h1, with the retained h1-mobile
size on narrow screens. Evidence receipts remain selectable and generally below
80 characters per line. Long identifiers and policy text wrap instead of widening
the viewport. Font declarations only associate these existing families with their
vendored files; they are not independent token authorities.

## Layout

Use the existing single masthead, instance switcher, instance heading and work
index. A compact mandate sits between observation health and work: purpose is
always visible; one native disclosure reveals outcomes, constraints and release
policy. A fleet entry summarizes declared purpose without implying active work.

The page is left aligned, capped at page-width, with fluid gutters between
gutter-min and gutter-max. Component geometry remains CSS, not generated utility
classes. Preserve breakpoints at 1100px, 850px and 650px. Desktop work selection
keeps an index beside the evidence reader; narrow screens give evidence its own
reading view. The mandate's two columns collapse at the existing mobile breakpoint.

## Elevation & Depth

White content on a cool canvas establishes the hierarchy. Rules separate evidence
and runtime sections. The instance menu alone uses nav-shadow to communicate an
overlay; do not introduce floating panels, gradients or global drop shadows.

## Shapes

Keep the small, role-specific radii in the token table. Status dots and evidence
nodes remain circles because they encode observation state. Do not homogenize the
existing work index, controls and reader into a repeated card kit.

## Components

Use Go `html/template` composition, embedded CSS and HTMX. `fleet.html` owns the
instance menu, `instance.html` composes the selected instance, `mandate.html` owns
both the full mandate and the reusable `mandate-summary` definition,
`tickets.html` owns work/evidence components, and `log.html` owns retained logs.
Reuse `.section-heading`, `.reading-note`, `.signal`, `.ticket-evidence` and the
native disclosure behavior; no second disclosure controller or icon library.

The mandate is declared policy from `forest config show --json`'s optional
`data.intent`. A missing observation, an absent intent object, explicitly empty
fields, and a stale retained declaration are different states. Escape all values
through Go templates. A declared release policy never enables a publish control.

Run records, independent review/merge evidence and provider attribution remain
authoritative in their existing projections. Complete, partial and unknown usage
coverage are distinct; absent provider data is not zero spend. Bootstrap fixtures
must be visibly labeled and must not invent active Runs, release evidence or cost.

### Generation and conformance

Use Node 18+ only for development tooling; the deployed artifact remains one Go
binary with plain CSS and vendored assets.

```sh
npm ci --ignore-scripts
npm run design:generate
npm run design:check
```

The generator uses the pinned official resolved-token API and emits ordinary
`:root` custom properties, not Tailwind `@theme`. `design:check` runs official
format/reference/contrast findings, verifies the committed generated sheet, and
checks hand-authored CSS for unknown or overridden tokens, literal color drift,
and new font stacks. Values belong here; expressions, layout, breakpoints and
semantic state selectors belong in the hand-authored sheet. Tokens with no
component text role may produce documented upstream warnings; errors fail.

Browser acceptance covers full pages and refreshed fragments, desktop and narrow
screens, keyboard focus and disclosures, long escaped policy content, two
different mandates, missing metadata, first-observation absence, stale snapshots,
and existing partial/unknown provider coverage. A passing token check is not a
claim that those rendered scenarios have been exercised.

## Do's and Don'ts

- Do preserve the existing identity and document an intentional token change here.
- Do keep mandate declarations separate from live observations and authorization.
- Do keep current evidence readable while labeling stale or unavailable sources.
- Do use native HTML, visible keyboard focus and the existing reduced-motion rule.
- Do verify actual templates and embedded assets in a browser before acceptance.
- Do not add mutation controls, an approval queue, a scheduler or another ledger.
- Do not infer authority, active Runs, delivery or zero spend from missing data.
- Do not edit generated token CSS by hand or introduce a second palette.
- Do not add React, Tailwind, remote font calls or decorative motion for tooling.
