# NADAPROD v7 alpha

Visual block-based Tailwind page editor, powered by Gemini Flash (latest) and an **ultra-specialized generator per block type**: each of the 15 blocks (header, hero, features, pricing, testimonials, FAQ, CTA, footer, stats, client logos, blog, team, contact, announcement banner, plus a generic « section libre ») has its own expert system prompt, with a strict output contract (a single responsive, accessible Tailwind HTML fragment, no lorem ipsum).

Three surfaces share one server:

1. **Public landing** (`/`) — the NADAPROD marketing site, fully static.
2. **Block studio** (`/editor`) — build a page from AI-generated blocks: streamed generation, three-variant proposals, a design system (colour/font/radius tokens), targeted element retouch, per-block history with undo/redo, an automatic quality score, and production output (compiled standalone HTML, ZIP site, React/Vue components, one-click publish). Projects are stored server-side (SQLite), one set per account.
3. **Imported sites** (`/sites`) — upload a static site (or create one from scratch, or add pages from the studio), edit any page visually (click-to-select, AI element retouch) or in a **code workshop** — the page and its local CSS/JS as tabs, live preview, CSS applied while you type — and point a domain at it. The filesystem is the database.

Access is **invitation-only**: the app surfaces sit behind a login (`/login`). The root account (from `.env`) creates user accounts and assigns sites to them — users edit their own sites (including the ⚙ publication settings), while import, delete, domains and account management stay root-only. Candidates apply through the public `/waiting-list` page (stored in a JSON file). Sites served on a mapped domain remain fully public.

Note: the generation prompts are in French. The UI is i18n-ready (`web/static/i18n/{fr,en}.json`, browser language first with per-key `en.json` fallback); French is the complete reference, English translation pending.

## Architecture

```
nadaprod/
├── cmd/nadaprod/main.go   # gin-gonic server: wiring, static + API
├── pkg/auth/              # Accounts (root + users), HMAC sessions, waiting list
├── pkg/gemini/            # Gemini REST client (generateContent) + HTML cleanup
├── pkg/prompts/           # Registry of specialized generators (1 expert prompt / block)
├── pkg/sites/             # Imported sites: upload, edit, pages & assets, domain routing, publish pipeline (config parsed, never executed)
├── pkg/projects/          # Studio projects (SQLite): blocks, version history, revisions
├── pkg/studio/            # Studio API: generation (JSON + SSE), rate limit, export, publish
├── pkg/export/            # Compiled HTML / ZIP site / SEO head / HTML → React & Vue components
├── pkg/publish/           # Netlify & Cloudflare Pages deploys (user-supplied token)
├── pkg/images/            # Optional stock photos (Pexels / Unsplash)
├── go.mod
└── web/
    ├── home.html         # Public landing page
    ├── about.html · solutions.html · opensource.html · privacy.html · terms.html · legal.html   # Marketing & legal satellite pages (French-only)
    ├── 404.html          # Custom not-found page (served with a real 404 status)
    ├── login.html        # Login (invitation-only)
    ├── waiting-list.html # Public application form
    ├── studio.html       # Studio markup: palette, sandboxed live preview, 5-tab panel
    ├── static/studio/    # Studio logic (native ES modules) + the in-preview frame script
    ├── static/sites-code.js # Code workshop of the imported-sites editor (Monaco tabs + live preview)
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
| `POST /api/generate` | `{ block, prompt, brand, current_html, theme?, siblings?, mode?, variant?, images? }` → `{ html, model, elapsed_ms, photos }` — errors carry a machine `code` |
| `POST /api/generate/stream` | Same request, Server-Sent Events: `delta` … then `done` (or `error`) |
| `GET /api/studio/config` | Server capabilities (model, compiler, fonts, stock photos, rate) |
| `GET/POST /api/projects` · `GET/PUT/DELETE /api/projects/:id` | Studio projects of the account (JSON import = `POST`) |
| `PUT /api/projects/:id/blocks/:bid` · `GET …/versions` · `POST …/checkout` | Block write (versions on change) · history · undo / redo / restore |
| `POST /api/projects/:id/export` | `{format: html\|zip\|jsx\|vue}` → compiled, CDN-free download · `GET …/export.json` = project backup |
| `POST /api/projects/:id/publish` | `{provider: netlify\|cloudflare, token, site?, account_id?}` → live URL (the token is never stored) |
| `POST /api/projects/:id/site` | Write the project's page into a site's `dev/` (« Enregistrer dans un site… ») |
| `POST /api/projects/from-page` | Open a site page in the studio: split it into blocks, create a project |
| `GET /api/images/search?q=` | Stock-photo search (needs `PEXELS_API_KEY` or `UNSPLASH_ACCESS_KEY`) |
| `POST /api/sites` | Upload a `.zip` / `.tar.gz` static site *(root)* |
| `POST /api/sites/new` | Create an empty site (name only) *(root)* |
| `GET /api/sites` | List the sites accessible to the account |
| `POST /api/sites/:id/pages` | Add a page: blank, copied from another page, or composed in the studio |
| `GET /api/sites/:id/assets` | List a page's local CSS/JS (code-workshop tabs) |
| `PUT /api/sites/:id/file` | Rewrite one file of a site (visual save & code editor) |
| `PUT /api/sites/:id/domain` | Attach a custom domain *(root)* |
| `PUT /api/sites/:id/settings` | Publication settings `{compile, fonts, pwa, gdpr}` (⚙ panel) |
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
go run ./cmd/nadaprod
# → http://localhost:8080
```

