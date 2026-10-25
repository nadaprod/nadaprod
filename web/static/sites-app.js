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
// Statut = pur repère graphique : point cyan qui pulse (en ligne), anneau ambre (brouillon).
const glyph = (s, size = 'h-2 w-2') => isLive(s)
  ? `<span class="${size} shrink-0 rounded-full bg-cyan-400 shadow-[0_0_8px_rgba(34,211,238,.7)] animate-pulse" title="${t('sites.searchLive')}"></span>`
  : `<span class="${size} shrink-0 rounded-full border border-amber-400/80" title="${t('sites.searchDraft')}"></span>`;
const ICON = {
  trash: '<svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 7h16M9 7V5a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2m-9 0 1 13h8l1-13" stroke-linecap="round" stroke-linejoin="round"/></svg>',
  search: '<svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5" stroke-linecap="round"/></svg>',
};

let sites = [];      // dernier GET /api/sites, trié A→Z
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
      class="hud-corners group h-9 min-w-0 max-w-xs flex items-center gap-2.5 px-3 rounded-md border border-ink-700 bg-ink-950/60 hover:border-cyan-400/50 hover:bg-ink-850 transition-colors">
      <span class="hide-editing text-[9px] font-mono uppercase tracking-[0.18em] text-ink-400 shrink-0" data-i18n="sites.current">Projet en cours</span>
      <span id="projectGlyph" class="contents"></span>
      <span id="projectName" class="min-w-0 truncate text-xs font-mono text-ink-100"></span>
      <kbd class="ml-auto text-[9px] font-mono text-ink-400 border border-ink-700 rounded px-1 py-px group-hover:text-cyan-300">⌘K</kbd>
    </button>`,

  editControls: `
    <div id="editControls" class="hidden mx-auto flex items-center gap-2">
      <select id="pageSelect" class="xl:hidden h-9 rounded-lg bg-ink-800 border border-ink-600 text-xs px-2 outline-none focus:border-vio-500"></select>
      <button id="btnNewPage" class="h-9 px-3 rounded-lg text-xs font-medium border border-ink-600 hover:bg-ink-800" title="Nouvelle page dans ce site" data-i18n-title="sites.newPageTitle" data-i18n="sites.newPage">+ Page</button>
      <button id="btnStudio" class="h-9 px-3 rounded-lg text-xs font-medium border border-vio-500/60 text-vio-400 hover:bg-vio-500/10" title="Ouvrir cette page dans le studio de blocs" data-i18n-title="sites.openStudioTitle" data-i18n="sites.openStudio">Studio</button>
      <div class="flex items-center gap-1 rounded-lg bg-ink-800 p-1 border border-ink-700" role="group" aria-label="Mode d'édition" data-i18n-aria-label="sites.modeGroup">
        <button data-mode="visual" class="mode-btn px-3 py-1.5 rounded-md text-xs font-medium flex items-center gap-1.5 text-ink-300 hover:text-ink-100" title="Édition visuelle assistée par IA" data-i18n-title="sites.modeVisualTitle">
          <svg class="h-3.5 w-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 4l1.5 4L18 9.5 13.5 11 12 15l-1.5-4L6 9.5 10.5 8 12 4Z" stroke-linejoin="round"/></svg><span data-i18n="sites.modeVisual">Visuel · IA</span>
        </button>
        <button data-mode="code" class="mode-btn px-3 py-1.5 rounded-md text-xs font-medium flex items-center gap-1.5 text-ink-300 hover:text-ink-100" title="Édition manuelle du code (Monaco)" data-i18n-title="sites.modeCodeTitle">
          <svg class="h-3.5 w-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="m8 8-4 4 4 4M16 8l4 4-4 4M13 5l-2 14" stroke-linecap="round" stroke-linejoin="round"/></svg><span data-i18n="sites.modeCode">Code</span>
        </button>
      </div>
      <button id="btnSave" class="h-9 px-4 rounded-lg text-xs font-semibold btn-primary" data-i18n="sites.save">Enregistrer la page</button>
      <span id="dirtyDot" class="hidden h-2 w-2 rounded-full bg-amber-400" title="Modifications non enregistrées" data-i18n-title="sites.dirtyTitle"></span>
    </div>

    <div id="editRight" class="hidden flex items-center gap-2">
      <button id="btnPanes" aria-pressed="false" title="Panneaux : tout masquer / restaurer (⌘B explorateur · ⌘⇧B panneau droit)" data-i18n-title="sites.panesTitle" aria-label="Panneaux" data-i18n-aria-label="sites.panes" class="h-9 w-9 rounded-lg border border-ink-600 hover:bg-ink-800 grid place-items-center text-ink-300 hover:text-ink-100 aria-pressed:border-vio-500/60 aria-pressed:text-vio-300">
        <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><rect x="3" y="4" width="18" height="16" rx="2"/><rect data-pane-l x="4.5" y="5.5" width="4" height="13" rx=".5" fill="currentColor" stroke="none"/><rect data-pane-r x="15.5" y="5.5" width="4" height="13" rx=".5" fill="currentColor" stroke="none"/></svg>
      </button>
      <input id="domainInput" placeholder="votre-domaine.fr" data-i18n-placeholder="sites.domainPh" class="h-9 w-40 rounded-lg bg-ink-800 border border-ink-600 text-xs px-3 outline-none focus:border-vio-500 select-text">
      <button id="btnDomain" class="h-9 px-3 rounded-lg text-xs font-medium border border-ink-600 hover:bg-ink-800" data-i18n="sites.domainAttach">Brancher le domaine</button>
      <span id="domainRO" class="hidden h-9 px-3 rounded-lg text-xs font-mono border border-ink-700 text-emerald-400 items-center"></span>
      <div class="relative">
        <button id="btnSettings" title="Réglages de publication" data-i18n-title="sites.settingsBtn" aria-label="Réglages de publication" data-i18n-aria-label="sites.settingsBtn" aria-expanded="false" class="h-9 w-9 rounded-lg border border-ink-600 hover:bg-ink-800 grid place-items-center text-ink-300 hover:text-ink-100">
          <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .34 1.87l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.7 1.7 0 0 0-1.87-.34 1.7 1.7 0 0 0-1.03 1.56V21a2 2 0 1 1-4 0v-.09a1.7 1.7 0 0 0-1.11-1.56 1.7 1.7 0 0 0-1.87.34l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.7 1.7 0 0 0 .34-1.87 1.7 1.7 0 0 0-1.56-1.03H3a2 2 0 1 1 0-4h.09a1.7 1.7 0 0 0 1.56-1.11 1.7 1.7 0 0 0-.34-1.87l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.7 1.7 0 0 0 1.87.34h.08A1.7 1.7 0 0 0 10.1 4.1V4a2 2 0 1 1 4 0v.09a1.7 1.7 0 0 0 1.03 1.56h.08a1.7 1.7 0 0 0 1.87-.34l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.7 1.7 0 0 0-.34 1.87v.08a1.7 1.7 0 0 0 1.56 1.03H21a2 2 0 1 1 0 4h-.09a1.7 1.7 0 0 0-1.56 1.03Z" stroke-linecap="round" stroke-linejoin="round"/></svg>
        </button>
        <div id="settingsPanel" class="hidden absolute right-0 top-11 w-80 rounded-xl border border-ink-700 bg-ink-900 shadow-2xl shadow-black/60 p-4 space-y-4 z-50">
          <p class="text-[10px] font-mono uppercase tracking-[0.15em] text-ink-400" data-i18n="sites.settingsTitle">Publication</p>
          <label class="flex items-start gap-3 cursor-pointer">
            <input type="checkbox" data-setting="compile" class="mt-0.5 h-4 w-4 rounded border-ink-600 bg-ink-800 accent-vio-500">
            <span class="text-xs leading-relaxed"><span class="font-semibold text-ink-100 block" data-i18n="sites.setCompileTitle">Tailwind compilé</span><span class="text-ink-400" data-i18n="sites.setCompileDesc">Remplace <span class="font-mono">cdn.tailwindcss.com</span> par un CSS compilé sur le serveur — plus rapide, sans dépendance externe.</span></span>
          </label>
          <label class="flex items-start gap-3 cursor-pointer">
            <input type="checkbox" data-setting="fonts" class="mt-0.5 h-4 w-4 rounded border-ink-600 bg-ink-800 accent-vio-500">
            <span class="text-xs leading-relaxed"><span class="font-semibold text-ink-100 block" data-i18n="sites.setFontsTitle">Polices locales</span><span class="text-ink-400" data-i18n="sites.setFontsDesc">Les polices <span class="font-mono">fonts.googleapis.com</span> sont copiées et servies par votre site — aucune requête vers Google.</span></span>
          </label>
          <label class="flex items-start gap-3 cursor-pointer">
            <input type="checkbox" data-setting="pwa" class="mt-0.5 h-4 w-4 rounded border-ink-600 bg-ink-800 accent-vio-500">
            <span class="text-xs leading-relaxed"><span class="font-semibold text-ink-100 block" data-i18n="sites.setPwaTitle">PWA automatique</span><span class="text-ink-400" data-i18n="sites.setPwaDesc">Manifest, icône et service worker générés : site installable et consultable hors-ligne. Si le site fournit déjà les siens, ils sont respectés.</span></span>
          </label>
          <label class="flex items-start gap-3 cursor-pointer">
            <input type="checkbox" data-setting="gdpr" class="mt-0.5 h-4 w-4 rounded border-ink-600 bg-ink-800 accent-vio-500">
            <span class="text-xs leading-relaxed"><span class="font-semibold text-ink-100 block" data-i18n="sites.setGdprTitle">Bandeau RGPD</span><span class="text-ink-400" data-i18n="sites.setGdprDesc">« Zéro stress : tout est local » — bouton OK et lien vers les <a href="https://nadaprod.com/legal" target="_blank" rel="noopener" class="text-vio-400 hover:underline">mentions légales</a>.</span></span>
          </label>
          <p class="text-[11px] text-ink-400 leading-relaxed border-t border-ink-700 pt-3" data-i18n="sites.settingsNote">Appliqué à la version publiée (domaine branché). L'éditeur travaille toujours sur la source d'origine.</p>
        </div>
      </div>
      <button id="btnBack" class="h-9 px-3 rounded-lg text-xs font-medium text-ink-300 hover:text-ink-100" data-i18n="sites.back">← Mes sites</button>
    </div>`,

  userBox: `
    <div id="userBox" class="ml-auto flex items-center gap-2">
      <span id="userEmail" class="text-xs font-mono text-ink-400 hidden lg:inline"></span>
      <button type="button" onclick="toggleTheme()" class="h-9 w-9 rounded-lg border border-ink-600 hover:bg-ink-800 text-ink-300 hover:text-ink-100 grid place-items-center" title="Thème clair / sombre" data-i18n-title="common.theme" aria-label="Thème clair / sombre" data-i18n-aria-label="common.theme">◐</button>
      <button id="btnLogout" class="h-9 px-3 rounded-lg text-xs font-medium border border-ink-600 hover:bg-ink-800 text-ink-300 hover:text-ink-100" data-i18n="common.logout">Déconnexion</button>
    </div>`,

  viewEdit: `
  <div id="viewEdit" class="hidden flex-1 min-h-0 flex">
    <aside id="explorer" class="hidden xl:flex relative w-60 shrink-0 flex-col border-r border-ink-700 bg-ink-900 min-h-0"></aside>
    <main class="flex-1 min-w-0 bg-ink-850 p-4">
      <div id="visualWrap" class="h-full rounded-xl overflow-hidden ring-1 ring-ink-700 shadow-2xl shadow-black/50 bg-white">
        <iframe id="frame" title="Page en cours d'édition" data-i18n-title="sites.frameTitle" class="w-full h-full"></iframe>
      </div>
      <div id="codeWrap" class="hidden h-full rounded-xl overflow-hidden ring-1 ring-ink-700 shadow-2xl shadow-black/50 bg-ink-850 select-text"></div>
    </main>
    <aside id="editAside" class="w-[22.5rem] shrink-0 border-l border-ink-700 bg-ink-900 flex flex-col min-h-0">
      <div class="p-4 space-y-4 flex-1 overflow-y-auto">
        <div>
          <p class="text-[10px] font-mono uppercase tracking-[0.15em] text-ink-400" data-i18n="sites.selLabel">Élément sélectionné</p>
          <p id="selInfo" class="font-display font-semibold text-lg tracking-tight mt-0.5 text-ink-400" data-i18n="sites.selNone">aucun</p>
          <p class="text-xs text-ink-400 mt-1 leading-relaxed" data-i18n="sites.selHelp">Cliquez sur un élément dans la page pour le sélectionner, double-cliquez pour éditer son texte, <span class="font-mono">Échap</span> pour désélectionner.</p>
        </div>
        <div class="flex gap-2">
          <button id="btnParent" disabled class="flex-1 h-9 rounded-lg text-xs font-medium border border-ink-600 hover:bg-ink-800 disabled:opacity-40" data-i18n="sites.parent">Parent ↰</button>
          <button id="btnDelete" disabled class="flex-1 h-9 rounded-lg text-xs font-medium border border-red-500/40 text-red-400 hover:bg-red-500/10 disabled:opacity-40" data-i18n="sites.deleteEl">Supprimer</button>
        </div>
        <div>
          <label for="aiPrompt" class="text-[10px] font-mono uppercase tracking-[0.15em] text-ink-400 block mb-2" data-i18n="sites.aiLabel">Retoucher avec l'IA</label>
          <textarea id="aiPrompt" rows="4" placeholder="Ex. : passe ce bouton en style outline violet, arrondi complet…" data-i18n-placeholder="sites.aiPh"
            class="w-full rounded-xl bg-ink-800 border border-ink-600 focus:border-vio-500 focus:ring-1 focus:ring-vio-500 outline-none text-sm p-3 placeholder:text-ink-400/70 resize-y select-text"></textarea>
          <button id="btnAI" disabled class="mt-2 w-full h-11 rounded-xl btn-primary text-sm font-semibold disabled:opacity-50 disabled:cursor-not-allowed flex items-center justify-center gap-2">
            <svg id="aiSpin" class="h-4 w-4 animate-spin hidden" viewBox="0 0 24 24" fill="none"><circle cx="12" cy="12" r="9" stroke="currentColor" stroke-opacity="0.25" stroke-width="3"/><path d="M21 12a9 9 0 0 0-9-9" stroke="currentColor" stroke-width="3" stroke-linecap="round"/></svg>
            <span id="aiLabel" data-i18n="sites.aiButton">Modifier l'élément</span>
          </button>
        </div>
        <div class="rounded-xl border border-ink-700 bg-ink-850 p-3">
          <p class="text-[10px] font-mono uppercase tracking-[0.15em] text-ink-400 mb-2" data-i18n="sites.liveTitle">Mise en ligne</p>
          <p id="liveInfo" class="text-xs text-ink-300 leading-relaxed"></p>
        </div>
      </div>
    </aside>
  </div>

  <!-- Nouvelle page : vierge dans le studio, copie d'une page, ou page vierge simple -->
  <dialog id="newPageDlg" class="rounded-2xl border border-ink-600 bg-ink-900 text-ink-100 p-0 w-full max-w-md backdrop:bg-black/70">
    <form method="dialog" id="newPageForm" class="p-5 space-y-4">
      <p class="font-display font-semibold tracking-tight" data-i18n="sites.newPageTitle">Nouvelle page dans ce site</p>
      <div>
        <label class="text-[10px] font-mono uppercase tracking-[0.15em] text-ink-400 block mb-2" for="npName" data-i18n="sites.newPageName">Nom de la page</label>
        <input id="npName" required maxlength="120" placeholder="a-propos" class="w-full h-10 rounded-xl bg-ink-800 border border-ink-600 focus:border-vio-500 outline-none text-sm px-3 select-text">
      </div>
      <div class="space-y-2 text-sm">
        <label class="flex items-center gap-2"><input type="radio" name="npMode" value="studio" checked class="accent-vio-500"><span data-i18n="sites.newPageStudio">La composer dans le studio (IA)</span></label>
        <label class="flex items-center gap-2"><input type="radio" name="npMode" value="copy" class="accent-vio-500"><span data-i18n="sites.newPageCopy">Copier une page existante :</span>
          <select id="npFrom" class="h-8 flex-1 rounded-lg bg-ink-800 border border-ink-600 text-xs px-2 outline-none"></select></label>
        <label class="flex items-center gap-2"><input type="radio" name="npMode" value="blank" class="accent-vio-500"><span data-i18n="sites.newPageBlank">Page vierge, à éditer ici</span></label>
      </div>
      <div class="flex justify-end gap-2">
        <button type="button" id="npCancel" class="h-9 px-4 rounded-lg text-sm border border-ink-600 hover:bg-ink-800" data-i18n="studio.cancel">Annuler</button>
        <button type="submit" class="h-9 px-4 rounded-lg text-sm font-semibold btn-primary" data-i18n="sites.newPageGo">Créer</button>
      </div>
    </form>
  </dialog>

  <!-- Palette : tous les sites A→Z, recherche instantanée -->
  <dialog id="palette" aria-label="Projets" data-i18n-aria-label="sites.current" class="hud-corners overflow-visible mt-[12vh] w-full max-w-xl rounded-lg border border-ink-700 bg-ink-900/95 text-ink-100 p-0 shadow-2xl shadow-black/70 backdrop:bg-black/70 backdrop:backdrop-blur-sm">
    <div class="flex items-center gap-2.5 px-3.5 border-b border-ink-700">
      <span class="text-cyan-400">${ICON.search}</span>
      <input id="paletteInput" type="text" autocomplete="off" spellcheck="false" role="combobox" aria-expanded="true" aria-controls="paletteList" aria-autocomplete="list"
        placeholder="Rechercher un site, un domaine, une page…" data-i18n-placeholder="sites.searchPh"
        class="flex-1 h-12 bg-transparent outline-none text-sm font-mono placeholder:text-ink-400 select-text">
      <span id="paletteCount" class="text-[10px] font-mono tabular-nums text-ink-400"></span>
    </div>
    <div id="paletteList" role="listbox" class="max-h-[60vh] overflow-y-auto overflow-x-hidden p-1.5"></div>
    <p class="px-3.5 py-2 border-t border-ink-700 text-[10px] font-mono uppercase tracking-[0.14em] text-ink-400" data-i18n="sites.paletteHint">↑↓ naviguer · ↵ ouvrir · Échap fermer</p>
  </dialog>

  <!-- Toast -->
  <div id="toast" class="fixed bottom-5 left-1/2 -translate-x-1/2 translate-y-16 opacity-0 transition-all duration-300 z-50 pointer-events-none">
    <div class="px-4 py-2.5 rounded-xl bg-ink-800 border border-ink-600 text-sm shadow-2xl flex items-center gap-2">
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

