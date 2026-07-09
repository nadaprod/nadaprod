# NADAPROD v7 alpha

Visual block-based Tailwind page editor, powered by Gemini Flash (latest) and an **ultra-specialized generator per block type**: each block (header, hero, features, pricing, testimonials, FAQ, CTA, footer) has its own expert system prompt, with a strict output contract (a single responsive, accessible Tailwind HTML fragment, no lorem ipsum).

Three surfaces share one server:

1. **Public landing** (`/`) — the NADAPROD marketing site, fully static.
2. **Block studio** (`/editor`) — build a page from AI-generated blocks. Project state lives in the browser's `localStorage`; the server is stateless for this flow.
3. **Imported sites** (`/sites`) — upload a static site, edit any page visually (click-to-select, AI element retouch) or in a full-page Monaco code editor, and point a domain at it. The filesystem is the database.

Note: the product UI and the generation prompts are in French.

## Architecture

```
nadaprod/
├── main.go       # gin-gonic server: static + API
├── gemini.go     # Gemini REST client (generateContent) + HTML cleanup
├── prompts.go    # Registry of specialized generators (1 expert prompt / block)
├── sites.go      # Imported sites: upload, edit, domain routing (filesystem-backed)
├── go.mod
└── web/
    ├── home.html   # Public landing page
    ├── index.html  # Studio: block palette, live preview, AI + code panel
    └── sites.html  # Imported-sites manager
```

### API

| Route | Description |
|---|---|
| `GET /api/health` | Status + active model |
| `GET /api/blocks` | Block catalog (labels, presets) — system prompts stay server-side |
| `POST /api/generate` | `{ block, prompt, brand{name,colors,tone}, current_html }` → `{ html, model, elapsed_ms }` |
| `POST /api/sites` | Upload a `.zip` / `.tar.gz` static site |
| `GET /api/sites` | List imported sites |
| `PUT /api/sites/:id/file` | Rewrite one file of a site (visual save & code editor) |
| `PUT /api/sites/:id/domain` | Attach a custom domain |
| `DELETE /api/sites/:id` | Delete a site |
| `GET /edit/:id/*filepath` | Serve a page with the visual editor injected |
| `GET /preview/:id/*filepath` | Serve the same page without injection |

`current_html` is sent back to Gemini as context: a second prompt on the same block **iterates** instead of starting over.

## Getting started

Configuration comes from a `.env` file (loaded at startup) or environment variables — the latter take precedence.

```bash
cp .env.example .env      # then fill in GEMINI_API_KEY
go mod tidy
go run .
# → http://localhost:8080
```

| Variable | Default | Purpose |
|---|---|---|
| `GEMINI_API_KEY` | — (required) | Gemini key — https://aistudio.google.com/apikey |
| `GEMINI_MODEL` | `gemini-flash-latest` | Model (alias for the latest stable Flash) |
| `ADDR` | `:8080` | HTTP listen address (`host:port`) |
| `SITES_DIR` | `./sites` | Storage root for imported sites |
| `GIN_MODE` | *(release)* | `debug` for verbose logs |

## Usage

1. **Brand** tab: name, palette, tone — injected into every generation.
2. Left rail: add blocks (Header, Hero, Pricing…), reorder with ↑↓.
3. **Generate** panel: pick a preset or write your own brief, `⌘/Ctrl + Enter` to generate.
4. Re-prompt the same block to refine it ("make the background light") — the AI starts from the current version.
5. **Code** tab: edit the Tailwind HTML by hand (Monaco editor, with a plain textarea fallback), then "Apply".
6. Top bar: Desktop / Tablet / Mobile toggle, **Copy page**, **Export HTML** (self-contained file with Tailwind CDN).

The project (blocks + brand) is persisted in `localStorage`.

## Extending the generators

Add an entry to `blockSpecs` (`prompts.go`): type, label, presets, and a specialized system prompt. The block automatically appears in the editor palette.

## Imported sites

Three primitives, zero database:

1. **Import** — `POST /api/sites` with a `.zip` or `.tar.gz` (archive type sniffed from magic bytes, a lone top-level directory is flattened automatically). One site = one `sites/<id>/public/` directory served as-is, plus a one-line `site.json`.
2. **Universal editing** — `/edit/<id>/<page>` serves the real page with a script injected before `</body>`: click = select, double-click = edit text, AI panel to retouch the selected element (the `element` generator, which preserves the page's stack). A Visual · AI ⇄ Code toggle switches to a full-page Monaco editor over the raw file. "Save" serializes the DOM (editing artifacts removed) and rewrites the file.
3. **Domain** — `PUT /api/sites/<id>/domain`. A gin middleware at the head of the chain matches `Host` against the domain→site table: on a match, the site is served directly. Point an A record at the server and it's live.

UI: `http://localhost:8080/sites`. For multi-domain HTTPS in production, put Caddy in front (`on_demand_tls`) or add `autocert`.
