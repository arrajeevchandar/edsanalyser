# EDS Migration-Mapping & Best-Practices Comparator — Implementation Plan

> Status: **Plan for review — no code yet.**
> Scope: two new capabilities built on the existing edsanalyser engine.
> **Tool A — Migration Mapping & Estimation:** analyze a *non-EDS* site, map each
> detected component to its closest EDS block, flag what won't convert cleanly, and
> estimate migration effort.
> **Tool B — Best-Practices Comparator:** score an EDS implementation against EDS
> conventions, and diff two implementations (A vs B, or scan vs reference profile).

This document is the production execution spec: data model, algorithms, file-by-file
changes, API, frontend, tests, calibration, security, and phased delivery.

---

## 0. Why this is different from the current tool

The current tool answers *"what is on this already-built EDS site?"* — a commodity audit.
These two tools answer *"how do I get to EDS, and is what I built good EDS?"* — the
forward-looking, higher-value questions. The crawl/fetch/SSE/storage/dashboard substrate is
~70% reusable; the new work is three engines: **generic component detection**, a
**block-mapping classifier**, and a **rule-based conformance/diff engine**.

### Goals
- **A:** Given any public URL (EDS or not), produce a per-page and site-wide component→block
  mapping with confidence, convertibility buckets, blocking flags, and an effort estimate with
  an honest low/high range and surfaced assumptions.
- **B:** Given one or two scans, produce per-rule/per-category conformance scores, a prioritized
  findings list, and (A/B) a structural + score diff.

### Non-goals (v1)
- No automatic code generation of EDS blocks (mapping + estimate only; codegen is a later phase).
- No authenticated crawling, no headless-rendered DOM (source HTML remains canonical — see §5.4).
- No "magic" — every confidence/score is explainable from named signals.

### Definitions
- **Component** (Tool A): a detected semantic UI region in a non-EDS page (hero, card grid, nav…).
- **Block** (EDS): a named unit of EDS content (`cards`, `columns`, `hero`, …).
- **Convertibility**: how cleanly a component maps to EDS — `easy | medium | hard | custom`.
- **Conformance** (Tool B): how well an EDS implementation follows EDS best practices.

---

## 1. How it reuses the existing engine

| Existing asset | Reused for | Notes |
|---|---|---|
| `Discoverer.Discover` ([discover.go]) | A & B crawl seeds | unchanged |
| worker pool + queue in `Service.runScan` ([service.go:255]) | A & B page fetching | parameterize the per-page analyzer (see §2.2) |
| `fetchAndAnalyzePage` ([service.go:565]) | A & B | swap `AnalyzeHTML` for an injected analyzer fn |
| SSE `publish`/`Subscribe` ([service.go:235]) | live progress for both | new event types only |
| `SQLiteStore` + `ensureColumn` ([storage.go:503]) | additive persistence | new tables, never alter existing ones |
| `LighthouseRunner` ([lighthouse.go]) | B (perf/a11y rules) | reused as-is |
| `scoreRollup` ([service.go:688]) | B category roll-ups | generalize to weighted averages |
| HTML helpers `walk/findFirst/classList/attr/nodeText/isElement/compactText` ([analyze.go]) | A detector | reuse verbatim — do not duplicate |
| React dashboard shell + `api.ts`/`types.ts` ([src/]) | A & B tabs | add tabs, keep contract style |

**Guiding principle (mirrors the chatbot plan):** *additive only*. New tables via `ensureColumn`,
new endpoints, new types — the existing audit path keeps working untouched.

---

## 2. Shared foundations

### 2.1 Scan "kind"

Introduce a discriminator so one pipeline serves three jobs:

```go
// types.go
type ScanKind string

const (
    KindAudit     ScanKind = "audit"      // existing behaviour (default)
    KindMigration ScanKind = "migration"  // Tool A
    KindComparison ScanKind = "comparison" // Tool B (A/B)
)
```