const btn = 'h-8 px-2.5 rounded-md text-[11px] font-mono uppercase tracking-[0.08em] border border-ink-700 text-ink-300 hover:text-ink-100 hover:border-ink-600 hover:bg-ink-850 grid place-items-center';
function row(s) {
  const n = pagesOf(s).length;
  const open = isLive(s)
    ? `<a href="https://${encodeURIComponent(s.domain)}/" target="_blank" rel="noopener" class="${btn} hover:!border-cyan-400/50 hover:!text-cyan-300">${t('sites.visit')}</a>`
    : `<a href="/preview/${encodeURIComponent(s.id)}/" target="_blank" class="${btn}">${t('sites.preview')}</a>`;
  return `
    <div class="group grid grid-cols-[auto_1fr_auto] items-center gap-4 px-4 py-3 border-b border-ink-800 last:border-b-0 hover:bg-ink-900/80">
      ${glyph(s)}
      <button data-open="${esc(s.id)}" class="min-w-0 text-left">
        <span class="block truncate font-mono text-sm text-ink-100 group-hover:text-cyan-300">${esc(s.id)}</span>
        <span class="block truncate text-[11px] font-mono text-ink-400">${isLive(s) ? esc(s.domain) : '—'} · <span class="tabular-nums">${n}</span> p.</span>
      </button>
      <div class="flex items-center gap-1.5">
        ${open}
        <button data-newpage="${esc(s.id)}" class="${btn}" title="${t('sites.newPageTitle')}">${t('sites.newPage')}</button>
        <button data-open="${esc(s.id)}" class="h-8 px-3 rounded-md text-[11px] font-mono uppercase tracking-[0.08em] font-semibold btn-primary">${t('sites.edit')}</button>
        ${ME && ME.root ? `<button data-remove="${esc(s.id)}" class="h-8 w-8 rounded-md border border-red-500/30 text-red-400 hover:bg-red-500/10 grid place-items-center" title="${t('sites.deleteSite')}" aria-label="${t('sites.deleteSite')}">${ICON.trash}</button>` : ''}
      </div>
    </div>`;
}

