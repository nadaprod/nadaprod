# BACKEND.md — NADAPROD server technical documentation

Go 1.27 · [gin](https://github.com/gin-gonic/gin) v1.12 · single binary. Imported sites have no database (the filesystem is the database); the block studio's projects live in one SQLite file (pure-Go driver, no CGO). Standard layout: `cmd/nadaprod/main.go` + `pkg/{auth,gemini,prompts,sites,projects,studio,export,publish,images,i18n}` (import path `nadaprod/pkg/<name>`):

| File | Lines | Role |
|---|---|---|
| `cmd/nadaprod/main.go` | ~175 | Wiring only: config, stores, middleware chain, static routes, route registration |
| `pkg/auth/` — `store.go` `session.go` `waitlist.go` `routes.go` | ~495 | Accounts (root + users), HMAC sessions, waiting list |
| `pkg/gemini/` — `client.go` `message.go` `translate.go` | ~790 | Gemini REST client (plain + SSE streaming), typed errors, `Generator` interface, request types (`Brand`, `Theme`, `Sibling`, `GenerateRequest/Response`), user-message assembly (incl. source language), inter-block summary, output sanitizing; `Translator` — batched JSON-mode translation (§2.4) |
| `pkg/prompts/` — `blocks.go` `translate.go` | ~420 | Block registry: one expert system prompt per block type (14 catalog types + the generic `section` + the hidden `element`); `TranslatorPrompt` |
| `pkg/sites/` — `store.go` `validate.go` `extract.go` `serve.go` `routes.go` `pages.go` `assets.go` | ~1 100 | Imported sites: storage model, input validation, archive extraction, domain serving, HTTP API; pages (create site, read/save/copy page — the studio bridge, §12) and page assets (local CSS/JS, the code workshop) |
| `pkg/sites/` — `build.go` `twconfig.go` `fonts.go` | ~1 285 | Publish pipeline: local Tailwind compile (inline config **parsed as a data literal, never executed**; CLI confined and unprivileged), Google Fonts localization, PWA, GDPR notice. Exports `CompileCSS`/`CanCompile`/`FontsDir` for the studio export |
| `pkg/projects/` — `store.go` `routes.go` | ~1 025 | Studio projects in SQLite: projects, ordered blocks, bounded per-block version history (undo/redo pointer), optimistic revision counter, owner-scoped HTTP API (§7); translation memory (§13) |
| `pkg/studio/` — `routes.go` `i18n.go` `limiter.go` | ~900 | Studio API: generation (JSON + SSE), per-account rate limit, capability report, stock-photo search, production export, one-click publish (§8), save-into-site / open-page-as-project (§12), translation routes (§13) |
| `pkg/export/` — `page.go` `components.go` `split.go` `i18n.go` | ~2 380 | Production output: standalone compiled HTML, full static site (ZIP), SEO head, favicon, theme → Tailwind config, local fonts, asset download, HTML → React/Vue components (§9); `PageCDN`/`SplitPage` for the sites bridge (§12); per-language pages (§13) |
| `pkg/stats/` — `store.go` `routes.go` | ~450 | Measurement for `/debate` (§14): daily aggregates of AI calls and published-site hits in their own SQLite file (`STATS_FILE`), `GET /api/stats` (public, cached 60 s), `GET /api/stats/sites` (root) |
| `pkg/i18n/` — `i18n.go` | ~510 | Translatable segments of an HTML fragment (`Extract`), re-application (`Apply`), marker check (`Valid`), `Key`, `LangRe` — pure, `x/net/html` only (§13) |
| `pkg/publish/` — `publish.go` `netlify.go` `cloudflare.go` | ~900 | Deploy a file set to Netlify (ZIP deploy) or Cloudflare Pages (direct upload) with a user-supplied token (§10) |
| `pkg/images/` — `images.go` `providers.go` `resolve.go` | ~975 | Stock photos: Pexels / Unsplash clients, cached search service, `data-photo` placeholder resolution (§11) |

Renames with the package split: `sites.Store`/`sites.NewStore`/`sites.Meta` (ex-`SiteStore`/`NewSiteStore`/`SiteMeta`), `gemini.Client`/`gemini.NewClient` (ex-`GeminiClient`). Dependencies: `cmd/nadaprod` → all packages; `pkg/sites` → `pkg/auth` (route authorization); `pkg/gemini` → `pkg/prompts`; `pkg/studio` → `gemini`, `prompts`, `projects`, `export`, `publish`, `images`, `i18n`; `pkg/export` → `i18n` (it receives the Tailwind compiler as a plain `func`, so it does not import `pkg/sites`); `pkg/stats` receives the app counts as a `func` from `main.go` (no import of `projects`/`sites`); `pkg/i18n`, `pkg/publish`, `pkg/images`, `pkg/projects`, `pkg/stats` are leaves (no gin in `export`, `i18n`, `publish`, `images`). The rest of this document uses the historical flat names in prose — same code, new homes.

Tests: `pkg/sites/{build,fonts,sites,twconfig,pages,assets}_test.go`, `pkg/auth/auth_test.go`, `pkg/gemini/gemini_test.go` (fake Gemini server: plain, SSE, retries, error classification), `pkg/projects/store_test.go` (incl. translations: upsert/delete/cap/cascade, migration from v1), `pkg/studio/routes_test.go` (fake `Generator`, mock Netlify), `pkg/studio/i18n_test.go` (fake `Translator`, end-to-end translation flow), `pkg/i18n/i18n_test.go`, `pkg/stats/{store,routes}_test.go` (aggregation, flush on Close, series with empty days, no identifying column, public vs root routes), `pkg/sites/hit_test.go` (`OnHit` from `HostMiddleware`), `pkg/export/{page,components,i18n}_test.go`, `pkg/publish/*_test.go` and `pkg/images/*_test.go` (httptest mocks only — see §10/§11 for what was never exercised against the live services) — run with `go test ./pkg/...` (bare `./...` works too now that the stray `books/` directory is gone — `tools/mywebfont` has its own module; same for `go vet`).

Direct dependencies: `gin-gonic/gin`, `joho/godotenv`, `golang.org/x/crypto` (bcrypt), `modernc.org/sqlite` (pure-Go SQLite — the binary stays CGO-free, at the cost of ~18 MB), `golang.org/x/net/html` (HTML → JSX/Vue), `lukechampine.com/blake3` (Cloudflare Pages asset hashes). Everything else in `go.sum` is transitive. One optional external tool: the Tailwind v3 **standalone binary** (`TAILWIND_BIN`, no Node runtime), fetched by `bin/build.sh`.

---

## 0. Accounts & sessions (`auth.go`)

Invitation-only: no public signup. Two kinds of accounts:

- **root** — `ROOT_EMAIL` + `ROOT_PASSWORD` from the environment, never written to disk (the password is bcrypt-hashed once at startup and compared like any other). Sees and can do everything.
- **users** — created by root, stored in `USERS_FILE` (`users.json`): `{ "email": { "hash": "$2a$…", "sites": ["id", …] } }`, guarded by a `sync.RWMutex`, rewritten whole on each mutation (same style as `site.json`). A user only reaches the sites in their list (`CanAccess`); import/delete/domain/accounts are root-only.

### 0.1 Sessions

`np_session = base64url(email) . expiryUnix . hex(hmac-sha256(AUTH_SECRET, payload))` — 30 days, `HttpOnly`, `SameSite=Lax`, `Secure` when the request came over TLS or `X-Forwarded-Proto: https`. No session storage server-side: deleting a user simply makes `exists()` fail on the next request (⇒ 401 despite a validly signed cookie). `RequireAuth` sets `user` + `isRoot` in the gin context; failures get 401 on `/api/*`, a redirect to `/login?next=…` elsewhere. Failed logins sleep 300 ms (brute-force damper).

### 0.2 Account management (root-only)

`GET /api/users` → `[{email, sites}]` · `POST /api/users` `{email, password}` (≥ 8 chars, email regex, root's email refused) · `PUT /api/users/:email` `{password?, sites?}` (partial, `nil` = untouched — same convention as `/settings`) · `DELETE /api/users/:email`.

### 0.3 Waiting list

`POST /waiting-list` (public): `{name, email, message, website}` — `website` is a **honeypot** (filled ⇒ `{ok:true}` but nothing stored — though a malformed email still gets a 400 first). Fields are truncated (name 100 / email 200 / message 2 000 runes). Valid entries are appended to `WAITLIST_FILE` with date + IP, deduplicated by email, capped at 5 000 entries (beyond: accepted silently). Root reads them via `GET /api/waiting-list` (shown in the « Comptes » card of `/sites`).

---

## 1. Startup & configuration (`main.go`)

Order of operations in `main()`:

1. `godotenv.Load()` — loads `.env` if present. Real environment variables always win (godotenv never overwrites). A missing file is not an error.
2. Read config:

   | Variable | Default | Behavior |
   |---|---|---|
   | `GEMINI_API_KEY` | — | **Required**: `log.Fatal` if empty |
   | `GEMINI_MODEL` | `gemini-3.7-flash` | Google alias for the latest stable Flash |
   | `SITES_DIR` | `./sites` | Root of imported-sites storage (created if absent) |
   | `ADDR` | `:8080` | HTTP listen address |
   | `TAILWIND_BIN` | `./bin/tailwindcss` | Tailwind v3 standalone binary; if the file is missing, local compilation is disabled (published pages keep the Play CDN) |
   | `WEBFONTS_DIR` | `./web/webfonts` | Local Google Fonts mirror (TTF families); missing ⇒ published sites keep their Google Fonts links. Cleaned with `filepath.Clean` at store construction (`safeJoin` compares prefixes) |
   | `ROOT_EMAIL` | `web@l3dlp.com` | Root account email |
   | `ROOT_PASSWORD` | — | **Required**: `log.Fatal` if empty (the app is behind auth) |
   | `AUTH_SECRET` | *(empty)* | Session-signing secret; empty ⇒ random per boot (+ warning log), all sessions expire on restart |
   | `USERS_FILE` | `./users.json` | User accounts storage |
   | `WAITLIST_FILE` | `./waiting-list.json` | Waiting-list storage |
   | `DB_FILE` | `./nadaprod.db` | SQLite file of the studio projects (created + migrated at startup; WAL ⇒ `-wal`/`-shm` siblings). Back it up with the rest of the runtime data |
   | `GEMINI_TEMPERATURE` | `0.7` | Base sampling temperature (variants add +0.25, re-theme caps at 0.4) |
   | `GEMINI_MAX_TOKENS` | `8192` | `maxOutputTokens` |
   | `GENERATE_RATE_PER_MIN` | `30` | Generations per minute **per account** (token bucket, burst = same value) |
   | `PEXELS_API_KEY` / `UNSPLASH_ACCESS_KEY` | *(empty)* | Optional stock-photo provider (Pexels wins if both). None ⇒ blocks keep `placehold.co`, the photo picker is hidden |
   | `GIN_MODE` | *(empty)* | Empty ⇒ `gin.ReleaseMode` is forced; any value ⇒ gin's own env handling (`debug` for verbose logs) |

3. Construct `gemini.Client` (with `gemini.Options`) and `SiteStore` (which migrates any legacy `public/`-only site to the `dev/` layout — §4.1 — then does an initial `reloadDomains()`), and launch `store.BuildAll()` in a goroutine (§4.7). Then `UserStore` (§0) — root's env password is bcrypt-hashed once here — then `projects.Open(DB_FILE)` (runs pending migrations, `log.Fatal` on failure) and `images.FromEnv`.
4. Build the router: `gin.New()` + `gin.Logger()` + `gin.Recovery()`.
5. **`store.HostMiddleware()` is installed before everything else** — see §4.3. This is load-bearing: on a mapped domain, the editor and API are deliberately unreachable — and published sites never see the login.
6. Public statics + public routes (`/login`, `/waiting-list`, `POST /waiting-list`, `POST /api/login`, `GET /api/health`), then the authed groups: `r.Group("/", users.RequireAuth())` for app pages and `api.Group("", users.RequireAuth())` for the rest of `/api` (`auth`, `sites`, `projects.RegisterRoutes`, `studio.Register`). `/webfonts` is served through a tiny group that adds `Access-Control-Allow-Origin: *` — the studio preview runs in an opaque-origin iframe and font loads are CORS-checked. `NoRoute` → `web/404.html`.
7. `r.Run(addr)`.

`r.MaxMultipartMemory = 32 << 20` (32 MB in-RAM threshold for multipart parsing; larger uploads spill to temp files — distinct from the 200 MB upload cap in §4.4). Request bodies are capped with `http.MaxBytesReader`: 8 MB on `/api/generate*`, 24 MB on the project routes (a JSON import must fit).

### 1.1 Route map

Auth column: **pub** = no session needed · **auth** = valid session (`RequireAuth`) · **owner** = auth + project owned by the account (root passes; anyone else gets **404**, not 403 — existence is not confirmed) · **site** = auth + site assigned to the account (`allowed()`/`CanAccess`) · **root** = auth + `RequireRoot`.

| Route | Auth | Serves |
|---|---|---|
| `GET /` | pub | `web/home.html` — public landing |
| `GET /login` `/waiting-list` `/demo` | pub | login page, public application form, demo |
| `GET /about` `/legal` `/opensource` `/privacy` `/solutions` `/terms` `/robots.txt` | pub | static pages |
| `GET /gfx/*` `/static/*` | pub | asset directories (`web/gfx`, `web/static` — incl. `fonts.css`, the app's local `@font-face` sheet) |
| `GET /webfonts/*` | pub | local Google Fonts mirror (TTF) + « My Webfonts » browser at its index |
| `POST /waiting-list` | pub | append a candidacy to `waiting-list.json` (honeypot + dedupe, §0.3) |
| `GET /api/health` | pub | `{ok, model}` |
| `POST /api/login` | pub | open a session (sets the `np_session` cookie) |
| `GET /editor` `/editor/:project` `/sites` `/drafts` | auth | app pages (`/editor*` → `web/studio.html`; the project id in the URL is resolved client-side; `/drafts` → `web/drafts.html`, now a redirect stub to `/sites`) |
| `POST /api/logout` · `GET /api/me` | auth | close session · `{email, root, sites}` |
| `GET /api/blocks` | auth | public block catalog (§3) |
| `GET /api/studio/config` | auth | what this server can do: `{model, images{enabled,provider}, compile, fonts[], publish[], rate, user, root, max_block, langs[{code,name}], translate}` |
| `POST /api/generate` | auth | block generation, JSON response (§2) — rate-limited per account |
| `POST /api/generate/stream` | auth | same request, `text/event-stream` response: `delta` events then `done` (or `error`) (§8.1) |
| `GET /api/images/search?q=&orientation=` | auth | stock-photo search (503 `disabled` without a provider key) |
| `GET/POST /api/projects` | auth | list own projects (`?all=1` for root) · create (empty, JSON import, localStorage migration) |
| `GET/PUT/DELETE /api/projects/:id` | owner | read · partial meta update `{name, brand, theme, seo, publish, order, rev}` · delete |
| `PUT /api/projects/:id/blocks/:bid` | owner | upsert one block `{type, html, prompt, source, rev}`; a changed `html` pushes a version |
| `GET /api/projects/:id/blocks/:bid/versions` | owner | version list, newest first |
| `POST /api/projects/:id/blocks/:bid/checkout` | owner | `{step: -1\|1}` (undo/redo) or `{version_id}` (restore) |
| `GET /api/projects/:id/export.json` | owner | portable project backup, re-importable through `POST /api/projects` |
| `POST /api/projects/:id/export` | owner | `{format: html\|zip\|jsx\|vue, lang?}` → file download (§9); `html` + `lang` = the page in that language, `zip`/site = every language (§13) |
| `GET /api/projects/:id/i18n` | owner | `{source, langs, segments:[{hash,src,attr,blocks}], translations:{lang:{hash:{text,status}}}}` (§13) |
| `PUT /api/projects/:id/i18n/:lang` | owner | `{items:[{hash,text}]}` → human translations (`text:""` deletes); 422 `invalid_markers` |
| `POST /api/projects/:id/i18n/:lang/translate` | owner | `{hashes?, redo?}` → Gemini on missing segments (≤ 200 per call) — `{translations, failed, remaining}`; rate-limited like `/generate`; 503 `no_translator` |
| `GET /api/projects/:id/i18n/:lang/preview` | owner | `{blocks:{id:html}, missing}` — translated block HTML for the studio preview |
| `POST /api/projects/:id/publish` | owner | `{provider, token, site, account_id}` → deploy (§10) |
| `POST /api/projects/:id/site` | owner + site | `{site, path, overwrite}` → writes the page (CDN source, SEO head) into `dev/` of an imported site the account can access, plus `<lang>/<path>` per target language, republishes it, remembers the target (`publish.provider = "site"`); reply adds `langs` (written copies) and `warnings` (§12, §13) |
| `POST /api/projects/from-page` | site | `{site, path}` → new project made of the page's `<body>` children (§12) |
| `POST /api/sites/new` | root | `{name}` → empty site (slug id, suffixed if taken) for the studio to fill |
| `POST /api/sites/:id/pages` | site | `{name, from?}` → blank page (`BlankPage`) or copy of `from`; 409 if it exists |
| `GET /api/sites` | auth | list **accessible** sites `[{id, domain, pages[], settings}]` |
| `GET /edit/:id/*filepath` | site | site page **with** editor injection (§4.5) |
| `GET /preview/:id/*filepath` | site | site page **without** injection |
| `PUT /api/sites/:id/file` | site | write one text file in `dev/` (`Editable`: html, htm, css, js, mjs, json, svg, txt, md, xml, webmanifest — creates it if absent) ; `.html` ⇒ background republish of that page, other files are served from `dev/` as they are |
| `GET /api/sites/:id/assets?page=` | site | `{referenced: [{path,size}], others: […]}` — local CSS/JS the page references (`PageAssets`, browser-like URL resolution, existing files only) and the site's other CSS/JS (`np-fonts/` excluded) |
| `PUT /api/sites/:id/settings` | site | publication settings `{compile, fonts, pwa, gdpr}` + background rebuild |
| `POST /api/sites` | root | archive upload (§4.4) |
| `PUT /api/sites/:id/domain` | root | attach/detach a domain |
| `DELETE /api/sites/:id` | root | delete a site directory |
| `GET/POST /api/users` · `PUT/DELETE /api/users/:email` | root | account management (§0.2) |
| `GET /api/waiting-list` | root | read the candidacies |
| *anything else* | pub | `web/404.html` with HTTP status 404 |

### 1.2 Generation API contract

`POST /api/generate` and `POST /api/generate/stream` — request (`gemini.GenerateRequest`):

```json
{
  "block": "hero",                  // required; a blockSpecs type or "element"
  "prompt": "…",                    // user brief, may be empty
  "brand": { "name": "", "colors": "", "tone": "" },
  "current_html": "",               // present ⇒ iterate instead of regenerate
  "theme": { "enabled": true, "mode": "light", "primary": "#0f766e", "accent": "#f59e0b",
             "font_display": "Playfair Display", "font_sans": "Inter", "radius": "lg", "style": "" },
  "siblings": [ { "type": "header", "html": "…" } ],  // the page's other generated blocks
  "mode": "",                       // "retheme" ⇒ restyle current_html to the theme, content untouched
  "variant": 0,                     // 1..3 ⇒ imposed art direction (A sober / B bold / C rich)
  "images": true,                   // resolve data-photo placeholders if a provider is configured
  "lang": "fr"                      // source language of the page; ≠ "" and ≠ "fr" ⇒ language section in the message
}
```

Response (`GenerateResponse`): `{ "html", "block", "model", "elapsed_ms", "photos" }`.

Errors are `{ "error": "<French message>", "code": "<machine code>", "retry_after"?: seconds }` — the studio maps `code` to an actionable panel:

| code | HTTP | Meaning |
|---|---|---|
| `invalid` | 400 | bad JSON / unknown block type / Gemini rejected the request (unknown model…) |
| `rate_limited` | 429 | our per-account limiter (`Retry-After` header set) — never reaches Gemini |
| `quota` | 429 | Gemini 429 / `RESOURCE_EXHAUSTED`; `retry_after` from Gemini's `RetryInfo` when present |
| `auth` | 502 | Gemini 401/403 / `API_KEY_INVALID` — a server-side config problem |
| `blocked` · `empty` | 422 | safety block · nothing usable came back |
| `timeout` | 504 | 120 s generation budget exceeded |
| `canceled` | 499 | the client aborted the request (nginx-style status, never shown as an error by the UI) |
| `network` · `upstream` | 502 | transport failure · Gemini 5xx / anything else (an unexpected non-2xx < 400 also lands here) |

Per-operation budgets in `pkg/studio`: 120 s generate, 90 s export, 3 min publish, 15 s image search.

---

## 2. Gemini client (`pkg/gemini`)

`Client` holds `apiKey`, `model`, `Options{Temperature, MaxOutputTokens, Endpoint}` and two HTTP clients: one with a **90 s timeout** for plain calls, one without for streams (the request context — 120 s, set by the route — bounds those). Auth via the `x-goog-api-key` header. Routes depend on the `Generator` interface (`Model`, `GenerateBlock`, `StreamBlock`), not on `*Client`: tests plug a fake, and a future server-side page orchestrator (v1.0) can chain blocks through the same seam.

### 2.1 Request assembly — `buildUserMessage(spec, req)`

- `systemInstruction` = the block's `SystemPrompt` (never leaves the server otherwise).
- One user message, in order:
  1. `Brief du bloc « <label> » :` + the prompt (or a "propose a polished variant" fallback) — **or**, in `retheme` mode with a current version, a "RE-THÈME" header instructing to keep texts/structure/images/links and change style only;
  2. optional brand context (name / palette / tone; the free-form palette is **omitted when a design system is active**, so it cannot contradict it);
  3. **design-system section** when `theme.enabled`: brand colours only through `primary-50…950` / `accent-50…950` (no hard-coded saturated hues; neutrals stay free), `font-display` on headings / `font-sans` on text, `rounded-brand` on buttons/fields/cards, optional light/dark mood and free art direction. These tokens are *defined by the page's Tailwind config* (studio preview and export, §9.1) — which is what makes a theme change instant, with no regeneration;
  4. **inter-block coherence** (`SummarizeSiblings`, not in retheme mode): per sibling block — dark-background flag, first headings, labels of "filled" CTAs — plus the 5 dominant non-neutral colour classes, and a reminder to reuse CTA labels/product name and keep a single `<h1>`. Pure, deterministic, capped at 12 siblings / ~1 500 chars;
  5. **page language** when `lang` is set and not French (not in retheme mode): write every visible text in that language (overrides the system prompt's « copywriting français »), keep each sentence inside one element (helps segmentation, §13). For `""`/`fr` the message is byte-identical to before (tested);
  6. variant direction (`variant` 1..3) when asked;
  7. optional current version fenced as ` ```html … ``` ` (wording differs for iterate / variant / retheme);
  8. final reminder: *"Réponds UNIQUEMENT avec le fragment HTML final."*
- `generationConfig`: temperature from `Options` (variants +0.25 capped at 1.2, retheme capped at 0.4), `maxOutputTokens` from `Options`.

### 2.2 Transport, retry & typed errors

`GenerateBlock` → `:generateContent`; `StreamBlock` → `:streamGenerateContent?alt=sse`, parsed line by line (`data: {json}` events, 4 MB line buffer), calling `onDelta(text)` per chunk and returning the **cleaned** final HTML — the raw stream may carry markdown fences, only the return value is authoritative.

Both modes (and `Translate`) share one loop, `do()`. Two attempts total (1 retry) on *transient* failures — transport error, HTTP 429, HTTP ≥ 500 — with a linear `(attempt+1) s` pause that aborts on client cancellation. **A stream is never retried once a delta has been sent.** Every failure is a `*gemini.Error{Code, Status, Message, RetryAfter}` built by `classify` (HTTP status + Gemini's `error.status` / `details[].reason` / `RetryInfo.retryDelay`); `HTTPStatus()` gives the status to answer the browser with (§1.2). Safety blocks (`promptFeedback.blockReason`, or a `SAFETY`-like `finishReason` with no text) ⇒ `blocked`; nothing usable after cleaning ⇒ `empty`.

### 2.3 Output sanitizing — `cleanHTML`

The model contract is "fragment only"; this is the safety net:
1. Trim whitespace.
2. If a ` ```html … ``` ` fence matches (`fenceRe`, dot-all), keep only its inside.
3. Drop any text before the first `<` and after the last `>`.
4. Trim again.

A fence opened but never closed (truncated answer) is stripped too. Then `Sanitize(html, spec.AllowJS)` enforces the verifiable part of the contract: a block whose specialization forbids JavaScript never comes out with a `<script>`. It still does **not** validate single-rootness or parse the HTML (see TODO).

### 2.4 Translation — `Translator` (`translate.go`)

`Client.Translate(ctx, TranslateRequest{From, To, Brand, Items})` implements the `Translator` interface (the studio depends on the interface; tests plug a fake). Items (id → source with `<n>…</n>` / `<n/>` markers) are sent in chunks of 40, sorted by id, one `generateContent` call each with `prompts.TranslatorPrompt` as system instruction, `responseMimeType: application/json` and temperature ≤ 0.3. The reply (a ```` ```json ```` fence is tolerated) keeps only requested ids with non-empty strings; missing ids are simply absent (the caller reports them as `failed`). Context cancellation is checked between chunks; results of chunks already done are returned alongside an error.

---

## 3. Block registry (`prompts.go`)

```go
type BlockSpec struct {
    Type, Label, Icon, Description string
    Presets []string   // quick prompts shown in the editor
    Hidden  bool       // true ⇒ not in the palette (internal use)
    AllowJS bool `json:"-"` // the contract allows one <script>; otherwise scripts are stripped from the output
    SystemPrompt string `json:"-"`  // NEVER serialized to the client
}
```

- `basePrompt` — the shared non-negotiable output contract: fragment only, single root, Tailwind v3 utilities only (no `<style>`/custom CSS), images via `placehold.co` or inline SVG — every *photographic* `<img>` carries `data-photo="2-4 English keywords"` (what `pkg/images` resolves into a real photo; never on logos/icons/UI captures), and real image `src`s already present in the current version are kept as they are — inline-SVG icons, AA accessibility, mobile-first 360–1440 px, real French copywriting, JS only where the specialization allows (one minimal idempotent `<script>`), `href="#"` for fake links.
- `blockSpecs` — the source of truth. **Adding a block type = appending one entry**; it appears in the editor palette automatically. Current types: `header` (mobile menu JS allowed), `hero`, `features`, `pricing`, `testimonials`, `faq` (native `<details>`), `cta`, `footer`, and since v0.3 `stats` (`<dl>`, French number formats, no count-up JS), `logos` (fictional wordmarks as inline SVG — never a real brand), `blog` (`<article>` cards, `<time datetime>`, whole-card link), `team` (portraits with `data-photo`), `contact` (decorative form, visible `<label for>`s, `.example` addresses), `banner` (thin announcement bar, dismiss JS allowed) — each with 4 presets and a specialization appended to `basePrompt`. `AllowJS` is true for `header` and `banner` only.
- `elementSpec` (`Hidden: true`, type `element`) — the "DOM surgeon" used by the imported-sites editor **and by the studio's targeted retouch**: retouch ONE existing element, preserve everything the brief doesn't mention, adapt to the page's own stack (Tailwind or inline styles), never introduce new dependencies, no `<script>`/`<style>`.
- `sectionSpec` (type `section`, « Section libre ») — the generic 15th catalog entry, shown last in the palette; also the type given to blocks imported from a site page (§12).
- `blockIndex` — package-init map of type → spec (blocks + section + element).
- `GetBlockSpec(t)` resolves for generation; `BlockCatalog()` returns `blockSpecs` plus `sectionSpec` (last) for `GET /api/blocks` — the hidden `elementSpec` is never in the catalog, and `SystemPrompt` is excluded by the `json:"-"` tag.

---

## 4. Imported sites (`sites.go`)

### 4.1 Storage model — the filesystem is the database

```
sites/<id>/site.json   → { "domain": "example.com", "compile": …, "fonts": …, "pwa": …, "gdpr": … }
sites/<id>/dev/…       → the editable source (upload target, edit/save/preview)
sites/<id>/public/…    → the published build output (build.go), fully regenerable
```

The publication settings are `*bool` — `nil` means **on** (the zero-effort default); `SiteMeta.CompileOn()/PWAOn()/GDPROn()` resolve them. `migrateLayout()` (at store construction) renames a legacy `public/`-only site to `dev/`; `srcDir(id)` returns `dev/` when it exists and falls back to `public/` for an unmigrated site (in which case `BuildSite` refuses to run — the source is never overwritten by a build).

`SiteStore{ root, twBin, fontsDir, gdprTag, mu sync.RWMutex, domains map[host]siteID, buildLocks sync.Map, builds sync.WaitGroup }`. The `domains` map is rebuilt by `reloadDomains()` (scan every `site.json`) at startup and after any domain change or site deletion. Reads take `RLock` (hot path: every request), writes take `Lock`. `buildLocks` holds one mutex per site to serialize builds (§4.7).

### 4.2 Path safety — `safeJoin(base, p)`

Every filesystem path derived from user input goes through it: normalizes `\` → `/`, `path.Clean("/"+p)` (collapses `..`), joins under `base`, then rejects anything that escapes `base`. Used by: archive extraction (zip-slip), `serveStatic`, `serveEdit`, `PUT file`, `DELETE`. **Never build paths with raw `filepath.Join` on request data.**

### 4.3 Domain routing — `HostMiddleware`

On every request: `hostname()` strips the port and lowercases `c.Request.Host`; on a `domains` map hit, `serveStatic` serves the site (directory ⇒ `index.html` fallback; a miss ⇒ **301 redirect to `/`** — a published domain never returns a 404 page) and `c.Abort()` stops the chain. No match ⇒ `c.Next()`. Consequence: **a mapped domain serves only the static site** — editor, API, everything else is short-circuited.

`serveStatic` serves the **published overlay**: it tries `public/` (the build) first, then falls back to `dev/` — so transformed pages and generated PWA files come from the build while untouched assets (images, videos, css…) come straight from the source, never duplicated. `serveSource` (used by `/preview/:id/*`) reads `dev/` only: it is the raw-file source of the Monaco code editor and must never show build artifacts.

### 4.4 Upload pipeline — `POST /api/sites`

1. Multipart field `archive`, read via `io.LimitReader(maxUploadBytes+1)`; > 200 MB (`maxUploadBytes = 200 << 20`) ⇒ 400.
2. `slugify(filename)` (lowercase, strip `.zip`/`.tar.gz`/`.tgz`, non-alphanumerics → `-`) becomes the site ID; the ID is claimed atomically with `os.Mkdir`, retrying with `-2`, `-3`, … suffixes on collision.
3. `extractArchive` sniffs **magic bytes**: `PK` ⇒ zip, `1f 8b` ⇒ gzip/tar.gz — extension is ignored. Extraction (`extractZip` / `extractTarGz`) skips directories, `__MACOSX`, `.DS_Store` (`isJunk`), routes every entry name through `safeJoin`, and writes with `writeFileCapped` against a shared **1 GB decompression budget** (`maxExtractBytes` — bytes actually written are counted, archive-declared sizes are not trusted; exceeding it aborts the upload).
4. `flattenSingleDir` — if the archive contained exactly one top-level directory, its content is lifted so `index.html` sits at `dev/` root (rename to `dir.tmp` → remove empty dir → rename back).
5. `listPages` (WalkDir over `dev/` for `*.html`, `index.html` sorted first). Zero pages ⇒ the site directory is removed and the upload rejected.
6. `go BuildSite(id)` — the first publication runs in the background; the response doesn't wait.
7. Response: `{id, pages[]}`.

### 4.5 Edit mode — `serveEdit` (`/edit/:id/*filepath`)

Serves the **real** page, with editing tools spliced in: non-`.html` assets pass through untouched (so the site's relative paths work inside the iframe); for HTML, `injectTag` — a `<link id="__wp_style">` + `<script id="__wp_inject" defer>` pointing at `/static/inject.{css,js}` — is inserted before the first `</body>` (case-insensitive regex; appended at EOF if no `</body>`). `/preview/:id/*filepath` is the same file without injection; the code editor uses it as its raw-content source.

### 4.6 Mutations

- `PUT /api/sites/:id/file` `{path, html}` — any `Editable` text extension accepted (§1.1); `safeJoin` under `dev/` then a plain `os.WriteFile` (no backup, no versioning). An `.html` save is followed by `go RebuildPage(id, path)` — the save responds instantly, the published page follows a few seconds later; other files are served from `dev/` as-is. This endpoint is shared by the visual save (serialized DOM) and the code workshop's save-all.
- `PUT /api/sites/:id/domain` `{domain}` — normalized by `hostname()`, validated by `validDomain` (FQDN-only regex: alphanumeric labels, alphabetic TLD, ≤ 253 chars — no IPs, no single labels), merged into `site.json` (publication settings preserved), `reloadDomains()` ⇒ effective immediately, then `go BuildSite(id)` (the PWA manifest is named after the domain). Empty string detaches (not validated).
- `PUT /api/sites/:id/settings` `{compile?, fonts?, pwa?, gdpr?}` — partial update (absent fields untouched), merged into `site.json`, `go BuildSite(id)`. Response echoes the resolved settings.
- `DELETE /api/sites/:id` — `validSiteID` + `safeJoin` guard (+ explicit `target != root` check), `os.RemoveAll`, `reloadDomains()`.

Every `:id` route additionally validates the id shape with `validSiteID` (`^\.?[a-z0-9][a-z0-9._-]*$`, ≤ 128 — leading dot allowed for hidden sites) inside `allowed()` — defense-in-depth alongside `safeJoin`, which stays authoritative.

### 4.7 Publish pipeline — `build.go`

`BuildSite(id)` (serialized per site by `buildLocks`) wipes `public/` and regenerates it from `dev/`; only transformed files are written, everything else is served by the §4.3 fallback. During the rebuild window the fallback serves the source (CDN version) — no downtime. Triggers: startup (`BuildAll`, goroutine), upload, per-page save (`RebuildPage`), domain and settings changes. Four steps per page, each toggleable via `site.json`:

1. **Local Tailwind compile** (`compile`, needs `TAILWIND_BIN`): if the page loads `cdn.tailwindcss.com`, the inline `tailwind.config` script (if any) is extracted (`extractTailwindConfig`) and **read as a data literal** by `ParseTailwindConfig` (`twconfig.go`) — never evaluated: the CLI receives `module.exports = <JSON>;`. Accepted: nested objects/arrays, `'` or `"` strings with the usual escapes (incl. surrogate pairs and LF/CRLF/CR/U+2028 line continuations), numbers (incl. hex, `.5`; re-emitted the way JS's `Number.toString` would, so numeric keys such as `zIndex: { 1000000: … }` keep their browser name), `true`/`false`/`null`, unquoted keys, trailing commas, `//` and `/* */` comments, a trailing `;` — key order preserved (it matters for `screens`), `<`/`>`/`&` escaped as `\uXXXX`. Refused (error ⇒ CDN kept, logged): anything that would need evaluation — calls (`require(...)`, plugins), functions/arrows/getters, template literals, spreads, computed keys, identifiers as values (`undefined` included), any statement before or after the assignment (a second statement could change the config the browser sees), > 64 KB, > 32 levels. All 65 pages of the existing corpus (37 distinct configs) parse identically to Node's evaluation and compile to byte-identical CSS. The CLI runs per page with `--content <that page>` (configs differ per page in the observed corpus) + `--minify`, and `rewriteTailwindHTML` swaps the CDN tag for an inline `<style data-np-tailwind>`. A config-only script is removed (a script mixing config with other code is now refused upstream, so the `window.tailwind` stub branch of `rewriteTailwindHTML` is only reached by the unit tests). Extra CDN tags are dropped. **Any compile error ⇒ the page keeps its CDN** (logged, graceful). ~2–3 s per page (CLI startup dominates, hard 60 s timeout per page), hence the async triggers.

   **Confinement of the CLI** (`compileTailwind`): the page is *copied* into a fresh temp directory which becomes the process's cwd, `HOME` and `TMPDIR` (hence `NewStore` absolutizes `TAILWIND_BIN` — the default `./bin/tailwindcss` would not resolve from there); the environment is reduced to `PATH`, `HOME`, `TMPDIR`, `LANG`; and when the service runs as root, the binary is started under `nobody` (else `www-data`) via `SysProcAttr.Credential` — uid, gid **and** supplementary groups dropped (`buildUser`, resolved once at startup). Ordering matters: the input files are written while the directory is still `0700 root`, *then* it is chowned to the build account, and the output is opened with `O_NOFOLLOW` and accepted only if it is a regular file owned by that account — root never follows a link in a directory it has handed over. If neither account exists the build stays privileged and a warning is logged. Both `buildPage` and the studio's `Store.CompileCSS` go through this single function; the config argument is always parsed, whatever its origin.
2. **Localized fonts** (`fonts`, needs `WEBFONTS_DIR`): each `fonts.googleapis.com` `<link>` (`/css2` modern or `/css` legacy syntax, HTML-entity `&amp;` handled) is parsed (`parseGoogleFonts` — never via `url.Query()`, which drops `;`-containing params since Go 1.17), the requested TTFs are copied from the mirror into `public/np-fonts/<Family>/`, and the link becomes an inline `<style data-np-fonts>` of `@font-face` rules. Per-link graceful degradation: an unknown family keeps its Google link (logged); a missing variant is dropped as long as one remains; variable ranges (`100..900`) expand to the standard weights. `fonts.g*` preconnects are stripped once no Google link remains.
3. **PWA** (`pwa`): generates `sw.js` (versioned cache `np-<id>-<unix>`, network-first navigations with offline fallback, stale-while-revalidate assets, same-origin only), `manifest.webmanifest` (named after the domain, falls back to the id) and `np-icon.svg` (first letter of the id), and injects the manifest `<link>` + a registration snippet. Each piece steps aside if the site brings its own: root `sw.js`/`service-worker.js` ⇒ no generated SW and no registration injection; `manifest.webmanifest`/`manifest.json` ⇒ no generated manifest; a page already containing `serviceWorker.register` or a `rel=manifest` link is left alone.
4. **GDPR notice** (`gdpr`): injects a self-contained snippet before `</body>` — « Zéro stress : tout est local », OK button, link to nadaprod.com/legal — shown until dismissed (`localStorage['np-gdpr-ok']`). **i18n'd with embedded translations** (published sites can't reach the app's JSON files): at startup `gdprTranslations("./web/static/i18n")` reads the `gdpr.*` keys of every `<lang>.json` (built-in fr/en as fallback — adding a language file is enough, embedded at next start + rebuild), and `buildGDPRTag` inlines the dictionary (`json.Marshal` escapes `<>&`, so it can't break the surrounding HTML). Language resolution in the visitor's browser: `?lang=` GET param (persisted in `localStorage['np-lang']`) → stored choice → `navigator.language` → English.

The pure rewrite helpers are covered by table-driven tests: `build_test.go` (`extractTailwindConfig`, `rewriteTailwindHTML`, `insertBefore` — single-line documents, strict vs. mixed configs, CDN tag variants) and `fonts_test.go` (`parseGoogleFonts`, `variantFile`, `localizeFonts` against a fixture mirror — unknown family, missing variant, preconnect handling).

Known limit: compiled CSS is static — class names computed at runtime by JS (string concatenation) lose their styles; class names appearing literally anywhere in the file (including inline scripts) are covered by the content scan. The escape hatch is the per-site `compile` toggle.

---

## 7. Studio projects (`pkg/projects`)

One SQLite file (`DB_FILE`), opened with `journal_mode=WAL`, `foreign_keys=1`, `busy_timeout=5000` and `_txlock=immediate` (a write transaction takes its lock up front — no read→write upgrade deadlocks). Schema changes are numbered entries of the `migrations` slice, applied in a transaction each and tracked by `PRAGMA user_version`; migration 2 added `translations` (§13), so a future table (e.g. the v1.0 favourite-blocks `library`) is "append migration 3".

| Table | Columns | Notes |
|---|---|---|
| `projects` | `id` (16-char base32 of 10 random bytes; accepted shape `^[a-z0-9]{8,32}$`), `owner` (email), `name`, `brand`/`theme`/`seo`/`publish` (JSON objects, ≤ 16 KB each), `rev`, timestamps | `publish` = last deploy target (provider, site, url) — **never a token** |
| `blocks` | PK `(project_id, id)`, `type`, `position`, `html` (≤ 512 KB), `prompt`, `version_id` | block ids are client-supplied and stable (regex-checked); `ON DELETE CASCADE` |
| `block_versions` | autoincrement `id`, `(project_id, block_id)` FK, `html`, `prompt`, `source`, `created_at` | last `KeepVersions` = 30 per block |
| `translations` | PK `(project_id, lang, hash)`, `src`, `text`, `status` (`machine`\|`human`), `updated_at` | translation memory (§13); `ON DELETE CASCADE`; ≤ 8 KB per text, ≤ 2 000 per write, ≤ 20 000 rows per project |

Design choices (they also prepare v1.0 — real-time collaboration, server-side orchestrator):

- **Per-block writes, not whole-document overwrites.** `PUT …/blocks/:bid` upserts one block; `PUT /projects/:id` carries only meta + `order` (which also deletes the blocks it no longer lists).
- **Optimistic concurrency.** Every mutation bumps `projects.rev` and returns it; a request may carry the `rev` it knows — mismatch ⇒ `ErrConflict` ⇒ HTTP 409 `conflict`, nothing written. The studio serializes its writes in one queue and always sends `rev`, so two tabs cannot silently overwrite each other.
- **History is a list + a pointer.** A changed `html` appends a version and moves `blocks.version_id` to it (`source` ∈ generate, variant, retouch, retheme, code, edit, import, image). Undo/redo (`Step`) and restore (`Checkout`) only move the pointer. If a new version arrives while the pointer is *not* at the tail (you undid, then regenerated), the current version is first re-appended as a `restore` point: "undo" then returns to what was on screen, and the abandoned versions stay listed — nothing is ever lost.
- **Limits:** 200 projects per owner, 60 blocks per project (`MaxBlocksPerProject`, surfaced as `max_block` in `/api/studio/config`), names ≤ 120 chars, prompts silently truncated at 4 000, an unknown `source` coerced to `edit`, sizes above. Errors are sentinel values (`ErrNotFound`, `ErrConflict`, `ErrInvalid`, `ErrLimit`) mapped to 404/409/400/413 with a `code` by `projects.Fail(c, err)` (exported — the studio's translation routes reuse it).
- **Access:** `Store.Allowed(c, id)` — owner or root; others get 404. Exported because the export/publish routes of `pkg/studio` apply the same rule.

## 8. Studio API (`pkg/studio`)

`studio.Register(api, Deps{Gen, Translator, Projects, Images, Compile, FontsDir, RatePerMin, …})` — `main.go` injects the real parts (the Gemini client is both `Gen` and `Translator`), tests a fake generator / translator and mock provider URLs. The rate-limit check is one closure, `throttled(c)`, shared by generation and translation.

### 8.1 Streaming — `POST /api/generate/stream`

Validation and rate limiting happen **before** the stream opens (so those failures are ordinary JSON errors). Then: `Content-Type: text/event-stream`, `Cache-Control: no-cache, no-transform`, `X-Accel-Buffering: no`; events `delta` `{text}` (raw model text, flushed as it arrives), then `done` (a full `GenerateResponse` — cleaned HTML, photos resolved) or `error` `{error, code, retry_after?}`. **Behind a reverse proxy, response buffering must be off for this route** or the stream arrives in one piece (Caddy streams by default; nginx honours the header).

### 8.2 Rate limit — `limiter.go`

Token bucket per account: `GENERATE_RATE_PER_MIN` tokens, refilled continuously, burst = the same value. One token per generation — a "3 variants" click costs three. In-memory (reset on restart), which is fine for a single instance; past 10 000 tracked accounts the whole bucket map is wiped (everyone's remaining tokens reset — a bound, not a policy).

### 8.3 Fonts offered to the design system

`listFonts(FontsDir)` filters a curated list (~35 safe families: sans / display / serif / mono) down to those present in the local mirror, with the weights (400/500/600/700) it can actually serve. No mirror ⇒ empty list ⇒ the studio hides the font pickers.

## 9. Production export (`pkg/export`)

Input: `export.Page{Name, Brand, SEO, Theme, Blocks, Translations}` built from the stored project (translations loaded with it). Two outputs:

- `HTML(page, compiler)` — **one standalone file**: compiled + purged Tailwind CSS inline, SEO head, SVG favicon as a data URI. Theme fonts come from a Google Fonts `<link>` (the only option without side files; re-importing the file into NADAPROD localizes them through the publish pipeline).
- `Site(ctx, page, opts)` — a **static site**: `index.html`, `styles.css`, `favicon.svg`, `assets/` (theme fonts copied from the local mirror as TTF + `@font-face` prepended to `styles.css`; images downloaded and `src`s rewritten), and optionally `components/` (`jsx` or `vue`, source language only); with target languages, also `<lang>/index.html` per language (§13). `Zip(files)` is deterministic. This is also what gets published (§10).

If compilation is unavailable or fails, both degrade to the Play CDN + inline theme config and report it (`Result.Warnings`, `X-Export-Warnings` / `X-Export-Compiled` response headers).

### 9.1 Theme → Tailwind config — `TailwindConfig(theme)`

The design system becomes `tailwind.config = {theme:{extend:{colors:{primary,accent}, fontFamily:{display,sans}, borderRadius:{brand}}}};`. The colour scales are computed **once, in the browser** (`web/static/studio/theme.js` — the picked colour is the 600 stop) and stored with the project; the server only validates them. **Security:** this string is *executed* by the Tailwind binary, so it is produced by `json.Marshal` from values validated one by one (`#rrggbb`, shade names, font-family charset, radius enum) — never by concatenating client text. For the same reason `sites.Store.CompileCSS(page, cfgBody)` takes the config as an argument and, unlike `buildPage`, never extracts a `tailwind.config` script from the document (block HTML is user-editable).

### 9.2 SEO — `Page.normalize`

Empty fields are derived, nothing is invented: title = `brand — <h1 text>` (≤ 70 chars), description = the first `<p>` after the `<h1>`, else the brand tone (≤ 160). `lang` must look like `fr` / `en-US` (default `fr`), `langs` (targets) are filtered by `i18n.LangRe`, deduplicated, source removed, ≤ 12; `url` and `og_image` must be absolute http(s) URLs or are dropped. Emitted: `<title>`, description, canonical, `og:type/title/description/url/site_name/image`, `twitter:card`, `theme-color`, favicon (initial of the brand on the primary colour).

### 9.3 Asset download — no SSRF

`fetchAssets` only downloads from hosts the studio itself inserts (`placehold.co`, `images.pexels.com`), over https, ≤ 40 files, ≤ 8 MB each, image MIME types only. Any other URL is left as it is — the server never fetches an arbitrary user-supplied URL. (Unsplash photos stay hot-linked, as their guidelines require.)

### 9.4 Components — `ToJSX` / `ToVue` / `ComponentName`

Parsed with `x/net/html` (fragment, body context), re-serialized by one shared layout engine: `class`→`className` & co., kebab-case SVG attributes → camelCase, `style` strings → objects, boolean attributes, form `value/checked` → `defaultValue/defaultChecked`, comments, brace/entity escaping, void elements. `<script>`/`<style>`, inline `on*` handlers and `javascript:` URLs are removed and *reported in a header comment* (they must be re-implemented with state/effects). One component per block (`Hero.jsx`, `Hero2.jsx`…) + `Page.jsx`/`Page.vue`.

## 10. One-click publish (`pkg/publish`)

`POST /api/projects/:id/publish` builds the §9 site and hands the files to a provider with **the user's own token, supplied on every call — never stored, never logged**. Only the target of the last deploy is saved in `projects.publish` so the next publish redeploys to the same place.

- **Netlify** — documented ZIP deploy: `POST /sites` (new site) or `POST /sites/{site}/deploys`, then polling `GET /deploys/{id}` until `ready` (2 s interval, 90 s cap; a timeout while still processing returns success — the deploy continues server-side).
- **Cloudflare Pages** — the direct-upload flow `wrangler pages deploy` uses: project lookup/creation, `upload-token`, `check-missing`, batched base64 `upload`, `upsert-hashes`, then a multipart `deployments` call with the manifest. Asset hash = `hex(blake3(base64(content) + extension))[:32]`.
- Hard limits: 200 files / 25 MB total per deploy, `index.html` required at the site root; 60 s per HTTP request, responses read ≤ 8 MB; Cloudflare uploads are batched at 50 files / 40 MB of base64.
- Errors are `*publish.Error{Provider, Status, Code}` (`auth`, `not_found`, `rate_limited`, `invalid`, `upstream`); `ErrInvalidInput` for pre-network rejections. A provider `auth` failure is answered with **422**, not 401, so the browser does not mistake it for an expired NADAPROD session.

⚠ **Never exercised against the live services** (no credentials were available): both providers are covered by `httptest` mocks only, and the Cloudflare `/pages/assets/*` endpoints are not part of the officially documented API. Expect to adjust details on first real use.

## 12. Studio ⇄ imported sites (`pkg/sites/pages.go`, `pkg/export/split.go`)

The two worlds meet through pages of `dev/`: `export.PageCDN` assembles the studio page exactly like an imported source (Tailwind CDN + inline theme config, Google Fonts link, SEO head) so the publish pipeline treats it like any other page; `export.SplitPage` does the reverse — each element child of `<body>` becomes a block (`header`/`nav` → `header`, `footer` → `footer`, anything else → the generic `section` generator), trailing scripts are re-attached to the block before them while other body-level `<script>`/`<style>`/`<link>` children are dropped; from `<head>` only `title`, the meta description and `html[lang]` are read — its styles never enter the studio (which only renders Tailwind). `Store.SavePage` (validated relative `.html` path, `safeJoin`, `ErrPageExists` unless `overwrite`, background `RebuildPage`), `ReadPage`, `CopyPage`, `CreateSite` (empty `dev/`, slug id) and `BlankPage` back the routes. Access: `Deps.SiteAccess` = `users.CanAccess` — the studio project must be the caller's, *and* the site assigned to them (root: all). The studio remembers the target in `projects.publish` (`provider: "site"`), so the next save proposes the same page (with an overwrite warning) — `/editor?site=X&path=Y` opens a fresh project aimed at that page, `/editor?from=X&path=Y` imports it.

## 13. Studio i18n — one URL per language (`pkg/i18n`, `pkg/studio/i18n.go`, `pkg/export/i18n.go`)

Spec and rationale: `doc/I18N.md`. Translations are keyed by the **source text**, never by markup: block HTML stays monolingual (source = `seo.lang`, targets = `seo.langs`), so regenerating a block cannot break anything — unchanged text finds its translation again, new or changed text shows up as missing.

- **Segmentation (`pkg/i18n`).** `Extract(fragment)` walks the parsed fragment and returns `[]Segment{Hash, Src, Attr}` in document order, deduplicated. A segment is a run of sibling text + inline elements containing a letter; inline tags become numbered markers (`Lisez <1>nos conditions</1>.`, opaque elements such as `br`, `img`, `svg`, `code`, `input` → `<n/>`); text is escaped (`& < >`) and HTML whitespace collapsed (NBSP kept). Rules learned on real blocks: a run made of a single inline element is entered (`<a>Fermer</a>` ⇒ « Fermer »); a run with no text of its own between elements (`<span>Label</span> <span>Value</span>`, menu links) is split per element; inert edges (icons, empty status dots, `<br>`) stay outside the segment. Attributes `alt`, `title`, `placeholder`, `aria-label` (+ `value` of button inputs) are segments too. Skipped: `script`, `style`, `noscript`, `template`, `pre`, `textarea`, `iframe`, `object`, `canvas`, `translate="no"`, `.notranslate`. `Key(src)` = 16 hex of SHA-256 of the collapsed source. `Apply(fragment, lookup)` re-renders with each segment replaced by its translation when `Valid(src, dst)` (same multiset of markers, well nested) — the original elements (attributes, opaque subtrees) are cloned back around the translated text, text is always a text node (a translated `<script>` is just text), attributes are translated first; returns the sources left untranslated.
- **Storage (`pkg/projects`).** `Translations(ctx, pid)` (lang → hash → `{hash, src, text, status, updated_at}`, never nil) and `SaveTranslations(ctx, pid, lang, ts)` (one transaction, upsert, `text:""` deletes, validation of lang/hash/status/sizes, project cap). Writing translations does **not** bump `rev` — they sit beside the blocks and must not cause conflicts with someone editing the page.
- **Routes (`pkg/studio/i18n.go`, §1.1).** Segments are recomputed on every read from the current blocks plus the SEO title/description (`export.PlainSegment`, listed under block `"seo"`); only translations of segments still present are returned. The target language must be one of the project's (`Page.Languages()`), else 400 `invalid`. `PUT` validates hashes against the current segments and markers with `Valid`. `translate` picks missing segments (or the given `hashes`; `redo` also redoes `machine` ones — a `human` entry is never overwritten), at most `maxTranslateRun` = 200 per call (`remaining` tells the client how many are left), consumes one rate-limit token, calls the `Translator` (3 min budget), keeps only replies that pass `Valid` (others ⇒ `failed`) and stores them as `machine` — even when the call errored partway. `preview` returns `Page.Localized(lang)` block HTML.
- **Export (`pkg/export/i18n.go`).** `Page.Localized(lang)` translates blocks and SEO texts and sets `SEO.Lang` (source ⇒ unchanged page). `Site` emits the source at the root and `<lang>/index.html` per target: one compiled `styles.css` (compiled once), `../` paths for CSS, favicon and the assets it downloaded, `<html lang>`, `hreflang` alternates for every language + `x-default` (absolute when `SEO.URL` is known, relative otherwise), canonical / `og:url` per language, and a small inline-styled language switcher (fixed bottom-right, `nav aria-label`, `aria-current`) when there are ≥ 2 languages. One warning per language with gaps (`en : 3 texte(s) non traduit(s) — source conservée`). Publish uses the same `Site`; the site bridge writes `PageCDN(Localized(lang))` to `<lang>/<path>` (overwrite only with `overwrite`, otherwise a warning).
- **Limits.** Text written at runtime by a block's `<script>` is not extracted; identical source sentences share one translation; imported sites (`/sites`) keep their own home-made i18n (TODO).

## 14. Measurement for `/debate` (`pkg/stats`)

Spec: `doc/STATS.md`. The public `/debate` page (`web/debate.html`, registered with the other marketing pages) puts what the AI consumes next to what static publishing saves, from our own numbers.

- **Collection, never tracking.** `gemini.Options.OnUsage` fires once per logical call (block generation incl. stream, each translation batch — `Kind` = block type or `"translate"`, `OK=false` after a definitive failure; tokens from `usageMetadata`: `promptTokenCount`, `candidatesTokenCount + thoughtsTokenCount`, the last SSE chunk carrying them). `sites.Store.OnHit` fires after every request `HostMiddleware` serves on a mapped domain (status, bytes, duration, `html` = 2xx `text/html`, user-agent **only** to recognize a robot via `stats.IsBot`). Nothing identifying is stored: no IP, UA, cookie or URL — `hits_daily(day, site)` and `ai_daily(day, kind, model)` counters only.
- **Writes.** Hits are summed in memory and upserted every 60 s, on each `Summary` read, and on `Close` (main catches SIGINT/SIGTERM to flush before exiting); AI calls are upserted immediately. Own migrations (`PRAGMA user_version`), separate file from the projects DB.
- **`GET /api/stats`** (public, 60 s in-memory cache + `Cache-Control: max-age=60`): `{since, updated_at, ai:{requests, failures, prompt_tokens, output_tokens, ms, by_kind[], daily[30]}, static:{requests, pages, bots, bytes, page_us, daily[30]}, app:{projects, blocks, versions{source:n}, translations{status:n}, sites, live_sites, pages}}` — `app` comes from `projects.Store.Counts` + `sites.Store.Counts`; no per-site data. **`GET /api/stats/sites`** (root): per-site totals + 30-day series.

## 11. Stock photos (`pkg/images`)

`images.FromEnv` picks Pexels (`PEXELS_API_KEY`) or Unsplash (`UNSPLASH_ACCESS_KEY`); neither ⇒ a disabled service (every call is a no-op). `Service.Search` caches by (normalized query, orientation) for 24 h, 500 entries. `Service.Resolve(ctx, fragment)` rewrites, byte-for-byte outside the touched tags, the `src` of `<img data-photo="…">` **placeholders** (an image whose `src` is already real is never re-resolved — the model echoes existing `src`s when iterating): orientation inferred from the `placehold.co/WxH` size, deterministic-but-varied pick, no duplicate photo within a fragment, `data-photo-credit` + `loading="lazy"` added, ≤ 12 images per fragment (page resolution: 8 per page, 4 searches in parallel, 20 s budget; small placeholders ≤ 160 px are searched as `square` avatars), searches in parallel, never blocking (a failed search keeps the placeholder). `/api/images/search` returns 18 results with a 15 s timeout. ⚠ Tested against mocks only. Provider guidelines beyond the code (Unsplash download pings, visible credits) are not implemented.

---

## 5. Security model (current state)

- **Session auth on every app surface** (§0): pages redirect to `/login`, `/api` returns 401. Root-only mutations (import, delete, domain, accounts) stack `RequireRoot`; site-scoped routes check `CanAccess`. Public remains: landing/marketing pages, `/login`, `/waiting-list`, `GET /api/health`, and everything served on a mapped domain.
- Passwords bcrypt-hashed (root's env password hashed once at startup, never stored). Failed logins sleep 300 ms. Cookies: `HttpOnly`, `SameSite=Lax`, `Secure` behind TLS or `X-Forwarded-Proto: https`.
- Path traversal centrally guarded by `safeJoin` (upload names, serve paths, save paths, delete IDs), with `validSiteID` on every `:id` route as defense-in-depth.
- Upload caps: 200 MB compressed, 1 GB decompressed (zip-bomb budget counted on bytes actually written).
- Domain claims are validated against a hostname grammar (`validDomain`, root-only route) but ownership is not verified (no DNS TXT check — see TODO if domain attach is ever opened to users).
- System prompts never reach the client (`json:"-"`).
- **Studio preview is sandboxed**: the iframe has `sandbox="allow-scripts"` without `allow-same-origin` — AI-generated HTML/JS runs in an opaque origin, with no access to the session cookie, the API or the parent DOM; the studio authenticates frame messages by `event.source`.
- Studio projects are owner-scoped (404 for anyone else); generation is rate-limited per account; publish tokens are never stored server-side; the export never fetches arbitrary URLs (§9.3) and never executes client-supplied config (§9.1).
- **Page content is never executed server-side** (fixed 2026-09-19; it used to be — the inline `tailwind.config` script went verbatim into the file the Tailwind CLI evaluates, reachable by any account that could save a page). Now: `ParseTailwindConfig` accepts data literals only and re-emits JSON (§4.7), and the CLI runs confined and unprivileged. Tests prove non-execution (`TestCompileTailwindNeverExecutesPageScript`: refused configs never even start the binary) and the identity/environment of the child (`TestCompileTailwindConfinement`). Done the same day: the systemd unit runs as `User=www-data`, so the service itself is no longer root either.
- TLS is out of scope: production expects Caddy (`on_demand_tls`) or similar in front.

## 6. Build & deploy

`bin/build.sh` — downloads the Tailwind v3.4.17 standalone binary to `bin/tailwindcss` if absent (gitignored; failure is non-fatal — local compile just stays off), then compiles the app pages' CSS (`web/static/tw.css` from `tools/appcss/`, skipped if the binary is missing), then `go build -ldflags "-s -w" -o nadaprod ./cmd/nadaprod` from the repo root (`full` argument adds `go get -u ./... && go mod tidy`), then the `chown -Rf www-data.www-data` of the tree, an optional `gigit "$2"` commit when a second argument is given, and `systemctl stop/start/status nadaprod`. The service reads config from real env vars or the `.env` next to the binary's working directory — **`ROOT_PASSWORD` must be in the production `.env` before deploying the auth build** (the server refuses to start without it, and `Restart=always` would loop). **Since the studio build (deployed 2026-09-19):** the server creates `DB_FILE` (`./nadaprod.db`) on first start — include it in backups; `/editor` serves `web/studio.html`; the unit runs as `User=www-data` and `bin/build.sh` chowns the tree to `www-data` before restarting the service (the SQLite file, `users.json`, `waiting-list.json` and `sites/` must stay writable by that account); the Tailwind CLI inherits that unprivileged identity (the `nobody` drop of §4.7 only kicks in when the service itself is root); if a reverse proxy sits in front, make sure it does not buffer `POST /api/generate/stream`. Tests: `go test ./pkg/...`; see TODO.md for the remaining intended targets. Day-to-day delivery goes through `bin/next.sh` (front staged in `next/web/`, see CLAUDE.md); migration 2 (`translations`) is applied automatically at the first start of a new binary. The webfonts mirror deploys separately (gitignored): unpack `web/webfonts.tgz` on the server (the « My Webfonts » page is regenerated by `tools/mywebfont`).