- Add `Kind ScanKind` to `ScanSummary` (defaults to `audit` for back-compat; old rows read as `audit`).
- Storage: `ensureColumn("scans","kind","TEXT NOT NULL DEFAULT 'audit'")`.
- `StartScan` gains a `Kind` in `ScanOptions`; the API chooses the analyzer + post-processing.

### 2.2 Pluggable per-page analyzer

Today the worker hard-codes `AnalyzeHTML`. Extract an interface so migration uses a different
analyzer while sharing the crawl/queue/dedupe/SSE machinery:

```go
type PageAnalyzer interface {
    Analyze(pageURL string, body io.Reader, root *url.URL) (PageResult, error)
}
```

- `EDSAnalyzer` wraps the current `AnalyzeHTML` (audit + comparison source data).
- `GenericComponentAnalyzer` (new, §3.1) produces the same `PageResult` envelope **plus** a new
  `Components []ComponentInfo` field (additive; nil for EDS analyzer).
- Inject via `ServiceOptions.Analyzer`; default = `EDSAnalyzer`. `fetchAndAnalyzePage` calls
  `s.analyzer.Analyze(...)`.

This keeps SEO/links/Lighthouse working identically for migration scans — we still want
link-graph, SEO gaps, and Lighthouse on the *source* site (they feed the estimate and the
"current state" report).

### 2.3 Persistence strategy

New tables, never altering `pages`/`scans` columns beyond `ensureColumn` additions:

- `page_components` — one row per detected component (Tool A).
- `migration_mappings` — one row per component→block decision (Tool A).
- `migration_estimates` — one row per scan (Tool A rollup).
- `conformance_findings` — one row per rule evaluation (Tool B).
- `conformance_reports` — one row per scan or per A/B pair (Tool B rollup).

All payload-heavy fields stored as JSON text columns (matches `blocks_json`/`links_json` pattern).

### 2.4 New SSE event types

Reuse the `Event{Type,ScanID,Message,PageURL,Data}` envelope. Add types:
`component-detected`, `page-mapped`, `mapping-complete`, `estimate-ready` (A);
`rule-evaluated`, `conformance-complete`, `diff-ready` (B). Frontend switch extends naturally.

---

## 3. Tool A — Migration Mapping & Estimation

Pipeline: **crawl (reused) → generic component detection → block-mapping classification →
convertibility & flags → effort estimation → report**.

### 3.1 Generic component detector (`internal/migrate/detect.go`)

The crux. Non-EDS HTML has no `main > div.section > div.block` convention, so we detect
semantic regions heuristically and emit a structural signature per region.

**Detection walk** (reusing `walk/classList/attr/nodeText`):
1. Establish the content root: first `<main>`, else `<body>` minus `<header>/<footer>/<nav>` landmarks.
2. Emit **landmark components** directly: `header`→`nav` candidate, `footer`→`footer`,
   top-level `nav`→nav, `aside`→sidebar.
3. Within the content root, segment into **candidate components** by walking direct structural
   children and grouping by semantic boundaries: `<section>`, `<article>`, ARIA `role`,
   heading-led blocks, and *repeated-sibling clusters* (≥3 structurally-similar siblings → a
   collection/grid).
4. For each candidate, compute a **signature** (the feature vector the classifier consumes):

```go
type ComponentInfo struct {
    ID            string         `json:"id"`            // stable per page (path index)
    Tag           string         `json:"tag"`           // section/div/article/...
    Role          string         `json:"role"`          // aria role if present
    ClassHints    []string       `json:"classHints"`    // normalized class tokens (carousel, hero...)
    Depth         int            `json:"depth"`         // nesting depth from content root
    RepeatCount   int            `json:"repeatCount"`   // similar-sibling count (grids/lists)
    ChildCounts   map[string]int `json:"childCounts"`   // img,picture,video,iframe,heading,button,link,p,li,form,input,table,svg,canvas
    HeadingLevels []int          `json:"headingLevels"` // e.g. [1,2,2]
    TextLength    int            `json:"textLength"`
    HasBgImage    bool           `json:"hasBgImage"`    // inline/style background-image
    HasInlineJS   bool           `json:"hasInlineJS"`   // <script> inside / on* handlers
    IsCustomEl    bool           `json:"isCustomEl"`    // hyphenated/web-component tag
    TextSnippet   string         `json:"textSnippet"`   // first ~200 chars (display + matching)
}
```

