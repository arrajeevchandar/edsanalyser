# EDS Author Chatbot — RAG Architecture Design

> Status: Draft for review · Target: `edsanalyser` · Author-facing assistant that answers
> questions about **blocks, variations, EDS concepts, and the author's own scanned site.**

## 1. Goal

Give an author a chat panel where they can ask things like:

- *"What is a block variation and how do I create one?"* → general EDS knowledge
- *"Why is my hero block hurting performance?"* → EDS knowledge + this site's data
- *"Which of my pages use the `columns (highlight)` variant?"* → this site's data only

These are **two different question types** and the system answers each with the right
mechanism instead of forcing everything through one path.

| Question type | Source of truth | Mechanism |
|---|---|---|
| EDS concepts (blocks, variations, best practice) | Curated EDS docs | **RAG** (semantic retrieval) |
| The author's scanned site | Existing SQLite scan data | **Tool-calling** (exact queries) |

The result is a **hybrid RAG + tool-calling** assistant. Pure RAG alone would give fuzzy,
hallucination-prone answers for the site half, where authors expect exact counts.

---

## 2. Chosen stack & the constraints it creates

| Concern | Decision | Note |
|---|---|---|
| Generation LLM | **Groq** (OpenAI-compatible `/chat/completions`) | Fast. Use a tool-calling-capable model, e.g. `llama-3.3-70b-versatile`. |
| Embeddings | **NOT Groq** — Groq has no embeddings endpoint | Must add a separate embedder. See §3. |
| Vector storage | **Embeddings as BLOBs in existing SQLite + brute-force cosine in Go** | Keeps the build pure-Go (CGo-free), matching `modernc.org/sqlite`. |
| Backend | New `internal/chat` + `internal/knowledge` packages, new endpoint in `internal/api` | Reuses existing `Store` and SSE-style event patterns. |
| Frontend | Chat panel in `src/App.tsx`, scoped to the active scan | "this site" is unambiguous. |

### Why not `sqlite-vec` / pgvector / Qdrant?

`sqlite-vec` is a **native C extension** and won't load into the pure-Go
`modernc.org/sqlite` driver you use today. Switching to a CGo driver (`mattn/go-sqlite3`)
or standing up an external vector DB both add real weight. **At medium corpus size,
brute-force cosine similarity in Go is fast enough** (a few thousand 768-dim vectors scan
in single-digit milliseconds), so we keep the clean pure-Go build and revisit only if the
corpus grows large (then: a pure-Go HNSW index, or an external vector DB).

---

## 3. The embeddings decision (must pick one)

Groq cannot embed. Options, best-fit first for a self-hosted desktop tool:

1. **Ollama + `nomic-embed-text` (local, free, 768-dim)** — runs alongside the app, no API
   cost, no data leaves the machine for indexing. Recommended default.
2. **Hosted embeddings API** (Voyage, Jina, OpenAI `text-embedding-3-small`) — zero local
   setup, small per-call cost, sends doc/query text to a third party.
3. **Pure-Go embedding** (ONNX via `onnxruntime-go`, or `fastembed`-style) — fully
   self-contained binary, more upfront wiring.

> The embedder choice only affects the `Embed(text) []float32` function behind an
> interface — everything else is independent of it.

---

## 4. Components

```
                         ┌─────────────────────────────────────────────┐
                         │                 React (App.tsx)              │
                         │   ChatPanel (scoped to active scanID)        │
                         └───────────────┬─────────────────────────────┘
                                         │  POST /api/chat  (SSE stream)
                         ┌───────────────▼─────────────────────────────┐
                         │            internal/api/server.go            │
                         │              chatHandler                     │
                         └───────────────┬─────────────────────────────┘
                                         │
                ┌────────────────────────▼────────────────────────┐
                │                internal/chat                     │
                │  Orchestrator:                                   │
                │   1. embed question                              │
                │   2. retrieve doc chunks (knowledge)             │
                │   3. call Groq w/ tools + retrieved context      │
                │   4. run any tool calls against Store            │
                │   5. stream grounded, cited answer               │
                └───┬───────────────────┬───────────────────┬─────┘
                    │                   │                   │
        ┌───────────▼──────┐  ┌─────────▼────────┐  ┌───────▼─────────┐
        │ internal/        │  │ internal/scanner │  │ Groq client     │
        │ knowledge        │  │ Store (existing) │  │ (chat + tools)  │
        │ (RAG retrieval)  │  │ block/page/SEO   │  │                 │
        │ + Embedder       │  │ /lighthouse data │  │                 │
        └──────────────────┘  └──────────────────┘  └─────────────────┘
                    │
            ┌───────▼────────┐
            │ SQLite          │
            │ knowledge_chunks│  ← content + embedding BLOB
            └─────────────────┘
```

---

## 5. Indexing pipeline (offline, run when docs change)

A new command, e.g. `cmd/indexer`, or a `--reindex` server flag.

