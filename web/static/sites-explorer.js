/* NADAPROD — explorateur de fichiers de l'éditeur de sites (grands écrans).
 *
 * Arbre façon VS Code : pages HTML + CSS/JS du site, dossiers repliables,
 * filtre rapide, clavier (↑↓ ←→ Entrée), largeur ajustable (mémorisée).
 * L'hôte fournit les fichiers et réagit aux ouvertures :
 *   Explorer.mount(host, { t, onOpen(path) })
 *   Explorer.load(siteId, files)        // files : chemins relatifs
 *   Explorer.mark({ active, dirty })    // fichier courant, Set des modifiés
 */
(function () {
  'use strict';
  const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const store = { get: (k) => { try { return localStorage.getItem(k); } catch (_) { return null; } },
                  set: (k, v) => { try { localStorage.setItem(k, v); } catch (_) {} } };

  const extOf = (p) => (p.split('.').pop() || '').toLowerCase();

  const X = { host: null, cfg: null, site: null, files: [], open: new Set(), active: null, dirty: new Set(), filter: '', focus: null };

  // Arbre : dossiers d'abord, puis fichiers, ordre alphabétique insensible à la casse.
  function tree(paths) {
    const root = { dirs: new Map(), files: [] };
    for (const p of paths) {
      const parts = p.split('/');
      let node = root, dir = '';
      for (const name of parts.slice(0, -1)) {
        dir += name + '/';
        if (!node.dirs.has(name)) node.dirs.set(name, { path: dir, dirs: new Map(), files: [] });
        node = node.dirs.get(name);
      }
      node.files.push(p);
    }
    return root;
  }
  const cmp = (a, b) => a.localeCompare(b, undefined, { sensitivity: 'base', numeric: true });

  function rows() {
    if (X.filter) {
      const q = X.filter.toLowerCase();
      return X.files.filter((p) => p.toLowerCase().includes(q)).sort(cmp).map((p) => ({ kind: 'file', path: p, depth: 0, flat: true }));
    }
    const out = [];
    const walk = (node, depth) => {
      for (const [name, d] of [...node.dirs].sort((a, b) => cmp(a[0], b[0]))) {
        const open = X.open.has(d.path);
        out.push({ kind: 'dir', path: d.path, name, depth, open });
        if (open) walk(d, depth + 1);
      }
      for (const p of node.files.sort(cmp)) out.push({ kind: 'file', path: p, name: p.split('/').pop(), depth });
    };
    walk(tree(X.files), 0);
    return out;
  }

  const chevron = (open) => `<svg class="h-3 w-3 shrink-0 text-ink-400 transition-transform ${open ? 'rotate-90' : ''}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><path d="m9 6 6 6-6 6" stroke-linecap="round" stroke-linejoin="round"/></svg>`;

  function render() {
    const list = rows();
    if (!list.some((r) => r.path === X.focus)) X.focus = list.find((r) => r.path === X.active)?.path ?? list[0]?.path ?? null;
    X.host.querySelector('[data-x-tree]').innerHTML = list.length ? list.map((r) => {
      const active = r.path === X.active, dirty = X.dirty.has(r.path);
      const pad = 8 + r.depth * 12;
      const label = r.kind === 'dir'
        ? `${chevron(r.open)}<span class="truncate">${esc(r.name)}</span>`
        // Pas de pastille de type : l'extension est déjà dans le nom.
        : `<span class="w-3 shrink-0"></span>` +
          (r.flat ? `<span class="truncate">${esc(r.path.split('/').pop())}<span class="ml-1.5 text-ink-400">${esc(r.path.split('/').slice(0, -1).join('/'))}</span></span>`
                  : `<span class="truncate">${esc(r.name)}</span>`) +
          (dirty ? `<span class="ml-auto h-1.5 w-1.5 shrink-0 rounded-full bg-amber-400"></span>` : '');
      return `<div role="treeitem" data-x-path="${esc(r.path)}" data-x-kind="${r.kind}" ${r.kind === 'dir' ? `aria-expanded="${r.open}"` : ''} aria-selected="${active}"
        tabindex="${r.path === X.focus ? 0 : -1}" style="padding-left:${pad}px"
        class="h-6 pr-2 flex items-center gap-1 font-mono text-[12px] cursor-pointer select-none outline-none border-l-2
        ${active ? 'bg-ink-800 text-ink-100 border-acc-fg' : 'border-transparent text-ink-300 hover:bg-ink-850 hover:text-ink-100'}
        focus-visible:bg-ink-800">${label}</div>`;
    }).join('') : `<p class="px-3 py-2 text-xs text-ink-300">${esc(X.cfg.t('sites.explorerEmpty'))}</p>`;
  }

  // Les dossiers du fichier actif sont dépliés (comme « Reveal in Explorer »).
  function reveal(path) {
    let dir = '';
    for (const name of (path || '').split('/').slice(0, -1)) { dir += name + '/'; X.open.add(dir); }
  }

  function activate(el) {
    const path = el.dataset.xPath;
    X.focus = path;
    if (el.dataset.xKind === 'dir') {
      X.open.has(path) ? X.open.delete(path) : X.open.add(path);
      render();
      focusRow(path);
    } else X.cfg.onOpen(path);
  }

  function focusRow(path) {
    X.host.querySelector(`[data-x-path="${CSS.escape(path)}"]`)?.focus();
  }

  function onKey(e) {
    const el = e.target.closest('[data-x-path]');
    if (!el) return;
    const items = [...X.host.querySelectorAll('[data-x-path]')];
    const i = items.indexOf(el), path = el.dataset.xPath, dir = el.dataset.xKind === 'dir';
    const move = (j) => { const n = items[Math.max(0, Math.min(items.length - 1, j))]; if (n) { X.focus = n.dataset.xPath; items.forEach((x) => x.tabIndex = -1); n.tabIndex = 0; n.focus(); } };
    switch (e.key) {
      case 'ArrowDown': move(i + 1); break;
      case 'ArrowUp': move(i - 1); break;
      case 'Home': move(0); break;
      case 'End': move(items.length - 1); break;
      case 'ArrowRight': if (dir && !X.open.has(path)) activate(el); else move(i + 1); break;
      case 'ArrowLeft':
        if (dir && X.open.has(path)) activate(el);
        else { const parent = path.replace(/[^/]+\/?$/, ''); if (parent) { const p = items.find((x) => x.dataset.xPath === parent); if (p) move(items.indexOf(p)); } }
        break;
      case 'Enter': case ' ': activate(el); break;
      default: return;
    }
    e.preventDefault();
  }

  function initResize(handle) {
    const host = X.host, min = 160, max = 480;
    const w = parseInt(store.get('np-explorer-w'), 10);
    if (w >= min && w <= max) host.style.width = w + 'px';
    let drag = null;
    handle.addEventListener('mousedown', (e) => {
      drag = { x: e.clientX, w: host.getBoundingClientRect().width };
      document.body.style.cursor = 'col-resize';
      document.querySelectorAll('iframe').forEach((f) => f.style.pointerEvents = 'none'); // l'iframe avalerait le mousemove
      e.preventDefault();
    });
    window.addEventListener('mousemove', (e) => {
      if (drag) host.style.width = Math.min(max, Math.max(min, drag.w + e.clientX - drag.x)) + 'px';
    });
    window.addEventListener('mouseup', () => {
      if (!drag) return;
      drag = null;
      document.body.style.cursor = '';
      document.querySelectorAll('iframe').forEach((f) => f.style.pointerEvents = '');
      store.set('np-explorer-w', String(Math.round(host.getBoundingClientRect().width)));
      window.CodeMode?.layout();
    });
  }

  function mount(host, cfg) {
    X.host = host; X.cfg = cfg;
    const t = cfg.t;
    host.innerHTML = `
      <div class="shrink-0 h-9 flex items-center gap-1 pl-3 pr-1.5 border-b border-ink-700">
        <p class="label" data-i18n="sites.explorer">${esc(t('sites.explorer'))}</p>
        <span data-x-site class="min-w-0 truncate text-[11px] font-mono text-ink-400"></span>
        <button data-x-collapse class="ml-auto h-6 w-6 grid place-items-center text-ink-300 hover:text-ink-100" title="${esc(t('sites.explorerCollapse'))}" aria-label="${esc(t('sites.explorerCollapse'))}">
          <svg class="h-3.5 w-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="m7 9 5-5 5 5M7 15l5 5 5-5" stroke-linecap="round" stroke-linejoin="round" transform="rotate(180 12 12)"/></svg>
        </button>
      </div>
      <div class="shrink-0 p-2 border-b border-ink-700">
        <input data-x-filter type="search" spellcheck="false" autocomplete="off" placeholder="${esc(t('sites.explorerFilter'))}" aria-label="${esc(t('sites.explorerFilter'))}"
          class="field w-full h-7 text-[12px] px-2 select-text">
      </div>
      <div data-x-tree role="tree" aria-label="${esc(t('sites.explorer'))}" class="flex-1 min-h-0 overflow-y-auto overflow-x-hidden py-1 scrollbar-thin"></div>
      <div data-x-resize class="absolute top-0 right-0 h-full w-1 cursor-col-resize hover:bg-ink-400" aria-hidden="true"></div>`;
    const treeEl = host.querySelector('[data-x-tree]');
    treeEl.addEventListener('click', (e) => { const el = e.target.closest('[data-x-path]'); if (el) activate(el); });
    treeEl.addEventListener('keydown', onKey);
    host.querySelector('[data-x-filter]').addEventListener('input', (e) => { X.filter = e.target.value.trim(); render(); });
    host.querySelector('[data-x-filter]').addEventListener('keydown', (e) => {
      if (e.key === 'ArrowDown') { e.preventDefault(); host.querySelector('[data-x-path]')?.focus(); }
      if (e.key === 'Escape') { e.target.value = ''; X.filter = ''; render(); }
    });
    host.querySelector('[data-x-collapse]').addEventListener('click', () => { X.open.clear(); reveal(X.active); render(); });
    initResize(host.querySelector('[data-x-resize]'));
  }

  function load(site, files) {
    if (site !== X.site) { X.open.clear(); X.filter = ''; const f = X.host.querySelector('[data-x-filter]'); if (f) f.value = ''; }
    X.site = site;
    X.files = [...new Set(files)];
    X.host.querySelector('[data-x-site]').textContent = site;
    reveal(X.active);
    render();
  }

  function mark({ active, dirty } = {}) {
    if (active !== undefined && active !== X.active) { X.active = active; X.focus = active; reveal(active); }
    if (dirty) X.dirty = new Set(dirty);
    if (X.host) render();
  }

  window.Explorer = { mount, load, mark, focus: () => X.host?.querySelector('[data-x-path][tabindex="0"]')?.focus() };
})();