This intentionally overlaps with the enriched `BlockInfo` from the chatbot plan (`childCounts`,
`textSnippet`, headings) — **build the shared extraction helper once** and use it for both.

**Repeated-sibling detection:** two siblings are "similar" if they share tag + a Jaccard
similarity of class tokens ≥ 0.5 and comparable `childCounts` shape. A cluster of ≥3 is strong
evidence of `cards`/`columns`. This is the single highest-value heuristic — prioritize it.

### 3.2 EDS block catalog & signatures (`internal/migrate/catalog.go`)

A static, versioned catalog of EDS standard blocks (seeded from the aem.live block collection)
plus the structural fingerprints we match against. Each entry:

```go
type BlockSpec struct {
    Name        string   // "cards","columns","hero","carousel","accordion","tabs",
                         // "table","embed","video","quote","form","breadcrumbs",
                         // "header","footer","fragment","search","modal"
    Description string   // human text (also feeds the chatbot block index — shared asset)
    Matchers    []Matcher
    BaseConvert Convertibility // default difficulty before signal adjustments
    DocURL      string   // aem.live reference, shown in the report
}
```

A `Matcher` is a predicate over `ComponentInfo` returning a partial score + matched-signal
labels (for the rationale string). Examples (illustrative weights, calibrated in §3.9):

- **hero**: single component, `HeadingLevels` starts at 1, `img|picture` present or `HasBgImage`,
  `button/link` ≤ 2, near top of page → +0.8.
- **cards**: `RepeatCount ≥ 3`, each child has `img + heading` → +0.9.
- **columns**: 2–4 similar siblings, balanced text, low repeat (not a long list) → +0.7.
- **carousel/slider**: classHints∈{carousel,slider,swiper,slick} or repeated items + nav dots → +0.85.
- **accordion/tabs**: classHints or `role∈{tablist,tab,region}` + toggle markup → +0.8.
- **embed/video**: `iframe`(youtube/vimeo/maps) or `video` present → +0.95.
- **table**: `table` child → +0.95.
- **form**: `form`/`input` present → form block (flag interactivity, §3.4) → +0.6.
- **breadcrumbs**: nav with ordered links + separators near top → +0.7.
- **default content** (fallback): only headings/paragraphs/lists/links/images → not a block;
  maps to EDS *default content* in a section (always "easy").

Keep the catalog data-driven so adding/retuning a block is a table edit, not a code change.

### 3.3 Mapping classifier (`internal/migrate/map.go`)

```go
type Mapping struct {
    ComponentID   string            `json:"componentId"`
    PageURL       string            `json:"pageUrl"`
    BestBlock     string            `json:"bestBlock"`
    Confidence    float64           `json:"confidence"`   // 0..1
    Alternatives  []BlockMatch      `json:"alternatives"` // ranked runners-up
    Convertibility Convertibility   `json:"convertibility"`
    Signals       []string          `json:"signals"`      // human rationale, e.g. ["3 repeated img+heading siblings","class hint: cards"]
    Flags         []ConvertFlag     `json:"flags"`        // blockers/risks (§3.4)
}
```

Algorithm: run every `BlockSpec.Matchers` over the component, sum weighted partial scores,
normalize to 0..1, pick the argmax as `BestBlock`, keep top-N as alternatives. Below a floor
(e.g. < 0.35) → `BestBlock = "custom"`, `Convertibility = custom`. Confidence is the normalized
top score; deliberately **explainable** — `Signals` lists exactly which matchers fired.

### 3.4 Convertibility & blocking flags

`Convertibility` starts at `BlockSpec.BaseConvert` and is downgraded by flags:

