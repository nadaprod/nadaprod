/* Studio NADAPROD — persistance : projets côté serveur (SQLite).
 *
 * Écritures par bloc + méta, sérialisées dans une file unique : chaque réponse
 * porte la nouvelle révision (rev), renvoyée à la requête suivante. Un 409
 * signifie que le projet a changé ailleurs (autre onglet) : on arrête tout et
 * on propose de recharger plutôt que d'écraser.
 */
import { S, $, api, emit, toast, sleep, openModal, esc, debounce } from './core.js';
import { normalizeTheme } from './theme.js';

const LAST = 'nadaprod:lastProject';
const MIGRATED = 'nadaprod:migrated';

const pendingBlocks = new Map(); // id → source de la dernière modification
let metaDirty = false;
let queue = [];
let running = false;
let idle = [];        // résolveurs de flush()
let halted = false;   // conflit : plus aucune écriture

function status(state) {
  const el = $('#saveState');
  if (!el) return;
  el.dataset.state = state;
  el.textContent = t('studio.save_' + state);
  el.className = 'text-[11px] font-mono ' + ({ saved: 'text-ink-400', saving: 'text-ink-300', offline: 'text-amber-400', conflict: 'text-red-400' }[state] || 'text-ink-400');
}

/* ───────── Chargement ───────── */

function adopt(p) {
  S.project = { id: p.id, name: p.name, rev: p.rev, publish: p.publish || {}, owner: p.owner };
  S.page = (p.blocks || []).map((b) => ({ id: b.id, type: b.type, html: b.html || '', prompt: b.prompt || '', can_undo: !!b.can_undo, can_redo: !!b.can_redo }));
  S.brand = { name: '', colors: '', tone: '', ...(p.brand || {}) };
  S.theme = normalizeTheme(p.theme);
  S.seo = { title: '', description: '', og_image: '', url: '', lang: 'fr', ...(p.seo || {}) };
  S.selectedId = S.page[0]?.id || null;
  S.element = null;
  pendingBlocks.clear(); metaDirty = false; queue = []; halted = false;
  try { localStorage.setItem(LAST, p.id); } catch (_) {}
  if (location.pathname !== '/editor/' + p.id) history.replaceState(null, '', '/editor/' + p.id);
  document.title = p.name + ' — NADAPROD';
  status('saved');
  emit('project');
  emit('render');
}

export async function openProject(id) {
  await flush();
  adopt(await api('/projects/' + id));
}

export async function createProject(input = {}) {
  await flush();
  adopt(await api('/projects', { method: 'POST', body: input }));
}

/* Premier lancement dans ce navigateur : le projet de l'ancien studio
 * (localStorage « nadaprod », ou « windpage » encore avant) devient un projet
 * serveur. La clé héritée « windpage » est supprimée au passage. */
async function migrateLegacy() {
  let legacy = null;
  try {
    if (localStorage.getItem(MIGRATED)) return false;
    legacy = JSON.parse(localStorage.getItem('nadaprod') || localStorage.getItem('windpage') || 'null');
  } catch (_) { return false; }
  const blocks = (legacy?.page || []).filter((b) => b && b.type);
  if (!blocks.length) { try { localStorage.setItem(MIGRATED, '1'); } catch (_) {} return false; }
  await createProject({
    name: legacy.brand?.name || t('editor.defaultPageName'),
    brand: legacy.brand || {},
    blocks: blocks.map((b) => ({ id: b.id, type: b.type, html: b.html || '', prompt: b.prompt || '' })),
  });
  try { localStorage.setItem(MIGRATED, '1'); localStorage.removeItem('windpage'); } catch (_) {}
  toast(t('studio.migrated'));
  return true;
}

/* Projet à ouvrir au démarrage : URL, sinon dernier ouvert, sinon le plus
 * récent, sinon migration de l'ancien studio, sinon projet vide. */
export async function boot() {
  const fromURL = (location.pathname.match(/^\/editor\/([a-z0-9]{8,32})$/) || [])[1];
  // Venu de /sites : ?site=X&path=Y ⇒ nouveau projet destiné à cette page ;
  // ?from=X&path=Y ⇒ la page existante découpée en blocs.
  const q = new URLSearchParams(location.search);
  if (q.get('from') && q.get('path')) {
    await flush();
    const r = await api('/projects/from-page', { method: 'POST', body: { site: q.get('from'), path: q.get('path') } });
    adopt(await api('/projects/' + r.id));
    toast(t('studio.importedFromSite', { n: r.blocks }));
    return;
  }
  if (q.get('site')) {
    const site = q.get('site'), path = q.get('path') || 'index.html';
    const p = await api('/projects', { method: 'POST', body: { name: path === 'index.html' ? site : site + ' · ' + path } });
    const publish = JSON.stringify({ provider: 'site', site, path });
    await api('/projects/' + p.id, { method: 'PUT', body: { publish: JSON.parse(publish), rev: p.rev } });
    adopt(await api('/projects/' + p.id));
    return;
  }
  if (await migrateLegacy()) return;
  let last = null;
  try { last = localStorage.getItem(LAST); } catch (_) {}
  for (const id of [fromURL, last].filter(Boolean)) {
    try { adopt(await api('/projects/' + id)); return; } catch (e) { if (e.status !== 404 && e.status !== 400) throw e; }
  }
  const list = await api('/projects');
  if (list.length) adopt(await api('/projects/' + list[0].id));
  else adopt(await api('/projects', { method: 'POST', body: { name: t('editor.defaultPageName') } }));
}

