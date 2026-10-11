/* Studio NADAPROD — rail gauche : palette des blocs et structure de la page
 * (glisser-déposer natif, boutons ↑↓ et clavier conservés pour l'accessibilité). */
import { S, $, esc, uid, emit, icon, specOf, toast } from './core.js';
import { saveBlock, saveMeta } from './store.js';
import { selectInPreview } from './preview.js';

export function renderPalette() {
  $('#palette').innerHTML = S.catalog.filter((c) => !c.hidden).map((c) => `
    <button data-add="${esc(c.type)}" draggable="true" title="${esc(c.description)}"
      class="group flex flex-col items-start gap-1.5 rounded-xl border border-ink-700 bg-ink-850 hover:border-vio-500/60 hover:bg-ink-800 p-2.5 text-left transition-colors">
      <span class="text-vio-400">${icon(c.icon)}</span>
      <span class="text-[11px] font-medium text-ink-200 leading-tight">${esc(c.label)}</span>
    </button>`).join('');
}

export function renderStructure() {
  const el = $('#structure');
  if (!S.page.length) {
    el.innerHTML = `<p class="text-xs text-ink-400 px-1">${esc(t('editor.emptyStructure'))}</p>`;
    return;
  }
  el.innerHTML = S.page.map((b, i) => {
    const c = specOf(b.type);
    const sel = b.id === S.selectedId;
    return `
    <div data-sel="${esc(b.id)}" draggable="true" role="option" aria-selected="${sel}" tabindex="${sel ? 0 : -1}"
      class="group relative flex items-center gap-2 rounded-xl border p-2 cursor-pointer transition-colors outline-none focus-visible:ring-2 focus-visible:ring-vio-500
        ${sel ? 'border-vio-500/70 bg-vio-500/10' : 'border-ink-700 bg-ink-850 hover:border-ink-600'}">
      <span class="absolute left-0 top-2 bottom-2 w-0.5 rounded-full ${sel ? 'bg-vio-500' : 'bg-transparent'}"></span>
      <span class="cursor-grab text-ink-600 group-hover:text-ink-400" aria-hidden="true">
        <svg class="h-3.5 w-3.5" viewBox="0 0 24 24" fill="currentColor"><circle cx="9" cy="6" r="1.6"/><circle cx="15" cy="6" r="1.6"/><circle cx="9" cy="12" r="1.6"/><circle cx="15" cy="12" r="1.6"/><circle cx="9" cy="18" r="1.6"/><circle cx="15" cy="18" r="1.6"/></svg>
      </span>
      <span class="${sel ? 'text-vio-400' : 'text-ink-400'}">${icon(c.icon)}</span>
      <div class="flex-1 min-w-0">
        <p class="text-xs font-medium truncate ${sel ? 'text-ink-100' : 'text-ink-200'}">${esc(c.label)}</p>
        <p class="text-[10px] font-mono ${b.html ? 'text-emerald-400/80' : 'text-ink-400'}">${esc(b.html ? t('editor.stGenerated') : t('editor.stEmpty'))}</p>
      </div>
      <div class="flex items-center gap-0.5 opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 transition-opacity">
        <button data-up="${esc(b.id)}" ${i === 0 ? 'disabled' : ''} tabindex="-1" class="p-1 rounded hover:bg-ink-700 disabled:opacity-30" title="${esc(t('editor.up'))}" aria-label="${esc(t('editor.up'))}">
          <svg class="h-3.5 w-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 19V5m0 0-6 6m6-6 6 6" stroke-linecap="round" stroke-linejoin="round"/></svg></button>
        <button data-down="${esc(b.id)}" ${i === S.page.length - 1 ? 'disabled' : ''} tabindex="-1" class="p-1 rounded hover:bg-ink-700 disabled:opacity-30" title="${esc(t('editor.down'))}" aria-label="${esc(t('editor.down'))}">
          <svg class="h-3.5 w-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 5v14m0 0 6-6m-6 6-6-6" stroke-linecap="round" stroke-linejoin="round"/></svg></button>
        <button data-del="${esc(b.id)}" tabindex="-1" class="p-1 rounded hover:bg-red-500/20 text-red-400" title="${esc(t('editor.delBlock'))}" aria-label="${esc(t('editor.delBlock'))}">
          <svg class="h-3.5 w-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 7h16M9 7V5a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2m-9 0 1 13h8l1-13" stroke-linecap="round" stroke-linejoin="round"/></svg></button>
      </div>
    </div>`;
  }).join('');
}

/* ───────── Actions ───────── */

export function select(id, { scroll = true, focus = false } = {}) {
  if (S.selectedId !== id) { S.selectedId = id; S.element = null; emit('select'); }
  emit('render');
  selectInPreview(id, scroll);
  if (focus) $(`#structure [data-sel="${CSS.escape(id)}"]`)?.focus();
}

export function addBlock(type, at = S.page.length) {
  if (S.page.length >= (S.cfg.max_block || 60)) return toast(t('studio.tooManyBlocks'), false);
  const b = { id: uid(), type, html: '', prompt: '', can_undo: false, can_redo: false };
  S.page.splice(at, 0, b);
  saveBlock(b, 'edit');
  saveMeta();
  select(b.id);
  emit('added', b);
}

