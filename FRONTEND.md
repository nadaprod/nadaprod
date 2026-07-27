# FRONTEND.md — NADAPROD frontend technical documentation

Everything under `web/`, served as plain static files by gin — **no bundler, no framework, no build step**. Each page is self-contained (inline `<script>`/`<style>`); the only shared JS on disk is the injected editor (`web/static/inject.js` + `inject.css`). All product UI copy is in French — keep that convention.

Three application surfaces + a set of static marketing/legal pages:

| File | Route | Role |
|---|---|---|
| `web/home.html` | `/` | Public landing (marketing, FR/EN, light/dark) — « Le Signal » form posts to `/waiting-list` |
| `web/login.html` | `/login` | Login page (public) — `POST /api/login`, then redirect to `?next` (internal paths only) or `/sites` |
| `web/waiting-list.html` | `/waiting-list` | Public application form (name/email/project + honeypot) → `POST /waiting-list` |
| `web/index.html` | `/editor` | Block studio — the app *(requires session)* |
| `web/sites.html` | `/sites` | Imported-sites manager & visual/code editor host *(requires session)* |
| `web/drafts.html` | `/drafts` | Same manager, filtered to sites **without** a domain (drafts) *(requires session)* |
| `web/static/inject.js` `.css` | *(injected)* | In-page editor for imported sites |
| `web/about,legal,privacy,terms,solutions,opensource,404.html` | *(matching routes)* | Static brand/legal pages |
| `web/gfx/` | `/gfx/*` | Logo, favicon, images |

Auth: unauthenticated page loads are redirected server-side to `/login?next=…`; `sites.html` additionally boots on `GET /api/me` and self-redirects on 401.

External CDNs (the only network dependencies): Tailwind Play CDN (`cdn.tailwindcss.com`), Google Fonts (Space Grotesk / Inter / JetBrains Mono), and Monaco from jsDelivr (`monaco-editor@0.52.2`, lazy-loaded).

Shared conventions in `/editor` and `/sites`: identical inline `tailwind.config` (custom `ink` gray scale + `vio` violet accents, `font-display/sans/mono`), identical `toast(msg, ok)` snackbar, identical `loadMonaco()` loader, `$ = document.querySelector`, a single module-level state object `S`, event delegation through one `document` click listener with `data-*` attributes.

---

## 1. Block studio — `web/index.html`

### 1.1 Layout

Top bar (logo, viewport toggle Bureau/Tablette/Mobile, "Copier la page", "Exporter le HTML") · left rail (block palette filled from `GET /api/blocks` + page structure list) · central canvas (preview `<iframe id="preview">` sized by viewport, empty-state overlay) · right panel with three tabs: **Générer / Code / Marque**.

### 1.2 State & persistence

```js
S = { catalog: [],            // BlockSpec subset from /api/blocks
      page: [{id, type, html, prompt}], // ordered blocks
      selectedId, viewport: 'desktop'|'tablet'|'mobile', generating }
```

- `save()` → `localStorage['nadaprod'] = {page, brand:{name,colors,tone}}` on every render and brand input.
- `restore()` reads `'nadaprod'`, **falling back to the legacy `'windpage'` key** (pre-rename projects stay readable; first save rewrites under the new key).
- The server is stateless for this flow — the project never leaves the browser except as generation context.

### 1.3 Rendering (full re-render, no virtual DOM)

`renderAll()` = `renderStructure()` + `renderPreview()` + `renderInspector()` + `save()`.