/* ───────── Écritures ───────── */

/* Une frappe (source « edit ») est regroupée avec les suivantes ; tout le reste
 * (génération, variante, code appliqué…) est une version à part entière,
 * écrite sans attendre pour ne pas être fusionnée avec l'action suivante. */
export function saveBlock(b, source = 'edit') {
  pendingBlocks.set(b.id, source);
  status('saving');
  if (source === 'edit') flushSoon(); else { flushSoon.cancel(); schedule(); }
}
export function saveMeta() { metaDirty = true; flushSoon(); status('saving'); }

const flushSoon = debounce(() => { schedule(); }, 500);

function schedule() {
  if (halted || !S.project) return;
  const pid = S.project.id;
  // Les blocs d'abord : l'ordre (méta) ne peut placer qu'un bloc déjà connu du serveur.
  for (const [id, source] of pendingBlocks) {
    const b = S.page.find((x) => x.id === id);
    if (!b) continue; // supprimé entre-temps : la méta s'en charge
    const snapshot = { type: b.type, html: b.html || '', prompt: b.prompt || '', source };
    queue.push(async (keepalive) => {
      const res = await api(`/projects/${pid}/blocks/${id}`, { method: 'PUT', body: { ...snapshot, rev: S.project.rev }, keepalive });
      S.project.rev = res.rev;
      const live = S.page.find((x) => x.id === id);
      if (live && res.block) { live.can_undo = res.block.can_undo; live.can_redo = res.block.can_redo; emit('history'); }
    });
  }
  pendingBlocks.clear();
  if (metaDirty) {
    metaDirty = false;
    queue.push(async (keepalive) => {
      const body = { name: S.project.name, brand: S.brand, theme: S.theme, seo: S.seo, order: S.page.map((b) => b.id), rev: S.project.rev };
      const res = await api('/projects/' + pid, { method: 'PUT', body, keepalive });
      S.project.rev = res.rev;
    });
  }
  run();
}

async function run() {
  if (running) return;
  running = true;
  let failures = 0;
  while (queue.length && !halted) {
    status('saving');
    try {
      await queue[0](document.visibilityState === 'hidden');
      queue.shift();
      failures = 0;
    } catch (e) {
      if (e.code === 'conflict') { halted = true; queue = []; status('conflict'); conflict(); break; }
      if (e.status >= 400 && e.status < 500 && e.status !== 429) { // refus définitif : inutile d'insister
        queue.shift();
        toast(e.message, false);
        continue;
      }
      status('offline');
      await sleep(Math.min(15000, 1000 * 2 ** failures++));
    }
  }
  running = false;
  if (!halted && !queue.length) status('saved');
  if (!queue.length) { idle.forEach((r) => r()); idle = []; }
}

/* flush : tout ce qui est en attente est écrit avant de continuer (annuler,
 * exporter, changer de projet : le serveur doit avoir le dernier état). */
export function flush() {
  flushSoon.cancel();
  schedule();
  if (!queue.length && !running) return Promise.resolve();
  return new Promise((r) => idle.push(r));
}

function conflict() {
  const m = openModal({
    title: t('studio.conflictTitle'), size: 'sm',
    body: `<p class="text-sm text-ink-200 leading-relaxed">${esc(t('studio.conflictBody'))}</p>
      <div class="mt-5 flex justify-end"><button data-reload autofocus class="h-9 px-4 rounded-lg text-sm font-semibold btn-primary">${esc(t('studio.reload'))}</button></div>`,
  });
  m.el.querySelector('[data-reload]').onclick = () => location.reload();
}

// Onglet masqué ou fermé : dernière chance d'écrire (keepalive survit au déchargement).
document.addEventListener('visibilitychange', () => {
  if (document.visibilityState !== 'hidden') return;
  flushSoon.cancel();
  schedule();
});
window.addEventListener('beforeunload', (e) => {
  if (queue.length || pendingBlocks.size || metaDirty) { e.preventDefault(); e.returnValue = ''; }
});

/* ───────── Historique d'un bloc ───────── */

export async function versions(blockId) {
  await flush();
  return api(`/projects/${S.project.id}/blocks/${blockId}/versions`);
}

/* checkout : { step: -1 | 1 } (annuler / rétablir) ou { version_id }. */
export async function checkout(blockId, move) {
  await flush();
  const res = await api(`/projects/${S.project.id}/blocks/${blockId}/checkout`, { method: 'POST', body: { ...move, rev: S.project.rev } });
  S.project.rev = res.rev;
  const b = S.page.find((x) => x.id === blockId);
  if (b) Object.assign(b, { html: res.block.html, prompt: res.block.prompt, can_undo: res.block.can_undo, can_redo: res.block.can_redo });
  emit('render');
  emit('history');
  return res.block;
}
