/* Studio NADAPROD — onglet Traduction : une URL par langue (voir doc/I18N.md).
 *
 * Le HTML des blocs reste dans la langue source ; le serveur en extrait des
 * segments (texte + balises inline réduites à des marqueurs <1>…</1>) et tient
 * une mémoire de traduction par projet, indexée par le texte source. Ici :
 * langues cibles, progression, traduction automatique des manquants, relecture
 * segment par segment au clavier, aperçu dans une langue cible. */
import { S, $, api, esc, on, toast, debounce } from './core.js';
import { saveMeta, flush } from './store.js';
import { setPreviewOverrides } from './preview.js';

// Repli si /api/studio/config ne fournit pas encore la liste (ancien serveur).
const FALLBACK_LANGS = [
  ['en', 'English'], ['fr', 'Français'], ['es', 'Español'], ['de', 'Deutsch'], ['it', 'Italiano'],
  ['pt', 'Português'], ['nl', 'Nederlands'], ['pl', 'Polski'], ['ja', '日本語'], ['zh', '中文'], ['ar', 'العربية'],
].map(([code, name]) => ({ code, name }));

const T = {
  data: null,        // GET /i18n : { source, langs, segments, translations }
  lang: null,        // langue cible en cours d'édition
  preview: null,     // langue affichée dans l'aperçu (null = source)
  scope: 'page',     // 'block' | 'page'
  filter: 'all',     // 'all' | 'missing' | 'machine' | 'human'
  busy: {},          // lang → traduction automatique en cours
  picker: false,
  loading: false,
};

const source = () => (S.seo.lang || 'fr').toLowerCase();
const targets = () => (S.seo.langs || []).filter((l) => l && l !== source());
const catalog = () => (S.cfg.langs && S.cfg.langs.length ? S.cfg.langs : FALLBACK_LANGS);
const langName = (code) => catalog().find((l) => l.code === code)?.name || code;
const entry = (lang, hash) => T.data?.translations?.[lang]?.[hash];
const status = (lang, hash) => {
  const e = entry(lang, hash);
  return e && e.text ? (e.status === 'human' ? 'human' : 'machine') : 'missing';
};
const DOT = { missing: 'bg-red-400/80', machine: 'bg-amber-400', human: 'bg-emerald-400' };
const BAR = { human: 'bg-emerald-400', machine: 'bg-amber-400' };

/* Marqueurs <1>…</1> / <2/> : même jeu, bien imbriqué (miroir de i18n.Valid). */
const MARK = /<(\/?)(\d+)(\/?)>/g;
function validMarkers(src, dst) {
  const seq = (s) => [...s.matchAll(MARK)].map((m) => m[0]);
  const a = seq(src).sort(), b = seq(dst).sort();
  if (a.length !== b.length || a.some((x, i) => x !== b[i])) return false;
  const stack = [];
  for (const [, close, n, self] of dst.matchAll(MARK)) {
    if (self) continue;
    if (!close) stack.push(n); else if (stack.pop() !== n) return false;
  }
  return !stack.length;
}
/* Le serveur échappe & < > dans le texte (seuls les marqueurs sont des
   chevrons) : on affiche le texte décodé et on ré-encode à l'enregistrement. */