```go
type ConvertFlag string
const (
    FlagHeavyJS       ConvertFlag = "heavy-interactivity"  // inline JS / on* handlers / canvas
    FlagWebComponent  ConvertFlag = "custom-element"       // hyphenated tag, shadow DOM likely
    FlagThirdParty    ConvertFlag = "third-party-widget"   // known vendor iframes/scripts
    FlagComplexForm   ConvertFlag = "complex-form"         // multi-step / validation-heavy
    FlagDeepNesting   ConvertFlag = "deep-nesting"         // depth beyond threshold
    FlagNoSemantics   ConvertFlag = "non-semantic-markup"  // div soup, no headings/landmarks
    FlagDynamicData   ConvertFlag = "dynamic-data"         // data-* driven, likely API-backed
)
```

Buckets after adjustment: `easy` (standard block, clear match, no flags), `medium` (clear block
+ minor flag, or default content with styling), `hard` (low confidence or 1 serious flag),
`custom` (no clean equivalent / multiple serious flags → needs a bespoke EDS block).

### 3.5 Effort estimation model (`internal/migrate/estimate.go`)

Transparent, tunable, range-based — never a single fake number.

```go
type EffortWeights struct { // defaults; overridable via config/request
    EasyHours   float64 // 0.5
    MediumHours float64 // 2
    HardHours   float64 // 6
    CustomHours float64 // 16
    PerPageBase float64 // 0.75  content migration per unique page template
    Scaffold    float64 // 8     one-time project setup
    ThemePort   float64 // 16    CSS/design-token port (scaled by unique component variety)
    NavFooter   float64 // 6     header/footer build
}

type Estimate struct {
    TotalHoursLow   float64           `json:"totalHoursLow"`   // confidence-weighted optimistic
    TotalHoursHigh  float64           `json:"totalHoursHigh"`  // pessimistic
    TotalHoursMid   float64           `json:"totalHoursMid"`
    ByBucket        map[string]int    `json:"byBucket"`        // counts: easy/medium/hard/custom
    ByBlock         map[string]int    `json:"byBlock"`         // cards:12, hero:3, custom:4...
    UniqueTemplates int               `json:"uniqueTemplates"` // de-duped page shapes
    ReadinessScore  float64           `json:"readinessScore"`  // 0..100 (share of easy+medium, conf-weighted)
    Assumptions     []string          `json:"assumptions"`     // surfaced, e.g. "source HTML used, not rendered DOM"
    Weights         EffortWeights     `json:"weights"`         // echo back what produced the number
}
```

- **Template de-dup:** many pages share a layout. Cluster pages by component-signature multiset
  so a 500-page site with 6 templates estimates as 6 builds + N content migrations, not 500
  builds. This is what makes the estimate credible at scale.
- **Range:** Low/High derived by weighting each component's hours by its mapping confidence
  (low-confidence components widen the band). Mid = expected value.
- **Readiness score:** `100 * (conf-weighted easy+medium component share)`; the headline metric.
- Always emit `Assumptions` (source-HTML-not-rendered, standard-block catalog version, weights used).

### 3.6 Types & storage (Tool A)

- `internal/migrate/types.go`: `ComponentInfo`, `Mapping`, `BlockMatch`, `Estimate`, `EffortWeights`, enums.
- `PageResult` gains `Components []ComponentInfo` (additive JSON; `page_components` stores it via
  `components_json` column added with `ensureColumn`).
- New tables (created in `init()` alongside existing `CREATE TABLE IF NOT EXISTS`):
  - `migration_mappings(scan_id, page_url, mapping_json)` (one row per page, JSON array).
  - `migration_estimates(scan_id, estimate_json)` (one row per scan).
- New `Store` methods: `SaveComponents`, `SaveMappings`, `SaveEstimate`, `GetMigrationReport`.
  Keep the `Store` interface additive; `SQLiteStore` implements them.

### 3.7 API (Tool A)

Extend the existing mux ([server.go]) — no router change needed:

| Method & path | Body / behaviour |
|---|---|
| `POST /api/scans` | add `"kind":"migration"`. Reuses the start flow; service routes to `GenericComponentAnalyzer` + post-mapping. |
| `GET /api/scans/:id` | when `kind=migration`, `ScanResult` includes `migration` block: `{components-per-page, mappings, estimate}`. |
| `GET /api/scans/:id/migration` | dedicated report payload (mappings + estimate + per-bucket rollups) for the report tab. |
| `POST /api/scans/:id/estimate` | re-run estimate with custom `EffortWeights` (no re-crawl). Mirrors the existing `/audit` re-run pattern. |
| `GET /api/scans/:id/events` | unchanged transport; new event types stream through. |

### 3.8 Frontend (Tool A)

New mode in the scan form: **"Migrate to EDS"** (vs "Audit"). New tabs in the result view:
- **Migration Overview** — readiness gauge, effort range (low–mid–high), bucket donut, top blocks.
- **Component Map** — per page: each detected component, its mapped block, confidence bar,
  rationale chips (`Signals`), flags, and alternatives on expand.
- **Estimate** — editable `EffortWeights` (live re-estimate via `POST /estimate`), template count,
  assumptions panel.
- Add `ComponentInfo/Mapping/Estimate` to `src/types.ts`; add `getMigrationReport`/`reEstimate`
  to `src/api.ts`. Follow the existing component/styling idiom in `App.tsx`.

### 3.9 Calibration & validation (this is what makes it production-grade)

- **Golden corpus:** assemble 15–25 real non-EDS pages (marketing sites, news, docs, e-commerce
  PDPs) with a hand-labeled "correct" mapping per component. Store fixtures under
  `internal/migrate/testdata/`.
- **Metrics:** top-1 mapping accuracy, top-3 accuracy, convertibility-bucket confusion matrix,
  estimate error vs a human estimate on 3–5 sites. Track in a `go test` that prints a scorecard.
- **Tune** matcher weights and the confidence floor against the corpus; commit the calibrated
  catalog. Re-run on every catalog change to prevent regressions.

### 3.10 Tests (Tool A)
- Unit: detector on hand-built fixtures (hero, card grid, carousel, table, form, nav, footer,
  custom-element, div-soup) → asserts signature + flags.
- Unit: classifier on synthetic signatures → asserts `BestBlock`, bucket, signals.
- Unit: estimator → deterministic hours for a fixed mapping set; template de-dup correctness.
- Integration: httptest fixture *non-EDS* site (mirror the existing fixture-site test) →
  full scan → assert report shape + readiness within tolerance.

---

## 4. Tool B — Best-Practices Comparator

Two modes over the **same** rule engine:
- **Audit mode:** one EDS scan vs the canonical best-practice profile → conformance report.
- **Diff mode:** two EDS scans (A vs B, or scan vs another scan-as-reference) → per-rule deltas,
  block-set diff, score regressions/improvements.

Reuses the existing EDS analyzer output (`blocks`, `sections`, `links`, `seo`, `lighthouse`) —
this is the part that "overlaps heavily with what we already built", so Tool B is mostly a new
*scoring layer* over existing data, not new crawling.

### 4.1 Rule engine (`internal/conformance/engine.go`)

```go
type Severity string // "fail" | "warn" | "pass"

type Rule struct {
    ID       string
    Category string  // "structure" | "naming" | "seo" | "media" | "a11y" | "performance"
    Weight   float64
    Eval     func(ctx RuleContext) []Finding // may emit per-page or site-level findings
    DocURL   string
}

type Finding struct {
    RuleID   string   `json:"ruleId"`
    Severity Severity `json:"severity"`
    Scope    string   `json:"scope"`    // "site" or a page URL
    Message  string   `json:"message"`  // specific, e.g. "12 anonymous div blocks on /products"
    Evidence []string `json:"evidence"` // offending block names / page URLs
    Fix      string   `json:"fix"`      // remediation hint
}

type RuleContext struct {
    Result   ScanResult     // full EDS scan (blocks, sections, seo, links, lighthouse, pages)
    Catalog  BlockCatalog   // recognized standard block names (shared with §3.2)
    Weights  CategoryWeights
}
```