- `renderPalette()` — one button per non-`hidden` catalog entry; icons come from the local `ICONS` map (keyed by the spec's `icon` name, inline SVG).
- `renderStructure()` — selectable rows with per-row ↑ ↓ 🗑 controls (`data-up/down/del/sel`), "généré/vide" status.
- `renderPreview()` — sets `iframe.srcdoc = pageHTML(false)`; un-generated blocks show a `skeleton(label)` placeholder section.
- `pageHTML(forExport)` — wraps `S.page[*].html` in a full document (`lang="fr"`, Tailwind CDN script, `<title>` from brand name). `forExport=true` drops skeletons. Used by preview, "Copier la page" and export.
- `renderInspector()` — binds the selected block to the Générer tab (label, description, presets, prompt) and pushes its HTML into the code editor via `setCode`.

### 1.4 Generation flow — `generate()`

Guards on `S.generating` → button spinner state → `POST /api/generate` with `{block, prompt, current_html, brand}`. **`current_html` is what makes a second prompt on the same block an iteration** rather than a regeneration. On success: store `data.html`, `renderAll()`, show `model · elapsed s` in `#genMeta`. Errors surface as a red toast. Shortcut: `⌘/Ctrl+Enter` in the brief textarea.

### 1.5 Code tab — Monaco with textarea fallback

- `loadMonaco()` (shared pattern with sites.html): injects the AMD `loader.js` from jsDelivr, configures `MonacoEnvironment.getWorkerUrl` as a **blob-proxy** (workers can't load cross-origin, so a one-line blob script `importScripts()`s the CDN worker), defines the custom dark theme `'nadaprod'`, memoizes the promise (`monacoP`, reset on failure).
- `mountCodeEditor()` runs on first switch to the Code tab: creates the editor over the hidden `#codeMonaco` div seeded from the `<textarea id="codeEditor">`, hides the textarea, maps `⌘/Ctrl+S` to "Appliquer". **If the CDN is unreachable the textarea simply stays** — same `getCode()`/`setCode()` API either way (`codeMonaco === null` ⇒ textarea).
- "Appliquer à l'aperçu" copies the buffer into the selected block's `html` and re-renders. "Agrandir" widens the right panel (22.5rem ⇄ 44rem).

### 1.6 Export

"Copier la page" → clipboard; "Exporter le HTML" → Blob download named after the slugified brand name. Both use `pageHTML(true)`: a self-contained file that still relies on the Tailwind CDN at runtime (see TODO for the compiled-CSS export).

---

## 2. Imported-sites manager — `web/sites.html`

### 2.1 Two views, one page — role-aware

Boot (`boot()`): `GET /api/me` → module-level `ME = {email, root, sites}` (401 ⇒ redirect `/login?next=/sites`), header shows the email + a « Déconnexion » button (`POST /api/logout` → `/login`).

- **List view** (`#viewList`): dropzone (click or drag-drop → `upload(file)` → `POST /api/sites` multipart; **hidden for non-root**) + site cards from `GET /api/sites` — already filtered server-side to the account's sites (id, page count, domain badge, Consulter, Éditer; Supprimer with `confirm()` is root-only). Root also gets the **« Comptes » card** (§2.9). Only sites **with** a domain are listed here — domain-less sites live on `/drafts` (§2.8).
- **Edit view** (`#viewEdit`): page `<select>`, **Visuel · IA ⇄ Code** mode toggle, "Enregistrer la page", amber dirty dot, domain input + "Brancher le domaine" (**root-only** — users see the domain read-only in `#domainRO`), a **⚙ settings popover** (see §2.7, available to everyone), and a right aside (selected element info, Parent/Supprimer, AI retouch prompt, go-live info).

### 2.2 State

```js
ME = { email, root, sites }   // compte connecté, chargé au boot
S = { siteId, page, domain, dirty, hasSelection, aiBusy,
      saveResolve,        // pending resolver for the iframe HTML round-trip
      mode: 'visual'|'code',
      editor,             // Monaco instance (created on first code switch)
      settingValue,       // true during programmatic setValue (don't mark dirty)
      frameStale,         // file rewritten while in code mode → reload iframe
      settings }          // publication settings {compile, pwa, gdpr} (⚙ panel)
```

`openEditor(site)` takes the full site object from `sitesCache` (id, pages, domain, settings).

### 2.3 Visual mode — postMessage protocol with the injected editor

The iframe loads `/edit/<id>/<page>` (the real page + `inject.js`, see §3). All communication is `postMessage`, **origin-checked on both sides** (`location.origin`):

| Direction | Message | Meaning |
|---|---|---|
| iframe → parent | `wp:ready {title}` | injected editor booted |
| iframe → parent | `wp:selected {tag, html}` | element selected (also re-sent after inline text edit); parent stores `lastSelectedHTML` |
| iframe → parent | `wp:deselected` | selection cleared |
| iframe → parent | `wp:dirty` | any input ⇒ `markDirty()` |
| iframe → parent | `wp:html {html}` | serialized page, resolves `getVisualHTML()` |
| parent → iframe | `wp:get-html` | request serialization (3 s timeout ⇒ resolves `null`) |
| parent → iframe | `wp:replace {html}` | swap the selected element with the AI result |
| parent → iframe | `wp:parent` / `wp:delete` | move selection up / remove element |

**AI retouch** (`aiEdit()`): `POST /api/generate` with `block:'element'`, the user brief, and `current_html = lastSelectedHTML`; the reply is pushed back with `wp:replace`. The element generator preserves the page's own stack (see BACKEND.md §3).

### 2.4 Code mode — state-safe toggle

- `enterCode()`: if the visual page is dirty, the buffer is seeded from `getVisualHTML()` (**unsaved visual edits carry over**); otherwise from `fetchRawPage()` (`/preview/…`, `cache:'no-store'` — the file on disk). Then `ensureEditor()` (same `loadMonaco()` blob-proxy pattern; `⌘S` bound to `savePage`, `onDidChangeModelContent` marks dirty except during `settingValue`).
- `enterVisual()`: a dirty code buffer prompts `confirm()` → `savePage()` before switching; `frameStale` (set when saving from code mode) forces an iframe reload so the visual view shows the saved file.

### 2.5 Saving — `savePage()`

Source depends on the mode: Monaco buffer, or the serialized iframe DOM. `PUT /api/sites/<id>/file {path, html}` — the same endpoint for both modes. Unsaved-changes guards: page switch, back button, and `beforeunload`. `⌘/Ctrl+S` works globally in the edit view.

### 2.6 Domain

"Brancher le domaine" → `PUT /api/sites/<id>/domain`; effective immediately (backend reloads its host map). `updateLiveInfo()` explains the DNS A-record step; after that, every "Enregistrer" is live instantly.

### 2.7 Publication settings — the ⚙ popover

The cog button next to "Brancher le domaine" opens `#settingsPanel`: three checkboxes (`data-setting="compile|pwa|gdpr"`) mapping 1:1 to the backend publish pipeline (local Tailwind compile, generated PWA, GDPR notice — BACKEND.md §4.7). Each change immediately `PUT /api/sites/<id>/settings` with just that key; on failure the checkbox reverts (`saveSettings` keeps the previous value). The panel closes on any outside click. A footer note reminds that settings apply to the **published** version — the editor always works on the source, which is why toggles never change what the edit iframe shows.

### 2.8 Drafts — `web/drafts.html`

A near-copy of `sites.html` whose list view filters to sites **without** a domain (`!s.domain`; `sites.html` shows the inverse) and links « Aperçu » to `/preview/<id>/` instead of « Consulter ». The top-bar nav cross-links the two pages (« Sites en ligne » ⇄ « Brouillons »). **Not yet role-aware**: it predates the auth pass — no `/api/me` boot, and the delete button / domain input render for everyone. Harmless (the server returns 403 and unauthenticated loads are redirected to `/login`), but the copy drift is tracked in TODO §3 alongside the shared-JS dedupe item.

### 2.9 « Comptes » card (root only) — `#accountsCard`

`loadAccounts()` fetches `/api/users` + `/api/waiting-list` in parallel. Per user: email, « Mot de passe… » (a `prompt()` → `PUT /api/users/:email {password}`), delete (`DELETE`, sites stay in place), and one checkbox per site from `sitesCache` — **each click saves immediately** (`saveAssignments` → `PUT {sites}` with the full checked list; on failure the panel re-renders from the server). Below: the add-account form (`POST /api/users`) and the waiting-list entries (newest first: date, email, name, message — all `esc()`-escaped). Delegated handlers: `data-upass` / `data-udel` on the document click listener, `data-assign` on a document `change` listener.

---

## 3. Injected editor — `web/static/inject.js` + `inject.css`

A single IIFE, idempotent (`window.__wp` guard), **zero state stored in the page**. Spliced before `</body>` by the backend (`injectTag`, ids `__wp_inject` / `__wp_style`) only on `/edit/` responses.

- **Hover**: mouseover/out toggle `data-wp-hover` (dashed violet outline via `inject.css`).
- **Select**: capture-phase click handler — `el(e)` filters out `<html>`, `<body>` and the injected nodes — `preventDefault`s the page's own links/buttons, sets `data-wp-selected` (solid outline), posts `wp:selected` with the element's `outerHTML`.
- **Inline text edit**: double-click sets `contenteditable`; `focusout` removes it and re-posts the updated `wp:selected`. Every `input` event posts `wp:dirty`. `Escape` deselects.
- **Serialization** (`serialize()`): clones `document.documentElement`, removes the two injected nodes and all `data-wp-hover`/`data-wp-selected`/`contenteditable` artifacts **from the clone**, returns `<!DOCTYPE html>` + `outerHTML`. What is saved is the page exactly as it will be served.
- **Commands from the parent**: `wp:get-html`, `wp:replace` (parses via an inert `<template>`, so any `<script>` in the AI reply does not execute; replaces and reselects), `wp:delete`, `wp:parent` (stops below `<body>`).

`inject.css` is 4 lines: outlines for `[data-wp-hover]`/`[data-wp-selected]` and their contenteditable focus style — all `!important`, so they win over the site's own CSS and are removed on save.

---

## 4. Public landing — `web/home.html`

Fully self-contained (~800 lines: inline CSS + JS, zero external requests beyond fonts). Brutalist design with CSS custom properties.

- **Tab navigation**: `switchTab(id)` toggles `.active` on `<section id="tab-home|app|services|network">` and the fixed bottom nav — a single-page pseudo-router, no URL changes, inline `onclick` handlers.
- **i18n**: `translations = {fr:{…}, en:{…}}` keyed by `data-i18n` attributes; `applyLanguage()` swaps `innerHTML` on every tagged node. Initial language sniffs `navigator.language` (`fr*` ⇒ FR, else EN); `toggleLanguage()` flips it. **Language is not persisted** (theme is).
- **Theme**: `data-theme="light|dark"` on `<html>`, CSS variables per theme, persisted in `localStorage['nadaprod-theme']`, `initTheme()` also honors `prefers-color-scheme`.
- **Hero rotator**: `setInterval` (5 s) cycles three FR/EN message pairs with an opacity/translate transition on `#hero-title`.
- **« Le Signal » form** (Network tab): `joinWaitlist(event)` posts the email to `/waiting-list` (message tagged « inscription depuis la page d'accueil ») and swaps the form for a ✓; a discreet link below it leads to the full `/waiting-list` page.

The other static pages (`about`, `legal`, `privacy`, `terms`, `solutions`, `opensource`, `404`) follow the same visual language, each self-contained; they contain no API calls. `404.html` is also gin's `NoRoute` fallback.

---

## 5. Cross-cutting notes

- **Security posture**: the app pages sit behind session auth (BACKEND.md §0/§5); role gating in the UI (`ME.root`) is cosmetic — every rule is enforced server-side. Generated block HTML is trusted and rendered in a same-origin `srcdoc` iframe; the block contract (Tailwind-only, JS only where allowed) plus server-side `cleanHTML` are the current guards.
- **Failure modes handled**: backend unreachable at studio boot (toast), Monaco CDN down (textarea fallback in the studio; explicit refusal to enter code mode in /sites), generation errors (red toast, button restored), iframe not answering `wp:get-html` (3 s timeout, save aborted with a toast).
- **Known quirks and planned improvements are tracked in TODO.md** — notably the duplicated Monaco/toast/config code between the two editors and the Tailwind-CDN-in-production issue.
