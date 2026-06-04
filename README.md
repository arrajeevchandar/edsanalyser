# EDS Analyser

A dashboard for crawling and auditing **Adobe Edge Delivery Services (EDS)** sites. Point it at any public URL and it discovers the site's pages, parses each page's EDS structure (sections, blocks, variations), extracts SEO/Open-Graph metadata and link inventory, and runs **real Lighthouse audits** — then rolls everything up into an interactive dashboard with live progress and per-scan history.

The backend is **Go** (crawling, parsing, orchestration, persistence, API). The frontend is a **Vite + React + TypeScript** single-page app. Lighthouse runs as a worker step via the official Lighthouse CLI, because Lighthouse itself is a Node/Chrome tool — Go owns everything else.

---

## Features

- **Automatic page discovery** — `sitemap.xml` → `sitemap.json` → sitemap-index expansion → `/query-index.json` → same-origin link fallback.
- **EDS structure analysis from source HTML** — counts sections (direct `div` children of `main`) and blocks (direct `div` children of each section, excluding section metadata), with block names and variations.
- **SEO / Open-Graph extraction** — title, `h1`, canonical, meta description, robots, language, and the full `og:*` set per page, with site-wide "missing field" rollups.
- **Link inventory** — every link classified as internal / external / asset / mail / tel / hash, with unique counts and per-link metadata.
- **Real Lighthouse scores** — Performance, Accessibility, Best Practices, SEO, plus an averaged Health score, per page and averaged site-wide.
- **Live progress** over Server-Sent Events: discovery, per-page analysis, audit start/complete, errors, cancellation.
- **Resilient** — pages that fail to fetch or fail Lighthouse still appear in results with their error recorded; the scan completes regardless.
- **Local history** — every scan is persisted to SQLite and browsable from the History tab.

---

## Architecture

```
┌──────────────────────────────────────────────────────────────────────┐
│  Browser  —  Vite + React + TypeScript SPA (src/)                      │
│  Tabs: Overview · Pages · Blocks · Links · SEO/OG · History            │
│  Live progress via EventSource (SSE);  full results via fetch          │
└───────────────┬───────────────────────────────┬──────────────────────┘
                │ REST (JSON)                    │ SSE (text/event-stream)
                ▼                                 ▼
┌──────────────────────────────────────────────────────────────────────┐
│  Go HTTP server  (cmd/server, internal/api)                            │
│   /api/scans  ·  /api/scans/:id  ·  /api/scans/:id/events  ·  /cancel  │
│   also serves the built SPA from dist/                                 │
└───────────────┬───────────────────────────────────────────────────────┘
                ▼
┌──────────────────────────────────────────────────────────────────────┐
│  Scanner service  (internal/scanner)                                   │
│                                                                        │
│   Discoverer ──► worker pool (net/http + x/net/html) ──► Analyzer      │
│        │                  │                                  │         │
│   sitemap/json/      fetch + parse                    blocks/sections, │
│   query-index/       each page                        SEO/OG, links    │
│   link fallback           │                                  │         │
│                           ▼                                  ▼         │
│                   "fast report" ──► Lighthouse audits ──► rollups      │
│                           │            (npx lighthouse CLI)            │
│                           ▼                                            │
│                   SQLite store (modernc.org/sqlite, pure-Go)          │
│                   .data/eds-analyser.sqlite                           │
└──────────────────────────────────────────────────────────────────────┘
```

**Two-phase scan.** The service first produces a *fast report* (crawl + structural/SEO analysis of every discovered page) and emits `fast-complete`, so the dashboard is useful within seconds. It then runs the slower Lighthouse audits on a selected subset and streams scores as they arrive.

### Tech stack

