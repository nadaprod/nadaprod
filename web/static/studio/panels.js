/* Studio NADAPROD — panneau droit : Générer (et retouche ciblée), Code,
 * Historique, Qualité, Marque (marque + design system + SEO). */
import { S, $, $$, api, esc, emit, on, toast, selected, specOf, openModal, closeModal, ago, debounce } from './core.js';
import { generate, variants, stop, retouchElement, rethemePage, commit, hideError, prefs } from './generate.js';
import { saveMeta, versions, checkout } from './store.js';
import { reloadShell, setMode, selectParent, deleteElement, setImage, requestAudit, onFrame, thumb, fitThumbs } from './preview.js';
import { normalizeTheme } from './theme.js';
import { select } from './structure.js';
import { openI18n, initI18n, translatedPreview, exitPreview } from './i18n.js';

const TABS = ['generate', 'code', 'history', 'quality', 'i18n', 'brand'];

export function switchTab(name) {
  S.tab = name;
  $$('.tab-btn').forEach((btn) => {
    const active = btn.dataset.tab === name;
    btn.classList.toggle('border-vio-500', active);
    btn.classList.toggle('text-ink-100', active);
    btn.classList.toggle('border-transparent', !active);
    btn.classList.toggle('text-ink-400', !active);
    btn.setAttribute('aria-selected', String(active));
  });
  TABS.forEach((n) => $('#tab-' + n).classList.toggle('hidden', n !== name));
  if (name === 'code') mountCodeEditor(); // Monaco monté au premier passage sur l'onglet
  if (name === 'history') renderHistory();
  if (name === 'quality') requestAudit();
  if (name === 'i18n') openI18n();
}

/* ───────── Onglet Générer ───────── */

export function renderInspector() {
  const b = selected();
  const el = S.mode === 'element' ? S.element : null;
  $('#noSelection').classList.toggle('hidden', !!b);
  $('#generator').classList.toggle('hidden', !b || !!el);
  $('#retouch').classList.toggle('hidden', !el);
  $('#noCode').classList.toggle('hidden', !!b);
  $('#codePanel').classList.toggle('hidden', !b);
  $('#btnRetouchMode').classList.toggle('bg-vio-600', S.mode === 'element');
  $('#btnRetouchMode').classList.toggle('text-white', S.mode === 'element');
  $('#btnRetouchMode').setAttribute('aria-pressed', String(S.mode === 'element'));
  $('#btnUndo').disabled = !b?.can_undo;
  $('#btnRedo').disabled = !b?.can_redo;
  if (el) renderRetouch(el);
  if (!b) return;
  const c = specOf(b.type);
  $('#genBlockLabel').textContent = c.label;
  $('#genBlockDesc').textContent = c.description;
  if (document.activeElement !== $('#promptInput')) $('#promptInput').value = b.prompt || '';
  $('#presets').innerHTML = (c.presets || []).map((p) => `
    <button data-preset class="w-full text-left text-xs rounded-lg border border-ink-700 bg-ink-850 hover:border-vio-500/50 hover:bg-ink-800 px-3 py-2 text-ink-300 hover:text-ink-100 transition-colors">${esc(p)}</button>`).join('');
  $('#btnVariants').disabled = S.generating;
  $('#imagesRow').classList.toggle('hidden', !S.cfg.images.enabled);
  $('#imagesToggle').checked = prefs.images;
  setCode(b.html || '');
}

function renderRetouch(el) {
  $('#retouchTag').textContent = '<' + el.tag + '>';
  $('#retouchRootHint').classList.toggle('hidden', !el.isRoot);
  $('#btnPickImage').classList.toggle('hidden', !el.isImg || !S.cfg.images.enabled);
  $('#btnRetouch').disabled = S.generating;
  $('#retouchSpin').classList.toggle('hidden', !S.generating);
}

/* ───────── Onglet Code (Monaco, chargé à la demande) ───────── */

