/* Studio NADAPROD — palette de commandes (⌘K / Ctrl+K). */
import { S, $, esc, icon, selected, openModal, closeModal, modalOpen } from './core.js';
import { addBlock, remove } from './structure.js';
import { generate, variants, rethemePage } from './generate.js';
import { switchTab, undo } from './panels.js';
import { setMode, renderViewport, requestAudit } from './preview.js';
import { exportAs, copyPage, exportJSON, publishDialog, projectsDialog, siteDialog } from './exporter.js';

function commands() {
  const b = selected();
  const has = S.page.some((x) => x.html);
  const list = [
    ...S.catalog.filter((c) => !c.hidden).map((c) => ({ label: t('studio.cmdAdd', { block: c.label }), icon: c.icon, run: () => addBlock(c.type) })),
    { label: t('editor.generate'), hint: '⌘↵', when: !!b, run: generate },
    { label: t('studio.variantsBtn'), when: !!b, run: variants },
    { label: t('studio.undo'), hint: '⌘Z', when: !!b?.can_undo, run: () => undo(-1) },
    { label: t('studio.redo'), hint: '⇧⌘Z', when: !!b?.can_redo, run: () => undo(1) },
    { label: t('editor.delBlock'), when: !!b, run: () => remove(b.id) },
    { label: t(S.mode === 'element' ? 'studio.retouchOff' : 'studio.retouchOn'), run: () => setMode(S.mode === 'element' ? 'block' : 'element') },
    { label: t('studio.cmdQuality'), when: has, run: () => { switchTab('quality'); requestAudit(); } },
    { label: t('studio.rethemeBtn'), when: has && S.theme.enabled, run: rethemePage },
    ...['generate', 'code', 'history', 'quality', 'brand'].map((n) => ({ label: t('studio.cmdTab', { tab: $(`.tab-btn[data-tab="${n}"]`).textContent.trim() }), run: () => switchTab(n) })),
    ...['desktop', 'tablet', 'mobile'].map((vp) => ({ label: t('studio.cmdViewport', { vp: $(`.vp-btn[data-vp="${vp}"] span`).textContent }), run: () => { S.viewport = vp; renderViewport(); } })),
    { label: t('studio.exportHTML'), when: has, run: () => exportAs('html') },
    { label: t('studio.exportZIP'), when: has, run: () => exportAs('zip') },
    { label: t('studio.exportJSX'), when: has, run: () => exportAs('jsx') },
    { label: t('studio.exportVue'), when: has, run: () => exportAs('vue') },
    { label: t('studio.exportJSON'), run: exportJSON },
    { label: t('editor.copyPage'), when: has, run: copyPage },
    { label: t('studio.siteTitle'), when: has, run: siteDialog },
    { label: t('studio.publishTitle'), when: has, run: publishDialog },
    { label: t('studio.projectsTitle'), run: projectsDialog },
    { label: t('editor.sitesLink'), run: () => { location.href = '/sites'; } },
  ];
  return list.filter((c) => c.when !== false);
}

// Recherche tolérante : sans accents ni casse, tous les mots doivent apparaître.
const fold = (s) => s.normalize('NFD').replace(/[\u0300-\u036f]/g, '').toLowerCase();

export function openPalette() {
  const all = commands();
  let shown = all, active = 0;
  const m = openModal({
    title: t('studio.paletteTitle'), size: 'sm',
    body: `<input data-q autofocus role="combobox" aria-expanded="true" aria-controls="cmdList" placeholder="${esc(t('studio.palettePh'))}"
        class="w-full h-11 rounded-xl bg-ink-800 border border-ink-600 focus:border-vio-500 focus:ring-1 focus:ring-vio-500 outline-none text-sm px-3 select-text">
      <div id="cmdList" role="listbox" class="mt-3 max-h-[50vh] overflow-y-auto scrollbar-thin space-y-0.5"></div>`,
  });
  const input = m.el.querySelector('[data-q]'), listEl = m.el.querySelector('#cmdList');
  const draw = () => {
    listEl.innerHTML = shown.length ? shown.map((c, i) => `
      <button data-i="${i}" role="option" aria-selected="${i === active}" class="w-full flex items-center gap-2.5 rounded-lg px-3 h-9 text-sm text-left ${i === active ? 'bg-vio-500/15 text-ink-100' : 'text-ink-300 hover:bg-ink-800'}">
        <span class="text-vio-400 w-4">${c.icon ? icon(c.icon) : ''}</span><span class="flex-1 truncate">${esc(c.label)}</span>
        ${c.hint ? `<kbd class="text-[10px] font-mono text-ink-400">${esc(c.hint)}</kbd>` : ''}</button>`).join('')
      : `<p class="px-3 py-4 text-sm text-ink-400">${esc(t('studio.paletteNone'))}</p>`;
    listEl.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: 'nearest' });
  };
  const run = (c) => { if (!c) return; closeModal(); c.run(); };
  input.addEventListener('input', () => {
    const words = fold(input.value).split(/\s+/).filter(Boolean);
    shown = all.filter((c) => words.every((w) => fold(c.label).includes(w)));
    active = 0; draw();
  });
  input.addEventListener('keydown', (e) => {
    if (e.key === 'ArrowDown') { e.preventDefault(); active = Math.min(shown.length - 1, active + 1); draw(); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); active = Math.max(0, active - 1); draw(); }
    else if (e.key === 'Enter') { e.preventDefault(); run(shown[active]); }
  });
  listEl.addEventListener('click', (e) => { const b = e.target.closest('[data-i]'); if (b) run(shown[+b.dataset.i]); });
  draw();
}

export function initPalette() {
  $('#btnPalette').addEventListener('click', openPalette);
  // Phase de capture : ⌘K doit gagner même quand Monaco a le focus (il s'en sert comme préfixe d'accords).
  window.addEventListener('keydown', (e) => {
    if ((e.metaKey || e.ctrlKey) && !e.altKey && e.key.toLowerCase() === 'k') { e.preventDefault(); e.stopPropagation(); if (!modalOpen()) openPalette(); }
  }, true);
  document.addEventListener('keydown', (e) => {
    const mod = e.metaKey || e.ctrlKey;
    // ⌘Z / ⇧⌘Z : historique du bloc — sauf quand on tape du texte (l'annulation native y règne).
    const typing = e.target.closest?.('input, textarea, [contenteditable], .monaco-editor');
    if (mod && e.key.toLowerCase() === 'z' && !typing && !modalOpen()) { e.preventDefault(); undo(e.shiftKey ? 1 : -1); }
  });
}
