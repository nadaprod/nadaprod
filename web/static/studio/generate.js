/* Studio NADAPROD — génération : flux progressif, variantes, re-thème,
 * retouche ciblée d'un élément, erreurs actionnables. */
import { S, $, api, ApiError, emit, toast, selected, specOf, esc, openModal, closeModal } from './core.js';
import { streamBlock, replaceElement, thumb, fitThumbs } from './preview.js';
import { themeForPrompt } from './theme.js';
import { saveBlock } from './store.js';

let aborter = null;

/* Les préférences locales du studio (ce navigateur) : photos réelles ou non. */
export const prefs = {
  get images() { try { return localStorage.getItem('nadaprod:images') !== '0'; } catch (_) { return true; } },
  set images(v) { try { localStorage.setItem('nadaprod:images', v ? '1' : '0'); } catch (_) {} },
};

function requestBody(b, extra = {}) {
  return {
    block: b.type,
    prompt: b.prompt || '',
    current_html: b.html || '',
    brand: S.brand,
    theme: themeForPrompt(),
    siblings: S.page.filter((x) => x.id !== b.id && x.html).map((x) => ({ type: x.type, html: x.html })),
    images: S.cfg.images.enabled && prefs.images,
    ...extra,
  };
}

/* commit : point de passage unique de tout nouveau HTML d'un bloc. */
export function commit(b, html, source) {
  b.html = html;
  saveBlock(b, source);
  emit('render');
}

function busy(on) {
  S.generating = on;
  $('#btnGenerate').disabled = on;
  $('#btnVariants').disabled = on;
  $('#genIcon').classList.toggle('hidden', on);
  $('#genSpin').classList.toggle('hidden', !on);
  $('#genLabel').textContent = on ? t('editor.generating') : t('editor.generate');
  $('#btnStop').classList.toggle('hidden', !on);
  $('#genStatus').textContent = on ? t('editor.generating') : ''; // aria-live
}

/* Flux partiel → HTML affichable : sans clôture markdown ni balise entamée. */
function partial(raw) {
  let s = raw.replace(/^\s*```(?:html)?\s*/, '');
  const fence = s.indexOf('```');
  if (fence >= 0) s = s.slice(0, fence);
  const lt = s.lastIndexOf('<');
  if (lt > s.lastIndexOf('>')) s = s.slice(0, lt);
  return s.replace(/<script\b[\s\S]*$/i, ''); // un script à moitié reçu n'a rien à faire dans l'aperçu
}

/* Lit le flux SSE de /api/generate/stream. */
async function stream(body, onDelta, signal) {
  let res;
  try {
    res = await fetch('/api/generate/stream', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body), signal });
  } catch (e) {
    if (e.name === 'AbortError') throw e;
    throw new ApiError(t('studio.errNetwork'), { code: 'network' });
  }
  if (res.status === 401) { location.href = '/login?next=' + encodeURIComponent(location.pathname); throw new ApiError(t('studio.errSession'), { code: 'session' }); }
  if (!res.ok) {
    let data = {}; try { data = await res.json(); } catch (_) {}
    throw new ApiError(data.error || t('common.errorCode', { code: res.status }), { status: res.status, code: data.code || '', data });
  }
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buf = '', done = null;
  for (;;) {
    const { value, done: eof } = await reader.read();
    if (eof) break;
    buf += decoder.decode(value, { stream: true });
    let cut;
    while ((cut = buf.indexOf('\n\n')) >= 0) {
      const evt = buf.slice(0, cut); buf = buf.slice(cut + 2);
      const name = (evt.match(/^event: (.*)$/m) || [])[1];
      const data = JSON.parse((evt.match(/^data: (.*)$/m) || [])[1] || '{}');
      if (name === 'delta') onDelta(data.text);
      else if (name === 'done') done = data;
      else if (name === 'error') throw new ApiError(data.error || t('editor.genFailed'), { code: data.code || '', data });
    }
  }
  if (!done) throw new ApiError(t('studio.errStreamCut'), { code: 'network' });
  return done;
}