const SPLIT = /(<\/?\d+\/?>)/;
const decode = (s) => s.split(SPLIT).map((p, i) => i % 2 ? p : p.replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&amp;/g, '&')).join('');
const encode = (s) => s.split(SPLIT).map((p, i) => i % 2 ? p : p.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')).join('');

/* Source lisible : marqueurs rendus en puces, jamais en balises brutes. */
const srcHTML = (src) => src.split(SPLIT).map((p, i) => i % 2
  ? `<span class="inline-block align-baseline px-1 mx-px rounded-sm border border-cyan-400/40 text-cyan-300 text-[9px] leading-[14px] font-mono">${esc(p.slice(1, -1))}</span>`
  : esc(decode(p))).join('');

/* ───────── Données ───────── */

async function load() {
  if (!S.project) return;
  const pid = S.project.id;
  T.loading = true;
  try {
    const data = await api(`/projects/${pid}/i18n`);
    if (S.project?.id !== pid) return;
    T.data = data;
  } catch (e) {
    T.data = null;
    if (S.tab === 'i18n') toast(errText(e), false);
  } finally {
    T.loading = false;
  }
  if (!targets().includes(T.lang)) T.lang = targets()[0] || null;
  render();
}
const refresh = debounce(() => { if (S.tab === 'i18n' || T.preview) load().then(() => T.preview && showPreview(T.preview)); }, 600);

function counts(lang) {
  const c = { missing: 0, machine: 0, human: 0, total: 0 };
  for (const s of T.data?.segments || []) { c[status(lang, s.hash)]++; c.total++; }
  return c;
}

function visibleSegments() {
  let list = T.data?.segments || [];
  if (T.scope === 'block' && S.selectedId) list = list.filter((s) => (s.blocks || []).includes(S.selectedId));
  if (T.filter !== 'all' && T.lang) list = list.filter((s) => status(T.lang, s.hash) === T.filter);
  return list;
}

const errText = (e) => {
  const key = 'tr.err_' + e.code;
  const msg = t(key);
  return msg !== key ? msg : (e.message || t('tr.err_generic'));
};

/* ───────── Rendu ───────── */

function render() {
  if (renderHead()) renderList();
}

/* En-tête : langues, progression, bascules — sans toucher à la liste (focus,
 * saisie en cours). Renvoie false s'il n'y a rien à lister. */
function renderHead() {
  const box = $('#tab-i18n');
  if (!box || S.tab !== 'i18n') return false;
  const langs = targets();
  $('#trSource').textContent = source().toUpperCase();
  $('#trSourceName').textContent = langName(source()) === source() ? '' : langName(source());

  // Langues cibles
  $('#trTargets').innerHTML = langs.map((l) => `
    <span class="inline-flex items-center h-6 rounded-sm border ${l === T.lang ? 'border-cyan-400/70 text-cyan-200 bg-cyan-400/10' : 'border-ink-600 text-ink-300'} font-mono text-[10px]">
      <button data-tr-lang="${esc(l)}" class="px-1.5 h-full uppercase tracking-wider" title="${esc(langName(l))}">${esc(l)}</button>
      <button data-tr-remove="${esc(l)}" class="px-1 h-full border-l border-ink-700 text-ink-400 hover:text-red-400" aria-label="${esc(t('tr.removeLang', { lang: langName(l) }))}">×</button>
    </span>`).join('');
  const pick = $('#trPicker');
  pick.classList.toggle('hidden', !T.picker);
  if (T.picker) {
    pick.innerHTML = catalog().filter((l) => l.code !== source() && !langs.includes(l.code)).map((l) =>
      `<button data-tr-add="${esc(l.code)}" class="flex items-center gap-2 w-full px-2 h-7 text-left hover:bg-ink-800 rounded-sm">
        <span class="w-7 font-mono text-[10px] uppercase text-cyan-300">${esc(l.code)}</span><span class="text-[11px] text-ink-200 truncate">${esc(l.name)}</span></button>`).join('')
      || `<p class="px-2 py-1.5 text-[10px] font-mono text-ink-400">${esc(t('tr.noMoreLangs'))}</p>`;
  }

  $('#trEmpty').classList.toggle('hidden', !!langs.length);
  $('#trBody').classList.toggle('hidden', !langs.length);
  if (!langs.length) return false;

  // Progression par langue
  $('#trStats').innerHTML = langs.map((l) => {
    const c = counts(l);
    const pct = (n) => (c.total ? (n / c.total) * 100 : 0).toFixed(1);
    const busy = !!T.busy[l];
    return `<div data-tr-row-lang="${esc(l)}" class="grid grid-cols-[2.2rem_1fr_auto_auto_auto] items-center gap-2 h-7 px-1.5 ${l === T.lang ? 'bg-cyan-400/[0.06]' : ''}">
      <button data-tr-lang="${esc(l)}" class="font-mono text-[10px] uppercase tracking-wider text-left ${l === T.lang ? 'text-cyan-300' : 'text-ink-300'}">${esc(l)}</button>
      <div class="h-1 bg-ink-700 overflow-hidden flex" title="${esc(t('tr.statsTitle', c))}">
        <span class="${BAR.human}" style="width:${pct(c.human)}%"></span><span class="${BAR.machine}" style="width:${pct(c.machine)}%"></span>
      </div>
      <span class="font-mono text-[10px] tabular-nums text-ink-400"><span class="text-ink-200">${c.human}</span>/<span class="text-amber-300">${c.machine}</span>/${c.total}</span>
      <button data-tr-auto="${esc(l)}" ${busy || !c.missing ? 'disabled' : ''} class="h-5 px-1.5 rounded-sm border border-ink-600 text-[10px] font-mono text-ink-200 hover:border-cyan-400/60 hover:text-cyan-200 disabled:opacity-30 disabled:hover:border-ink-600 flex items-center gap-1" title="${esc(t('tr.autoTitle', { n: c.missing }))}">
        <span class="${busy ? 'animate-spin' : ''} inline-block">⟳</span>${c.missing}</button>
      <button data-tr-preview="${esc(l)}" aria-pressed="${T.preview === l}" class="h-5 w-5 grid place-items-center rounded-sm border ${T.preview === l ? 'border-cyan-400 text-cyan-300 bg-cyan-400/10' : 'border-ink-600 text-ink-400 hover:text-ink-100'}" title="${esc(t('tr.previewTitle'))}">
        <svg class="h-3 w-3" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M2 12s3.6-7 10-7 10 7 10 7-3.6 7-10 7S2 12 2 12Z"/><circle cx="12" cy="12" r="3"/></svg></button>
    </div>`;
  }).join('');

  // Bascules
  document.querySelectorAll('[data-tr-scope]').forEach((b) => b.setAttribute('aria-pressed', String(b.dataset.trScope === T.scope)));
  document.querySelectorAll('[data-tr-filter]').forEach((b) => b.setAttribute('aria-pressed', String(b.dataset.trFilter === T.filter)));
  return true;
}

function renderList() {
  const list = visibleSegments();
  $('#trCount').textContent = T.loading && !T.data ? '…' : String(list.length).padStart(3, '0');
  $('#trList').innerHTML = list.length ? list.map((s) => {
    const st = status(T.lang, s.hash);
    const e = entry(T.lang, s.hash);
    return `<div data-tr-seg="${esc(s.hash)}" class="tr-seg group grid grid-cols-[6px_1fr] gap-x-2 px-2 py-1.5 border-l border-transparent">
      <span class="mt-1.5 h-1.5 w-1.5 rounded-full ${DOT[st]}" title="${esc(t('tr.st_' + st))}"></span>
      <div class="min-w-0">
        <p class="text-[11px] leading-snug text-ink-300 break-words">${s.attr ? `<span class="mr-1 px-1 rounded-sm bg-ink-800 text-[9px] font-mono uppercase text-ink-400">${esc(s.attr)}</span>` : ''}${srcHTML(s.src)}</p>
        <textarea rows="1" spellcheck="true" lang="${esc(T.lang)}" class="tr-input mt-1 w-full resize-none overflow-hidden bg-transparent border-0 border-b border-ink-700 focus:border-cyan-400 outline-none text-[11px] leading-snug font-mono text-ink-100 placeholder:text-ink-600 py-0.5 select-text"
          placeholder="${esc(t('tr.missingPh'))}">${esc(decode(e?.text || ''))}</textarea>
        <p data-tr-err class="hidden mt-0.5 text-[10px] font-mono text-red-400">${esc(t('tr.err_invalid_markers'))}</p>
      </div>
    </div>`;
  }).join('') : `<p class="px-2 py-6 text-center text-[10px] font-mono uppercase tracking-[0.15em] text-ink-400/70">${esc(t(T.data ? 'tr.noSegments' : 'tr.loading'))}</p>`;
  document.querySelectorAll('#trList .tr-input').forEach(grow);
}

function grow(ta) { ta.style.height = 'auto'; ta.style.height = ta.scrollHeight + 'px'; }

/* ───────── Actions ───────── */

function setLangs(list) {
  S.seo.langs = [...new Set(list)].filter((l) => l && l !== source());
  saveMeta();
  if (!S.seo.langs.includes(T.lang)) T.lang = S.seo.langs[0] || null;
  if (T.preview && !S.seo.langs.includes(T.preview)) exitPreview();
  render();
  flush().then(load); // le serveur doit connaître les langues avant de relire
}

async function save(row, { validate = false } = {}) {
  const hash = row.dataset.trSeg;
  const ta = row.querySelector('.tr-input');
  const seg = T.data?.segments.find((s) => s.hash === hash);
  const prev = entry(T.lang, hash);
  const text = encode(ta.value.trim());
  if (!seg || !T.lang) return false;
  if (!validate && text === (prev?.text || '')) return true; // rien à écrire
  if (text && !validMarkers(seg.src, text)) { mark(row, true); return false; }
  mark(row, false);
  try {
    await api(`/projects/${S.project.id}/i18n/${T.lang}`, { method: 'PUT', body: { items: [{ hash, text }] } });
    const tr = (T.data.translations[T.lang] ||= {});
    if (text) tr[hash] = { text, status: 'human' }; else delete tr[hash];
    paintRow(row);
    renderHead();
    if (T.preview === T.lang) showPreview(T.lang);
    return true;
  } catch (e) {
    if (e.code === 'invalid_markers') mark(row, true); else toast(errText(e), false);
    return false;
  }
}

function mark(row, bad) {
  row.querySelector('[data-tr-err]').classList.toggle('hidden', !bad);
  row.querySelector('.tr-input').classList.toggle('border-red-400', bad);
}
function paintRow(row) {
  const st = status(T.lang, row.dataset.trSeg);
  row.querySelector('span').className = `mt-1.5 h-1.5 w-1.5 rounded-full ${DOT[st]}`;
}

async function autoTranslate(lang) {
  if (T.busy[lang]) return;
  T.busy[lang] = true;
  render();
  try {
    await flush(); // le serveur traduit ce qu'il a en base
    // Le serveur traduit au plus 200 segments par appel : on enchaîne tant
    // qu'il en reste et qu'un lot progresse (un échec persistant ne boucle pas).
    let n = 0, failed = 0, r;
    do {
      r = await api(`/projects/${S.project.id}/i18n/${lang}/translate`, { method: 'POST', body: {} });
      const got = Object.keys(r.translations || {}).length;
      Object.assign((T.data.translations[lang] ||= {}), r.translations || {});
      n += got;
      failed = (r.failed || []).length;
      render();
      if (!got) break;
    } while (r.remaining > 0);
    toast(failed ? t('tr.autoPartial', { n, failed }) : t('tr.autoDone', { n, lang: langName(lang) }), !failed);
    if (T.preview === lang) showPreview(lang);
  } catch (e) {
    toast(errText(e), false);
  } finally {
    delete T.busy[lang];
    render();
  }
}

/* ───────── Aperçu dans une langue cible ───────── */

async function showPreview(lang) {
  try {
    await flush();
    const r = await api(`/projects/${S.project.id}/i18n/${lang}/preview`);
    T.preview = lang;
    setPreviewOverrides(r.blocks || {}, lang);
    $('#trPreviewBadge').textContent = lang.toUpperCase() + (r.missing ? ' · ' + t('tr.previewMissing', { n: r.missing }) : '');
    $('#trPreviewBadge').classList.remove('hidden');
  } catch (e) {
    toast(errText(e), false);
    exitPreview();
  }
  render();
}

export function exitPreview(notify = false) {
  if (!T.preview) return;
  if (notify) toast(t('tr.previewReadOnly'), false);
  T.preview = null;
  setPreviewOverrides(null);
  $('#trPreviewBadge')?.classList.add('hidden');
  render();
}
export const translatedPreview = () => !!T.preview;

/* ───────── Clavier ─────────
 * ↑/↓ : segment précédent / suivant (en bord de texte), Entrée : enregistre et
 * passe au suivant, ⇧Entrée : retour à la ligne, ⌘/Ctrl+Entrée : valide la
 * traduction machine telle quelle, Échap : annule la saisie du segment. */
function focusRow(row, atEnd = true) {
  if (!row) return;
  const ta = row.querySelector('.tr-input');
  ta.focus();
  const p = atEnd ? ta.value.length : 0;
  ta.setSelectionRange(p, p);
  row.scrollIntoView({ block: 'nearest' });
}

function onKey(e) {
  const ta = e.target.closest?.('.tr-input');
  if (!ta) return;
  const row = ta.closest('[data-tr-seg]');
  const next = () => row.nextElementSibling, prev = () => row.previousElementSibling;
  if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
    e.preventDefault();
    save(row, { validate: true }).then((ok) => ok && focusRow(next()));
  } else if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault();
    save(row).then((ok) => ok && focusRow(next()));
  } else if (e.key === 'Escape') {
    e.preventDefault();
    ta.value = decode(entry(T.lang, row.dataset.trSeg)?.text || '');
    mark(row, false); grow(ta);
  } else if (e.key === 'ArrowUp' && !ta.value.slice(0, ta.selectionStart).includes('\n')) {
    if (prev()) { e.preventDefault(); focusRow(prev()); }
  } else if (e.key === 'ArrowDown' && !ta.value.slice(ta.selectionEnd).includes('\n')) {
    if (next()) { e.preventDefault(); focusRow(next(), false); }
  }
}