```
EDS docs (aem.live block collection, variation guides,
          your internal best-practice notes, markdown/HTML)
   │
   ├─ 1. Load & clean   → plain text, keep heading hierarchy & source URL
   ├─ 2. Chunk          → ~300–800 tokens, ~15% overlap, split on headings
   ├─ 3. Embed          → Embedder.Embed(chunk) → []float32 (768-dim)
   └─ 4. Store          → INSERT into knowledge_chunks
```

**Source candidates for the corpus:**
- aem.live block collection & component docs
- EDS block/section/variation authoring guides
- Your own internal "house style" / best-practice notes (high value — unique to you)

---

## 6. Query pipeline (per question)

```
POST /api/chat { scanID, message, history[] }
   │
   1. Embed the question
   2. knowledge.Retrieve(queryVec, k=6)      → top doc chunks (+ source URLs)
   3. Build prompt:
        - system: role, rules (cite docs, use tools for site facts, never invent
                  block names), the active scanID
        - retrieved doc chunks (as context, each tagged with its source)
        - tool definitions (see §7)
        - conversation history + new message
   4. Call Groq /chat/completions (stream=true, tools=[...])
   5. If the model emits tool_calls:
        run them against Store → append results → call Groq again (loop, capped)
   6. Stream final answer tokens to the client, with doc citations
```

The loop in step 5 is the standard tool-use cycle; cap iterations (e.g. 4) to bound cost.

---

## 7. Tools exposed to the model (the "this site" half)

Thin wrappers over functions that already exist in `storage.go` / `aggregate`:

| Tool | Args | Returns | Backed by |
|---|---|---|---|
| `get_scan_summary` | scanID | site URL, page count, phase, scores | `GetScan` |
| `list_blocks` | scanID | block names, counts, variations | `aggregate` block stats |
| `get_block_detail` | scanID, blockName | per-page usage, variants, sections | block stats |
| `list_pages` | scanID, filter? | matching page URLs + status | `GetScan` pages |
| `get_page_detail` | scanID, url | blocks, SEO, lighthouse for one page | page result |
| `get_seo_gaps` | scanID | missing titles/descriptions/h1, etc. | SEO stats |
| `get_lighthouse` | scanID, url? | perf/SEO/a11y scores | lighthouse data |

The model decides which to call. Because answers come from real rows, "how many hero
blocks?" is **exact**, not a similarity guess.

---

## 8. Data model additions (SQLite)

```sql
CREATE TABLE IF NOT EXISTS knowledge_chunks (
    id          INTEGER PRIMARY KEY,
    source_url  TEXT NOT NULL,
    title       TEXT,
    heading     TEXT,
    content     TEXT NOT NULL,
    token_count INTEGER,
    embedding   BLOB NOT NULL,          -- []float32 packed little-endian
    indexed_at  TEXT NOT NULL
);

-- optional: persist chat per scan
CREATE TABLE IF NOT EXISTS chat_messages (
    id        INTEGER PRIMARY KEY,
    scan_id   TEXT NOT NULL,
    role      TEXT NOT NULL,            -- user | assistant
    content   TEXT NOT NULL,
    created_at TEXT NOT NULL
);
```

Retrieval = load candidate embeddings, compute cosine in Go, take top-k. (Optionally
pre-filter by a cheap keyword `LIKE` to shrink the candidate set first.)

---

## 9. Key risks & honest call-outs

1. **Embeddings are a required, separate decision** (§3). Nothing works until this is picked.
2. **Privacy/data flow** — with hosted Groq, the prompt (including the author's site facts
   pulled by tools) is sent to Groq. Fine for most cases; call it out if the data is
   sensitive. Local embeddings keep at least the *indexing* on-machine.
3. **Hallucinated block/variant names** — mitigated by: tool-calling for all site facts, a
   strict system prompt ("only name blocks returned by tools"), and doc citations.
4. **Doc freshness** — RAG answers are only as current as the last index run; make
   re-indexing easy and show the index date.
5. **Brute-force ceiling** — fine for medium corpora; if chunks reach tens of thousands,
   add a pure-Go ANN index or external vector DB (interface already isolates this).
6. **Token budget** — cap retrieved chunks (k≈6) and tool-result sizes so prompts stay lean
   and fast on Groq.

---

## 10. Suggested build order (incremental, each step demoable)

1. **Tool-calling only, no RAG** — chat endpoint + Groq + the `Store` tools. Answers
   "this site" questions end-to-end. Proves the loop with the least moving parts.
2. **Add the knowledge package** — `Embedder` interface + brute-force `Retrieve`, seeded
   with a handful of EDS docs. Wire retrieved context into the prompt.
3. **Add the indexer** — `cmd/indexer` to chunk + embed the full doc corpus.
4. **Frontend chat panel** — scoped to active scan, SSE streaming, render citations.
5. **Polish** — persist chat history, show index date, cap costs, add tests for chunking,
   cosine, and tool dispatch.

---

## 11. Open questions for you

- **Embedder**: Ollama `nomic-embed-text` (local, recommended) vs a hosted API?
- **Corpus sources**: which exact doc sets, and do you have internal best-practice notes to
  include (highest-value content)?
- **Chat persistence**: keep history per scan, or stateless per session to start?
- **Streaming**: reuse the existing SSE/event pattern from the scanner, or simple JSON first?
```