/* ───────── Génération du bloc sélectionné ───────── */

export async function generate() {
  const b = selected();
  if (!b || S.generating) return;
  b.prompt = $('#promptInput').value.trim();
  hideError();
  $('#genMeta').classList.add('hidden');
  busy(true);
  aborter = new AbortController();
  let raw = '', last = 0;
  try {
    const data = await stream(requestBody(b), (text) => {
      raw += text;
      const now = performance.now();
      if (now - last > 120) { last = now; streamBlock(b.id, partial(raw)); }
    }, aborter.signal);
    commit(b, data.html, 'generate');
    const meta = [data.model, (data.elapsed_ms / 1000).toFixed(1) + ' s'];
    if (data.photos) meta.push(t('studio.photosCount', { n: data.photos }));
    $('#genMeta').textContent = meta.join(' · ');
    $('#genMeta').classList.remove('hidden');
    toast(t('editor.toastGenerated'));
  } catch (e) {
    emit('render'); // retire le flux partiel de l'aperçu
    if (e.name === 'AbortError') toast(t('studio.genStopped'));
    else showError(e);
  } finally {
    aborter = null;
    busy(false);
  }
}

export function stop() { aborter?.abort(); }

/* ───────── Erreurs actionnables ─────────
 * Le serveur renvoie un code ; on en tire un titre, une explication et une
 * action, au lieu d'un toast qui disparaît. */
function showError(e) {
  const known = ['quota', 'rate_limited', 'auth', 'blocked', 'empty', 'network', 'upstream', 'timeout', 'invalid'];
  const code = known.includes(e.code) ? e.code : 'upstream';
  const retry = e.data?.retry_after;
  $('#genErrorTitle').textContent = t('studio.err_' + code + '_title');
  $('#genErrorBody').textContent = t('studio.err_' + code + '_body') + (retry ? ' ' + t('studio.errRetryIn', { s: retry }) : '');
  $('#genErrorDetail').textContent = e.message || '';
  $('#genErrorRetry').classList.toggle('hidden', code === 'auth' || code === 'blocked' || code === 'invalid');
  $('#genError').classList.remove('hidden');
  $('#genStatus').textContent = $('#genErrorTitle').textContent;
}
export function hideError() { $('#genError').classList.add('hidden'); }

/* ───────── Variantes : 3 propositions parallèles en vignettes ───────── */

export async function variants() {
  const b = selected();
  if (!b || S.generating) return;
  b.prompt = $('#promptInput').value.trim();
  hideError();
  const ctrl = new AbortController();
  const slot = (i) => `
    <div data-slot="${i}" class="flex flex-col gap-3 rounded-xl border border-ink-700 bg-ink-850 p-3">
      <div class="flex items-center justify-between">
        <p class="text-[10px] font-mono uppercase tracking-[0.15em] text-ink-400">${esc(t('studio.variantDir' + i))}</p>
        <span data-state class="text-[10px] font-mono text-ink-400">${esc(t('editor.generating'))}</span>
      </div>
      <div data-view class="grid place-items-center rounded-lg bg-ink-800 text-ink-400 text-xs" style="aspect-ratio:1280/720">
        <svg class="h-5 w-5 animate-spin" viewBox="0 0 24 24" fill="none"><circle cx="12" cy="12" r="9" stroke="currentColor" stroke-opacity="0.25" stroke-width="3"/><path d="M21 12a9 9 0 0 0-9-9" stroke="currentColor" stroke-width="3" stroke-linecap="round"/></svg>
      </div>
      <button data-pick disabled class="h-9 rounded-lg text-sm font-semibold bg-vio-600 hover:bg-vio-500 text-white disabled:opacity-40 disabled:cursor-not-allowed">${esc(t('studio.variantPick'))}</button>
    </div>`;
  const m = openModal({
    title: t('studio.variantsTitle', { block: specOf(b.type).label }), size: 'xl', onClose: () => ctrl.abort(),
    body: `<p class="text-xs text-ink-400 mb-4">${esc(t('studio.variantsHint'))}</p><div class="grid gap-4 lg:grid-cols-3">${[1, 2, 3].map(slot).join('')}</div>`,
  });
  [1, 2, 3].forEach(async (i) => {
    const box = m.el.querySelector(`[data-slot="${i}"]`);
    try {
      const data = await api('/generate', { method: 'POST', body: requestBody(b, { variant: i }), signal: ctrl.signal });
      box.querySelector('[data-view]').outerHTML = thumb(data.html);
      fitThumbs(box);
      box.querySelector('[data-state]').textContent = (data.elapsed_ms / 1000).toFixed(1) + ' s';
      const pick = box.querySelector('[data-pick]');
      pick.disabled = false;
      pick.onclick = () => { commit(b, data.html, 'variant'); closeModal(); toast(t('studio.variantApplied')); };
    } catch (e) {
      if (e.name === 'AbortError') return;
      box.querySelector('[data-view]').innerHTML = `<p class="p-4 text-center text-red-400">${esc(e.message)}</p>`;
      box.querySelector('[data-state]').textContent = '—';
    }
  });
}

