# NADAPROD v7 alpha

Visual block-based Tailwind page editor, powered by Gemini Flash (latest) and an **ultra-specialized generator per block type**: each block (header, hero, features, pricing, testimonials, FAQ, CTA, footer) has its own expert system prompt, with a strict output contract (a single responsive, accessible Tailwind HTML fragment, no lorem ipsum).

Three surfaces share one server:

1. **Public landing** (`/`) — the NADAPROD marketing site, fully static.
2. **Block studio** (`/editor`) — build a page from AI-generated blocks. Project state lives in the browser's `localStorage`; the server is stateless for this flow.
3. **Imported sites** (`/sites`) — upload a static site, edit any page visually (click-to-select, AI element retouch) or in a full-page Monaco code editor, and point a domain at it. The filesystem is the database.

Access is **invitation-only**: the app surfaces sit behind a login (`/login`). The root account (from `.env`) creates user accounts and assigns sites to them — users edit their own sites (including the ⚙ publication settings), while import, delete, domains and account management stay root-only. Candidates apply through the public `/waiting-list` page (stored in a JSON file). Sites served on a mapped domain remain fully public.

Note: the product UI and the generation prompts are in French.

## Architecture

```
nadaprod/
├── main.go       # gin-gonic server: static + API
├── auth.go       # Accounts (root + users), HMAC sessions, waiting list
├── gemini.go     # Gemini REST client (generateContent) + HTML cleanup
├── prompts.go    # Registry of specialized generators (1 expert prompt / block)
├── sites.go      # Imported sites: upload, edit, domain routing (filesystem-backed)
├── build.go      # Publish pipeline: local Tailwind compile, PWA files, GDPR notice
├── go.mod
└── web/
    ├── home.html         # Public landing page
    ├── login.html        # Login (invitation-only)
    ├── waiting-list.html # Public application form
    ├── index.html        # Studio: block palette, live preview, AI + code panel
    ├── sites.html        # Imported-sites manager (+ root: accounts panel)
    └── drafts.html       # Same manager, filtered to sites without a domain
```

### API

All routes below require a session except `GET /api/health`, `POST /api/login` and `POST /waiting-list`. Site-scoped routes check the site is assigned to the account (root sees everything).

| Route | Description |
|---|---|
| `GET /api/health` | Status + active model *(public)* |
| `POST /api/login` · `POST /api/logout` · `GET /api/me` | Session (HMAC-signed cookie, 30 days) |
| `GET /api/blocks` | Block catalog (labels, presets) — system prompts stay server-side |
| `POST /api/generate` | `{ block, prompt, brand{name,colors,tone}, current_html }` → `{ html, model, elapsed_ms }` |
| `POST /api/sites` | Upload a `.zip` / `.tar.gz` static site *(root)* |
| `GET /api/sites` | List the sites accessible to the account |
| `PUT /api/sites/:id/file` | Rewrite one file of a site (visual save & code editor) |
| `PUT /api/sites/:id/domain` | Attach a custom domain *(root)* |
| `PUT /api/sites/:id/settings` | Publication settings `{compile, pwa, gdpr}` (⚙ panel) |
| `DELETE /api/sites/:id` | Delete a site *(root)* |
| `GET/POST/PUT/DELETE /api/users*` | Account management *(root)* |
| `POST /waiting-list` | Public application form → `waiting-list.json` (root reads it via `GET /api/waiting-list`) |
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
| `TAILWIND_BIN` | `./bin/tailwindcss` | Tailwind v3 standalone binary (fetched by `bin/build.sh`); missing ⇒ published sites keep the Play CDN |
| `ROOT_EMAIL` | `web@l3dlp.com` | Root account email |
| `ROOT_PASSWORD` | — (required) | Root password — the server refuses to start without it |
| `AUTH_SECRET` | *(random per boot)* | Session-cookie signing secret; leave empty and sessions expire on restart |
| `USERS_FILE` | `./users.json` | User accounts storage (bcrypt hashes, gitignored) |
| `WAITLIST_FILE` | `./waiting-list.json` | Waiting-list storage (gitignored) |
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

Four primitives, zero database:

1. **Import** — `POST /api/sites` with a `.zip` or `.tar.gz` (archive type sniffed from magic bytes, a lone top-level directory is flattened automatically). One site = one directory: `sites/<id>/dev/` (the editable source), `sites/<id>/public/` (the published build, fully regenerable), plus a `site.json`.
2. **Universal editing** — `/edit/<id>/<page>` serves the real page with a script injected before `</body>`: click = select, double-click = edit text, AI panel to retouch the selected element (the `element` generator, which preserves the page's stack). A Visual · AI ⇄ Code toggle switches to a full-page Monaco editor over the raw file. "Save" serializes the DOM (editing artifacts removed) and rewrites the file in `dev/`.
3. **Publication (automatic)** — every save/upload triggers a background rebuild of `public/`, all steps toggleable per site from the ⚙ panel next to the domain controls:
   - **Compiled Tailwind**: `cdn.tailwindcss.com` is replaced by locally compiled, purged CSS (Tailwind v3 standalone binary — no Node, no `node_modules`); inline `tailwind.config` blocks are honored per page. Compilation failure ⇒ the page keeps its CDN.
   - **Automatic PWA**: `manifest.webmanifest`, icon and a versioned offline `sw.js` are generated and registered — unless the site ships its own, which are respected.
   - **GDPR notice**: a small "Zéro stress : tout est local" banner (OK button, link to [nadaprod.com/legal](https://nadaprod.com/legal)), dismissed once per visitor.
   Domain serving overlays `public/` on top of `dev/`, so untransformed files (images, videos…) are never duplicated.
4. **Domain** — `PUT /api/sites/<id>/domain`. A gin middleware at the head of the chain matches `Host` against the domain→site table: on a match, the published site is served directly. Point an A record at the server and it's live.

UI: `http://localhost:8080/sites` (sites with a domain) and `/drafts` (sites without one). For multi-domain HTTPS in production, put Caddy in front (`on_demand_tls`) or add `autocert`.