| Variable | Default | Purpose |
|---|---|---|
| `GEMINI_API_KEY` | — (required) | Gemini key — https://aistudio.google.com/apikey |
| `GEMINI_MODEL` | `gemini-3.7-flash` | Model (alias for the latest stable Flash) |
| `ADDR` | `:8080` | HTTP listen address (`host:port`) |
| `SITES_DIR` | `./sites` | Storage root for imported sites |
| `TAILWIND_BIN` | `./bin/tailwindcss` | Tailwind v3 standalone binary (fetched by `bin/build.sh`); missing ⇒ published sites keep the Play CDN |
| `WEBFONTS_DIR` | `./web/webfonts` | Local Google Fonts mirror (served on `/webfonts`, used by the publish pipeline); missing ⇒ published sites keep their Google Fonts links |
| `ROOT_EMAIL` | `web@l3dlp.com` | Root account email |
| `ROOT_PASSWORD` | — (required) | Root password — the server refuses to start without it |
| `AUTH_SECRET` | *(random per boot)* | Session-cookie signing secret; leave empty and sessions expire on restart |
| `USERS_FILE` | `./users.json` | User accounts storage (bcrypt hashes, gitignored) |
| `WAITLIST_FILE` | `./waiting-list.json` | Waiting-list storage (gitignored) |
| `DB_FILE` | `./nadaprod.db` | SQLite file of the studio projects (gitignored — back it up) |
| `GEMINI_TEMPERATURE` · `GEMINI_MAX_TOKENS` | `0.7` · `8192` | Generation parameters |
| `GENERATE_RATE_PER_MIN` | `30` | Generations per minute, per account |
| `PEXELS_API_KEY` / `UNSPLASH_ACCESS_KEY` | — | Optional: real photos instead of `placehold.co` placeholders |
| `GIN_MODE` | *(release)* | `debug` for verbose logs |

## Usage