export function move(id, to) {
  const from = S.page.findIndex((b) => b.id === id);
  to = Math.max(0, Math.min(S.page.length - 1, to));
  if (from < 0 || from === to) return;
  S.page.splice(to, 0, S.page.splice(from, 1)[0]);
  saveMeta();
  emit('render');
}

export function remove(id) {
  const i = S.page.findIndex((b) => b.id === id);
  if (i < 0) return;
  S.page.splice(i, 1);
  if (S.selectedId === id) { S.selectedId = (S.page[i] || S.page[i - 1])?.id || null; S.element = null; emit('select'); }
  saveMeta(); // l'ordre sans ce bloc ⇒ le serveur le supprime (versions comprises)
  emit('render');
}

/* ───────── Glisser-déposer ─────────
 * Deux sources : un bloc de la structure (réordonner) ou une tuile de la
 * palette (insérer à un endroit précis). L'indicateur est une bordure haute
 * ou basse sur l'élément survolé. */
let drag = null; // { id } | { type }

function clearMarks() {
  document.querySelectorAll('#structure [data-drop]').forEach((n) => { n.removeAttribute('data-drop'); n.style.boxShadow = ''; });
}

function dropIndex(e) {
  const item = e.target.closest('#structure [data-sel]');
  if (!item) return { index: S.page.length, item: null, after: true };
  const r = item.getBoundingClientRect();
  const after = e.clientY > r.top + r.height / 2;
  const i = S.page.findIndex((b) => b.id === item.dataset.sel);
  return { index: after ? i + 1 : i, item, after };
}

export function initStructure() {
  const zone = $('#structure');

  document.addEventListener('dragstart', (e) => {
    const item = e.target.closest?.('#structure [data-sel]');
    const tile = e.target.closest?.('#palette [data-add]');
    if (!item && !tile) return;
    drag = item ? { id: item.dataset.sel } : { type: tile.dataset.add };
    e.dataTransfer.effectAllowed = item ? 'move' : 'copy';
    e.dataTransfer.setData('text/plain', item ? item.dataset.sel : tile.dataset.add);
    if (item) requestAnimationFrame(() => item.classList.add('opacity-40'));
  });
  document.addEventListener('dragend', () => {
    drag = null; clearMarks();
    document.querySelectorAll('#structure .opacity-40').forEach((n) => n.classList.remove('opacity-40'));
  });
  zone.addEventListener('dragover', (e) => {
    if (!drag) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = drag.id ? 'move' : 'copy';
    clearMarks();
    const { item, after } = dropIndex(e);
    if (item) { item.setAttribute('data-drop', ''); item.style.boxShadow = after ? '0 2px 0 0 #8B5CF6' : '0 -2px 0 0 #8B5CF6'; }
  });
  zone.addEventListener('dragleave', (e) => { if (!zone.contains(e.relatedTarget)) clearMarks(); });
  zone.addEventListener('drop', (e) => {
    if (!drag) return;
    e.preventDefault();
    let { index } = dropIndex(e);
    if (drag.type) addBlock(drag.type, index);
    else {
      const from = S.page.findIndex((b) => b.id === drag.id);
      if (from < index) index--; // l'élément retiré décale la cible
      move(drag.id, index);
    }
    drag = null; clearMarks();
  });

  // Clavier : ↑/↓ naviguent, Alt+↑/↓ déplacent, Suppr retire, Entrée ouvre le générateur.
  zone.addEventListener('keydown', (e) => {
    const item = e.target.closest('[data-sel]');
    if (!item) return;
    const i = S.page.findIndex((b) => b.id === item.dataset.sel);
    const dir = e.key === 'ArrowUp' ? -1 : e.key === 'ArrowDown' ? 1 : 0;
    if (dir && e.altKey) { e.preventDefault(); move(item.dataset.sel, i + dir); select(item.dataset.sel, { focus: true }); }
    else if (dir) { e.preventDefault(); const n = S.page[i + dir]; if (n) select(n.id, { focus: true }); }
    else if (e.key === 'Delete' || e.key === 'Backspace') { e.preventDefault(); remove(item.dataset.sel); $('#structure [data-sel][tabindex="0"]')?.focus(); }
    else if (e.key === 'Enter') { e.preventDefault(); $('#promptInput').focus(); }
  });

  document.addEventListener('click', (e) => {
    const el = e.target.closest('[data-add],[data-up],[data-down],[data-del],#structure [data-sel]');
    if (!el) return;
    if (el.dataset.add) return addBlock(el.dataset.add);
    if (el.dataset.up || el.dataset.down) {
      const id = el.dataset.up || el.dataset.down;
      return move(id, S.page.findIndex((b) => b.id === id) + (el.dataset.up ? -1 : 1));
    }
    if (el.dataset.del) return remove(el.dataset.del);
    if (el.dataset.sel) return select(el.dataset.sel);
  });
}