const MONACO_CDN = 'https://cdn.jsdelivr.net/npm/monaco-editor@0.52.2/min';
let monacoP = null;
function loadMonaco() {
  if (monacoP) return monacoP;
  monacoP = new Promise((resolve, reject) => {
    // Les workers Monaco ne se chargent pas cross-origin : on passe par un blob-proxy.
    window.MonacoEnvironment = {
      getWorkerUrl: () => URL.createObjectURL(new Blob(
        ["self.MonacoEnvironment={baseUrl:'" + MONACO_CDN + "/'};importScripts('" + MONACO_CDN + "/vs/base/worker/workerMain.js');"],
        { type: 'text/javascript' })),
    };
    const s = document.createElement('script');
    s.src = MONACO_CDN + '/vs/loader.js';
    s.onload = () => {
      window.require.config({ paths: { vs: MONACO_CDN + '/vs' } });
      window.require(['vs/editor/editor.main'], () => {
        monaco.editor.defineTheme('nadaprod', {
          base: 'vs-dark', inherit: true, rules: [],
          colors: {
            'editor.background': '#0E1117', 'editor.lineHighlightBackground': '#131722', 'editorGutter.background': '#0E1117',
            'editorLineNumber.foreground': '#5C687F', 'editor.selectionBackground': '#2A3245', 'editorWidget.background': '#131722',
            'editorCursor.foreground': '#A78BFA',
          },
        });
        resolve(window.monaco);
      });
    };
    s.onerror = () => { monacoP = null; reject(new Error('CDN Monaco inaccessible')); };
    document.head.appendChild(s);
  });
  return monacoP;
}

let codeMonaco = null; // instance Monaco (null ⇒ repli textarea)
let mounting = false;
const getCode = () => (codeMonaco ? codeMonaco.getValue() : $('#codeEditor').value);
function setCode(v) {
  if (codeMonaco) { if (codeMonaco.getValue() !== v) codeMonaco.setValue(v); } else $('#codeEditor').value = v;
}

async function mountCodeEditor() {
  if (codeMonaco || mounting) return;
  mounting = true;
  try {
    const monaco = await loadMonaco();
    const host = $('#codeMonaco');
    host.classList.remove('hidden');
    codeMonaco = monaco.editor.create(host, {
      value: $('#codeEditor').value, language: 'html', theme: 'nadaprod',
      automaticLayout: true, minimap: { enabled: false }, wordWrap: 'on',
      fontSize: 12, fontFamily: '"JetBrains Mono", monospace', tabSize: 2,
      scrollBeyondLastLine: false, padding: { top: 10 },
    });
    codeMonaco.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => $('#btnApplyCode').click());
    $('#codeEditor').classList.add('hidden');
  } catch (_) {
    toast(t('editor.monacoDown'), false);
  } finally {
    mounting = false;
  }
}

/* ───────── Onglet Historique ───────── */

