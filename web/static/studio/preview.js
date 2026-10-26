/* Studio NADAPROD — aperçu : iframe isolée + pont postMessage (voir frame.js). */
import { S, $, esc, emit, specOf, debounce } from './core.js';
import { tailwindConfig, fontFaceCSS, families, googleFontsHref } from './theme.js';

const VP_WIDTH = { desktop: '100%', tablet: '768px', mobile: '390px' };
let ready = false;
let shellKey = '';      // empreinte de ce qui impose de recharger l'iframe (thème, polices)
const handlers = {};    // type de message → fonction
let overrides = null;   // aperçu dans une langue cible : id de bloc → HTML traduit (onglet Traduction)

const frame = () => $('#preview');
function send(msg) { if (ready) frame().contentWindow.postMessage(msg, '*'); }

export function onFrame(type, fn) { handlers[type] = fn; }

/* Document hôte de l'aperçu : Tailwind (Play CDN — les classes des blocs sont
 * imprévisibles), config du thème, polices locales, puis frame.js. */
function shell() {
  const cfg = tailwindConfig();
  const o = location.origin;
  return `<!DOCTYPE html>
<html lang="${esc(S.seo.lang || 'fr')}">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<style>${fontFaceCSS()}</style>
<script src="https://cdn.tailwindcss.com"><\/script>
${cfg ? `<script>tailwind.config = ${JSON.stringify(cfg).replace(/</g, '\\u003c')};<\/script>` : ''}
<link rel="stylesheet" href="${o}/static/studio/frame.css">
</head>
<body class="antialiased${S.theme?.enabled ? ' font-sans' : ''}">
<div id="np-root"></div>
<script src="${o}/static/studio/frame.js"><\/script>
</body>
</html>`;
}

function blocksPayload() {
  return {
    type: 'np:sync',
    hint: t('editor.skeletonHint'),
    selectedId: S.selectedId,
    blocks: S.page.map((b) => ({ id: b.id, html: overrides?.[b.id] ?? b.html ?? '', label: specOf(b.type).label })),
  };
}

/* Aperçu traduit : null rend la main à la source. Le mode retouche est coupé
 * (une retouche porterait sur le texte traduit, pas sur la source). */
export function setPreviewOverrides(map) {
  overrides = map;
  if (map && S.mode === 'element') setMode('block');
  send(blocksPayload());
}

/* Rend l'aperçu : recharge l'iframe seulement si l'enveloppe a changé (thème),
 * sinon synchronise les blocs en place (le défilement est conservé). */
export function renderPreview() {
  $('#emptyState').classList.toggle('hidden', S.page.length > 0);
  const key = JSON.stringify([tailwindConfig(), families(), S.seo.lang]);
  if (key !== shellKey) {
    shellKey = key;
    ready = false;
    frame().srcdoc = shell();
    return; // np:ready déclenchera la synchronisation
  }
  send(blocksPayload());
}

export const reloadShell = debounce(() => { shellKey = ''; renderPreview(); }, 350);

export function streamBlock(id, html) { send({ type: 'np:stream', id, html }); }
export function selectInPreview(id, scroll = true) { send({ type: 'np:select', id, scroll }); }
export function setMode(mode) {
  S.mode = mode;
  if (mode === 'block') S.element = null;
  send({ type: 'np:mode', mode });
  emit('render');
}
export const replaceElement = (html) => send({ type: 'np:replace-el', html });
export const setImage = (src, credit) => send({ type: 'np:set-img', src, credit });
export const selectParent = () => send({ type: 'np:parent' });
export const deleteElement = () => send({ type: 'np:delete-el' });
export const requestAudit = () => send({ type: 'np:audit' });

export function renderViewport() {
  document.querySelectorAll('.vp-btn').forEach((btn) => {
    const active = btn.dataset.vp === S.viewport;
    btn.setAttribute('aria-pressed', String(active));
  });
  const el = $('#frameShell');
  el.style.width = VP_WIDTH[S.viewport];
  el.style.maxWidth = '100%';
}

window.addEventListener('message', (e) => {
  // L'iframe est isolée (origine opaque « null ») : on authentifie par la source.
  if (e.source !== frame().contentWindow || !e.data || typeof e.data.type !== 'string') return;
  if (e.data.type === 'np:ready') {
    ready = true;
    send({ type: 'np:mode', mode: S.mode });
    send(blocksPayload());
    return;
  }
  handlers[e.data.type]?.(e.data);
});

/* ───────── Documents autonomes (copie rapide, vignettes de variantes) ───────── */

/* Page complète version CDN : « Copier la page » — l'export compilé passe par le serveur. */
export function pageHTML() {
  const cfg = tailwindConfig();
  const fam = families();
  const body = S.page.map((b) => b.html).filter(Boolean).join('\n\n');
  return `<!DOCTYPE html>
<html lang="${esc(S.seo.lang || 'fr')}">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>${esc(S.seo.title || S.brand.name || S.project?.name || t('editor.defaultPageName'))}</title>
${fam.length ? `<link rel="stylesheet" href="${esc(googleFontsHref())}">\n` : ''}<script src="https://cdn.tailwindcss.com"><\/script>
${cfg ? `<script>tailwind.config = ${JSON.stringify(cfg).replace(/</g, '\\u003c')};<\/script>\n` : ''}</head>
<body class="antialiased${S.theme?.enabled ? ' font-sans' : ''}">
${body}
</body>
</html>`;
}

/* Un bloc seul, pour une vignette (variantes, comparaison de versions). */
export function blockDoc(html) {
  const cfg = tailwindConfig();
  return `<!DOCTYPE html><html><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1.0">
<style>${fontFaceCSS()}html{pointer-events:none}</style>
<script src="https://cdn.tailwindcss.com"><\/script>
${cfg ? `<script>tailwind.config = ${JSON.stringify(cfg).replace(/</g, '\\u003c')};<\/script>` : ''}
</head><body class="antialiased${S.theme?.enabled ? ' font-sans' : ''}">${html}</body></html>`;
}

/* Vignette : iframe 1280 px mise à l'échelle dans son conteneur. */
export function thumb(html, { width = 1280, height = 720 } = {}) {
  return `<div class="np-thumb relative w-full overflow-hidden border border-ink-700 bg-white" style="aspect-ratio:${width}/${height}">
    <iframe sandbox="allow-scripts" tabindex="-1" loading="lazy" title="" class="absolute top-0 left-0 origin-top-left border-0"
      style="width:${width}px;height:${height}px" srcdoc="${esc(blockDoc(html))}"></iframe></div>`;
}

/* Ajuste l'échelle des vignettes à la largeur réellement disponible. */
export function fitThumbs(root = document) {
  root.querySelectorAll('.np-thumb').forEach((box) => {
    const f = box.querySelector('iframe');
    f.style.transform = `scale(${box.clientWidth / parseFloat(f.style.width)})`;
  });
}
