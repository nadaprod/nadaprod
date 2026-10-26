/* Studio NADAPROD — socle : état, bus d'événements, API, toasts, modales.
 * Tous les modules importent d'ici ; aucun n'importe main.js. */

export const S = {
  catalog: [],        // catalogue des blocs (/api/blocks)
  cfg: { model: '', images: { enabled: false }, compile: false, fonts: [], publish: [] },
  project: null,      // { id, name, rev, publish }
  page: [],           // [{ id, type, html, prompt, can_undo, can_redo }]
  brand: { name: '', colors: '', tone: '' },
  theme: null,        // voir theme.js (defaultTheme)
  seo: { title: '', description: '', og_image: '', url: '', lang: 'fr' },
  selectedId: null,
  viewport: 'desktop',
  mode: 'block',      // 'block' | 'element' (retouche ciblée dans l'aperçu)
  element: null,      // élément sélectionné dans l'aperçu (mode element)
  generating: false,
  tab: 'generate',
};

export const $ = (sel, root = document) => root.querySelector(sel);
export const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));
export const uid = () => 'b' + Math.random().toString(36).slice(2, 11);
export const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const ESC = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' };
export const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ESC[c]);

export function debounce(fn, ms) {
  let timer;
  const d = (...a) => { clearTimeout(timer); timer = setTimeout(() => fn(...a), ms); };
  d.cancel = () => clearTimeout(timer);
  return d;
}

export const specOf = (type) => S.catalog.find((c) => c.type === type) || { type, label: type, icon: 'grid', description: '', presets: [] };
export const selected = () => S.page.find((b) => b.id === S.selectedId) || null;

/* ───────── Bus d'événements ─────────
 * 'render'  : l'état a changé, chaque vue se redessine
 * 'select'  : le bloc sélectionné a changé
 * 'project' : un projet vient d'être chargé
 */
const listeners = {};
export function on(evt, fn) { (listeners[evt] ||= []).push(fn); }
export function emit(evt, data) { (listeners[evt] || []).forEach((fn) => { try { fn(data); } catch (e) { console.error(evt, e); } }); }

/* ───────── API ───────── */

export class ApiError extends Error {
  constructor(message, { status = 0, code = '', data = {} } = {}) {
    super(message);
    this.status = status; this.code = code; this.data = data;
  }
}

export async function api(path, { method = 'GET', body, signal, keepalive, raw = false } = {}) {
  let res;
  const payload = body === undefined ? undefined : JSON.stringify(body);
  try {
    res = await fetch('/api' + path, {
      method, signal,
      // keepalive (écriture pendant la fermeture de l'onglet) est plafonné à 64 Ko par le navigateur.
      keepalive: !!keepalive && (!payload || payload.length < 60000),
      headers: payload === undefined ? {} : { 'Content-Type': 'application/json' },
      body: payload,
    });
  } catch (e) {
    if (e.name === 'AbortError') throw e;
    throw new ApiError(t('studio.errNetwork'), { code: 'network' });
  }
  if (res.status === 401) { // session expirée : retour à la connexion, puis ici
    location.href = '/login?next=' + encodeURIComponent(location.pathname);
    throw new ApiError(t('studio.errSession'), { status: 401, code: 'session' });
  }
  if (!res.ok) {
    let data = {};
    try { data = await res.json(); } catch (_) {}
    throw new ApiError(data.error || t('common.errorCode', { code: res.status }), { status: res.status, code: data.code || '', data });
  }
  return raw ? res : res.json();
}

/* ───────── Toast (aria-live : annoncé aux lecteurs d'écran) ───────── */

let toastTimer;
export function toast(msg, ok = true) {
  $('#toastMsg').textContent = msg;
  $('#toastDot').className = 'h-2 w-2 rounded-full shrink-0 ' + (ok ? 'bg-emerald-400' : 'bg-red-400');
  const el = $('#toast');
  el.classList.remove('translate-y-16', 'opacity-0');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.classList.add('translate-y-16', 'opacity-0'), ok ? 3200 : 5200);
}

/* ───────── Modales ─────────
 * openModal({ title, body (HTML), wide, onClose }) → { el, close }
 * Une seule modale à la fois ; Échap et clic sur le fond ferment. */

let current = null;
export function openModal({ title, body, size = 'md', onClose }) {
  closeModal();
  const widths = { sm: 'max-w-md', md: 'max-w-2xl', lg: 'max-w-5xl', xl: 'max-w-[92rem]' };
  const host = document.createElement('div');
  host.className = 'fixed inset-0 z-40 grid place-items-center p-4 bg-black/60';
  host.innerHTML = `
    <div role="dialog" aria-modal="true" aria-label="${esc(title)}" class="w-full ${widths[size] || widths.md} max-h-[90vh] flex flex-col border border-ink-700 bg-ink-900">
      <div class="shrink-0 flex items-center justify-between gap-4 px-5 h-14 border-b border-ink-700">
        <h2 class="font-semibold text-base truncate">${esc(title)}</h2>
        <button data-close class="btn btn-sm btn-icon btn-ghost" aria-label="${esc(t('studio.close'))}">
          <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M6 6l12 12M18 6 6 18" stroke-linecap="round"/></svg>
        </button>
      </div>
      <div data-body class="flex-1 min-h-0 overflow-y-auto scrollbar-thin p-5">${body}</div>
    </div>`;
  const previous = document.activeElement;
  const close = () => {
    if (current?.host !== host) return;
    host.remove(); current = null;
    try { onClose?.(); } catch (e) { console.error(e); }
    previous?.focus?.();
  };
  host.addEventListener('mousedown', (e) => { if (e.target === host) close(); });
  host.querySelector('[data-close]').addEventListener('click', close);
  document.body.appendChild(host);
  current = { host, close };
  (host.querySelector('[autofocus]') || host.querySelector('[data-close]')).focus();
  return { el: host.querySelector('[data-body]'), host, close };
}
export function closeModal() { current?.close(); }
export const modalOpen = () => !!current;