let historyFor = null;
async function renderHistory() {
  const b = selected();
  const host = $('#historyList');
  if (!b) { host.innerHTML = `<p class="text-sm text-ink-400 border border-dashed border-ink-600 rounded-xl p-4">${esc(t('editor.noCode'))}</p>`; return; }
  historyFor = b.id;
  let list;
  try { list = await versions(b.id); } catch (e) { host.innerHTML = `<p class="text-sm text-red-400">${esc(e.message)}</p>`; return; }
  if (historyFor !== b.id || S.tab !== 'history') return; // la sélection a changé pendant le chargement
  if (!list.length) { host.innerHTML = `<p class="text-sm text-ink-400 border border-dashed border-ink-600 rounded-xl p-4">${esc(t('studio.historyEmpty'))}</p>`; return; }
  host.innerHTML = list.map((v) => `
    <div class="rounded-xl border ${v.current ? 'border-vio-500/70 bg-vio-500/10' : 'border-ink-700 bg-ink-850'} p-3">
      <div class="flex items-center justify-between gap-2">
        <span class="text-[10px] font-mono uppercase tracking-[0.12em] ${v.current ? 'text-vio-400' : 'text-ink-400'}">${esc(t('studio.src_' + v.source))}${v.current ? ' · ' + esc(t('studio.current')) : ''}</span>
        <span class="text-[10px] font-mono text-ink-400" title="${esc(v.created_at)}">${esc(ago(v.created_at))}</span>
      </div>
      ${v.prompt ? `<p class="mt-1.5 text-xs text-ink-300 line-clamp-2">${esc(v.prompt)}</p>` : ''}
      <div class="mt-2.5 flex gap-2">
        <button data-compare="${v.id}" class="h-7 px-2.5 rounded-md text-[11px] font-medium border border-ink-600 hover:bg-ink-800">${esc(t('studio.compare'))}</button>
        ${v.current ? '' : `<button data-restore="${v.id}" class="h-7 px-2.5 rounded-md text-[11px] font-medium border border-ink-600 hover:bg-ink-800">${esc(t('studio.restore'))}</button>`}
      </div>
    </div>`).join('');
  host.onclick = (e) => {
    const cmp = e.target.closest('[data-compare]'), res = e.target.closest('[data-restore]');
    if (cmp) compare(b, list.find((v) => v.id === +cmp.dataset.compare));
    if (res) restore(b.id, +res.dataset.restore);
  };
}

async function restore(blockId, versionId) {
  try { await checkout(blockId, { version_id: versionId }); toast(t('studio.restored')); closeModal(); }
  catch (e) { toast(e.message, false); }
}

/* Avant / après : la version choisie à côté de la version courante. */
function compare(b, v) {
  const m = openModal({
    title: t('studio.compareTitle', { block: specOf(b.type).label }), size: 'xl',
    body: `<div class="grid gap-4 lg:grid-cols-2">
        <div><p class="mb-2 text-[10px] font-mono uppercase tracking-[0.15em] text-ink-400">${esc(t('studio.src_' + v.source))} · ${esc(ago(v.created_at))}</p>${thumb(v.html)}</div>
        <div><p class="mb-2 text-[10px] font-mono uppercase tracking-[0.15em] text-vio-400">${esc(t('studio.current'))}</p>${thumb(b.html)}</div>
      </div>
      ${v.current ? '' : `<div class="mt-5 flex justify-end"><button data-go class="h-9 px-4 rounded-lg text-sm font-semibold bg-vio-600 hover:bg-vio-500 text-white">${esc(t('studio.restoreThis'))}</button></div>`}`,
  });
  fitThumbs(m.el);
  const go = m.el.querySelector('[data-go]');
  if (go) go.onclick = () => restore(b.id, v.id);
}

export async function undo(dir) {
  const b = selected();
  if (!b || S.generating || !(dir < 0 ? b.can_undo : b.can_redo)) return;
  try { await checkout(b.id, { step: dir }); toast(t(dir < 0 ? 'studio.undone' : 'studio.redone')); }
  catch (e) { toast(e.message, false); }
}

/* ───────── Onglet Qualité ─────────
 * L'audit tourne dans l'aperçu (styles calculés) ; ici, on note et on affiche. */

const WEIGHT = { error: 8, warning: 4, info: 1 };
function score(issues) { return Math.max(0, 100 - issues.reduce((n, i) => n + (WEIGHT[i.severity] || 0), 0)); }
const tone = (n) => (n >= 90 ? 'text-emerald-400' : n >= 70 ? 'text-amber-400' : 'text-red-400');

function issueText(i) {
  return t('studio.q_' + i.kind.replace(/-/g, '_'), { ratio: i.ratio, need: i.need, from: i.from, to: i.to });
}

