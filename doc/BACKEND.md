# BACKEND.md — NADAPROD server technical documentation

Go 1.25 · [gin](https://github.com/gin-gonic/gin) v1.12 · single binary, no database. Standard layout: `cmd/nadaprod/main.go` + `pkg/{auth,gemini,prompts,sites}` (import path `nadaprod/pkg/<name>`):

| File | Lines | Role |
|---|---|---|
| `cmd/nadaprod/main.go` | ~200 | Wiring: config, middleware chain, static routes, block-studio API |
| `pkg/auth/` — `store.go` `session.go` `waitlist.go` `routes.go` | ~460 | Accounts (root + users), HMAC sessions, waiting list |
| `pkg/gemini/` — `client.go` `message.go` | ~260 | Gemini REST client, request types (`Brand`, `GenerateRequest/Response`) + output sanitizing |
| `pkg/prompts/` — `blocks.go` | ~245 | Block registry: one expert system prompt per block type |
| `pkg/sites/` — `store.go` `validate.go` `extract.go` `serve.go` `routes.go` | ~690 | Imported sites: storage model, input validation, archive extraction, domain serving, HTTP API |
| `pkg/sites/` — `build.go` `fonts.go` | ~570 | Publish pipeline: local Tailwind compile, Google Fonts localization, PWA, GDPR notice |

Renames with the package split: `sites.Store`/`sites.NewStore`/`sites.Meta` (ex-`SiteStore`/`NewSiteStore`/`SiteMeta`), `gemini.Client`/`gemini.NewClient` (ex-`GeminiClient`). Dependencies: `cmd/nadaprod` → all packages; `pkg/sites` → `pkg/auth` (route authorization); `pkg/gemini` → `pkg/prompts`. The rest of this document uses the historical flat names in prose — same code, new homes.

Tests: `pkg/sites/{build,fonts,sites}_test.go`, `pkg/auth/auth_test.go` — run with `go test ./pkg/...` (never bare `./...`, the stray `books/` directory pollutes it; same for `go vet ./cmd/... ./pkg/...`).

Direct dependencies: `gin-gonic/gin`, `joho/godotenv`, `golang.org/x/crypto` (bcrypt). Everything else in `go.sum` is transitive. One optional external tool: the Tailwind v3 **standalone binary** (`TAILWIND_BIN`, no Node runtime), fetched by `bin/build.sh`.

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

`POST /waiting-list` (public): `{name, email, message, website}` — `website` is a **honeypot** (filled ⇒ `{ok:true}` but nothing stored). Valid entries are appended to `WAITLIST_FILE` with date + IP, deduplicated by email, capped at 5 000 entries (beyond: accepted silently). Root reads them via `GET /api/waiting-list` (shown in the « Comptes » card of `/sites`).

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
   | `GIN_MODE` | *(empty)* | Empty ⇒ `gin.ReleaseMode` is forced; any value ⇒ gin's own env handling (`debug` for verbose logs) |

3. Construct `GeminiClient` and `SiteStore` (which migrates any legacy `public/`-only site to the `dev/` layout — §4.1 — then does an initial `reloadDomains()`), and launch `store.BuildAll()` in a goroutine (§4.7). Then `UserStore` (§0) — root's env password is bcrypt-hashed once here.
4. Build the router: `gin.New()` + `gin.Logger()` + `gin.Recovery()`.
5. **`store.HostMiddleware()` is installed before everything else** — see §4.3. This is load-bearing: on a mapped domain, the editor and API are deliberately unreachable — and published sites never see the login.
6. Public statics + public routes (`/login`, `/waiting-list`, `POST /waiting-list`, `POST /api/login`, `GET /api/health`), then the authed groups: `r.Group("/", users.RequireAuth())` for app pages and `api.Group("", users.RequireAuth())` for the rest of `/api`. `NoRoute` → `web/404.html`.
7. `r.Run(addr)`.

`r.MaxMultipartMemory = 32 << 20` (32 MB in-RAM threshold for multipart parsing; larger uploads spill to temp files — distinct from the 200 MB upload cap in §4.4).

### 1.1 Route map

Auth column: **pub** = no session needed · **auth** = valid session (`RequireAuth`) · **site** = auth + site assigned to the account (`allowed()`/`CanAccess`) · **root** = auth + `RequireRoot`.

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
| `GET /editor` `/sites` `/drafts` | auth | app pages |
| `POST /api/logout` · `GET /api/me` | auth | close session · `{email, root, sites}` |
| `GET /api/blocks` | auth | public block catalog (§3) |
| `POST /api/generate` | auth | block generation (§2) |
| `GET /api/sites` | auth | list **accessible** sites `[{id, domain, pages[], settings}]` |
| `GET /edit/:id/*filepath` | site | site page **with** editor injection (§4.5) |
| `GET /preview/:id/*filepath` | site | site page **without** injection |
| `PUT /api/sites/:id/file` | site | rewrite one `.html` file (in `dev/`) + background republish of that page |
| `PUT /api/sites/:id/settings` | site | publication settings `{compile, fonts, pwa, gdpr}` + background rebuild |
| `POST /api/sites` | root | archive upload (§4.4) |
| `PUT /api/sites/:id/domain` | root | attach/detach a domain |
| `DELETE /api/sites/:id` | root | delete a site directory |
| `GET/POST /api/users` · `PUT/DELETE /api/users/:email` | root | account management (§0.2) |
| `GET /api/waiting-list` | root | read the candidacies |
| *anything else* | pub | `web/404.html` with HTTP status 404 |

### 1.2 Generation API contract

`POST /api/generate` — request (`GenerateRequest`):

```json
{
  "block": "hero",                  // required; a blockSpecs type or "element"
  "prompt": "…",                    // user brief, may be empty
  "brand": { "name": "", "colors": "", "tone": "" },
  "current_html": ""                // present ⇒ iterate instead of regenerate
}
```

Response (`GenerateResponse`): `{ "html", "block", "model", "elapsed_ms" }`.
Errors: 400 (bad JSON / unknown block type), 502 (Gemini failure), messages in French.

---

## 2. Gemini client (`gemini.go`)

`GeminiClient` holds `apiKey`, `model`, and an `http.Client` with a **90 s timeout**. Endpoint: `POST https://generativelanguage.googleapis.com/v1beta/models/<model>:generateContent`, auth via `x-goog-api-key` header.

### 2.1 Request assembly — `GenerateBlock(ctx, spec, req)`

- `systemInstruction` = the block's `SystemPrompt` (never leaves the server otherwise).
- One user message built by `buildUserMessage`:
  1. `Brief du bloc « <label> » :` + the prompt (or a "propose a polished variant" fallback when empty);
  2. optional brand context (name / palette / tone lines, only non-empty fields);
  3. optional current version fenced as ` ```html … ``` ` with the instruction to evolve it rather than start over;
  4. final reminder: *"Réponds UNIQUEMENT avec le fragment HTML final."*
- `generationConfig`: `temperature 0.7`, `maxOutputTokens 8192` (hardcoded).

### 2.2 Retry & error handling

Two attempts total (1 retry). Retried cases: transport error, body-read error, HTTP 429, HTTP ≥ 500 — with a linear `(attempt+1) s` sleep. Non-retried: JSON decode failure, API-level `error` object, empty candidates (safety-filtered ⇒ "réponse vide"). Client cancellation propagates through `ctx` into the HTTP request (but not into the inter-attempt sleep).

### 2.3 Output sanitizing — `cleanHTML`

The model contract is "fragment only"; this is the safety net:
1. Trim whitespace.
2. If a ` ```html … ``` ` fence matches (`fenceRe`, dot-all), keep only its inside.
3. Drop any text before the first `<` and after the last `>`.
4. Trim again.

It does **not** validate single-rootness or parse the HTML (see TODO).

---

## 3. Block registry (`prompts.go`)

```go
type BlockSpec struct {
    Type, Label, Icon, Description string
    Presets []string   // quick prompts shown in the editor
    Hidden  bool       // true ⇒ not in the palette (internal use)
    SystemPrompt string `json:"-"`  // NEVER serialized to the client
}
```

- `basePrompt` — the shared non-negotiable output contract: fragment only, single root, Tailwind v3 utilities only (no `<style>`/custom CSS), images via `placehold.co` or inline SVG, inline-SVG icons, AA accessibility, mobile-first 360–1440 px, real French copywriting, JS only where the specialization allows (one minimal idempotent `<script>`), `href="#"` for fake links.
- `blockSpecs` — the source of truth. **Adding a block type = appending one entry**; it appears in the editor palette automatically. Current types: `header` (mobile menu JS allowed), `hero`, `features`, `pricing`, `testimonials`, `faq` (native `<details>`), `cta`, `footer` — each with 4 presets and a specialization appended to `basePrompt`.
- `elementSpec` (`Hidden: true`, type `element`) — the "DOM surgeon" used by the imported-sites editor: retouch ONE existing element, preserve everything the brief doesn't mention, adapt to the page's own stack (Tailwind or inline styles), never introduce new dependencies, no `<script>`/`<style>`.
- `blockIndex` — package-init map of type → spec (blocks + element).
- `GetBlockSpec(t)` resolves for generation; `BlockCatalog()` returns `blockSpecs` for `GET /api/blocks` — Hidden entries are included in JSON but filtered client-side, and `SystemPrompt` is excluded by the `json:"-"` tag.

---

## 4. Imported sites (`sites.go`)

### 4.1 Storage model — the filesystem is the database

```
sites/<id>/site.json   → { "domain": "example.com", "compile": …, "fonts": …, "pwa": …, "gdpr": … }
sites/<id>/dev/…       → the editable source (upload target, edit/save/preview)
sites/<id>/public/…    → the published build output (build.go), fully regenerable
```

The publication settings are `*bool` — `nil` means **on** (the zero-effort default); `SiteMeta.CompileOn()/PWAOn()/GDPROn()` resolve them. `migrateLayout()` (at store construction) renames a legacy `public/`-only site to `dev/`; `srcDir(id)` returns `dev/` when it exists and falls back to `public/` for an unmigrated site (in which case `BuildSite` refuses to run — the source is never overwritten by a build).

`SiteStore{ root, twBin, mu sync.RWMutex, domains map[host]siteID, buildLocks sync.Map }`. The `domains` map is rebuilt by `reloadDomains()` (scan every `site.json`) at startup and after any domain change or site deletion. Reads take `RLock` (hot path: every request), writes take `Lock`. `buildLocks` holds one mutex per site to serialize builds (§4.7).

### 4.2 Path safety — `safeJoin(base, p)`

Every filesystem path derived from user input goes through it: normalizes `\` → `/`, `path.Clean("/"+p)` (collapses `..`), joins under `base`, then rejects anything that escapes `base`. Used by: archive extraction (zip-slip), `serveStatic`, `serveEdit`, `PUT file`, `DELETE`. **Never build paths with raw `filepath.Join` on request data.**

### 4.3 Domain routing — `HostMiddleware`

On every request: `hostname()` strips the port and lowercases `c.Request.Host`; on a `domains` map hit, `serveStatic` serves the site (directory ⇒ `index.html` fallback; 404 as plain text) and `c.Abort()` stops the chain. No match ⇒ `c.Next()`. Consequence: **a mapped domain serves only the static site** — editor, API, everything else is short-circuited.

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

- `PUT /api/sites/:id/file` `{path, html}` — only `.html` paths accepted; `safeJoin` under `dev/` then a plain `os.WriteFile` (no backup, no versioning), followed by `go RebuildPage(id, path)` — the save responds instantly, the published page follows a few seconds later. This endpoint is shared by the visual save (serialized DOM) and the Monaco code editor.
- `PUT /api/sites/:id/domain` `{domain}` — normalized by `hostname()`, validated by `validDomain` (FQDN-only regex: alphanumeric labels, alphabetic TLD, ≤ 253 chars — no IPs, no single labels), merged into `site.json` (publication settings preserved), `reloadDomains()` ⇒ effective immediately, then `go BuildSite(id)` (the PWA manifest is named after the domain). Empty string detaches (not validated).
- `PUT /api/sites/:id/settings` `{compile?, fonts?, pwa?, gdpr?}` — partial update (absent fields untouched), merged into `site.json`, `go BuildSite(id)`. Response echoes the resolved settings.
- `DELETE /api/sites/:id` — `validSiteID` + `safeJoin` guard (+ explicit `target != root` check), `os.RemoveAll`, `reloadDomains()`.

Every `:id` route additionally validates the id shape with `validSiteID` (`^\.?[a-z0-9][a-z0-9._-]*$`, ≤ 128 — leading dot allowed for hidden sites) inside `allowed()` — defense-in-depth alongside `safeJoin`, which stays authoritative.

### 4.7 Publish pipeline — `build.go`

`BuildSite(id)` (serialized per site by `buildLocks`) wipes `public/` and regenerates it from `dev/`; only transformed files are written, everything else is served by the §4.3 fallback. During the rebuild window the fallback serves the source (CDN version) — no downtime. Triggers: startup (`BuildAll`, goroutine), upload, per-page save (`RebuildPage`), domain and settings changes. Four steps per page, each toggleable via `site.json`:

1. **Local Tailwind compile** (`compile`, needs `TAILWIND_BIN`): if the page loads `cdn.tailwindcss.com`, the inline `tailwind.config` script (if any) is extracted (`extractTailwindConfig`) and evaluated by the standalone CLI (real JS config: `var(--x)` strings, font arrays… all work), the CLI runs per page with `--content <that page>` (configs differ per page in the observed corpus) + `--minify`, and `rewriteTailwindHTML` swaps the CDN tag for an inline `<style data-np-tailwind>`. A config-only script is removed; a script mixing config with other code is kept and neutralized (`window.tailwind` stub). Extra CDN tags are dropped. **Any compile error ⇒ the page keeps its CDN** (logged, graceful). ~2–3 s per page (CLI startup dominates), hence the async triggers.
2. **Localized fonts** (`fonts`, needs `WEBFONTS_DIR`): each `fonts.googleapis.com` `<link>` (`/css2` modern or `/css` legacy syntax, HTML-entity `&amp;` handled) is parsed (`parseGoogleFonts` — never via `url.Query()`, which drops `;`-containing params since Go 1.17), the requested TTFs are copied from the mirror into `public/np-fonts/<Family>/`, and the link becomes an inline `<style data-np-fonts>` of `@font-face` rules. Per-link graceful degradation: an unknown family keeps its Google link (logged); a missing variant is dropped as long as one remains; variable ranges (`100..900`) expand to the standard weights. `fonts.g*` preconnects are stripped once no Google link remains.
3. **PWA** (`pwa`): generates `sw.js` (versioned cache `np-<id>-<unix>`, network-first navigations with offline fallback, stale-while-revalidate assets, same-origin only), `manifest.webmanifest` (named after the domain, falls back to the id) and `np-icon.svg` (first letter of the id), and injects the manifest `<link>` + a registration snippet. Each piece steps aside if the site brings its own: root `sw.js`/`service-worker.js` ⇒ no generated SW and no registration injection; `manifest.webmanifest`/`manifest.json` ⇒ no generated manifest; a page already containing `serviceWorker.register` or a `rel=manifest` link is left alone.
4. **GDPR notice** (`gdpr`): injects a self-contained snippet before `</body>` — « Zéro stress : tout est local », OK button, link to nadaprod.com/legal — shown until dismissed (`localStorage['np-gdpr-ok']`). **i18n'd with embedded translations** (published sites can't reach the app's JSON files): at startup `gdprTranslations("./web/static/i18n")` reads the `gdpr.*` keys of every `<lang>.json` (built-in fr/en as fallback — adding a language file is enough, embedded at next start + rebuild), and `buildGDPRTag` inlines the dictionary (`json.Marshal` escapes `<>&`, so it can't break the surrounding HTML). Language resolution in the visitor's browser: `?lang=` GET param (persisted in `localStorage['np-lang']`) → stored choice → `navigator.language` → English.

The pure rewrite helpers are covered by table-driven tests: `build_test.go` (`extractTailwindConfig`, `rewriteTailwindHTML`, `insertBefore` — single-line documents, strict vs. mixed configs, CDN tag variants) and `fonts_test.go` (`parseGoogleFonts`, `variantFile`, `localizeFonts` against a fixture mirror — unknown family, missing variant, preconnect handling).

Known limit: compiled CSS is static — class names computed at runtime by JS (string concatenation) lose their styles; class names appearing literally anywhere in the file (including inline scripts) are covered by the content scan. The escape hatch is the per-site `compile` toggle.

---

## 5. Security model (current state)

- **Session auth on every app surface** (§0): pages redirect to `/login`, `/api` returns 401. Root-only mutations (import, delete, domain, accounts) stack `RequireRoot`; site-scoped routes check `CanAccess`. Public remains: landing/marketing pages, `/login`, `/waiting-list`, `GET /api/health`, and everything served on a mapped domain.
- Passwords bcrypt-hashed (root's env password hashed once at startup, never stored). Failed logins sleep 300 ms. Cookies: `HttpOnly`, `SameSite=Lax`, `Secure` behind TLS or `X-Forwarded-Proto: https`.
- Path traversal centrally guarded by `safeJoin` (upload names, serve paths, save paths, delete IDs), with `validSiteID` on every `:id` route as defense-in-depth.
- Upload caps: 200 MB compressed, 1 GB decompressed (zip-bomb budget counted on bytes actually written).
- Domain claims are validated against a hostname grammar (`validDomain`, root-only route) but ownership is not verified (no DNS TXT check — see TODO if domain attach is ever opened to users).
- System prompts never reach the client (`json:"-"`).
- TLS is out of scope: production expects Caddy (`on_demand_tls`) or similar in front.

## 6. Build & deploy

`bin/build.sh` — downloads the Tailwind v3.4.17 standalone binary to `bin/tailwindcss` if absent (gitignored; failure is non-fatal — local compile just stays off), then compiles the app pages' CSS (`web/static/tw.css` from `tools/appcss/`, skipped if the binary is missing), then `go build -ldflags "-s -w" -o nadaprod ./cmd/nadaprod` from the repo root (`full` argument adds `go get -u ./... && go mod tidy`), then `systemctl stop/start/status nadaprod`. The service reads config from real env vars or the `.env` next to the binary's working directory — **`ROOT_PASSWORD` must be in the production `.env` before deploying the auth build** (the server refuses to start without it, and `Restart=always` would loop). Tests: `go test ./pkg/...`; see TODO.md for the remaining intended targets. The webfonts mirror deploys separately (gitignored): unpack `web/webfonts.tgz` on the server (the « My Webfonts » page is regenerated by `tools/mywebfont`).