Engine: run all rules → collect findings → roll up to category scores (weighted pass rate) →
overall conformance score (0–100, weighted across categories). Reuse/generalize `scoreRollup`
([service.go:688]) into a weighted averager.

### 4.2 Rule catalog (`internal/conformance/rules.go`) — initial set

**Structure**
- `no-anonymous-blocks` — every block has a recognized/declared name (no bare `div`); fail per offender.
- `recognized-block-ratio` — share of blocks in the standard catalog vs custom; warn if custom-heavy.
- `section-metadata-valid` — section metadata keys are known (`style`, etc.); reuse `metadataVariations`.
- `blocks-per-section` — flag sections with excessive block counts (over-stuffed) — heuristic threshold.
- `single-main` / `has-main` — page has exactly one content root.

**Naming**
- `kebab-case-blocks` — block + variation names are kebab-case (EDS convention).
- `variation-sanity` — variations are known modifiers, not accidental classes.

**SEO** (reuse `SEOStats` directly)
- `title/description/h1/canonical/og` presence → map each `Missing*` count to findings.
- `single-h1` — exactly one h1 per page (needs per-page h1 list; cheap to add to analyzer).

**Media**
- `responsive-images` — images delivered via `<picture>`/srcset (EDS auto-optimizes via
  `createOptimizedPicture`); flag raw `<img>` with no srcset. (Detect in analyzer — additive.)
- `alt-coverage` — share of images with non-empty alt (already capturable; aligns with chatbot enrichment `ImageAlts`).

**Accessibility** (reuse Lighthouse a11y + structure)
- `lighthouse-a11y-threshold` — a11y score ≥ target (configurable, default 0.9).
- `heading-order` — no skipped heading levels per page.
- `lang-present` — `<html lang>` set (reuse `PageResult.Lang`).

**Performance** (reuse Lighthouse)
- `lighthouse-perf-threshold`, `lighthouse-seo-threshold`, `lighthouse-bp-threshold` — each
  category ≥ configurable target; findings carry the actual vs target.

Each rule is data-light and independently testable. Thresholds live in `CategoryWeights`/config.

### 4.3 Diff mode (`internal/conformance/diff.go`)

Given reports A and B:
```go
type ConformanceDiff struct {
    OverallDelta   float64                  `json:"overallDelta"`
    CategoryDeltas map[string]float64       `json:"categoryDeltas"`
    BlocksAdded    []string                 `json:"blocksAdded"`    // present in B not A
    BlocksRemoved  []string                 `json:"blocksRemoved"`
    RuleDeltas     []RuleDelta              `json:"ruleDeltas"`     // severity change per rule
    Regressions    []Finding                `json:"regressions"`    // pass→warn/fail in B
    Improvements   []Finding                `json:"improvements"`
}
```
Block-set diff reuses the aggregated `BlockStat` lists. Score deltas come from the two reports.
"Reference profile" = a stored canonical report (e.g. an exemplary EDS site scanned once and
pinned) so a single scan can be diffed against "ideal" without a second live crawl.

### 4.4 Types & storage (Tool B)
- `internal/conformance/types.go`: `Rule`, `Finding`, `ConformanceReport`, `ConformanceDiff`, weights.
- Tables: `conformance_reports(scan_id, report_json)`, optional `reference_profiles(name, report_json)`.
- `Store` additions: `SaveConformanceReport`, `GetConformanceReport`, `SaveReferenceProfile`,
  `ListReferenceProfiles`.

### 4.5 API (Tool B)

| Method & path | Behaviour |
|---|---|
| `POST /api/scans/:id/conformance` | compute (or recompute with custom thresholds) the report for an existing EDS scan. |
| `GET /api/scans/:id/conformance` | fetch the stored report (findings + category scores + overall). |
| `POST /api/compare` | body `{ "scanA": id, "scanB": id }` → `ConformanceDiff`. Either id may name a reference profile. |
| `GET /api/reference-profiles` / `POST /api/reference-profiles` | list / pin a scan as a named reference. |