/* ───────── Re-thème de toute la page ─────────
 * Convertit les blocs existants aux jetons du design system, un par un. Chaque
 * bloc gagne une version : tout reste annulable. */
export async function rethemePage() {
  const todo = S.page.filter((b) => b.html);
  if (!todo.length || S.generating) return;
  const ctrl = new AbortController();
  const m = openModal({
    title: t('studio.rethemeTitle'), size: 'sm', onClose: () => ctrl.abort(),
    body: `<p data-line class="text-sm text-ink-200"></p>
      <div class="mt-3 h-1.5 rounded-full bg-ink-700 overflow-hidden"><div data-bar class="h-full bg-vio-500 transition-all" style="width:0%"></div></div>
      <p class="mt-3 text-xs text-ink-400">${esc(t('studio.rethemeHint'))}</p>`,
  });
  S.generating = true;
  let done = 0, failed = 0;
  try {
    for (const b of todo) {
      m.el.querySelector('[data-line]').textContent = t('studio.rethemeProgress', { i: done + 1, n: todo.length, block: specOf(b.type).label });
      try {
        const data = await api('/generate', { method: 'POST', body: requestBody(b, { mode: 'retheme', siblings: [], images: false }), signal: ctrl.signal });
        commit(b, data.html, 'retheme');
      } catch (e) {
        if (e.name === 'AbortError') break;
        failed++;
        if (e.code === 'quota' || e.code === 'rate_limited' || e.code === 'auth') { toast(e.message, false); break; }
      }
      done++;
      m.el.querySelector('[data-bar]').style.width = Math.round((done / todo.length) * 100) + '%';
    }
  } finally {
    S.generating = false;
    closeModal();
    toast(failed ? t('studio.rethemePartial', { n: done - failed, total: todo.length }) : t('studio.rethemeDone', { n: done }), !failed);
  }
}

/* ───────── Retouche ciblée d'un élément de l'aperçu ───────── */

export async function retouchElement(brief) {
  const el = S.element;
  if (!el || S.generating || !brief.trim()) return;
  const b = S.page.find((x) => x.id === el.id);
  if (!b) return;
  S.generating = true;
  emit('render');
  try {
    const data = await api('/generate', {
      method: 'POST',
      body: { block: 'element', prompt: brief.trim(), current_html: el.html, brand: S.brand, theme: themeForPrompt(), images: false },
    });
    replaceElement(data.html); // l'aperçu remplace l'élément puis renvoie le HTML du bloc (np:block-html)
    toast(t('studio.retouched'));
  } catch (e) {
    toast(e.message || t('editor.genFailed'), false);
  } finally {
    S.generating = false;
    emit('render');
  }
}
