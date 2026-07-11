# BACKEND.md — NADAPROD server technical documentation

Go 1.25 · [gin](https://github.com/gin-gonic/gin) v1.12 · single binary, no database. Four source files, all `package main`:

| File | Lines | Role |
|---|---|---|
| `main.go` | ~150 | Wiring: config, middleware chain, static routes, block-studio API |
| `gemini.go` | ~185 | REST client for Gemini `generateContent` + output sanitizing |
| `prompts.go` | ~245 | Block registry: one expert system prompt per block type |
| `sites.go` | ~440 | Imported-sites feature: upload, edit, save, domain routing |

Direct dependencies: `gin-gonic/gin`, `joho/godotenv`. Everything else in `go.sum` is transitive.

---

## 1. Startup & configuration (`main.go`)

Order of operations in `main()`:

1. `godotenv.Load()` — loads `.env` if present. Real environment variables always win (godotenv never overwrites). A missing file is not an error.
2. Read config:

   | Variable | Default | Behavior |
   |---|---|---|
   | `GEMINI_API_KEY` | — | **Required**: `log.Fatal` if empty |
   | `GEMINI_MODEL` | `gemini-flash-latest` | Google alias for the latest stable Flash |
   | `SITES_DIR` | `./sites` | Root of imported-sites storage (created if absent) |
   | `ADDR` | `:8080` | HTTP listen address |
   | `GIN_MODE` | *(empty)* | Empty ⇒ `gin.ReleaseMode` is forced; any value ⇒ gin's own env handling (`debug` for verbose logs) |

3. Construct `GeminiClient` and `SiteStore` (which does an initial `reloadDomains()`).
4. Build the router: `gin.New()` + `gin.Logger()` + `gin.Recovery()`.
5. **`store.HostMiddleware()` is installed before everything else** — see §4.3. This is load-bearing: on a mapped domain, the editor and API are deliberately unreachable.
6. Static routes, `/api` group, `NoRoute` → `web/404.html`.
7. `r.Run(addr)`.

`r.MaxMultipartMemory = 32 << 20` (32 MB in-RAM threshold for multipart parsing; larger uploads spill to temp files — distinct from the 200 MB upload cap in §4.4).

### 1.1 Route map

| Route | Serves |
|---|---|
| `GET /` | `web/home.html` — public landing |
| `GET /editor` | `web/index.html` — block studio |
| `GET /sites` | `web/sites.html` — imported-sites manager |
| `GET /about` `/legal` `/opensource` `/privacy` `/solutions` `/terms` `/robots.txt` | static pages |
| `GET /gfx/*` `/static/*` | asset directories (`web/gfx`, `web/static`) |
| `GET /edit/:id/*filepath` | site page **with** editor injection (§4.5) |
| `GET /preview/:id/*filepath` | site page **without** injection |
| `GET /api/health` | `{ok, model}` |
| `GET /api/blocks` | public block catalog (§3) |
| `POST /api/generate` | block generation (§2) |
| `POST /api/sites` | archive upload (§4.4) |
| `GET /api/sites` | list sites `[{id, domain, pages[]}]` |
| `PUT /api/sites/:id/file` | rewrite one `.html` file |
| `PUT /api/sites/:id/domain` | attach/detach a domain |
| `DELETE /api/sites/:id` | delete a site directory |
| *anything else* | `web/404.html` with HTTP status 404 |

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
sites/<id>/site.json   → { "domain": "example.com" }   (one line, optional)
sites/<id>/public/…    → the uploaded site, served as-is
```

`SiteStore{ root, mu sync.RWMutex, domains map[host]siteID }`. The only in-memory state is the `domains` map, rebuilt by `reloadDomains()` (scan every `site.json`) at startup and after any domain change or site deletion. Reads take `RLock` (hot path: every request), writes take `Lock`.

### 4.2 Path safety — `safeJoin(base, p)`

Every filesystem path derived from user input goes through it: normalizes `\` → `/`, `path.Clean("/"+p)` (collapses `..`), joins under `base`, then rejects anything that escapes `base`. Used by: archive extraction (zip-slip), `serveStatic`, `serveEdit`, `PUT file`, `DELETE`. **Never build paths with raw `filepath.Join` on request data.**

### 4.3 Domain routing — `HostMiddleware`

On every request: `hostname()` strips the port and lowercases `c.Request.Host`; on a `domains` map hit, `serveStatic` serves the site (directory ⇒ `index.html` fallback; 404 as plain text) and `c.Abort()` stops the chain. No match ⇒ `c.Next()`. Consequence: **a mapped domain serves only the static site** — editor, API, everything else is short-circuited.

### 4.4 Upload pipeline — `POST /api/sites`

1. Multipart field `archive`, read via `io.LimitReader(maxUploadBytes+1)`; > 200 MB (`maxUploadBytes = 200 << 20`) ⇒ 400.
2. `slugify(filename)` (lowercase, strip `.zip`/`.tar.gz`/`.tgz`, non-alphanumerics → `-`) becomes the site ID; the ID is claimed atomically with `os.Mkdir`, retrying with `-2`, `-3`, … suffixes on collision.
3. `extractArchive` sniffs **magic bytes**: `PK` ⇒ zip, `1f 8b` ⇒ gzip/tar.gz — extension is ignored. Extraction (`extractZip` / `extractTarGz`) skips directories, `__MACOSX`, `.DS_Store` (`isJunk`), routes every entry name through `safeJoin`, writes with `writeFile` (mkdir-p + copy).
4. `flattenSingleDir` — if the archive contained exactly one top-level directory, its content is lifted so `index.html` sits at `public/` root (rename to `dir.tmp` → remove empty dir → rename back).
5. `listPages` (WalkDir for `*.html`, `index.html` sorted first). Zero pages ⇒ the site directory is removed and the upload rejected.
6. Response: `{id, pages[]}`.

### 4.5 Edit mode — `serveEdit` (`/edit/:id/*filepath`)

Serves the **real** page, with editing tools spliced in: non-`.html` assets pass through untouched (so the site's relative paths work inside the iframe); for HTML, `injectTag` — a `<link id="__wp_style">` + `<script id="__wp_inject" defer>` pointing at `/static/inject.{css,js}` — is inserted before the first `</body>` (case-insensitive regex; appended at EOF if no `</body>`). `/preview/:id/*filepath` is the same file without injection; the code editor uses it as its raw-content source.

### 4.6 Mutations

- `PUT /api/sites/:id/file` `{path, html}` — only `.html` paths accepted; `safeJoin` then a plain `os.WriteFile` (no backup, no versioning). This endpoint is shared by the visual save (serialized DOM) and the Monaco code editor.
- `PUT /api/sites/:id/domain` `{domain}` — normalized by `hostname()`, written to `site.json`, `reloadDomains()` ⇒ effective immediately. Empty string detaches.
- `DELETE /api/sites/:id` — `safeJoin` guard (+ explicit `target != root` check), `os.RemoveAll`, `reloadDomains()`.

---

## 5. Security model (current state)

- **No authentication anywhere.** Anyone who can reach the server can generate (spend Gemini quota), upload, edit, delete sites, and claim any domain. Acceptable for a private single-user deployment behind a reverse proxy; **not** for public exposure — see TODO before publishing.
- Path traversal centrally guarded by `safeJoin` (upload names, serve paths, save paths, delete IDs).
- Upload cap 200 MB (compressed); decompressed size is currently unbounded (zip-bomb exposure, see TODO).
- Domain claims are not verified (no DNS/ownership check) and not validated against a hostname grammar.
- System prompts never reach the client (`json:"-"`).
- TLS is out of scope: production expects Caddy (`on_demand_tls`) or similar in front.

## 6. Build & deploy

`bin/build.sh` — `go build -ldflags "-s -w" -o nadaprod .` from the repo root (`full` argument adds `go get -u ./... && go mod tidy`), then `systemctl stop/start/status nadaprod`. The service reads config from real env vars or the `.env` next to the binary's working directory. There are **no tests yet** (`go vet ./...` is the only check; see TODO.md for the intended test targets).