/* ───────── Câblage ───────── */

export function openI18n() { render(); load(); }

export function initI18n() {
  const tab = $('#tab-i18n');
  tab.addEventListener('click', (e) => {
    const el = (sel) => e.target.closest(sel);
    let b;
    if ((b = el('[data-tr-lang]'))) { T.lang = b.dataset.trLang; return render(); }
    if ((b = el('[data-tr-remove]'))) return setLangs(targets().filter((l) => l !== b.dataset.trRemove));
    if ((b = el('[data-tr-add]'))) { T.picker = false; T.lang = b.dataset.trAdd; return setLangs([...targets(), b.dataset.trAdd]); }
    if (el('#trAdd')) { T.picker = !T.picker; return render(); }
    if ((b = el('[data-tr-auto]'))) return autoTranslate(b.dataset.trAuto);
    if ((b = el('[data-tr-preview]'))) return T.preview === b.dataset.trPreview ? exitPreview() : showPreview(b.dataset.trPreview);
    if ((b = el('[data-tr-scope]'))) { T.scope = b.dataset.trScope; return render(); }
    if ((b = el('[data-tr-filter]'))) { T.filter = b.dataset.trFilter; return render(); }
  });
  tab.addEventListener('keydown', onKey);
  tab.addEventListener('input', (e) => { if (e.target.classList.contains('tr-input')) { grow(e.target); mark(e.target.closest('[data-tr-seg]'), false); } });
  tab.addEventListener('focusin', (e) => {
    const row = e.target.closest?.('[data-tr-seg]');
    document.querySelectorAll('#trList .tr-seg').forEach((r) => {
      r.classList.toggle('border-cyan-400', r === row);
      r.classList.toggle('bg-cyan-400/[0.04]', r === row);
    });
  });
  // Quitter un champ enregistre aussi (sans valider une traduction machine).
  tab.addEventListener('focusout', (e) => {
    const row = e.target.closest?.('[data-tr-seg]');
    if (row && e.target.classList.contains('tr-input') && !row.contains(e.relatedTarget)) save(row);
  });
  $('#trPreviewBadge').addEventListener('click', exitPreview);

  on('project', () => { exitPreview(); T.data = null; T.lang = null; T.busy = {}; if (S.tab === 'i18n') load(); });
  on('select', () => { if (S.tab === 'i18n' && T.scope === 'block') renderList(); });
  // Tout nouveau HTML (génération, retouche, code, variante) : retour à la
  // source dans l'aperçu, puis relecture des segments.
  on('commit', () => { exitPreview(); refresh(); });
  on('history', () => { exitPreview(); refresh(); });
}