Compute conformance automatically at the end of an `audit`-kind scan (cheap, all data present) so
the report is ready without an extra call; the POST endpoint allows threshold re-tuning.

### 4.6 Frontend (Tool B)
- **Conformance** tab on any audit scan: overall gauge, category bars, findings table grouped by
  severity with fix hints and evidence links to the offending page/block.
- **Compare** view: pick scan A + scan B (or a reference profile) → side-by-side category bars,
  added/removed blocks, regressions highlighted in red, improvements in green.
- Types/api additions mirror Tool A's.

### 4.7 Tests (Tool B)
- Unit per rule: craft a `ScanResult` fixture that should pass/warn/fail → assert findings.
- Scoring: fixed findings → deterministic category + overall scores.
- Diff: two crafted reports → assert added/removed blocks, regressions, deltas.
- Integration: run audit on the existing EDS fixture site → assert a report is produced and stored.

---

## 5. Cross-cutting production concerns

### 5.1 Security — SSRF (must-fix before any non-localhost deploy)
The server fetches arbitrary user-supplied URLs. In production this is an SSRF vector (internal
metadata endpoints, private ranges). Add a guarded `http.Client` dialer that **rejects
non-public IPs** (loopback, RFC1918, link-local, ULA, `169.254.0.0/16`, `::1`) after DNS
resolution, blocks non-`http(s)` schemes, caps redirects, and enforces per-host rate limits.
Centralize in `internal/scanner` and use it for *all* outbound fetches (audit + migration).
Gate the metadata-IP block behind a config flag so the localhost fixture tests still pass.

### 5.2 Resource limits & abuse control
- Per-scan caps: max pages (existing `CrawlLimit`), max bytes/page (existing 16 MB limit),
  max components/page, overall scan timeout (`context.WithTimeout`).
- Bound concurrent scans server-wide (semaphore) — today scans spawn unbounded goroutines.
- Politeness: per-host crawl delay + `robots.txt` honoring (new, small) — important when pointing
  at third-party sites you don't own.

### 5.3 Config (`internal/config` or env, matching existing `ADDR`/`EDS_ANALYSER_DB`)
- `EDS_EFFORT_WEIGHTS_JSON`, `EDS_CONFORMANCE_THRESHOLDS_JSON` — override defaults without rebuild.
- `EDS_BLOCK_CATALOG_PATH` — externalize the catalog so it's updatable as aem.live evolves.
- `EDS_ALLOW_PRIVATE_IPS` (default false) — the SSRF escape hatch for local testing.

### 5.4 Known accuracy boundary (state honestly in the UI)
Source HTML is canonical (matching the existing tool). Heavily client-rendered (SPA) non-EDS
sites will under-detect components. Surface this as a first-class assumption in the estimate and,
optionally, add a *later* opt-in headless-render path (Chrome is already a dependency for
Lighthouse) behind a flag — not in v1.

### 5.5 Observability
- Structured logs per scan phase + per-rule timing.
- A `GET /api/scans/:id` already exposes status/phase; add counters (components detected, rules
  evaluated) to the summary for ops visibility.

### 5.6 Migrations & back-compat
All schema changes go through `ensureColumn` / `CREATE TABLE IF NOT EXISTS`. Existing audit scans
keep working; `kind` defaults to `audit`. No destructive migrations.

---

## 6. Phased delivery (each phase is demoable and shippable)