| Layer        | Technology |
|--------------|------------|
| Frontend     | React 18, TypeScript, Vite 6, lucide-react |
| Backend      | Go 1.23+, standard library `net/http` + `context` |
| HTML parsing | `golang.org/x/net/html` |
| Database     | SQLite via `modernc.org/sqlite` (pure Go — no CGO) |
| Auditing     | Lighthouse CLI (`npx lighthouse`) + headless Chrome |
| Tests        | Go `testing`; Vitest + Testing Library |

### Project structure

```
edsanalyser/
├── cmd/server/main.go          # entrypoint: wires store + service + API, serves :8787
├── internal/
│   ├── api/server.go           # HTTP routes, SSE, CORS, static file serving
│   └── scanner/
│       ├── service.go          # scan orchestration, worker pool, SSE pub/sub, rollups
│       ├── discover.go         # sitemap / json / query-index / link discovery
│       ├── analyze.go          # EDS block/section + SEO/OG + link extraction from HTML
│       ├── lighthouse.go       # Lighthouse CLI runner + score parsing
│       ├── normalize.go        # page/result normalization
│       ├── url.go              # URL normalization & same-origin / classification helpers
│       ├── storage.go          # SQLite schema + persistence
│       └── types.go            # shared domain types
├── src/                        # React dashboard (App.tsx, api.ts, types.ts, styles.css)
├── index.html                  # Vite entry
├── go.mod / go.sum
└── package.json
```

---

## Prerequisites

- **Go 1.23+**
- **Node.js 18+** and npm
- **Google Chrome** (Lighthouse drives headless Chrome). Lighthouse itself is installed as an npm dependency.

> **Windows note:** if `go` is not on your `PATH` but Go is installed at the default location, prepend it for the session:
> ```powershell
> $env:Path = "C:\Program Files\Go\bin;" + $env:Path
> ```

---

## Getting started

```bash
# 1. Install frontend + Lighthouse dependencies
npm install

# 2. Build the dashboard (Go serves it from dist/)
npm run build

# 3. Run the Go server (serves API + dashboard on http://localhost:8787)
npm run server          # == go run ./cmd/server
```

Open **http://localhost:8787**, paste a public EDS URL, and start a scan.

### Development mode (hot reload)

Run the API and the Vite dev server separately. Vite proxies `/api` to the Go server, so the SPA talks to the real backend with hot module reload:

```bash
# Terminal 1 — backend
npm run server          # http://localhost:8787

# Terminal 2 — frontend dev server
npm run dev             # http://localhost:5173  (proxies /api → :8787)
```

### Configuration

| Env var            | Default                          | Purpose                          |
|--------------------|----------------------------------|----------------------------------|
| `ADDR`             | `:8787`                          | Server listen address            |
| `PORT`             | `8787`                           | Server port when `ADDR` is unset |
| `EDS_ANALYSER_DB`  | `.data/eds-analyser.sqlite`      | SQLite database path             |
| `VITE_API_URL`     | same origin                      | Backend URL baked into the frontend build |

---

## Deploying

The repository includes `Dockerfile` and `render.yaml` for the Go backend, plus
`vercel.json` for the Vite frontend.

### Backend on Render

1. Push the repository to GitHub.
2. In Render, choose **New > Blueprint** and connect the repository.
3. Render reads `render.yaml`, builds the Docker image, and exposes the backend
   as a web service. The image includes Node, Lighthouse, and Chromium.
4. After deployment, verify:

   ```text
   https://YOUR-SERVICE.onrender.com/api/health
   ```

The blueprint attaches a persistent disk at `/app/.data` so SQLite scan history
and visual-diff files survive restarts. Render persistent disks require a paid
web service. For a temporary free deployment, remove the `disk` block from
`render.yaml` and set `EDS_ANALYSER_DB` to `/tmp/eds-analyser.sqlite`; history
will then be lost whenever the service restarts or redeploys.

### Frontend on Vercel

1. In Vercel, import the same GitHub repository as a new project.
2. Keep the root directory as the repository root. Vercel will use:
   - Build command: `npm run build`
   - Output directory: `dist`