async function loadSites() {
  sites = (await fetch('/api/sites').then(r => r.json()).catch(() => []) || []).sort(byName);
  const live = sites.filter(isLive).length;
  const count = (sel, n) => document.querySelectorAll(`[data-count="${sel}"]`).forEach(el => { el.textContent = String(n).padStart(2, '0'); });
  count('sites', sites.length);
  count('live', live);
  count('drafts', sites.length - live);
  count('pages', sites.reduce((n, s) => n + pagesOf(s).length, 0));
  $('#siteList').innerHTML = sites.length ? sites.map(row).join('')
    : `<p class="px-4 py-8 text-sm text-ink-400 text-center">${t('sites.emptyList')}</p>`;
  if ($('#palette').open) renderPalette();
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
  $('#paletteCount').textContent = `${String(hits.length).padStart(2, '0')}/${String(sites.length).padStart(2, '0')}`;
  $('#paletteList').innerHTML = hits.length ? hits.map((h, i) => `
    <div id="hit-${i}" role="option" aria-selected="${i === cursor}" data-hit="${i}"
      class="relative flex items-center gap-3 rounded-md px-3 py-2 cursor-pointer ${i === cursor ? 'bg-ink-800 before:absolute before:left-0 before:inset-y-1.5 before:w-0.5 before:rounded before:bg-cyan-400' : 'hover:bg-ink-850'}">
      ${glyph(h.s)}
      <div class="flex-1 min-w-0">
        <p class="truncate text-sm font-mono ${i === cursor ? 'text-cyan-200' : 'text-ink-100'}">${esc(h.s.id)}${isLive(h.s) ? `<span class="text-ink-400 text-xs ml-2">${esc(h.s.domain)}</span>` : ''}</p>
        ${h.pages.length && q.trim() ? `<p class="flex flex-wrap gap-1 mt-1">${h.pages.slice(0, 5).map(p =>
          `<button data-hit="${i}" data-page="${esc(p)}" class="text-[10px] font-mono px-1.5 py-0.5 rounded border border-ink-700 text-ink-300 hover:text-cyan-300 hover:border-cyan-400/40">${esc(p)}</button>`).join('')}</p>` : ''}
      </div>
      <span class="text-[10px] font-mono tabular-nums text-ink-400 shrink-0">${String(pagesOf(h.s).length).padStart(2, '0')} p.</span>
    </div>`).join('')
    : `<p class="px-3 py-6 text-xs font-mono text-ink-400 text-center">${esc(t('sites.searchEmpty', { q: q.trim() }))}</p>`;
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
    <div class="rounded-xl border border-ink-700 bg-ink-850 p-3 space-y-2">
      <div class="flex items-center gap-2">
        <p class="flex-1 min-w-0 text-sm font-mono truncate">${esc(u.email)}</p>
        <button data-upass="${esc(u.email)}" class="h-8 px-2.5 rounded-lg text-[11px] font-medium border border-ink-600 hover:bg-ink-800">${t('sites.accPass')}</button>
        <button data-udel="${esc(u.email)}" class="h-8 w-8 rounded-lg border border-red-500/40 text-red-400 hover:bg-red-500/10 grid place-items-center" title="${t('sites.accDeleteBtn')}" aria-label="${t('sites.accDeleteBtn')}">${ICON.trash}</button>
      </div>
      <div class="flex flex-wrap gap-x-4 gap-y-1.5">
        ${siteIds.map(id => `<label class="flex items-center gap-1.5 text-xs cursor-pointer text-ink-300 hover:text-ink-100">
          <input type="checkbox" data-assign="${esc(u.email)}" value="${esc(id)}" ${u.sites.includes(id) ? 'checked' : ''} class="h-3.5 w-3.5 rounded border-ink-600 bg-ink-800 accent-vio-500"><span class="font-mono">${esc(id)}</span>
        </label>`).join('') || `<span class="text-xs text-ink-400">${t('sites.accNoSites')}</span>`}
      </div>
    </div>`).join('')
  : `<p class="text-xs text-ink-400">${t('sites.accEmpty')}</p>`;
  $('#waitList').innerHTML = (wl || []).length ? wl.slice().reverse().map(e => `
    <div class="rounded-lg border border-ink-700 bg-ink-850 px-3 py-2 text-xs">
      <span class="font-mono text-ink-400">${esc((e.date || '').slice(0, 10))}</span>
      <span class="font-mono text-vio-400 ml-2">${esc(e.email)}</span>
      ${e.name ? `<span class="text-ink-200 ml-2">${esc(e.name)}</span>` : ''}
      ${e.message ? `<p class="text-ink-300 mt-1 leading-relaxed">${esc(e.message)}</p>` : ''}
    </div>`).join('')
  : `<p class="text-xs text-ink-400">${t('sites.accWlEmpty')}</p>`;
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
  S.mode = 'visual'; S.frameStale = false; renderMode();
  renderSettings();
  showEditor(true);
  $('#domainInput').value = S.domain;
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
  S.page = p; S.dirty = false; S.frameStale = false; setSelection(false, t('sites.selNone'));
  $('#dirtyDot').classList.add('hidden');
  $('#frame').src = '/edit/' + S.siteId + '/' + p;
  markExplorer();
  if (S.mode === 'code') {
    try { await CodeMode.open(S.siteId, p); } catch (_) { toast(t('sites.cantRead'), false); }
  }
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
  const isRoot = !!(ME && ME.root);
  $('#domainInput').classList.toggle('hidden', !isRoot);
  $('#btnDomain').classList.toggle('hidden', !isRoot);
  const ro = $('#domainRO');
  ro.textContent = S.domain;
  ro.classList.toggle('hidden', isRoot || !S.domain);
  ro.classList.toggle('flex', !isRoot && !!S.domain);
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
    toast(domain ? t('sites.domainOn', { domain: data.domain }) : t('sites.domainOff'));
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
  document.querySelectorAll('.mode-btn').forEach(b => {
    const on = b.dataset.mode === S.mode;
    b.classList.toggle('bg-ink-700', on);
    b.classList.toggle('text-ink-100', on);
  });
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
const P = Object.assign({ explorer: true, aside: true, preview: true, zen: false }, (() => {
  try { return JSON.parse(localStorage.getItem('np-panes')) || {}; } catch (_) { return {}; }
})());
const savePanes = () => { try { localStorage.setItem('np-panes', JSON.stringify(P)); } catch (_) {} };
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

async function enterCode() {
  if (S.mode === 'code' || !S.siteId) return;
  // Les retouches visuelles non enregistrées sont reprises telles quelles dans le code.
  let html = null;
  if (S.dirty) {
    html = await getVisualHTML();
    if (html == null) return toast(t('sites.pageNoAnswerSave'), false);
  }
  CodeMode.configure({
    loadMonaco, toast, t, onDirty: setDirty, onFiles: markExplorer,
    onPreview: (on) => { if (shown('preview') !== on) setPane('preview', on); },
  });
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
  if (S.frameStale) loadPage(S.page);
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
  history.replaceState(null, '', location.pathname);
  if (site) openEditor(site, q.get('page'));
}

window.SitesApp = { start };
})();