onFrame('np:audit-result', (r) => {
  const all = r.blocks.flatMap((b) => b.issues).concat(r.page);
  const total = r.blocks.length ? Math.round(r.blocks.reduce((n, b) => n + score(b.issues), 0) / r.blocks.length) - r.page.length * 4 : null;
  $('#qualityScore').textContent = total === null ? '—' : Math.max(0, total);
  $('#qualityScore').className = 'font-display font-bold text-4xl tracking-tight ' + (total === null ? 'text-ink-400' : tone(total));
  $('#qualitySummary').textContent = total === null ? t('studio.qualityEmpty')
    : all.length ? t('studio.qualityIssues', { n: all.length }) : t('studio.qualityPerfect');
  const pageRows = r.page.map((i) => `<li class="text-xs text-amber-400">${esc(issueText(i))}</li>`).join('');
  $('#qualityList').innerHTML = (pageRows ? `<ul class="rounded-xl border border-ink-700 bg-ink-850 p-3 space-y-1">${pageRows}</ul>` : '') +
    r.blocks.map((blk) => {
      const b = S.page.find((x) => x.id === blk.id);
      if (!b) return '';
      const n = score(blk.issues);
      return `<div class="rounded-xl border border-ink-700 bg-ink-850 p-3">
        <button data-goto="${esc(blk.id)}" class="w-full flex items-center justify-between text-left">
          <span class="text-xs font-medium text-ink-100">${esc(specOf(b.type).label)}</span>
          <span class="text-xs font-mono ${tone(n)}">${n}/100</span>
        </button>
        ${blk.issues.length ? `<ul class="mt-2 space-y-1.5">${blk.issues.map((i) => `
          <li class="text-[11px] leading-snug ${i.severity === 'error' ? 'text-red-400' : 'text-amber-400'}">${esc(issueText(i))}
            ${i.snippet ? `<span class="block text-ink-400 font-mono truncate">${esc(i.snippet)}</span>` : ''}</li>`).join('')}</ul>` : ''}
      </div>`;
    }).join('');
});

/* ───────── Onglet Marque : marque, design system, SEO ───────── */

const BRAND = { brandName: 'name', brandColors: 'colors', brandTone: 'tone' };
const SEO = { seoTitle: 'title', seoDescription: 'description', seoImage: 'og_image', seoUrl: 'url', seoLang: 'lang' };
const THEME = { themeMode: 'mode', themePrimary: 'primary', themeAccent: 'accent', themeDisplay: 'font_display', themeSans: 'font_sans', themeRadius: 'radius', themeStyle: 'style' };

function fillBrand() {
  for (const [id, k] of Object.entries(BRAND)) $('#' + id).value = S.brand[k] || '';
  for (const [id, k] of Object.entries(SEO)) $('#' + id).value = S.seo[k] || '';
  const opts = (kinds) => `<option value="">${esc(t('studio.fontSystem'))}</option>` +
    S.cfg.fonts.filter((f) => kinds.includes(f.kind)).map((f) => `<option value="${esc(f.family)}">${esc(f.family)}</option>`).join('');
  $('#themeDisplay').innerHTML = opts(['display', 'serif', 'sans']);
  $('#themeSans').innerHTML = opts(['sans', 'serif', 'mono']);
  $('#themeFontsRow').classList.toggle('hidden', !S.cfg.fonts.length);
  $('#themeEnabled').checked = !!S.theme.enabled;
  for (const [id, k] of Object.entries(THEME)) $('#' + id).value = S.theme[k] || '';
  renderThemeState();
  $('#projectName').value = S.project.name;
}

function renderThemeState() {
  $('#themeFields').classList.toggle('opacity-40', !S.theme.enabled);
  $('#themeFields').classList.toggle('pointer-events-none', !S.theme.enabled);
  $('#themeSwatches').innerHTML = ['primary', 'accent'].map((name) =>
    `<div class="flex h-4 rounded overflow-hidden">${Object.values(S.theme.scales[name]).map((c) => `<span class="flex-1" style="background:${esc(c)}"></span>`).join('')}</div>`).join('');
  $('#btnRetheme').disabled = !S.theme.enabled || !S.page.some((b) => b.html);
}