document.addEventListener('keydown', (e) => { if (e.key === 'Escape' && current) { e.stopPropagation(); current.close(); } }, true);

export function confirmBox(message, okLabel) {
  return new Promise((resolve) => {
    let answer = false;
    const m = openModal({
      title: t('studio.confirm'), size: 'sm', onClose: () => resolve(answer),
      body: `<p class="text-sm text-ink-200 leading-relaxed">${esc(message)}</p>
        <div class="mt-5 flex justify-end gap-2">
          <button data-no class="btn">${esc(t('studio.cancel'))}</button>
          <button data-yes autofocus class="btn btn-danger font-semibold">${esc(okLabel)}</button>
        </div>`,
    });
    m.el.querySelector('[data-no]').onclick = m.close;
    m.el.querySelector('[data-yes]').onclick = () => { answer = true; m.close(); };
  });
}

/* ───────── Icônes des blocs ───────── */

const ICONS = {
  'layout-top':    '<rect x="3" y="4" width="18" height="4" rx="1"/><rect x="3" y="11" width="18" height="9" rx="1" opacity=".35"/>',
  'sparkles':      '<path d="M12 4l1.5 4L18 9.5 13.5 11 12 15l-1.5-4L6 9.5 10.5 8 12 4Z"/><path d="M19 15l.7 1.8L21.5 17.5l-1.8.7L19 20l-.7-1.8-1.8-.7 1.8-.7L19 15Z"/>',
  'grid':          '<rect x="4" y="4" width="7" height="7" rx="1"/><rect x="13" y="4" width="7" height="7" rx="1"/><rect x="4" y="13" width="7" height="7" rx="1"/><rect x="13" y="13" width="7" height="7" rx="1"/>',
  'credit-card':   '<rect x="3" y="5" width="18" height="14" rx="2"/><path d="M3 10h18"/>',
  'quote':         '<path d="M8 11c0-3 2-5 5-5M8 11v5H4v-5h4ZM17 11c0-3 2-5 5-5M17 11v5h-4v-5h4Z" transform="scale(0.85) translate(2 2)"/>',
  'help':          '<circle cx="12" cy="12" r="9"/><path d="M9.5 9.5a2.5 2.5 0 1 1 3.4 2.3c-.8.3-.9 1-.9 1.7M12 17h.01"/>',
  'megaphone':     '<path d="M3 11l14-6v14L3 13v-2ZM17 8a4 4 0 0 1 0 8M7 13v5a2 2 0 0 0 4 0v-4"/>',
  'layout-bottom': '<rect x="3" y="4" width="18" height="9" rx="1" opacity=".35"/><rect x="3" y="16" width="18" height="4" rx="1"/>',
  'chart':         '<path d="M4 20V10M10 20V4M16 20v-8M22 20H2"/>',
  'badge':         '<circle cx="12" cy="10" r="6"/><path d="M8.5 15 7 21l5-2.5L17 21l-1.5-6"/>',
  'newspaper':     '<rect x="3" y="4" width="15" height="16" rx="2"/><path d="M18 8h2a1 1 0 0 1 1 1v9a2 2 0 0 1-2 2M7 8h7M7 12h7M7 16h4"/>',
  'users':         '<circle cx="9" cy="8" r="3.5"/><path d="M2.5 20c.6-3.4 3.2-5.5 6.5-5.5s5.9 2.1 6.5 5.5M16 4.8a3.5 3.5 0 0 1 0 6.4M18.5 14.9c1.7.8 2.7 2.6 3 5.1"/>',
  'mail':          '<rect x="3" y="5" width="18" height="14" rx="2"/><path d="m3.5 7 8.5 6 8.5-6"/>',
  'flag':          '<path d="M5 21V4M5 4h11l-1.5 4 1.5 4H5"/>',
};
export const icon = (name, cls = 'h-4 w-4') =>
  `<svg class="${cls}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round" stroke-linecap="round" aria-hidden="true">${ICONS[name] || ICONS.grid}</svg>`;

/* Temps relatif court (« il y a 3 min ») — Intl fait le travail de traduction. */
export function ago(iso) {
  const s = Math.round((Date.parse(iso) - Date.now()) / 1000);
  const rtf = new Intl.RelativeTimeFormat(I18N.lang, { numeric: 'auto' });
  const abs = Math.abs(s);
  if (abs < 60) return rtf.format(Math.round(s), 'second');
  if (abs < 3600) return rtf.format(Math.round(s / 60), 'minute');
  if (abs < 86400) return rtf.format(Math.round(s / 3600), 'hour');
  return rtf.format(Math.round(s / 86400), 'day');
}