3. Add this environment variable for Production and Preview:

   ```text
   VITE_API_URL=https://YOUR-SERVICE.onrender.com
   ```

4. Deploy the project. If `VITE_API_URL` changes later, redeploy the frontend
   because Vite embeds it at build time.

---

## API reference

| Method & path                  | Description |
|--------------------------------|-------------|
| `GET  /api/health`             | Liveness check → `{"status":"ok"}` |
| `POST /api/scans`              | Start a scan. Body: `{ "url": string, "auditLimit": number\|null, "lighthouseMode": "top"\|"none", "lighthouseLimit": number }`. Returns the created scan summary. |
| `GET  /api/scans`              | Recent scan history with aggregate scores. |
| `GET  /api/scans/:id`          | Full result: summary, per-page data, block/section stats, link stats, SEO/OG rollups. |
| `GET  /api/scans/:id/events`   | SSE stream of progress events (`start`, `discovered`, `page-analyzed`, `fast-complete`, `audit-start`, `audit-complete`, `audit-error`, `complete`, `cancel`). |
| `POST /api/scans/:id/cancel`   | Cancel discovery/auditing (via Go `context` cancellation). |

**Scan options**
- `auditLimit` — cap on how many pages to crawl. `null` (default) crawls all discovered pages.
- `lighthouseMode` — `"top"` audits the first `lighthouseLimit` pages; `"none"` skips Lighthouse entirely (fast, deterministic — handy for structural-only runs).
- `lighthouseLimit` — number of pages to audit in `top` mode (default 5).

**Example**

```bash
curl -X POST http://localhost:8787/api/scans \
  -H "Content-Type: application/json" \
  -d '{"url":"https://www.example-eds-site.com","lighthouseMode":"top","lighthouseLimit":5}'
```

---

## How scanning works

1. **Discovery** — try `sitemap.xml`, `sitemap.json`, then `/query-index.json`; expand any sitemap-index references; if nothing is found, fall back to same-origin links on the entered page. Crawl expansion stays same-origin and excludes non-page assets (images, PDFs, video, fonts, CSS/JS, fragments, `mailto:`, `tel:`).
2. **Fast analysis** — a worker pool fetches each page and parses the **source HTML** as the canonical EDS structure: sections, blocks + variations, title/`h1`/canonical/description/robots/lang, the `og:*` set, and the full link list. Internal links discovered here are enqueued for crawling. A `fast-complete` event fires once all pages are analysed.
3. **Lighthouse audits** — selected pages are run through `npx lighthouse … --output=json --headless`. Category scores (Performance, Accessibility, Best Practices, SEO) are parsed and averaged into a Health score; site scores are the average across audited pages.
4. **Persistence & streaming** — every update is written to SQLite and published to SSE subscribers, so the dashboard reflects progress live and history survives restarts.

---

## Testing

```bash
# Go unit + integration tests (URL normalization, sitemap/json/query-index parsing,
# EDS block/section counting, SEO/OG extraction, link classification, fixture-site
# discovery, cancellation, failed-page & Lighthouse-error persistence)
go test ./...

# Frontend tests (scan form, dashboard rendering, missing-OG handling)
npm test

# Type-check + production build
npm run build
```

---

## Notes & limitations

- **Public sites only.** Authenticated crawling is out of scope for this version.
- **Source HTML is canonical** for EDS block/section counts (not the client-rendered DOM).
- **Lighthouse needs Chrome.** On Windows, chrome-launcher can exit non-zero with `EPERM` while deleting its temporary Chrome profile *after* a successful audit (a known flaky cleanup behaviour, more frequent on OneDrive-synced paths). The runner handles this: if Lighthouse exits non-zero but a complete JSON report was written to stdout, the scores are recovered instead of failing the audit.
- `.data/` (SQLite + Lighthouse temp) and `dist/` (built SPA) are git-ignored.