| Phase | Deliverable | Acceptance |
|---|---|---|
| **0. Foundations** | `ScanKind`, `PageAnalyzer` interface, analyzer injection, SSRF-safe client, new event types, new tables via `ensureColumn`. Audit path unchanged. | All existing Go + Vitest tests still green; audit scan behaves identically. |
| **1. Detector (A)** | `GenericComponentAnalyzer` + `ComponentInfo`, repeated-sibling clustering, signatures persisted; raw "Component Map" tab (no mapping yet). | Fixtures detect hero/cards/carousel/table/form/nav/footer/custom-el correctly. |
| **2. Mapping (A)** | Block catalog + classifier + convertibility/flags; mappings persisted + rendered with rationale. | Top-1 ≥ target on golden corpus (§3.9); every mapping shows ≥1 signal. |
| **3. Estimate (A)** | Effort model + template de-dup + readiness score + editable weights; `POST /estimate`. | Deterministic hours on fixtures; re-estimate with custom weights works live. |
| **4. Conformance (B)** | Rule engine + full rule catalog + scoring; auto-run on audit scans; Conformance tab. | Each rule unit-tested pass/warn/fail; report stored + rendered. |
| **5. Compare (B)** | Diff engine + reference profiles + Compare view. | A/B diff shows added/removed blocks, regressions, category deltas on fixtures. |
| **6. Hardening** | Calibration scorecard CI, rate limits/robots, concurrent-scan cap, docs, README update. | Scorecard test gates catalog changes; load test of N concurrent scans is bounded. |

Phases 1–3 (Tool A) deliver the differentiated, fundable demo first; 4–5 (Tool B) reuse the most
existing code and can run partly in parallel.

---

## 7. Risks & mitigations

| Risk | Mitigation |
|---|---|
| Component detection accuracy on messy real-world HTML | Golden corpus + calibration CI (§3.9); explainable signals so failures are debuggable, not opaque. |
| Effort estimate seen as authoritative | Always range + assumptions + editable weights; label as "planning estimate". |
| SPA sites under-detected | State the source-HTML boundary in UI; optional headless path later. |
| SSRF / crawling third-party sites | Private-IP-blocking client, robots.txt, rate limits (§5.1–5.2) before any shared deploy. |
| Catalog drift as aem.live evolves | Externalized, versioned catalog file; one place to update. |
| Scope creep into codegen | Explicit v1 non-goal; mapping+estimate only. |
| Two new engines inflate the codebase | New packages (`internal/migrate`, `internal/conformance`) isolate them; shared HTML helpers reused, not copied. |

---

## 8. Open decisions (need a call before Phase 1)

1. **Mapping engine: rules-only v1, or rules + the planned embeddings?** Recommendation:
   ship rules-only (deterministic, testable, explainable); add embedding-assisted matching as a
   Phase-2.5 enhancer reusing the chatbot block index, not a dependency.
2. **Standard-block catalog source of truth** — pin a specific aem.live block-collection snapshot
   and version it.
3. **Reference profile for Tool B** — do we ship a pre-scanned exemplary EDS site as the default
   "ideal", or require the user to pin one?
4. **Headless rendering** — accept the source-HTML limitation for v1 (recommended), or invest in
   the Chrome-render path early?
5. **Effort weight defaults** — calibrate against 3–5 real internal migration estimates before
   committing the numbers in §3.5.

---

## Appendix — file/change map

**New packages**
- `internal/migrate/{detect,catalog,map,estimate,types}.go` (+ `testdata/`)
- `internal/conformance/{engine,rules,diff,types}.go` (+ `testdata/`)

**Modified (additive)**
- `internal/scanner/types.go` — `ScanKind`, `ScanSummary.Kind`, `PageResult.Components`.
- `internal/scanner/service.go` — `PageAnalyzer` interface + injection; route by kind; new events;
  concurrent-scan cap.
- `internal/scanner/analyze.go` — extract shared `ComponentInfo`-style helper (childCounts,
  headings, snippet, image alts, responsive-image detection); single-h1/heading-order data.
- `internal/scanner/storage.go` — new tables in `init()`; `ensureColumn` for `kind`,
  `components_json`; new `Store` methods.
- `internal/scanner/lighthouse.go` — unchanged (reused).
- `internal/api/server.go` — new routes (§3.7, §4.5).
- `src/types.ts`, `src/api.ts`, `src/App.tsx`, `src/styles.css` — new types, calls, tabs/views.
- `README.md` — document the two modes, SSRF config, limitations.
