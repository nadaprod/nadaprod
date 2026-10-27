/* NADAPROD — /sites : tous les sites, une seule liste, ambiance HUD.
 *
 * Un site = une ligne (A→Z) ; « en ligne » / « brouillon » n'est qu'un repère
 * graphique (domaine branché ou non). L'en-tête porte la case « Projet en
 * cours » : un clic (ou ⌘K, /) ouvre la palette — recherche instantanée sur
 * tous les sites, ouverture directe d'une page. Ce module porte tout : liste,
 * palette, éditeur visuel / code (sites-code.js), réglages de publication,
 * nouvelle page, import, administration (root). /drafts redirige ici.
 */
'use strict';
(function () {
const $ = (s) => document.querySelector(s);
const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const api = async (url, method = 'GET', body) => {
  const res = await fetch(url, body === undefined ? { method } : { method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || t('common.errorCode', { code: res.status }));
  return data;
};
const isLive = (s) => !!s.domain;
const pagesOf = (s) => s.pages || [];
const fold = (s) => String(s || '').normalize('NFD').replace(/\p{M}/gu, '').toLowerCase();
const byName = (a, b) => fold(a.id).localeCompare(fold(b.id));
// Statut = pur repère graphique, immobile : disque plein (en ligne), anneau (brouillon).
const glyph = (s, size = 'h-2 w-2') => isLive(s)
  ? `<span class="${size} shrink-0 rounded-full bg-ink-100" title="${t('sites.searchLive')}"></span>`
  : `<span class="${size} shrink-0 rounded-full border border-ink-300" title="${t('sites.searchDraft')}"></span>`;
const ICON = {
  trash: '<svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 7h16M9 7V5a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2m-9 0 1 13h8l1-13" stroke-linecap="round" stroke-linejoin="round"/></svg>',
  search: '<svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5" stroke-linecap="round"/></svg>',
};

let sites = [];      // dernier GET /api/sites, trié A→Z
// Filtre de la liste (?filtre=live|drafts, partageable) ; la palette ⌘K, elle, reste globale.
const FILTERS = { all: () => true, live: isLive, drafts: (s) => !isLive(s) };
const param = (k, allowed, def) => { const v = new URLSearchParams(location.search).get(k); return allowed[v] ? v : def; };
let filter = param('filtre', FILTERS, 'all');
// Tri dans le filtre courant (?tri=) ; plus récent / plus gros d'abord, égalités → A→Z.
// Une date vide compte comme la plus ancienne. La palette ⌘K reste A→Z.
const newest = (k) => (a, b) => (b[k] || '').localeCompare(a[k] || '') || byName(a, b);
const SORTS = {
  az: byName,
  edit: newest('updated_at'),
  pages: (a, b) => pagesOf(b).length - pagesOf(a).length || byName(a, b),
  creation: newest('created_at'),
};
let sort = param('tri', SORTS, 'az');

// Dates lisibles, sans bibliothèque : « il y a 3 j », « 12 sept. ».
function relTime(iso) {
  const t0 = Date.parse(iso); if (!iso || isNaN(t0)) return '';
  const sec = (t0 - Date.now()) / 1000;
  const rtf = new Intl.RelativeTimeFormat(I18N.lang, { numeric: 'auto', style: 'short' });
  for (const [u, n] of [['year', 31536000], ['month', 2592000], ['week', 604800], ['day', 86400], ['hour', 3600], ['minute', 60]]) {
    if (Math.abs(sec) >= n) return rtf.format(Math.round(sec / n), u);
  }
  return rtf.format(0, 'minute');
}
function shortDate(iso) {
  const t0 = Date.parse(iso); if (!iso || isNaN(t0)) return '';
  const d = new Date(t0), sameYear = d.getFullYear() === new Date().getFullYear();
  return new Intl.DateTimeFormat(I18N.lang, { day: 'numeric', month: 'short', ...(sameYear ? {} : { year: 'numeric' }) }).format(d);
}

// Écrit ?filtre= et ?tri= dans l'adresse (les valeurs par défaut n'y figurent pas).
function writeQuery() {
  const q = new URLSearchParams(location.search);
  if (filter === 'all') q.delete('filtre'); else q.set('filtre', filter);
  if (sort === 'az') q.delete('tri'); else q.set('tri', sort);
  history.replaceState(null, '', location.pathname + (q.toString() ? '?' + q : ''));
}
let ME = null;       // compte connecté ({email, root, sites}) — /api/me au boot (le serveur fait foi)
const S = {
  siteId: null, page: null, domain: '', dirty: false, hasSelection: false, aiBusy: false, saveResolve: null,
  mode: 'visual',     // 'visual' (IA) ou 'code' (Monaco)
  frameStale: false,  // vrai si le fichier a été réécrit pendant le mode code → recharger l'iframe
  settings: {},       // réglages de publication (panneau ⚙)
};
const DEFAULT_SETTINGS = { compile: true, pwa: true, gdpr: true, fonts: true };

/* ───────── Gabarits partagés (injectés avant le passage de l'i18n) ───────── */
const MARKUP = {
  project: `
    <button id="btnProject" type="button" aria-haspopup="dialog" title="Changer de projet (⌘K)" data-i18n-title="sites.currentTitle"
      class="btn min-w-0 max-w-xs gap-2.5 shrink">
      <span class="hide-editing text-ink-300 shrink-0" data-i18n="sites.current">Projet en cours</span>
      <span id="projectGlyph" class="contents"></span>
      <span id="projectName" class="min-w-[3rem] truncate font-mono text-[12px] text-ink-100"></span>
      <kbd class="ml-1 hidden xl:inline">⌘K</kbd>
    </button>`,

  editControls: `
    <div id="editControls" class="hidden mx-auto flex items-center gap-2">
      <select id="pageSelect" class="xl:hidden field font-mono text-[12px] px-2"></select>
      <button id="btnNewPage" class="btn" title="Nouvelle page dans ce site" data-i18n-title="sites.newPageTitle" data-i18n="sites.newPage">+ Page</button>
      <button id="btnStudio" class="btn" title="Ouvrir cette page dans le studio de blocs" data-i18n-title="sites.openStudioTitle" data-i18n="sites.openStudio">Studio</button>
      <div class="seg" role="group" aria-label="Mode d'édition" data-i18n-aria-label="sites.modeGroup">
        <button data-mode="code" aria-pressed="false" class="mode-btn" title="Édition manuelle du code (Monaco)" data-i18n-title="sites.modeCodeTitle">
          <svg class="h-3.5 w-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="m8 8-4 4 4 4M16 8l4 4-4 4M13 5l-2 14" stroke-linecap="round" stroke-linejoin="round"/></svg><span class="hidden xl:inline" data-i18n="sites.modeCode">Code</span>
        </button>
        <button data-mode="visual" aria-pressed="false" class="mode-btn" title="Édition visuelle assistée par IA" data-i18n-title="sites.modeVisualTitle">
          <svg class="h-3.5 w-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 4l1.5 4L18 9.5 13.5 11 12 15l-1.5-4L6 9.5 10.5 8 12 4Z" stroke-linejoin="round"/></svg><span class="hidden xl:inline" data-i18n="sites.modeVisual">Visuel · IA</span>
        </button>
      </div>
      <button id="btnSave" class="btn-primary h-9 px-4" data-i18n="sites.save">Enregistrer la page</button>
      <span id="dirtyDot" class="hidden h-2 w-2 rounded-full bg-amber-400" title="Modifications non enregistrées" data-i18n-title="sites.dirtyTitle"></span>
    </div>

    <div id="editRight" class="hidden flex items-center gap-2">
      <button id="btnPanes" aria-pressed="false" title="Panneaux : tout masquer / restaurer (⌘B explorateur · ⌘⇧B panneau droit)" data-i18n-title="sites.panesTitle" aria-label="Panneaux" data-i18n-aria-label="sites.panes" class="btn btn-icon">
        <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><rect x="3" y="4" width="18" height="16" rx="1"/><rect data-pane-l x="4.5" y="5.5" width="4" height="13" fill="currentColor" stroke="none"/><rect data-pane-r x="15.5" y="5.5" width="4" height="13" fill="currentColor" stroke="none"/></svg>
      </button>
      <input id="domainInput" placeholder="votre-domaine.fr" data-i18n-placeholder="sites.domainPh" aria-label="Domaine" data-i18n-aria-label="sites.domainLabel" class="field w-32 xl:w-44 font-mono text-[12px] select-text read-only:bg-transparent read-only:text-ink-100 read-only:border-ink-700">
      <button id="btnDomain" class="btn" data-i18n="sites.domainAttach">Brancher le domaine</button>
      <a id="btnOpenDomain" href="#" target="_blank" rel="noopener" class="hidden btn" title="Ouvrir le site en ligne" data-i18n-title="sites.domainOpenTitle" data-i18n="sites.domainOpen">Ouvrir</a>
      <button id="btnDetach" class="hidden btn" data-i18n="sites.domainDetach">Débrancher</button>
      <div class="relative">
        <button id="btnSettings" title="Réglages de publication" data-i18n-title="sites.settingsBtn" aria-label="Réglages de publication" data-i18n-aria-label="sites.settingsBtn" aria-expanded="false" class="btn btn-icon">
          <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .34 1.87l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.7 1.7 0 0 0-1.87-.34 1.7 1.7 0 0 0-1.03 1.56V21a2 2 0 1 1-4 0v-.09a1.7 1.7 0 0 0-1.11-1.56 1.7 1.7 0 0 0-1.87.34l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.7 1.7 0 0 0 .34-1.87 1.7 1.7 0 0 0-1.56-1.03H3a2 2 0 1 1 0-4h.09a1.7 1.7 0 0 0 1.56-1.11 1.7 1.7 0 0 0-.34-1.87l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.7 1.7 0 0 0 1.87.34h.08A1.7 1.7 0 0 0 10.1 4.1V4a2 2 0 1 1 4 0v.09a1.7 1.7 0 0 0 1.03 1.56h.08a1.7 1.7 0 0 0 1.87-.34l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.7 1.7 0 0 0-.34 1.87v.08a1.7 1.7 0 0 0 1.56 1.03H21a2 2 0 1 1 0 4h-.09a1.7 1.7 0 0 0-1.56 1.03Z" stroke-linecap="round" stroke-linejoin="round"/></svg>
        </button>
        <div id="settingsPanel" class="hidden absolute right-0 top-11 w-80 border border-ink-700 bg-ink-900 p-4 space-y-4 z-50">
          <p class="label" data-i18n="sites.settingsTitle">Publication</p>
          <label class="flex items-start gap-3 cursor-pointer">
            <input type="checkbox" data-setting="compile" class="mt-0.5 h-4 w-4 accent-vio-500">
            <span class="text-[13px] leading-relaxed"><span class="font-semibold text-ink-100 block" data-i18n="sites.setCompileTitle">Tailwind compilé</span><span class="text-ink-300" data-i18n="sites.setCompileDesc">Remplace <span class="font-mono">cdn.tailwindcss.com</span> par un CSS compilé sur le serveur — plus rapide, sans dépendance externe.</span></span>
          </label>
          <label class="flex items-start gap-3 cursor-pointer">
            <input type="checkbox" data-setting="fonts" class="mt-0.5 h-4 w-4 accent-vio-500">
            <span class="text-[13px] leading-relaxed"><span class="font-semibold text-ink-100 block" data-i18n="sites.setFontsTitle">Polices locales</span><span class="text-ink-300" data-i18n="sites.setFontsDesc">Les polices <span class="font-mono">fonts.googleapis.com</span> sont copiées et servies par votre site — aucune requête vers Google.</span></span>
          </label>
          <label class="flex items-start gap-3 cursor-pointer">
            <input type="checkbox" data-setting="pwa" class="mt-0.5 h-4 w-4 accent-vio-500">
            <span class="text-[13px] leading-relaxed"><span class="font-semibold text-ink-100 block" data-i18n="sites.setPwaTitle">PWA automatique</span><span class="text-ink-300" data-i18n="sites.setPwaDesc">Manifest, icône et service worker générés : site installable et consultable hors-ligne. Si le site fournit déjà les siens, ils sont respectés.</span></span>
          </label>
          <label class="flex items-start gap-3 cursor-pointer">
            <input type="checkbox" data-setting="gdpr" class="mt-0.5 h-4 w-4 accent-vio-500">
            <span class="text-[13px] leading-relaxed"><span class="font-semibold text-ink-100 block" data-i18n="sites.setGdprTitle">Bandeau RGPD</span><span class="text-ink-300" data-i18n="sites.setGdprDesc">« Zéro stress : tout est local » — bouton OK et lien vers les <a href="https://nadaprod.com/legal" target="_blank" rel="noopener" class="text-acc-fg underline underline-offset-2">mentions légales</a>.</span></span>
          </label>
          <p class="text-xs text-ink-300 leading-relaxed border-t border-ink-700 pt-3" data-i18n="sites.settingsNote">Appliqué à la version publiée (domaine branché). L'éditeur travaille toujours sur la source d'origine.</p>
        </div>
      </div>
      <button id="btnBack" class="btn btn-ghost shrink-0" data-i18n="sites.back">← Mes sites</button>
    </div>`,

  userBox: `
    <div id="userBox" class="ml-auto flex items-center gap-2">
      <span id="userEmail" class="text-xs text-ink-300 hidden lg:inline mr-1"></span>
      <button type="button" onclick="toggleTheme()" class="btn btn-icon" title="Thème clair / sombre" data-i18n-title="common.theme" aria-label="Thème clair / sombre" data-i18n-aria-label="common.theme">◐</button>
      <button id="btnLogout" class="btn" data-i18n="common.logout">Déconnexion</button>
    </div>`,

  viewEdit: `
  <div id="viewEdit" class="hidden flex-1 min-h-0 flex">
    <aside id="explorer" class="hidden xl:flex relative w-60 shrink-0 flex-col border-r border-ink-700 bg-ink-900 min-h-0"></aside>
    <main class="flex-1 min-w-0 bg-ink-850 p-3">
      <div id="visualWrap" class="h-full overflow-hidden border border-ink-700 bg-white">
        <iframe id="frame" title="Page en cours d'édition" data-i18n-title="sites.frameTitle" class="w-full h-full"></iframe>
      </div>
      <div id="codeWrap" class="hidden h-full overflow-hidden border border-ink-700 bg-ink-900 select-text"></div>
    </main>
    <aside id="editAside" class="w-[22rem] shrink-0 border-l border-ink-700 bg-ink-900 flex flex-col min-h-0">
      <div class="flex-1 overflow-y-auto divide-y divide-ink-700">
        <section class="p-4">
          <p class="label" data-i18n="sites.selLabel">Élément sélectionné</p>
          <p id="selInfo" class="font-mono text-sm mt-2 text-ink-400" data-i18n="sites.selNone">aucun</p>
          <p class="text-xs text-ink-300 mt-2 leading-relaxed" data-i18n="sites.selHelp">Cliquez sur un élément dans la page pour le sélectionner, double-cliquez pour éditer son texte, <span class="font-mono">Échap</span> pour désélectionner.</p>
          <div class="flex gap-2 mt-3">
            <button id="btnParent" disabled class="btn btn-sm flex-1" data-i18n="sites.parent">Parent ↰</button>
            <button id="btnDelete" disabled class="btn btn-sm btn-danger flex-1" data-i18n="sites.deleteEl">Supprimer</button>
          </div>
        </section>
        <section class="p-4">
          <label for="aiPrompt" class="label block mb-2" data-i18n="sites.aiLabel">Retoucher avec l'IA</label>
          <textarea id="aiPrompt" rows="4" placeholder="Ex. : passe ce bouton en style outline violet, arrondi complet…" data-i18n-placeholder="sites.aiPh"
            class="w-full bg-ink-850 border border-ink-700 focus:border-acc-fg outline-none text-sm p-3 placeholder:text-ink-400 resize-y select-text"></textarea>
          <button id="btnAI" disabled class="btn w-full mt-2 h-10">
            <svg id="aiSpin" class="h-4 w-4 animate-spin hidden" viewBox="0 0 24 24" fill="none"><circle cx="12" cy="12" r="9" stroke="currentColor" stroke-opacity="0.25" stroke-width="3"/><path d="M21 12a9 9 0 0 0-9-9" stroke="currentColor" stroke-width="3" stroke-linecap="round"/></svg>
            <span id="aiLabel" data-i18n="sites.aiButton">Modifier l'élément</span>
          </button>
        </section>
        <section class="p-4">
          <p class="label mb-2" data-i18n="sites.liveTitle">Mise en ligne</p>
          <p id="liveInfo" class="text-xs text-ink-300 leading-relaxed"></p>
        </section>
      </div>
    </aside>
  </div>

  <!-- Nouvelle page : vierge dans le studio, copie d'une page, ou page vierge simple -->
  <dialog id="newPageDlg" class="border border-ink-700 bg-ink-900 text-ink-100 p-0 w-full max-w-md">
    <form method="dialog" id="newPageForm" class="p-6 space-y-5">
      <p class="font-semibold text-base" data-i18n="sites.newPageTitle">Nouvelle page dans ce site</p>
      <div>
        <label class="label block mb-2" for="npName" data-i18n="sites.newPageName">Nom de la page</label>
        <input id="npName" required maxlength="120" placeholder="a-propos" class="field w-full font-mono select-text">
      </div>
      <div class="space-y-2.5 text-sm">
        <label class="flex items-center gap-2"><input type="radio" name="npMode" value="studio" checked class="accent-vio-500"><span data-i18n="sites.newPageStudio">La composer dans le studio (IA)</span></label>
        <label class="flex items-center gap-2"><input type="radio" name="npMode" value="copy" class="accent-vio-500"><span data-i18n="sites.newPageCopy">Copier une page existante :</span>
          <select id="npFrom" class="field h-8 flex-1 font-mono text-[12px] px-2"></select></label>
        <label class="flex items-center gap-2"><input type="radio" name="npMode" value="blank" class="accent-vio-500"><span data-i18n="sites.newPageBlank">Page vierge, à éditer ici</span></label>
      </div>
      <div class="flex justify-end gap-2 pt-1">
        <button type="button" id="npCancel" class="btn" data-i18n="studio.cancel">Annuler</button>
        <button type="submit" class="btn-primary h-9 px-4" data-i18n="sites.newPageGo">Créer</button>
      </div>
    </form>
  </dialog>

  <!-- Palette : tous les sites A→Z, recherche instantanée -->
  <dialog id="palette" aria-label="Projets" data-i18n-aria-label="sites.current" class="mt-[12vh] w-full max-w-xl border border-ink-700 bg-ink-900 text-ink-100 p-0">
    <div class="flex items-center gap-2.5 px-4 border-b border-ink-700">
      <span class="text-ink-300">${ICON.search}</span>
      <input id="paletteInput" type="text" autocomplete="off" spellcheck="false" role="combobox" aria-expanded="true" aria-controls="paletteList" aria-autocomplete="list"
        placeholder="Rechercher un site, un domaine, une page…" data-i18n-placeholder="sites.searchPh"
        class="flex-1 h-12 bg-transparent outline-none text-sm placeholder:text-ink-400 select-text">
      <span id="paletteCount" class="text-xs tnum text-ink-300"></span>
    </div>
    <div id="paletteList" role="listbox" class="max-h-[60vh] overflow-y-auto overflow-x-hidden py-1"></div>
    <p class="px-4 py-2 border-t border-ink-700 text-xs text-ink-300" data-i18n="sites.paletteHint">↑↓ naviguer · ↵ ouvrir · Échap fermer</p>
  </dialog>

  <!-- Toast -->
  <div id="toast" class="fixed bottom-5 left-1/2 -translate-x-1/2 translate-y-16 opacity-0 transition-all duration-300 z-50 pointer-events-none">
    <div class="px-4 py-2.5 bg-ink-800 border border-ink-600 text-sm flex items-center gap-2">
      <span id="toastDot" class="h-2 w-2 rounded-full bg-emerald-400"></span><span id="toastMsg"></span>
    </div>
  </div>`,
};

// Chaque gabarit remplace son emplacement (<div data-slot="…">) dans la page.
function mount() {
  document.querySelectorAll('[data-slot]').forEach(el => { el.outerHTML = MARKUP[el.dataset.slot]; });
}

/* ───────── Monaco (chargé à la demande depuis le CDN) ───────── */
const MONACO_CDN = 'https://cdn.jsdelivr.net/npm/monaco-editor@0.52.2/min';
let monacoP = null;
function loadMonaco() {
  if (monacoP) return monacoP;
  monacoP = new Promise((resolve, reject) => {
    // Les workers Monaco ne se chargent pas cross-origin : on passe par un blob-proxy.
    window.MonacoEnvironment = {
      getWorkerUrl: () => URL.createObjectURL(new Blob(
        ["self.MonacoEnvironment={baseUrl:'" + MONACO_CDN + "/'};importScripts('" + MONACO_CDN + "/vs/base/worker/workerMain.js');"],
        { type: 'text/javascript' }
      )),
    };
    const s = document.createElement('script');
    s.src = MONACO_CDN + '/vs/loader.js';
    s.onload = () => {
      window.require.config({ paths: { vs: MONACO_CDN + '/vs' } });
      window.require(['vs/editor/editor.main'], () => {
        NPTheme.monaco(monaco); // thèmes clair / sombre, suit la bascule (static/theme.js)
        resolve(window.monaco);
      });
    };
    s.onerror = () => { monacoP = null; reject(new Error('CDN Monaco inaccessible')); };
    document.head.appendChild(s);
  });
  return monacoP;
}

let toastTimer;
function toast(msg, ok = true) {
  $('#toastMsg').textContent = msg;
  $('#toastDot').className = 'h-2 w-2 rounded-full ' + (ok ? 'bg-emerald-400' : 'bg-red-400');
  const el = $('#toast');
  el.classList.remove('translate-y-16', 'opacity-0');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.classList.add('translate-y-16', 'opacity-0'), 3200);
}

/* ───────── Vue liste ───────── */

// Le fait sur lequel on trie, en fin de ligne (rien si la date est inconnue).
function sortFact(s) {
  const f = sort === 'edit' && relTime(s.updated_at) ? t('sites.factEdited', { when: relTime(s.updated_at) })
    : sort === 'creation' && shortDate(s.created_at) ? t('sites.factCreated', { date: shortDate(s.created_at) }) : '';
  return f ? ` · <span data-fact class="text-ink-200">${esc(f)}</span>` : '';
}

function row(s) {
  const n = pagesOf(s).length;
  const open = isLive(s)
    ? `<a href="https://${encodeURIComponent(s.domain)}/" target="_blank" rel="noopener" class="btn btn-sm btn-ghost">${t('sites.visit')}</a>`
    : `<a href="/preview/${encodeURIComponent(s.id)}/" target="_blank" class="btn btn-sm btn-ghost">${t('sites.preview')}</a>`;
  // Toute la rangée ouvre l'éditeur (clic) ; le bouton du nom reste le chemin clavier.
  return `
    <div data-row="${esc(s.id)}" class="group grid grid-cols-[auto_1fr_auto] items-center gap-4 px-2 py-3 border-b border-ink-700 cursor-pointer hover:bg-ink-850">
      ${glyph(s)}
      <button data-open="${esc(s.id)}" class="min-w-0 text-left outline-offset-4">
        <span class="block truncate font-mono text-sm text-ink-100">${esc(s.id)}</span>
        <span class="block truncate text-xs text-ink-300 mt-0.5">${isLive(s) ? `<span class="font-mono">${esc(s.domain)}</span> · ` : ''}<span class="tnum ${sort === 'pages' ? 'text-ink-100 font-semibold' : ''}">${n === 1 ? t('sites.pageCountOne') : t('sites.pageCountMany', { n })}</span>${sortFact(s)}</span>
      </button>
      <div class="flex items-center gap-1 opacity-70 group-hover:opacity-100 focus-within:opacity-100 transition-opacity">
        ${open}
        <button data-newpage="${esc(s.id)}" class="btn btn-sm btn-ghost" title="${t('sites.newPageTitle')}">${t('sites.newPage')}</button>
        <button data-open="${esc(s.id)}" class="btn btn-sm">${t('sites.edit')}</button>
        ${ME && ME.root ? `<button data-remove="${esc(s.id)}" class="btn btn-sm btn-icon btn-ghost btn-danger" title="${t('sites.deleteSite')}" aria-label="${t('sites.deleteSite')}">${ICON.trash}</button>` : ''}
      </div>
    </div>`;
}

async function loadSites() {
  sites = (await fetch('/api/sites').then(r => r.json()).catch(() => []) || []).sort(byName);
  const live = sites.filter(isLive).length;
  const count = (sel, n) => document.querySelectorAll(`[data-count="${sel}"]`).forEach(el => { el.textContent = n; });
  count('sites', sites.length);
  count('live', live);
  count('drafts', sites.length - live);
  const pages = sites.reduce((n, s) => n + pagesOf(s).length, 0);
  count('pages', pages);
  renderList();
  if ($('#palette').open) renderPalette();
}

// La liste suit le filtre courant ; l'ordre A→Z est celui de `sites`.
function renderList() {
  for (const [attr, cur] of [['filter', filter], ['sort', sort]]) {
    document.querySelectorAll(`[data-${attr}]`).forEach(b => {
      const on = b.dataset[attr] === cur;
      b.setAttribute('aria-checked', String(on));
      b.tabIndex = on ? 0 : -1;
    });
  }
  const shown = sites.filter(FILTERS[filter]).sort(SORTS[sort]);
  // KPI pages : celles des sites affichés, libellé explicite selon le filtre.
  const n = shown.reduce((k, s) => k + pagesOf(s).length, 0);
  if ($('#pagesTotal')) {
    $('[data-pages-n]').textContent = n;
    $('[data-pages-label]').textContent = t(`sites.pagesKpi_${filter}${n === 1 ? '_one' : ''}`);
  }
  const empty = !sites.length ? t('sites.emptyList') : t(filter === 'live' ? 'sites.emptyLiveFilter' : 'sites.emptyDraftsFilter');
  $('#siteList').innerHTML = shown.length ? shown.map(row).join('')
    : `<p class="py-10 text-sm text-ink-300">${empty}</p>`;
}

function setFilter(f) {
  if (!FILTERS[f] || f === filter) return;
  filter = f;
  writeQuery();
  renderList();
}

function setSort(k) {
  if (!SORTS[k] || k === sort) return;
  sort = k;
  writeQuery();
  renderList();
}

/* ───────── Palette « Projet en cours » : tous les sites A→Z ───────── */
let hits = [], cursor = 0;

// Requête vide ⇒ tous les sites ; sinon nom, domaine ou nom de page.
function search(q) {
  q = fold(q.trim());
  if (!q) return sites.map(s => ({ s, pages: [] }));
  return sites.map(s => {
    const byPage = pagesOf(s).filter(p => fold(p).includes(q));
    const named = fold(s.id).includes(q) || fold(s.domain).includes(q);
    // Site trouvé par son nom : ses pages servent de raccourcis.
    return (named || byPage.length) && { s, pages: byPage.length ? byPage : pagesOf(s) };
  }).filter(Boolean);
}

function renderPalette() {
  const q = $('#paletteInput').value;
  hits = search(q);
  cursor = Math.min(cursor, Math.max(hits.length - 1, 0));
  $('#paletteCount').textContent = hits.length === sites.length ? sites.length : `${hits.length} / ${sites.length}`;
  $('#paletteList').innerHTML = hits.length ? hits.map((h, i) => `
    <div id="hit-${i}" role="option" aria-selected="${i === cursor}" data-hit="${i}"
      class="relative flex items-center gap-3 px-4 py-2 cursor-pointer ${i === cursor ? 'bg-ink-800 before:absolute before:left-0 before:inset-y-0 before:w-0.5 before:bg-acc-fg' : 'hover:bg-ink-850'}">
      ${glyph(h.s)}
      <div class="flex-1 min-w-0">
        <p class="truncate text-sm font-mono text-ink-100">${esc(h.s.id)}${isLive(h.s) ? `<span class="text-ink-300 text-xs ml-2">${esc(h.s.domain)}</span>` : ''}</p>
        ${h.pages.length && q.trim() ? `<p class="flex flex-wrap gap-1 mt-1">${h.pages.slice(0, 5).map(p =>
          `<button data-hit="${i}" data-page="${esc(p)}" class="text-[11px] font-mono px-1.5 py-0.5 border border-ink-700 text-ink-300 hover:text-ink-100 hover:border-ink-400">${esc(p)}</button>`).join('')}</p>` : ''}
      </div>
      <span class="text-xs tnum text-ink-300 shrink-0">${pagesOf(h.s).length}</span>
    </div>`).join('')
    : `<p class="px-4 py-6 text-sm text-ink-300">${esc(t('sites.searchEmpty', { q: q.trim() }))}</p>`;
  $('#paletteInput').setAttribute('aria-activedescendant', hits.length ? 'hit-' + cursor : '');
  $('#hit-' + cursor)?.scrollIntoView({ block: 'nearest' });
}

function openPalette() {
  if (modalOpen()) return;
  $('#paletteInput').value = '';
  cursor = Math.max(0, sites.findIndex(s => s.id === S.siteId));
  renderPalette();
  $('#palette').showModal();
  $('#paletteInput').focus();
}

// Un autre dialogue (nouvelle page) est déjà ouvert : la palette attend.
const modalOpen = () => [...document.querySelectorAll('dialog')].some(d => d.open);

function go(site, page) {
  $('#palette').close();
  openEditor(site, page);
}

function bindPalette() {
  const input = $('#paletteInput');
  $('#btnProject').addEventListener('click', openPalette);
  input.addEventListener('input', () => { cursor = 0; renderPalette(); });
  input.addEventListener('keydown', (e) => {
    if (!hits.length) return;
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      cursor = (cursor + (e.key === 'ArrowDown' ? 1 : -1) + hits.length) % hits.length;
      renderPalette();
    } else if (e.key === 'Enter') {
      e.preventDefault();
      go(hits[cursor].s);
    }
  });
  $('#paletteList').addEventListener('click', (e) => {
    const hit = e.target.closest('[data-hit]');
    if (hit) go(hits[+hit.dataset.hit].s, hit.dataset.page);
  });
  // Clic sur le fond (hors du contenu) : fermeture.
  $('#palette').addEventListener('click', (e) => { if (e.target === e.currentTarget) e.currentTarget.close(); });
  document.addEventListener('keydown', (e) => {
    const typing = /^(INPUT|TEXTAREA|SELECT)$/.test(document.activeElement?.tagName) || document.activeElement?.isContentEditable;
    if (((e.metaKey || e.ctrlKey) && e.key === 'k') || (e.key === '/' && !typing)) {
      e.preventDefault();
      $('#palette').open ? $('#palette').close() : openPalette();
    }
  });
}

function renderProject() {
  const site = sites.find(s => s.id === S.siteId);
  $('#projectGlyph').innerHTML = site ? glyph(site) : '';
  $('#projectName').textContent = site ? site.id : t('sites.currentNone');
  $('#projectName').classList.toggle('text-ink-400', !site);
}

/* ───────── Nouveau site / nouvelle page (passerelle avec le studio) ───────── */

// Site créé de zéro (root) : dossier vide, puis le studio pour composer index.html.
async function newSite() {
  const name = prompt(t('sites.newSitePrompt'));
  if (!name || !name.trim()) return;
  try {
    const data = await api('/api/sites/new', 'POST', { name: name.trim() });
    location.href = '/editor?site=' + encodeURIComponent(data.id) + '&path=index.html';
  } catch (err) { toast(err.message, false); }
}

let npSite = null; // site visé par le dialogue « nouvelle page »
function openNewPage(siteId) {
  npSite = sites.find(s => s.id === siteId);
  if (!npSite) return;
  $('#npName').value = '';
  $('#npFrom').innerHTML = pagesOf(npSite).map(p => `<option value="${esc(p)}">${esc(p)}</option>`).join('');
  $('#npFrom').disabled = !pagesOf(npSite).length;
  $('#newPageDlg').showModal();
  $('#npName').focus();
}
async function createNewPage() {
  const name = $('#npName').value.trim();
  const mode = document.querySelector('input[name=npMode]:checked').value;
  if (!name || !npSite) return;
  $('#newPageDlg').close();
  if (mode === 'studio') {
    const path = /\.html$/i.test(name) || name.includes('/') ? name : name.toLowerCase().replace(/[^a-z0-9/._-]+/g, '-').replace(/^-|-$/g, '') + '.html';
    location.href = '/editor?site=' + encodeURIComponent(npSite.id) + '&path=' + encodeURIComponent(path);
    return;
  }
  try {
    const data = await api('/api/sites/' + encodeURIComponent(npSite.id) + '/pages', 'POST', mode === 'copy' ? { name, from: $('#npFrom').value } : { name });
    toast(t('sites.newPageDone', { path: data.path }));
    await loadSites();
    const site = sites.find(s => s.id === npSite.id);
    if (site) openEditor(site, data.path);
  } catch (err) { toast(err.message, false); }
}

async function upload(file) {
  const st = $('#uploadStatus');
  st.classList.remove('hidden');
  st.textContent = t('sites.importing', { name: file.name });
  const fd = new FormData();
  fd.append('archive', file);
  try {
    const res = await fetch('/api/sites', { method: 'POST', body: fd });
    const data = await res.json();
    if (!res.ok) throw new Error(data.error || t('common.errorCode', { code: res.status }));
    st.textContent = t('sites.importedPages', { n: data.pages.length });
    toast(t('sites.importedToast', { id: data.id }));
    await loadSites();
  } catch (err) {
    st.textContent = '';
    toast(err.message, false);
  } finally {
    setTimeout(() => st.classList.add('hidden'), 2500);
  }
}

function bindDropzone() {
  const dz = $('#dropzone');
  if (!dz) return;
  $('#fileInput').addEventListener('change', (e) => e.target.files[0] && upload(e.target.files[0]));
  ['dragover', 'dragleave', 'drop'].forEach(ev => dz.addEventListener(ev, (e) => {
    e.preventDefault();
    dz.classList.toggle('border-vio-500', ev === 'dragover');
    if (ev === 'drop' && e.dataTransfer.files[0]) upload(e.dataTransfer.files[0]);
  }));
}

/* ───────── Administration (root) : comptes + liste d'attente ───────── */
async function loadAccounts() {
  if (!$('#accountsCard') || !(ME && ME.root)) return;
  $('#accountsCard').classList.remove('hidden');
  const [users, wl] = await Promise.all([
    fetch('/api/users').then(r => r.ok ? r.json() : []).catch(() => []),
    fetch('/api/waiting-list').then(r => r.ok ? r.json() : []).catch(() => []),
  ]);
  const siteIds = sites.map(s => s.id);
  $('#userList').innerHTML = users.length ? users.map(u => `
    <div class="py-3 border-b border-ink-700 space-y-2.5">
      <div class="flex items-center gap-2">
        <p class="flex-1 min-w-0 text-sm font-mono truncate">${esc(u.email)}</p>
        <button data-upass="${esc(u.email)}" class="btn btn-sm">${t('sites.accPass')}</button>
        <button data-udel="${esc(u.email)}" class="btn btn-sm btn-icon btn-ghost btn-danger" title="${t('sites.accDeleteBtn')}" aria-label="${t('sites.accDeleteBtn')}">${ICON.trash}</button>
      </div>
      <div class="flex flex-wrap gap-x-4 gap-y-1.5">
        ${siteIds.map(id => `<label class="flex items-center gap-1.5 text-xs cursor-pointer text-ink-300 hover:text-ink-100">
          <input type="checkbox" data-assign="${esc(u.email)}" value="${esc(id)}" ${u.sites.includes(id) ? 'checked' : ''} class="h-3.5 w-3.5 accent-vio-500"><span class="font-mono">${esc(id)}</span>
        </label>`).join('') || `<span class="text-xs text-ink-300">${t('sites.accNoSites')}</span>`}
      </div>
    </div>`).join('')
  : `<p class="py-3 text-sm text-ink-300">${t('sites.accEmpty')}</p>`;
  $('#waitList').innerHTML = (wl || []).length ? wl.slice().reverse().map(e => `
    <div class="py-2.5 border-b border-ink-700 text-xs">
      <span class="font-mono tnum text-ink-300">${esc((e.date || '').slice(0, 10))}</span>
      <span class="font-mono text-ink-100 ml-2">${esc(e.email)}</span>
      ${e.name ? `<span class="text-ink-200 ml-2">${esc(e.name)}</span>` : ''}
      ${e.message ? `<p class="text-ink-300 mt-1 leading-relaxed">${esc(e.message)}</p>` : ''}
    </div>`).join('')
  : `<p class="py-3 text-sm text-ink-300">${t('sites.accWlEmpty')}</p>`;
}

// L'attribution d'un site est enregistrée dès le clic sur sa case.
async function saveAssignments(email) {
  const assigned = [...document.querySelectorAll(`[data-assign="${CSS.escape(email)}"]`)]
    .filter(cb => cb.checked).map(cb => cb.value);
  try {
    await api('/api/users/' + encodeURIComponent(email), 'PUT', { sites: assigned });
    toast(t('sites.accUpdated', { email }));
  } catch (err) { toast(err.message, false); loadAccounts(); }
}

async function addUser() {
  const email = $('#nuEmail').value.trim(), password = $('#nuPass').value;
  if (!email || !password) return toast(t('sites.accRequired'), false);
  try {
    await api('/api/users', 'POST', { email, password });
    $('#nuEmail').value = ''; $('#nuPass').value = '';
    toast(t('sites.accCreated', { email }));
    loadAccounts();
  } catch (err) { toast(err.message, false); }
}

/* ───────── Vue édition ───────── */
function showEditor(on) {
  $('#viewList').classList.toggle('hidden', on);
  $('#viewEdit').classList.toggle('hidden', !on);
  $('#viewEdit').classList.toggle('flex', on);
  $('#editControls').classList.toggle('hidden', !on);
  $('#editRight').classList.toggle('hidden', !on);
  $('#userBox').classList.toggle('hidden', on);
  document.querySelector('header').toggleAttribute('data-editing', on);
  renderProject();
}

function openEditor(site, page) {
  if (S.siteId && S.dirty && !confirm(t('sites.confirmLeave'))) return;
  if (S.mode === 'code') CodeMode.close();
  S.siteId = site.id; S.domain = site.domain || ''; S.dirty = false;
  S.settings = Object.assign({}, DEFAULT_SETTINGS, site.settings);
  // L'atelier de code d'abord (plus léger) ; le visuel · IA est le mode de
  // secours — chargé seulement à la demande, ou si Monaco est injoignable.
  configureCode();
  S.mode = 'code'; S.frameStale = true; renderMode();
  renderSettings();
  showEditor(true);
  renderDomain();
  $('#pageSelect').innerHTML = pagesOf(site).map(p => `<option value="${esc(p)}">${esc(p)}</option>`).join('');
  const target = pagesOf(site).includes(page) ? page : pagesOf(site)[0];
  loadExplorer(site);
  if (target) { loadPage(target); $('#pageSelect').value = target; } else openNewPage(site.id);
}
function closeEditor() {
  S.siteId = null;
  showEditor(false);
  loadSites();
}
async function loadPage(p) {
  S.page = p; S.dirty = false; setSelection(false, t('sites.selNone'));
  $('#dirtyDot').classList.add('hidden');
  markExplorer();
  if (S.mode !== 'code') return showFrame();
  S.frameStale = true; // la vue visuelle se chargera si on y passe
  try { await CodeMode.open(S.siteId, p); }
  catch (_) { S.mode = 'visual'; renderMode(); showFrame(); } // Monaco injoignable : le visuel prend le relais
}
function showFrame() {
  S.frameStale = false;
  $('#frame').src = '/edit/' + S.siteId + '/' + S.page;
}
function setSelection(has, label) {
  S.hasSelection = has;
  $('#selInfo').textContent = label;
  $('#selInfo').classList.toggle('text-ink-400', !has);
  $('#btnAI').disabled = !has || S.aiBusy;
  $('#btnParent').disabled = !has;
  $('#btnDelete').disabled = !has;
}
function setDirty(d) {
  S.dirty = d;
  $('#dirtyDot').classList.toggle('hidden', !d);
  markExplorer();
}

// Le branchement de domaine est réservé à root ; un utilisateur le voit en lecture seule.
function renderDomain() {
  const isRoot = !!(ME && ME.root), has = !!S.domain;
  const input = $('#domainInput');
  // Un seul motif : sans domaine, champ éditable + « Brancher » (root) ;
  // branché, le même champ en lecture seule (sélectionnable) + « Ouvrir » (+ « Débrancher », root).
  input.value = S.domain;
  input.readOnly = has;
  input.setAttribute('aria-readonly', String(has));
  input.classList.toggle('hidden', !has && !isRoot);
  $('#btnDomain').classList.toggle('hidden', has || !isRoot);
  $('#btnOpenDomain').classList.toggle('hidden', !has);
  $('#btnOpenDomain').href = has ? 'https://' + encodeURIComponent(S.domain) + '/' : '#';
  $('#btnDetach').classList.toggle('hidden', !has || !isRoot);
  $('#liveInfo').innerHTML = S.domain
    ? t('sites.liveOn', { domain: esc(S.domain) })
    : t(isRoot ? 'sites.liveNoneRoot' : 'sites.liveNoneUser');
}

async function attachDomain() {
  const domain = $('#domainInput').value.trim();
  try {
    const data = await api('/api/sites/' + S.siteId + '/domain', 'PUT', { domain });
    S.domain = data.domain;
    renderDomain();
    // Sans index.html, la racine du domaine est vide (choix d'auteur possible) : on le dit, sans alarme.
    const home = pagesOf(currentSite()).includes('index.html');
    toast(!domain ? t('sites.domainOff') : t(home ? 'sites.domainOn' : 'sites.domainNoIndex', { domain: data.domain }));
    loadSites(); // compteurs et filtres de la liste
  } catch (err) { toast(err.message, false); }
}

async function detachDomain() {
  if (!S.domain || !confirm(t('sites.domainDetachConfirm', { domain: S.domain }))) return;
  try {
    await api('/api/sites/' + S.siteId + '/domain', 'PUT', { domain: '' });
    S.domain = '';
    renderDomain();
    $('#domainInput').focus();
    toast(t('sites.domainOff'));
    loadSites();
  } catch (err) { toast(err.message, false); }
}

/* ───────── Réglages de publication (panneau ⚙) ───────── */
function renderSettings() {
  document.querySelectorAll('[data-setting]').forEach(cb => { cb.checked = !!S.settings[cb.dataset.setting]; });
}
function toggleSettingsPanel(show) {
  const panel = $('#settingsPanel');
  const open = show ?? panel.classList.contains('hidden');
  panel.classList.toggle('hidden', !open);
  $('#btnSettings').setAttribute('aria-expanded', String(open));
}
async function saveSettings(key, value) {
  const prev = S.settings[key];
  S.settings[key] = value;
  try {
    S.settings = (await api('/api/sites/' + S.siteId + '/settings', 'PUT', { [key]: value })).settings;
    toast(t('sites.toastSettings'));
  } catch (err) {
    S.settings[key] = prev;
    toast(err.message, false);
  }
  renderSettings();
}

/* ───────── Bascule Visuel · IA ⇄ Code (Monaco) ───────── */
const frameWin = () => $('#frame').contentWindow;
const post = (msg) => frameWin().postMessage(msg, location.origin);
let lastSelectedHTML = '';

function renderMode() {
  document.querySelectorAll('.mode-btn').forEach(b => b.setAttribute('aria-pressed', String(b.dataset.mode === S.mode)));
  $('#visualWrap').classList.toggle('hidden', S.mode !== 'visual');
  $('#codeWrap').classList.toggle('hidden', S.mode !== 'code');
  applyPanes();
  markExplorer();
}

/* ───────── Panneaux : explorateur (grands écrans) · panneau droit ─────────
   Le panneau droit dépend du mode : retouche IA en visuel, aperçu en code.
   Chaque panneau garde sa préférence ; le mode focus les masque tous sans y
   toucher, et en sortir rend la disposition telle qu'elle était. */
const wide = matchMedia('(min-width: 1280px)');
// v2 : l'aperçu du code est masqué par défaut (clé changée pour que tout le monde reparte de là).
const PANES_KEY = 'np-panes-v2';
const P = Object.assign({ explorer: true, aside: true, preview: false, zen: false }, (() => {
  try { return JSON.parse(localStorage.getItem(PANES_KEY)) || {}; } catch (_) { return {}; }
})());
const savePanes = () => { try { localStorage.setItem(PANES_KEY, JSON.stringify(P)); } catch (_) {} };
const rightPane = () => S.mode === 'code' ? 'preview' : 'aside';
const panes = () => (wide.matches ? ['explorer', rightPane()] : [rightPane()]);
const shown = (k) => !P.zen && P[k];

function applyPanes() {
  $('#explorer').classList.toggle('!hidden', !shown('explorer'));
  $('#editAside').classList.toggle('hidden', S.mode === 'code' || !shown('aside'));
  if (S.mode === 'code' && CodeMode.previewOn() !== shown('preview')) CodeMode.setPreview(shown('preview'));
  $('#btnPanes').setAttribute('aria-pressed', String(!panes().some(shown)));
  $('#btnPanes [data-pane-l]').style.opacity = wide.matches && shown('explorer') ? 1 : 0.15;
  $('#btnPanes [data-pane-r]').style.opacity = shown(rightPane()) ? 1 : 0.15;
  CodeMode.layout();
}
// Un panneau précis (⌘B, bouton « Aperçu ») : en mode focus, seul lui réapparaît.
function setPane(k, on) {
  if (P.zen) { panes().forEach(x => { P[x] = false; }); P.zen = false; }
  P[k] = on;
  savePanes(); applyPanes();
}
function togglePanes() {
  if (panes().some(shown)) P.zen = true;
  else {
    P.zen = false;
    if (!panes().some(k => P[k])) panes().forEach(k => { P[k] = true; }); // rien à restaurer : tout afficher
  }
  savePanes(); applyPanes();
}

/* ───────── Explorateur ───────── */
const currentSite = () => sites.find(s => s.id === S.siteId);

async function loadExplorer(site) {
  Explorer.load(site.id, pagesOf(site));
  const page = pagesOf(site)[0];
  if (!page) return;
  const a = await fetch('/api/sites/' + encodeURIComponent(site.id) + '/assets?page=' + encodeURIComponent(page))
    .then(r => r.ok ? r.json() : null).catch(() => null);
  if (a && S.siteId === site.id) Explorer.load(site.id, [...pagesOf(site), ...a.referenced, ...a.others].map(f => f.path || f));
}

function markExplorer() {
  const code = S.mode === 'code';
  Explorer.mark({
    active: code ? CodeMode.active() : S.page,
    dirty: code ? CodeMode.dirtyFiles() : (S.dirty ? [S.page] : []),
  });
}

// Une page s'ouvre comme depuis la liste déroulante ; un CSS/JS, dans l'atelier de code.
async function openPath(path) {
  if (pagesOf(currentSite()).includes(path) && path !== S.page) {
    if (S.dirty && !confirm(t('sites.confirmSwitchPage'))) return;
    $('#pageSelect').value = path;
    return loadPage(path);
  }
  if (S.mode !== 'code') {
    await enterCode();
    if (S.mode !== 'code') return;
  }
  try { await CodeMode.openAsset(path); } catch (_) { toast(t('sites.cantRead'), false); }
}

/* HTML courant de l'iframe, sérialisé sans artefacts d'édition. */
function getVisualHTML() {
  return new Promise((resolve) => {
    S.saveResolve = resolve;
    post({ type: 'wp:get-html' });
    setTimeout(() => { if (S.saveResolve) { S.saveResolve(null); S.saveResolve = null; } }, 3000);
  });
}

function configureCode() {
  CodeMode.configure({
    loadMonaco, toast, t, onDirty: setDirty, onFiles: markExplorer,
    onPreview: (on) => { if (shown('preview') !== on) setPane('preview', on); },
  });
}

async function enterCode() {
  if (S.mode === 'code' || !S.siteId) return;
  // Les retouches visuelles non enregistrées sont reprises telles quelles dans le code.
  let html = null;
  if (S.dirty) {
    html = await getVisualHTML();
    if (html == null) return toast(t('sites.pageNoAnswerSave'), false);
  }
  configureCode();
  try { await CodeMode.open(S.siteId, S.page, { html }); } catch (_) { return toast(t('sites.monacoDown'), false); }
  S.mode = 'code'; renderMode();
}

async function enterVisual() {
  if (S.mode === 'visual') return;
  if (CodeMode.isDirty()) {
    if (!confirm(t('sites.confirmSaveCode'))) return;
    if (!await savePage()) return;
  }
  CodeMode.close();
  S.mode = 'visual'; renderMode();
  if (S.frameStale) showFrame();
}

window.addEventListener('message', (e) => {
  if (e.origin !== location.origin || !e.data || typeof e.data.type !== 'string') return;
  switch (e.data.type) {
    case 'wp:selected':
      lastSelectedHTML = e.data.html;
      setSelection(true, '<' + e.data.tag + '>');
      break;
    case 'wp:deselected':
      setSelection(false, t('sites.selNone'));
      break;
    case 'wp:html':
      if (S.saveResolve) { S.saveResolve(e.data.html); S.saveResolve = null; }
      break;
    case 'wp:dirty':
      setDirty(true);
      break;
  }
});

async function savePage() {
  if (!S.siteId || !S.page) return false;
  if (S.mode === 'code') {
    // L'atelier écrit tous ses onglets modifiés (page + assets).
    if (!await CodeMode.saveAll()) return false;
    S.frameStale = true; // l'iframe visuelle montre encore l'ancien fichier
  } else {
    const html = await getVisualHTML();
    if (html == null) { toast(t('sites.pageNoAnswer'), false); return false; }
    try { await api('/api/sites/' + S.siteId + '/file', 'PUT', { path: S.page, html }); }
    catch (err) { toast(err.message || t('sites.saveFailed'), false); return false; }
  }
  setDirty(false);
  toast(S.domain ? t('sites.toastSavedLive', { domain: S.domain }) : t('sites.toastSaved'));
  return true;
}

async function aiEdit() {
  if (!S.hasSelection || S.aiBusy) return;
  const prompt = $('#aiPrompt').value.trim();
  if (!prompt) return toast(t('sites.aiDescribe'), false);
  S.aiBusy = true;
  $('#btnAI').disabled = true;
  $('#aiSpin').classList.remove('hidden');
  $('#aiLabel').textContent = t('sites.aiBusy');
  try {
    const data = await api('/api/generate', 'POST', { block: 'element', prompt, current_html: lastSelectedHTML, brand: {} });
    post({ type: 'wp:replace', html: data.html });
    setDirty(true);
    toast(t('sites.toastAiDone'));
  } catch (err) {
    toast(err.message, false);
  } finally {
    S.aiBusy = false;
    $('#btnAI').disabled = !S.hasSelection;
    $('#aiSpin').classList.add('hidden');
    $('#aiLabel').textContent = t('sites.aiButton');
  }
}

/* ───────── Écouteurs ───────── */
function bind() {
  // Filtre et tri : groupes radio, flèches gauche/droite (motif ARIA).
  const radios = (sel, attr, order, get, set) => $(sel)?.addEventListener('keydown', (e) => {
    const d = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[e.key];
    if (!d) return;
    e.preventDefault();
    set(order[(order.indexOf(get()) + d + order.length) % order.length]);
    $(`[data-${attr}="${get()}"]`).focus();
  });
  radios('#siteFilter', 'filter', ['all', 'live', 'drafts'], () => filter, setFilter);
  radios('#siteSort', 'sort', ['az', 'edit', 'pages', 'creation'], () => sort, setSort);
  $('#btnPanes').addEventListener('click', togglePanes);
  wide.addEventListener('change', () => S.siteId && applyPanes());
  // ⌘B / ⌘⇧B comme dans VS Code ; en capture, avant que Monaco ne voie la touche.
  window.addEventListener('keydown', (e) => {
    if (!S.siteId || !(e.metaKey || e.ctrlKey) || e.altKey || e.key.toLowerCase() !== 'b') return;
    e.preventDefault(); e.stopPropagation();
    if (e.shiftKey) setPane(rightPane(), !P[rightPane()]);
    else if (wide.matches) setPane('explorer', !P.explorer);
  }, true);
  document.addEventListener('click', async (e) => {
    const on = (sel) => e.target.closest(sel);
    let el;
    if ((el = on('.mode-btn'))) return el.dataset.mode === 'code' ? enterCode() : enterVisual();
    if ((el = on('[data-newpage]'))) return openNewPage(el.dataset.newpage);
    if ((el = on('[data-open]'))) {
      const site = sites.find(x => x.id === el.dataset.open);
      return site && openEditor(site);
    }
    if ((el = on('[data-filter]'))) return setFilter(el.dataset.filter);
    if ((el = on('[data-sort]'))) return setSort(el.dataset.sort);
    if ((el = on('[data-row]')) && !on('a, button, input, label')) {
      const site = sites.find(x => x.id === el.dataset.row);
      return site && openEditor(site);
    }
    if (on('#btnSettings')) return toggleSettingsPanel();
    if (!on('#settingsPanel')) toggleSettingsPanel(false);
    if ((el = on('[data-remove]')) && confirm(t('sites.confirmDeleteSite'))) {
      await fetch('/api/sites/' + el.dataset.remove, { method: 'DELETE' });
      toast(t('sites.toastDeleted'));
      await loadSites();
      return loadAccounts();
    }
    if ((el = on('[data-upass]'))) {
      const email = el.dataset.upass;
      const password = prompt(t('sites.accPassPrompt', { email }));
      if (!password) return;
      try { await api('/api/users/' + encodeURIComponent(email), 'PUT', { password }); toast(t('sites.accPassUpdated')); }
      catch (err) { toast(err.message, false); }
      return;
    }
    if ((el = on('[data-udel]')) && confirm(t('sites.accConfirmDelete', { email: el.dataset.udel }))) {
      await fetch('/api/users/' + encodeURIComponent(el.dataset.udel), { method: 'DELETE' });
      toast(t('sites.accDeleted'));
      return loadAccounts();
    }
  });
  document.addEventListener('change', (e) => {
    if (e.target.matches('[data-assign]')) saveAssignments(e.target.dataset.assign);
    if (e.target.matches('[data-setting]')) saveSettings(e.target.dataset.setting, e.target.checked);
  });
  $('#nuAdd')?.addEventListener('click', addUser);
  $('#btnNewSite')?.addEventListener('click', newSite);
  $('#btnLogout').addEventListener('click', async () => {
    await fetch('/api/logout', { method: 'POST' }).catch(() => {});
    location.href = '/login';
  });
  $('#btnNewPage').addEventListener('click', () => openNewPage(S.siteId));
  $('#btnStudio').addEventListener('click', () => { location.href = '/editor?from=' + encodeURIComponent(S.siteId) + '&path=' + encodeURIComponent(S.page); });
  $('#newPageForm').addEventListener('submit', (e) => { e.preventDefault(); createNewPage(); });
  $('#npCancel').addEventListener('click', () => $('#newPageDlg').close());
  $('#btnBack').addEventListener('click', () => {
    if (S.dirty && !confirm(t('sites.confirmLeave'))) return;
    closeEditor();
  });
  $('#pageSelect').addEventListener('change', (e) => {
    if (S.dirty && !confirm(t('sites.confirmSwitchPage'))) { e.target.value = S.page; return; }
    loadPage(e.target.value);
  });
  $('#btnSave').addEventListener('click', savePage);
  $('#btnAI').addEventListener('click', aiEdit);
  $('#btnParent').addEventListener('click', () => post({ type: 'wp:parent' }));
  $('#btnDelete').addEventListener('click', () => { post({ type: 'wp:delete' }); setDirty(true); });
  $('#btnDomain').addEventListener('click', attachDomain);
  $('#btnDetach').addEventListener('click', detachDomain);
  $('#domainInput').addEventListener('keydown', (e) => { if (e.key === 'Enter' && !e.target.readOnly) attachDomain(); });
  document.addEventListener('keydown', (e) => {
    if ((e.metaKey || e.ctrlKey) && e.key === 's' && S.siteId) { e.preventDefault(); savePage(); }
  });
  window.addEventListener('beforeunload', (e) => { if (S.dirty) e.preventDefault(); });
  bindPalette();
  bindDropzone();
}

/* ───────── Boot : session d'abord, puis les vues selon le rôle ───────── */
async function start() {
  mount();
  bind();
  await I18N.ready;
  Explorer.mount($('#explorer'), { t, onOpen: openPath });
  const res = await fetch('/api/me').catch(() => null);
  if (!res || res.status === 401) { location.href = '/login?next=' + encodeURIComponent(location.pathname); return; }
  ME = await res.json();
  $('#userEmail').textContent = ME.email;
  document.querySelectorAll('[data-root]').forEach(el => el.classList.toggle('hidden', !ME.root)); // import, création : root
  await loadSites();
  renderProject();
  loadAccounts();
  // Lien profond (?open=<site>&page=<chemin>), y compris depuis l'ancienne URL /drafts.
  const q = new URLSearchParams(location.search);
  const site = q.get('open') && sites.find(s => s.id === q.get('open'));
  const page = q.get('page');
  q.delete('open'); q.delete('page'); // ?filtre= reste dans l'adresse
  history.replaceState(null, '', location.pathname + (q.toString() ? '?' + q : ''));
  if (site) openEditor(site, page);
}

window.SitesApp = { start };
})();
