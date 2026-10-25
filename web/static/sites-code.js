/* NADAPROD — atelier de code des sites importés (mode « Code » de /sites et /drafts).
 *
 * Un « CodePen » par page : la page HTML et chaque CSS / JS local qu'elle
 * référence s'ouvrent en onglets dans un seul éditeur Monaco (un modèle par
 * fichier, état de vue conservé), avec l'aperçu de la page à côté.
 *   - CSS : appliqué à l'aperçu à la volée, sans enregistrer (la <link> est
 *     doublée d'un <style> vivant, puis désactivée) ;
 *   - HTML / JS : l'aperçu se recharge à l'enregistrement ;
 *   - ⌘S / « Enregistrer » écrit TOUS les onglets modifiés.
 * Les autres CSS/JS du site sont à portée de clic (« + »).
 *
 * Usage (page hôte) :
 *   CodeMode.configure({ loadMonaco, toast, t, onDirty })
 *   await CodeMode.open(siteId, page, { html })  // html : contenu initial de la page (retouches visuelles)
 *   CodeMode.saveAll() · CodeMode.isDirty() · CodeMode.close() · CodeMode.pageHTML()
 */
(function () {
  'use strict';

  const LANG = { html: 'html', htm: 'html', css: 'css', js: 'javascript', mjs: 'javascript', json: 'json', svg: 'xml', xml: 'xml', md: 'markdown', webmanifest: 'json', txt: 'plaintext' };
  const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const $ = (sel, root = document) => root.querySelector(sel);

  // onFiles : onglet actif ou modifiés changés (explorateur) ; onPreview : aperçu affiché/masqué.
  let cfg = { loadMonaco: null, toast: () => {}, t: (k) => k, onDirty: () => {}, onFiles: () => {}, onPreview: () => {} };
  const S = {
    site: null, page: null,
    editor: null, files: [],        // [{ path, model, dirty, saved, view }]
    active: null,                    // path de l'onglet actif
    others: [],                      // autres CSS/JS du site (menu « + »)
    settingValue: false,
    cssTimer: null, reloadTimer: null,
    previewOn: false,                // l'aperçu se montre à la demande (plus léger)
    stale: false,                    // aperçu à recharger quand il réapparaîtra
  };
  const t = (k, p) => cfg.t(k, p);

  /* ───────── DOM de l'atelier (construit dans #codeWrap) ───────── */

  function mount(host) {
    if (host.dataset.codeMounted) return;
    host.dataset.codeMounted = '1';
    host.classList.add('flex', 'flex-col');
    host.innerHTML = `
      <div class="shrink-0 flex items-stretch border-b border-ink-700 bg-ink-900">
        <div id="cmTabs" role="tablist" class="flex-1 min-w-0 flex overflow-x-auto scrollbar-thin"></div>
        <div class="relative shrink-0 flex items-center border-l border-ink-700">
          <button id="cmMore" aria-haspopup="menu" aria-expanded="false" class="h-9 px-3 text-xs font-medium text-ink-300 hover:text-ink-100 hover:bg-ink-800" title="${esc(t('code.moreTitle'))}">+</button>
          <div id="cmMoreMenu" role="menu" class="hidden absolute right-0 top-9 z-30 w-72 max-h-80 overflow-y-auto rounded-xl border border-ink-600 bg-ink-900 shadow-2xl p-1.5"></div>
          <button id="cmPreview" aria-pressed="true" class="h-9 px-3 text-xs font-medium text-ink-300 hover:text-ink-100 hover:bg-ink-800 border-l border-ink-700" title="${esc(t('code.previewToggle'))}">${esc(t('code.preview'))}</button>
        </div>
      </div>
      <div class="flex-1 min-h-0 flex">
        <div id="cmEditor" class="flex-1 min-w-0 select-text"></div>
        <div id="cmSplit" class="w-1.5 shrink-0 cursor-col-resize bg-ink-800 hover:bg-vio-500/60" title="${esc(t('code.resize'))}"></div>
        <div id="cmPane" class="w-[45%] shrink-0 min-w-0 flex flex-col bg-white">
          <div class="shrink-0 h-8 flex items-center gap-2 px-3 bg-ink-900 border-b border-ink-700 text-[11px] font-mono text-ink-400">
            <span id="cmLive" class="h-2 w-2 rounded-full bg-emerald-400" title="${esc(t('code.liveTitle'))}"></span>
            <span id="cmPaneLabel" class="truncate"></span>
            <button id="cmReload" class="ml-auto px-2 h-6 rounded text-ink-300 hover:text-ink-100 hover:bg-ink-800" title="${esc(t('code.reload'))}">↻</button>
          </div>
          <iframe id="cmFrame" title="${esc(t('code.previewTitle'))}" class="flex-1 w-full bg-white"></iframe>
        </div>
      </div>`;
    $('#cmTabs', host).addEventListener('click', (e) => {
      const close = e.target.closest('[data-close]');
      if (close) { e.stopPropagation(); return closeTab(close.dataset.close); }
      const tab = e.target.closest('[data-tab]');
      if (tab) activate(tab.dataset.tab);
    });
    $('#cmMore', host).addEventListener('click', (e) => { e.stopPropagation(); toggleMore(); });
    $('#cmMoreMenu', host).addEventListener('click', (e) => {
      const it = e.target.closest('[data-open]');
      if (it) { toggleMore(false); openFile(it.dataset.open); }
    });
    document.addEventListener('click', () => toggleMore(false));
    $('#cmPreview', host).addEventListener('click', () => setPreview(!S.previewOn));
    $('#cmReload', host).addEventListener('click', reload);
    $('#cmFrame', host).addEventListener('load', applyLiveCSS); // ré-applique le CSS non enregistré après un rechargement
    initSplit(host);
  }

  function initSplit(host) {
    const bar = $('#cmSplit', host), pane = $('#cmPane', host), frame = $('#cmFrame', host);
    let drag = null;
    bar.addEventListener('mousedown', (e) => { drag = { x: e.clientX, w: pane.getBoundingClientRect().width }; frame.style.pointerEvents = 'none'; e.preventDefault(); });
    window.addEventListener('mousemove', (e) => {
      if (!drag) return;
      const total = host.getBoundingClientRect().width;
      const w = Math.min(total * 0.8, Math.max(240, drag.w - (e.clientX - drag.x)));
      pane.style.width = w + 'px';
    });
    window.addEventListener('mouseup', () => { if (drag) { drag = null; frame.style.pointerEvents = ''; S.editor?.layout(); } });
  }

  function toggleMore(show) {
    const menu = $('#cmMoreMenu'), btn = $('#cmMore');
    const open = show === undefined ? menu.classList.contains('hidden') : show;
    if (open) renderMore();
    menu.classList.toggle('hidden', !open);
    btn.setAttribute('aria-expanded', String(open));
  }

  function renderMore() {
    const opened = new Set(S.files.map((f) => f.path));
    const list = S.others.filter((a) => !opened.has(a.path));
    $('#cmMoreMenu').innerHTML = list.length
      ? `<p class="px-3 py-1.5 text-[10px] font-mono uppercase tracking-[0.15em] text-ink-400">${esc(t('code.otherFiles'))}</p>` +
        list.map((a) => `<button role="menuitem" data-open="${esc(a.path)}" class="w-full text-left rounded-lg px-3 py-1.5 text-xs hover:bg-ink-800 flex justify-between gap-3">
          <span class="font-mono truncate">${esc(a.path)}</span><span class="text-ink-400 shrink-0">${(a.size / 1024).toFixed(1)} Ko</span></button>`).join('')
      : `<p class="px-3 py-2 text-xs text-ink-400">${esc(t('code.noOtherFiles'))}</p>`;
  }

  function setPreview(on) {
    S.previewOn = on;
    if (!$('#cmPane')) return; // atelier pas encore monté : appliqué à l'ouverture
    $('#cmPane').classList.toggle('hidden', !on);
    $('#cmSplit').classList.toggle('hidden', !on);
    $('#cmPreview').setAttribute('aria-pressed', String(on));
    $('#cmPreview').classList.toggle('text-vio-400', on);
    S.editor?.layout();
    cfg.onPreview(on);
    if (on && S.stale) reload();
  }

  /* ───────── Onglets & modèles ───────── */

  const langOf = (path) => LANG[(path.split('.').pop() || '').toLowerCase()] || 'plaintext';
  const isPage = (path) => path === S.page;

  function renderTabs() {
    $('#cmTabs').innerHTML = S.files.map((f) => `
      <div role="tab" data-tab="${esc(f.path)}" aria-selected="${f.path === S.active}" title="${esc(f.path)}"
        class="group flex items-center gap-2 h-9 px-3 border-r border-ink-700 text-xs font-mono cursor-pointer whitespace-nowrap ${f.path === S.active ? 'bg-ink-850 text-ink-100 border-b-2 border-b-vio-500' : 'text-ink-400 hover:text-ink-200 hover:bg-ink-800'}">
        <span class="h-1.5 w-1.5 rounded-full ${f.dirty ? 'bg-amber-400' : 'bg-transparent'}"></span>
        <span>${esc(f.path.split('/').pop())}</span>
        ${isPage(f.path) ? '' : `<button data-close="${esc(f.path)}" class="ml-1 -mr-1 h-4 w-4 rounded grid place-items-center text-ink-500 hover:text-ink-100 hover:bg-ink-700" aria-label="${esc(t('code.closeTab'))}">×</button>`}
      </div>`).join('');
    $('#cmPaneLabel').textContent = S.page ? '/preview/' + S.site + '/' + S.page : '';
    updateLive();
    cfg.onDirty(isDirty());
    cfg.onFiles();
  }

  async function fetchText(path) {
    const r = await fetch('/preview/' + S.site + '/' + path, { cache: 'no-store' });
    if (!r.ok) throw new Error(t('sites.cantRead'));
    return r.text();
  }

  async function openFile(path, { content, activateIt = true } = {}) {
    let f = S.files.find((x) => x.path === path);
    if (!f) {
      const text = content ?? await fetchText(path);
      const model = monaco.editor.createModel(text, langOf(path), monaco.Uri.parse('np:/' + S.site + '/' + path));
      f = { path, model, dirty: false, saved: text, view: null };
      model.onDidChangeContent(() => {
        if (S.settingValue) return;
        const was = f.dirty;
        f.dirty = model.getValue() !== f.saved;
        if (was !== f.dirty) renderTabs();
        if (langOf(path) === 'css') scheduleLiveCSS();
      });
      S.files.push(f);
    }
    if (activateIt) activate(path); else renderTabs();
    return f;
  }

  function activate(path) {
    const next = S.files.find((f) => f.path === path);
    if (!next || !S.editor) return;
    const cur = S.files.find((f) => f.path === S.active);
    if (cur) cur.view = S.editor.saveViewState();
    S.active = path;
    S.editor.setModel(next.model);
    if (next.view) S.editor.restoreViewState(next.view);
    renderTabs();
    S.editor.focus();
  }

  const dirtyFiles = () => S.files.filter((f) => f.dirty).map((f) => f.path);

  function closeTab(path) {
    const i = S.files.findIndex((f) => f.path === path);
    if (i < 0 || isPage(path)) return;
    const f = S.files[i];
    if (f.dirty && !confirm(t('code.confirmCloseDirty', { file: path }))) return;
    S.files.splice(i, 1);
    f.model.dispose();
    if (S.active === path) activate((S.files[i] || S.files[i - 1]).path); else renderTabs();
  }

  /* ───────── Aperçu ───────── */

  const frameDoc = () => { try { return $('#cmFrame').contentDocument; } catch (_) { return null; } };

  function reload() {
    clearTimeout(S.reloadTimer);
    const fr = $('#cmFrame');
    if (!S.site || !S.page) return;
    if (!S.previewOn) { S.stale = true; return; } // masqué : rien à charger pour l'instant
    S.stale = false;
    fr.src = '/preview/' + S.site + '/' + S.page + '?t=' + Date.now();
  }

  function scheduleLiveCSS() {
    clearTimeout(S.cssTimer);
    S.cssTimer = setTimeout(applyLiveCSS, 250);
  }

  // Pour chaque CSS ouvert et modifié : la feuille liée est doublée d'un
  // <style data-np-live> contenant le tampon, puis désactivée. Une feuille
  // revenue à son état enregistré retrouve sa <link>.
  function applyLiveCSS() {
    const doc = frameDoc();
    if (!doc || !S.previewOn) return;
    const links = Array.from(doc.querySelectorAll('link[rel~="stylesheet"][href]'));
    for (const f of S.files) {
      if (langOf(f.path) !== 'css') continue;
      const mine = links.filter((l) => { try { return new URL(l.href).pathname === '/preview/' + S.site + '/' + f.path; } catch (_) { return false; } });
      let style = doc.querySelector(`style[data-np-live="${CSS.escape(f.path)}"]`);
      if (f.dirty) {
        if (!style) {
          style = doc.createElement('style');
          style.setAttribute('data-np-live', f.path);
          if (mine[0]) mine[0].after(style); else doc.head.appendChild(style);
        }
        const css = f.model.getValue();
        if (style.textContent !== css) style.textContent = css;
        mine.forEach((l) => { l.disabled = true; });
      } else {
        style?.remove();
        mine.forEach((l) => { l.disabled = false; });
      }
    }
  }

  // Pastille de l'aperçu : vert = fidèle (le CSS suit la frappe), orange =
  // HTML ou JS modifiés, il faudra enregistrer pour recharger.
  function updateLive() {
    const stale = S.files.some((f) => f.dirty && langOf(f.path) !== 'css');
    const dot = $('#cmLive');
    if (!dot) return;
    dot.classList.toggle('bg-amber-400', stale);
    dot.classList.toggle('bg-emerald-400', !stale);
  }

  /* ───────── Cycle de vie ───────── */

  async function ensureEditor(host) {
    if (S.editor) return;
    const monacoNS = await cfg.loadMonaco();
    S.editor = monacoNS.editor.create($('#cmEditor', host), {
      model: null, theme: NPTheme.dark() ? 'nadaprod-dark' : 'nadaprod-light',
      automaticLayout: true, minimap: { enabled: true }, wordWrap: 'on',
      fontSize: 13, fontFamily: '"JetBrains Mono", monospace', tabSize: 2,
      scrollBeyondLastLine: false, padding: { top: 12 },
    });
    S.editor.addCommand(monacoNS.KeyMod.CtrlCmd | monacoNS.KeyCode.KeyS, () => { saveAll(); });
  }

  function disposeAll() {
    S.files.forEach((f) => f.model.dispose());
    S.files = []; S.active = null; S.others = [];
  }

  /* open : ouvre la page (avec, si fourni, son HTML courant — retouches
   * visuelles non enregistrées) et ses assets locaux. */
  async function open(site, page, { html } = {}) {
    const host = $('#codeWrap');
    mount(host);
    try { await ensureEditor(host); } catch (e) { cfg.toast(t('sites.monacoDown'), false); throw e; }
    if (S.site !== site || S.page !== page) disposeAll();
    S.site = site; S.page = page;
    S.settingValue = true;
    try {
      const existing = S.files.find((f) => f.path === page);
      if (existing && html != null) { existing.model.setValue(html); existing.dirty = html !== existing.saved; }
      else if (!existing) {
        const f = await openFile(page, { content: html ?? await fetchText(page), activateIt: false });
        if (html != null) { f.saved = await fetchText(page).catch(() => html); f.dirty = html !== f.saved; }
      }
      let assets = { referenced: [], others: [] };
      try { assets = await fetch('/api/sites/' + encodeURIComponent(site) + '/assets?page=' + encodeURIComponent(page)).then((r) => (r.ok ? r.json() : assets)); } catch (_) {}
      for (const a of assets.referenced) { try { await openFile(a.path, { activateIt: false }); } catch (_) {} }
      S.others = assets.others || [];
    } finally { S.settingValue = false; }
    activate(page);
    setPreview(S.previewOn);
    reload();
  }

  function isDirty() { return S.files.some((f) => f.dirty); }
  function pageHTML() { return S.files.find((f) => f.path === S.page)?.model.getValue() ?? null; }

  /* saveAll : écrit tous les onglets modifiés (la page en dernier, pour que
   * sa republication voie les assets déjà à jour). */
  async function saveAll() {
    const dirty = S.files.filter((f) => f.dirty).sort((a, b) => (isPage(a.path) ? 1 : 0) - (isPage(b.path) ? 1 : 0));
    if (!dirty.length) return true;
    let ok = true;
    for (const f of dirty) {
      const res = await fetch('/api/sites/' + encodeURIComponent(S.site) + '/file', {
        method: 'PUT', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: f.path, html: f.model.getValue() }),
      });
      if (!res.ok) {
        let msg = t('sites.saveFailed'); try { msg = (await res.json()).error || msg; } catch (_) {}
        cfg.toast(f.path + ' : ' + msg, false); ok = false; continue;
      }
      f.saved = f.model.getValue(); f.dirty = false;
    }
    renderTabs();
    if (dirty.some((f) => langOf(f.path) !== 'css' || !ok)) reload(); else applyLiveCSS();
    return ok;
  }

  function close() { clearTimeout(S.cssTimer); }

  function layout() { S.editor?.layout(); }

  window.CodeMode = {
    configure: (o) => Object.assign(cfg, o),
    open, close, saveAll, isDirty, pageHTML, reload, layout, setPreview, dirtyFiles,
    openAsset: (path) => openFile(path),
    active: () => S.active,
    previewOn: () => S.previewOn,
  };
})();