1. **Marque** tab: project name, brand (name, tone), and the **design system** — primary/accent colours, heading/body fonts, radius, mood. Once enabled, blocks use shared tokens (`primary-600`, `font-display`, `rounded-brand`…): changing the theme restyles the whole page instantly. "Convertir la page au thème" rewrites older blocks to the tokens. SEO fields (title, description, sharing image, URL, language) live here too.
2. Left rail: add blocks by click or **drag them into the structure**; reorder by drag-and-drop, ↑↓, or `Alt+↑/↓`.
3. **Générer** tab: pick a preset or write a brief, `⌘/Ctrl + Enter` — the block **builds up live** in the preview. "3 variantes" proposes three art directions side by side.
4. Re-prompt the same block to refine it ("make the background light") — the AI starts from the current version and knows the rest of the page (headings, CTA labels, colours).
5. **Retouche** (top bar): click any element in the preview to retouch just that element with the AI, double-click a text to fix it in place, swap a photo.
6. **Historique** tab: every generation/retouch/edit is a version — compare before/after, restore, `⌘Z` / `⇧⌘Z`.
7. **Qualité** tab: contrast (WCAG AA), heading hierarchy, missing `alt`, unlabeled fields, scored per block.
8. **Code** tab: edit the Tailwind HTML by hand (Monaco, plain textarea fallback), then "Appliquer".
9. `⌘K`: command palette. Top bar: Desktop / Tablet / Mobile, project switcher (new, import JSON, delete).
10. **Exporter → Enregistrer dans un site…**: the page becomes a page of an imported site (new or existing; root can create an empty site on the spot) and goes through the publish pipeline. From `/sites` and `/drafts`: **+ Nouveau site (studio)** (root), **+ Page** on any site (compose in the studio, copy an existing page, or a blank page), and **Studio** on an open page to bring it into the studio as blocks.
11. **Exporter**: standalone HTML (compiled + purged CSS, SEO, favicon — no CDN), ZIP site (with local fonts and images), React or Vue components, project backup (JSON), or **publish** to Netlify / Cloudflare Pages with your own token.

Projects are saved automatically on the server, per account; the URL carries the project id. A project left in the old studio's `localStorage` is migrated on first visit.

> The Netlify / Cloudflare deploys and the Pexels / Unsplash integration are covered by mock-server tests only — they have not yet been exercised against the live services.

## Extending the generators

Add an entry to `blockSpecs` (`pkg/prompts/blocks.go`): type, label, icon, presets, `AllowJS`, and a specialized system prompt. The block automatically appears in the editor palette (add its icon to `ICONS` in `web/static/studio/core.js`, else a generic one is used).

## Imported sites

Four primitives, zero database:

1. **Import** — `POST /api/sites` with a `.zip` or `.tar.gz` (archive type sniffed from magic bytes, a lone top-level directory is flattened automatically). One site = one directory: `sites/<id>/dev/` (the editable source), `sites/<id>/public/` (the published build, fully regenerable), plus a `site.json`.
2. **Universal editing** — `/edit/<id>/<page>` serves the real page with a script injected before `</body>`: click = select, double-click = edit text, AI panel to retouch the selected element (the `element` generator, which preserves the page's stack). A Visual · AI ⇄ Code toggle switches to the code workshop: the page plus every local CSS/JS it references open as Monaco tabs (other CSS/JS of the site one click away), with a live preview beside — CSS is applied as you type, HTML/JS reload on save, ⌘S saves every modified tab. "Save" serializes the DOM (visual mode) or writes the tabs (code mode) into `dev/`.
3. **Publication (automatic)** — every save/upload triggers a background rebuild of `public/`, all steps toggleable per site from the ⚙ panel next to the domain controls:
   - **Compiled Tailwind**: `cdn.tailwindcss.com` is replaced by locally compiled, purged CSS (Tailwind v3 standalone binary — no Node, no `node_modules`); inline `tailwind.config` blocks are honored per page. Compilation failure ⇒ the page keeps its CDN.
   - **Local fonts**: `fonts.googleapis.com` links become `@font-face` rules over TTFs copied into the site from a local mirror (~1 400 families) — no request to Google. Unknown family ⇒ that link is kept.
   - **Automatic PWA**: `manifest.webmanifest`, icon and a versioned offline `sw.js` are generated and registered — unless the site ships its own, which are respected.
   - **GDPR notice**: a small "Zéro stress : tout est local" banner (OK button, link to [nadaprod.com/legal](https://nadaprod.com/legal)), dismissed once per visitor.
   Domain serving overlays `public/` on top of `dev/`, so untransformed files (images, videos…) are never duplicated.
4. **Domain** — `PUT /api/sites/<id>/domain`. A gin middleware at the head of the chain matches `Host` against the domain→site table: on a match, the published site is served directly. Point an A record at the server and it's live.

UI: `http://localhost:8080/sites` (sites with a domain) and `/drafts` (sites without one). For multi-domain HTTPS in production, put Caddy in front (`on_demand_tls`) or add `autocert`.