const themeChanged = debounce(() => { saveMeta(); reloadShell(); }, 250);

export function initPanels() {
  document.addEventListener('click', (e) => {
    const tab = e.target.closest('.tab-btn'); if (tab) return switchTab(tab.dataset.tab);
    const preset = e.target.closest('[data-preset]');
    if (preset) { $('#promptInput').value = preset.textContent.trim(); $('#promptInput').focus(); return; }
    const goto = e.target.closest('[data-goto]'); if (goto) return select(goto.dataset.goto);
  });

  $('#btnGenerate').addEventListener('click', generate);
  $('#btnVariants').addEventListener('click', variants);
  $('#btnStop').addEventListener('click', stop);
  $('#genErrorRetry').addEventListener('click', generate);
  $('#genErrorClose').addEventListener('click', hideError);
  $('#promptInput').addEventListener('keydown', (e) => { if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') generate(); });
  $('#promptInput').addEventListener('input', () => { const b = selected(); if (b) b.prompt = $('#promptInput').value; });
  $('#imagesToggle').addEventListener('change', (e) => { prefs.images = e.target.checked; });

  // Retouche ciblée
  $('#btnRetouchMode').addEventListener('click', () => setMode(S.mode === 'element' ? 'block' : 'element'));
  $('#btnRetouch').addEventListener('click', () => retouchElement($('#retouchInput').value));
  $('#retouchInput').addEventListener('keydown', (e) => { if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') retouchElement($('#retouchInput').value); });
  $('#btnElParent').addEventListener('click', selectParent);
  $('#btnElDelete').addEventListener('click', deleteElement);
  $('#btnElDone').addEventListener('click', () => setMode('block'));
  $('#btnPickImage').addEventListener('click', pickImage);

  // Code
  $('#btnApplyCode').addEventListener('click', () => { const b = selected(); if (!b) return; commit(b, getCode(), 'code'); toast(t('editor.applied')); });
  $('#btnExpandCode').addEventListener('click', () => {
    const panel = $('#rightPanel');
    const wide = panel.classList.toggle('w-[46rem]');
    panel.classList.toggle('w-[24rem]', !wide);
    $('#btnExpandCode span').textContent = wide ? t('editor.collapse') : t('editor.expand');
  });
  $('#btnCopyBlock').addEventListener('click', async () => {
    const b = selected(); if (!b) return;
    await navigator.clipboard.writeText(b.html || '');
    toast(t('editor.copiedBlock'));
  });

  $('#btnUndo').addEventListener('click', () => undo(-1));
  $('#btnRedo').addEventListener('click', () => undo(1));
  $('#btnAudit').addEventListener('click', requestAudit);

  // Marque / SEO / nom du projet
  for (const [id, k] of Object.entries(BRAND)) $('#' + id).addEventListener('input', (e) => { S.brand[k] = e.target.value; saveMeta(); });
  for (const [id, k] of Object.entries(SEO)) $('#' + id).addEventListener('input', (e) => { S.seo[k] = e.target.value; saveMeta(); if (k === 'lang') reloadShell(); });
  $('#projectName').addEventListener('input', (e) => {
    const name = e.target.value.trim(); if (!name) return;
    S.project.name = name; document.title = name + ' — NADAPROD'; $('#projectLabel').textContent = name; saveMeta();
  });

  // Design system
  $('#themeEnabled').addEventListener('change', (e) => { S.theme.enabled = e.target.checked; renderThemeState(); themeChanged(); });
  for (const [id, k] of Object.entries(THEME)) {
    $('#' + id).addEventListener('input', (e) => {
      S.theme = normalizeTheme({ ...S.theme, [k]: e.target.value });
      renderThemeState();
      if (k === 'style') saveMeta(); else themeChanged(); // la direction artistique ne change pas l'aperçu
    });
  }
  $('#btnRetheme').addEventListener('click', rethemePage);

  initI18n();
  on('project', fillBrand);
  on('select', () => { hideError(); if (S.tab === 'history') renderHistory(); });
  on('history', () => { renderInspector(); if (S.tab === 'history') renderHistory(); });
}

/* ───────── Sélecteur de photo (banque d'images) ───────── */

function pickImage() {
  const el = S.element;
  if (!el?.isImg) return;
  const m = openModal({
    title: t('studio.imagesTitle'), size: 'lg',
    body: `<form data-form class="flex gap-2">
        <input data-q autofocus value="${esc(el.photo || el.alt || '')}" placeholder="${esc(t('studio.imagesPh'))}"
          class="flex-1 h-10 rounded-xl bg-ink-800 border border-ink-600 focus:border-vio-500 focus:ring-1 focus:ring-vio-500 outline-none text-sm px-3 select-text">
        <button class="h-10 px-4 rounded-xl text-sm font-semibold bg-vio-600 hover:bg-vio-500 text-white">${esc(t('studio.search'))}</button>
      </form>
      <p data-note class="mt-3 text-xs text-ink-400"></p>
      <div data-grid class="mt-3 grid grid-cols-2 sm:grid-cols-3 gap-3"></div>`,
  });
  const grid = m.el.querySelector('[data-grid]'), note = m.el.querySelector('[data-note]');
  const search = async () => {
    const q = m.el.querySelector('[data-q]').value.trim();
    if (!q) return;
    note.textContent = t('studio.searching');
    grid.innerHTML = '';
    try {
      const r = await api('/images/search?q=' + encodeURIComponent(q));
      note.textContent = r.photos.length ? t('studio.imagesCredit', { provider: r.provider }) : t('studio.imagesNone');
      grid.innerHTML = r.photos.map((p, i) => `
        <button data-i="${i}" class="group relative rounded-lg overflow-hidden border border-ink-700 hover:border-vio-500 focus-visible:ring-2 focus-visible:ring-vio-500 outline-none">
          <img src="${esc(p.thumb)}" alt="${esc(p.alt)}" loading="lazy" class="w-full aspect-[4/3] object-cover">
          <span class="absolute inset-x-0 bottom-0 bg-black/70 px-2 py-1 text-[10px] text-ink-200 truncate text-left">${esc(p.author)}</span>
        </button>`).join('');
      grid.onclick = (e) => {
        const btn = e.target.closest('[data-i]'); if (!btn) return;
        const p = r.photos[+btn.dataset.i];
        setImage(p.url, [p.author, p.provider].filter(Boolean).join(' — '));
        closeModal();
      };
    } catch (e) { note.textContent = e.message; }
  };
  m.el.querySelector('[data-form]').onsubmit = (e) => { e.preventDefault(); search(); };
  if (el.photo) search();
}

/* ───────── Messages de l'aperçu ───────── */

onFrame('np:block-click', (d) => { if (S.selectedId !== d.id) select(d.id, { scroll: false }); });
onFrame('np:el-selected', (d) => {
  if (translatedPreview()) return exitPreview(true); // on ne retouche que la source
  S.element = d;
  if (S.selectedId !== d.id) { S.selectedId = d.id; emit('select'); }
  if (S.tab !== 'generate') switchTab('generate');
  emit('render');
});
onFrame('np:el-deselected', () => { S.element = null; emit('render'); });
onFrame('np:block-html', (d) => {
  if (translatedPreview()) return exitPreview(true); // HTML traduit : jamais réécrit dans le bloc source
  const b = S.page.find((x) => x.id === d.id);
  if (b && d.html && d.html !== b.html) commit(b, d.html, d.reason === 'retouch' ? 'retouch' : d.reason === 'image' ? 'image' : 'edit');
});
