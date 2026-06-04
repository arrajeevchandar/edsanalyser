# EDS Author Chatbot — Implementation Plan

> Status: **Plan for review — no code yet.** Supersedes `chatbot-rag-design.md`.
> Modeled on the existing "Zenith" agentic-RAG backend (FastAPI + Groq), adapted for EDS.

## 1. What the chatbot must do

1. **Answer EDS-related questions only** — refuse anything off-topic (guardrails).
2. **Answer from all scraped & stored data** — everything edsanalyser has crawled: scans,
   pages, blocks, variations, sections, links, SEO, Lighthouse.
3. **Describe → fetch a block** — author describes a block's look & feel in natural language
   ("a big banner with a heading and a button over a dark image") and the bot identifies and
   returns the matching block(s) with their details.

## 2. Reference architecture (Zenith) we are mirroring

| Layer | Zenith |
|---|---|
| Server | FastAPI, `POST /api/agent/chat`, startup indexing, `/api/admin/reindex` |
| LLM | Groq `llama-3.3-70b-versatile` via httpx (OpenAI-compatible) |
| Vector store | TF-IDF + cosine (scikit-learn), pickled to disk, synonym query-expansion |
| Knowledge | JSON files → `{type,title,content,category,source}` → chunk (~1200 chars/200 overlap) |
| Agent loop | Prompt-based tool calls (` ```tool_call ` JSON), parse → execute → feed back, max 3 iters |
| Guardrails | Regex intent classifier (greeting/out-of-scope/in-scope) + response leak validator |

## 3. Key decisions (locked)

| Decision | Choice | Rationale |
|---|---|---|
| **Integration** | **Separate Python service** adapted from Zenith | Reuse working code; Python reads the Go-written SQLite natively; embeddings are trivial in Python; decoupled evolution |
| **Retrieval** | **Hybrid by role** — structured tools for exact facts, **embeddings** for describe→fetch, TF-IDF for EDS concept docs | Each need has different characteristics; embeddings are required for fuzzy block matching, exact counts must not be similarity-ranked |
| **Block enrichment** | **Extend the Go scanner now** to capture look-and-feel per block | Without descriptive block data, describe→fetch has nothing to match against |
| **Embedding model** | `all-MiniLM-L6-v2` (sentence-transformers, local, 384-dim, ~80 MB) | Free, offline, already named in Zenith's config |

## 4. The critical gap & the scanner change

**Today** ([types.go](../internal/scanner/types.go), [analyze.go](../internal/scanner/analyze.go)):
`BlockInfo { Name, Variations, SectionIndex }` — only the CSS class name. Nothing about
look & feel. Describe→fetch is impossible against this.

**Change** — enrich block capture in `extractEDS` (analyze.go). For each block subtree,
collect a descriptive bundle:

```go
type BlockInfo struct {
    Name         string            `json:"name"`
    Variations   []string          `json:"variations"`
    SectionIndex int               `json:"sectionIndex"`
    // NEW — look & feel
    TextSnippet  string            `json:"textSnippet"`  // first ~280 chars of visible text
    Headings     []string          `json:"headings"`     // h1–h6 text inside the block
    ImageAlts    []string          `json:"imageAlts"`    // alt text of <img> inside
    ChildCounts  map[string]int    `json:"childCounts"`  // {img,heading,button,link,p,list,...}
    Rows         int               `json:"rows"`         // EDS blocks are table-like div grids
}
```

`BlockStat` gains a representative description (built by aggregating the above across
occurrences) used as the embedding text for describe→fetch.

- Storage: persist new fields in [storage.go](../internal/scanner/storage.go) (additive
  columns via the existing `ensureColumn` migration helper — no breaking change).
- Tests: extend `analyze_test.go` with a fixture block asserting the new fields.

## 5. Python service layout (`eds-chat/`)

Adapted 1:1 from Zenith, with EDS-specific swaps:

```
eds-chat/
  main.py                      # FastAPI: /api/chat, /api/admin/reindex, /health
  app/core/config.py           # Groq + paths + EDS data source
  app/services/
    eds_data.py                # NEW: load scans/pages/blocks/etc. from edsanalyser
    knowledge_service.py       # normalize → {type,title,content,category} → chunk → index
    vector_store.py            # TF-IDF index (concepts)  +  embedding index (blocks)
    block_index.py             # NEW: embedding store for block descriptions
    llm_client.py              # Groq client (reused as-is)
  app/agent/
    brain.py                   # agentic loop (reused; EDS system prompt)
    tools.py                   # search_knowledge, find_block, + structured data tools
    guardrails.py              # EDS in/out-of-scope + leak validation
```

### Data source (`eds_data.py`)

How Python gets edsanalyser's data — **prefer consuming the existing Go HTTP API**
([server.go](../internal/api/server.go)) which already returns `ScanResult` JSON. Benefits:
reuses the existing contract, no SQLite schema coupling, always current. (Fallback: open the
SQLite file read-only in WAL mode.) Index is (re)built per scan completion or via `/reindex`.

## 6. Retrieval design (hybrid)

```
Author question
   │
   ├─ exact data?  ("how many X", "which pages…")  → STRUCTURED TOOLS → API/SQL → exact answer
   ├─ describe a block?  ("banner with a button…") → EMBEDDING SEARCH over block descriptions
   └─ EDS concept?  ("what is a variation?")        → TF-IDF over EDS docs/knowledge
```

The LLM picks the tool. Block descriptions for the embedding index are built from the
enriched scanner fields (§4) plus canonical aem.live block-collection descriptions for
standard blocks (helps map vague language to standard names).

## 7. Tools exposed to the agent

| Tool | Purpose | Backed by |
|---|---|---|
| `find_block` | **Describe→fetch.** Embedding search over block descriptions; returns matching block(s) + details (name, variations, pages, look-and-feel) | block embedding index |
| `search_knowledge` | EDS concept retrieval (variations, blocks, best practice) | TF-IDF doc index |
| `get_scan_summary` | Site URL, page count, scores | Go API |
| `list_blocks` | All blocks on a scan + counts + variations | Go API |
| `get_block_detail` | One block: usage, variants, pages, look-and-feel | Go API |
| `list_pages` / `get_page_detail` | Page inventory & per-page facts | Go API |
| `get_seo_gaps` | Missing titles/descriptions/h1/canonical/OG | Go API |
| `get_lighthouse` | Perf/SEO/a11y/best-practice scores | Go API |

## 8. Guardrails (EDS scope)

Retune Zenith's `guardrails.py`:
- **In-scope keywords**: block, variation, section, hero, columns, cards, EDS, Edge Delivery,
  AEM, lighthouse, performance, SEO, page, crawl, link, the scanned site's domain, etc.
- **Out-of-scope**: same generic refusals (coding help, trivia, recipes, homework…).
- **Leak validator**: strip "knowledge base / database / vector store / embedding" mentions
  so answers read naturally.
- **System prompt** rewritten for an EDS assistant persona: never invent block names, only
  report blocks returned by tools, cite EDS docs when explaining concepts.

## 9. Frontend

Chat panel in [App.tsx](../src/App.tsx), scoped to the active scan so "this site" is
unambiguous. Calls the Python service (directly, or proxied through the Go server to keep one
origin). Renders the matched block(s) for describe→fetch with a jump-to-block-detail link.

## 10. Build order (each step demoable)

1. **Go scanner enrichment** (§4) — new BlockInfo fields, storage migration, tests. Produces
   the data describe→fetch needs.
2. **Python skeleton from Zenith** — `eds_data.py` reading the Go API, TF-IDF concept search,
   structured data tools, EDS guardrails + prompt. → answers data & concept questions.
3. **Block embedding index + `find_block`** — sentence-transformers over enriched block
   descriptions. → describe→fetch works.
4. **Frontend chat panel** — scoped to scan, render block matches.
5. **Polish** — reindex on scan completion, optional chat persistence, tests for guardrails /
   block matching / tool dispatch.

## 11. Risks & mitigations

| Risk | Mitigation |
|---|---|
| Go↔Python contract coupling | Consume the existing Go JSON API, not raw SQLite schema |
| Describe→fetch quality depends on enrichment richness | Capture structural + textual signals (§4); seed standard blocks from aem.live |
| Embeddings too literal vs. exact-fact questions answered by similarity | Route exact questions to structured tools, never to vector search |
| Index staleness as scans change | Rebuild index on scan completion + `/reindex` admin route |
| Two runtimes to run/deploy | Acceptable for internal/desktop; document a single start script |
| Block data may include page text → privacy | Snippets only; note that prompts go to hosted Groq |

## 12. Open questions

- **Embedding scope**: also embed pages/sections, or blocks only to start?
- **EDS doc corpus**: which aem.live sections to seed for concept Q&A + standard-block descriptions?
- **Hosting**: run Python alongside the Go binary locally, or as a separate deployed service?
- **Chat persistence**: stateless per session first, or store history per scan?
