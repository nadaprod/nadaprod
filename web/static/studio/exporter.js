/* Studio NADAPROD — projets (ouvrir, créer, importer, supprimer), export de
 * production (HTML compilé, ZIP, React, Vue, JSON) et publication en un clic. */
import { S, $, api, esc, toast, openModal, closeModal, confirmBox, ago, emit } from './core.js';
import { flush, openProject, createProject } from './store.js';
import { pageHTML } from './preview.js';

function download(blob, filename) {
  const a = document.createElement('a');
  a.href = URL.createObjectURL(blob);
  a.download = filename;
  a.click();
  setTimeout(() => URL.revokeObjectURL(a.href), 1000);
}

/* ───────── Projets ───────── */

export async function projectsDialog() {
  await flush();
  const list = await api('/projects');
  const m = openModal({
    title: t('studio.projectsTitle'), size: 'md',
    body: `<div class="flex flex-wrap gap-2 mb-4">
        <button data-new class="h-9 px-3.5 rounded-lg text-sm font-semibold btn-primary">${esc(t('studio.projectNew'))}</button>
        <button data-import class="h-9 px-3.5 rounded-lg text-sm border border-ink-600 hover:bg-ink-800">${esc(t('studio.projectImport'))}</button>
        <input data-file type="file" accept=".json,application/json" class="hidden">
      </div>
      <div class="space-y-1.5">${list.map((p) => `
        <div class="flex items-center gap-3 rounded-xl border ${p.id === S.project.id ? 'border-vio-500/70 bg-vio-500/10' : 'border-ink-700 bg-ink-850'} p-3">
          <button data-open="${esc(p.id)}" class="flex-1 min-w-0 text-left">
            <p class="text-sm font-medium text-ink-100 truncate">${esc(p.name)}</p>
            <p class="text-[11px] font-mono text-ink-400">${esc(t('studio.projectMeta', { n: p.blocks }))} · ${esc(ago(p.updated_at))}</p>
          </button>
          <a href="/api/projects/${esc(p.id)}/export.json" download class="p-1.5 rounded-lg text-ink-300 hover:text-ink-100 hover:bg-ink-800" title="${esc(t('studio.exportJSON'))}" aria-label="${esc(t('studio.exportJSON'))}">
            <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 3v12m0 0 4-4m-4 4-4-4M4 17v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2" stroke-linecap="round" stroke-linejoin="round"/></svg></a>
          <button data-del="${esc(p.id)}" class="p-1.5 rounded-lg text-red-400 hover:bg-red-500/20" title="${esc(t('studio.projectDelete'))}" aria-label="${esc(t('studio.projectDelete'))}">
            <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 7h16M9 7V5a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2m-9 0 1 13h8l1-13" stroke-linecap="round" stroke-linejoin="round"/></svg></button>
        </div>`).join('')}</div>`,
  });
  const file = m.el.querySelector('[data-file]');
  m.el.querySelector('[data-new]').onclick = async () => { await createProject({ name: t('editor.defaultPageName') }); closeModal(); toast(t('studio.projectCreated')); };
  m.el.querySelector('[data-import]').onclick = () => file.click();
  file.onchange = async () => {
    try {
      const data = JSON.parse(await file.files[0].text());
      if (!Array.isArray(data.blocks)) throw new Error(t('studio.importInvalid'));
      // Les identifiants de blocs sont repris tels quels (uniques par projet) ; le serveur valide le reste.
      await createProject({ name: data.name, brand: data.brand, theme: data.theme, seo: data.seo, blocks: data.blocks });
      closeModal(); toast(t('studio.projectImported'));
    } catch (e) { toast(e.message || t('studio.importInvalid'), false); }
  };
  m.el.addEventListener('click', async (e) => {
    const open = e.target.closest('[data-open]'), del = e.target.closest('[data-del]');
    try {
      if (open) { await openProject(open.dataset.open); closeModal(); }
      if (del) {
        const p = list.find((x) => x.id === del.dataset.del);
        if (!(await confirmBox(t('studio.projectDeleteConfirm', { name: p.name }), t('studio.projectDelete')))) return projectsDialog();
        await api('/projects/' + p.id, { method: 'DELETE' });
        if (p.id === S.project.id) { // on supprime le projet ouvert : on bascule sur un autre (ou un neuf)
          const rest = list.filter((x) => x.id !== p.id);
          if (rest.length) await openProject(rest[0].id); else await createProject({ name: t('editor.defaultPageName') });
        }
        toast(t('studio.projectDeleted'));
        projectsDialog();
      }
    } catch (err) { toast(err.message, false); }
  });
}

/* ───────── Export ───────── */

export async function exportAs(format, lang) {
  if (!S.page.some((b) => b.html)) return toast(t('studio.exportEmpty'), false);
  // Page HTML seule d'un projet multilingue : une langue à choisir (le ZIP et
  // la publication, eux, contiennent toutes les langues, une URL chacune).
  const langs = (S.seo.langs || []).filter((l) => l && l !== (S.seo.lang || 'fr'));
  if (format === 'html' && lang === undefined && langs.length) return pickExportLang(langs);
  toast(t('studio.exporting'));
  try {
    await flush(); // le serveur exporte ce qu'il a en base
    const res = await api(`/projects/${S.project.id}/export`, { method: 'POST', body: lang ? { format, lang } : { format }, raw: true });
    const name = (res.headers.get('Content-Disposition') || '').match(/filename="?([^"]+)"?/)?.[1] || 'page';
    download(await res.blob(), name);
    let warnings = [];
    try { warnings = JSON.parse(JSON.parse(res.headers.get('X-Export-Warnings') || '"[]"')); } catch (_) {}
    if (warnings.length) toast(warnings[0], false);
    else toast(t(res.headers.get('X-Export-Compiled') === 'true' ? 'studio.exportedCompiled' : 'studio.exportedCdn'));
  } catch (e) { toast(e.message, false); }
}

function pickExportLang(langs) {
  const src = S.seo.lang || 'fr';
  const m = openModal({
    title: t('tr.exportLangTitle'), size: 'sm',
    body: `<p class="text-xs text-ink-400 mb-3">${esc(t('tr.exportLangHint'))}</p>
      <div class="grid grid-cols-3 gap-1.5 font-mono text-[11px]">
        ${[src, ...langs].map((l, i) => `<button data-lang="${esc(l)}" ${i === 0 ? 'autofocus' : ''} class="h-9 border ${i === 0 ? 'border-vio-500/60 text-vio-300' : 'border-ink-600 text-ink-200'} hover:border-cyan-400/70 hover:text-cyan-200 uppercase tracking-wider">${esc(l)}${i === 0 ? ' · src' : ''}</button>`).join('')}
      </div>`,
  });
  m.el.addEventListener('click', (e) => {
    const b = e.target.closest('[data-lang]'); if (!b) return;
    m.close();
    exportAs('html', b.dataset.lang === src ? '' : b.dataset.lang);
  });
}

export async function copyPage() {
  await navigator.clipboard.writeText(pageHTML());
  toast(t('editor.copiedPage'));
}

export function exportJSON() {
  flush().then(() => { location.href = `/api/projects/${S.project.id}/export.json`; });
}

/* ───────── Publication ─────────
 * Le jeton du fournisseur n'est jamais stocké côté serveur. Dans le navigateur,
 * il n'est mémorisé que si l'utilisateur le demande (case décochée par défaut). */

const TOKENS = 'nadaprod:publishTokens';
function savedTokens() { try { return JSON.parse(localStorage.getItem(TOKENS) || '{}'); } catch (_) { return {}; } }

export function publishDialog() {
  if (!S.page.some((b) => b.html)) return toast(t('studio.exportEmpty'), false);
  const last = ['netlify', 'cloudflare'].includes(S.project.publish?.provider) ? S.project.publish : {};
  const tokens = savedTokens();
  const field = 'w-full h-10 rounded-xl bg-ink-800 border border-ink-600 focus:border-vio-500 focus:ring-1 focus:ring-vio-500 outline-none text-sm px-3 select-text';
  const label = 'text-[10px] font-mono uppercase tracking-[0.15em] text-ink-400 block mb-2';
  const m = openModal({
    title: t('studio.publishTitle'), size: 'sm',
    body: `<form data-form class="space-y-4">
        ${last.url ? `<p class="text-xs text-ink-300">${esc(t('studio.publishLast'))} <a href="${esc(last.url)}" target="_blank" rel="noopener" class="text-vio-400 hover:underline">${esc(last.url)}</a></p>` : ''}
        <div><span class="${label}">${esc(t('studio.publishProvider'))}</span>
          <div class="grid grid-cols-2 gap-2">
            ${['netlify', 'cloudflare'].map((p) => `<label class="flex items-center gap-2 rounded-xl border border-ink-600 px-3 h-10 text-sm cursor-pointer has-[:checked]:border-vio-500 has-[:checked]:bg-vio-500/10">
              <input type="radio" name="provider" value="${p}" ${(last.provider || 'netlify') === p ? 'checked' : ''} class="accent-vio-500">${p === 'netlify' ? 'Netlify' : 'Cloudflare Pages'}</label>`).join('')}
          </div></div>
        <div><label class="${label}" for="pubToken">${esc(t('studio.publishToken'))}</label>
          <input id="pubToken" type="password" autocomplete="off" required class="${field}">
          <p data-help class="mt-1.5 text-[11px] text-ink-400 leading-relaxed"></p></div>
        <div data-cf><label class="${label}" for="pubAccount">${esc(t('studio.publishAccount'))}</label>
          <input id="pubAccount" autocomplete="off" class="${field}"></div>
        <div><label class="${label}" for="pubSite" data-site-label></label>
          <input id="pubSite" autocomplete="off" class="${field}"></div>
        <label class="flex items-start gap-2 text-xs text-ink-300"><input id="pubRemember" type="checkbox" class="mt-0.5 accent-vio-500"><span>${esc(t('studio.publishRemember'))}</span></label>
        <p data-out class="hidden text-sm rounded-xl border p-3"></p>
        <button data-go class="w-full h-10 rounded-xl text-sm font-semibold btn-primary disabled:opacity-60 disabled:cursor-wait">${esc(t('studio.publishGo'))}</button>
      </form>`,
  });
  const q = (s) => m.el.querySelector(s);
  const provider = () => m.el.querySelector('input[name=provider]:checked').value;
  const sync = () => {
    const p = provider(), cf = p === 'cloudflare';
    q('[data-cf]').classList.toggle('hidden', !cf);
    q('[data-help]').textContent = t(cf ? 'studio.publishHelpCf' : 'studio.publishHelpNetlify');
    q('[data-site-label]').textContent = t(cf ? 'studio.publishProject' : 'studio.publishSite');
    q('#pubSite').placeholder = t(cf ? 'studio.publishProjectPh' : 'studio.publishSitePh');
    q('#pubSite').value = last.provider === p ? last.site || '' : '';
    q('#pubAccount').value = (last.provider === p && last.account_id) || tokens[p + ':account'] || '';
    q('#pubToken').value = tokens[p] || '';
    q('#pubRemember').checked = !!tokens[p];
  };
  m.el.querySelectorAll('input[name=provider]').forEach((r) => r.addEventListener('change', sync));
  sync();

  q('[data-form]').onsubmit = async (e) => {
    e.preventDefault();
    const p = provider(), out = q('[data-out]'), go = q('[data-go]');
    const body = { provider: p, token: q('#pubToken').value.trim(), site: q('#pubSite').value.trim(), account_id: q('#pubAccount').value.trim() };
    go.disabled = true; go.textContent = t('studio.publishing');
    out.classList.add('hidden');
    try {
      await flush();
      const r = await api(`/projects/${S.project.id}/publish`, { method: 'POST', body });
      S.project.rev = r.rev;
      S.project.publish = { provider: p, site: r.result.site, url: r.result.url, account_id: body.account_id };
      const all = savedTokens();
      if (q('#pubRemember').checked) { all[p] = body.token; if (body.account_id) all[p + ':account'] = body.account_id; } else { delete all[p]; delete all[p + ':account']; }
      try { localStorage.setItem(TOKENS, JSON.stringify(all)); } catch (_) {}
      out.className = 'text-sm rounded-xl border border-emerald-500/40 bg-emerald-500/10 text-emerald-300 p-3';
      out.innerHTML = `${esc(t('studio.publishDone'))} <a href="${esc(r.result.url)}" target="_blank" rel="noopener" class="underline font-medium">${esc(r.result.url)}</a>` +
        (r.warnings?.length ? `<span class="block mt-1.5 text-xs text-amber-300">${r.warnings.map(esc).join('<br>')}</span>` : '');
      q('#pubSite').value = r.result.site;
      emit('published');
    } catch (err) {
      if (err.data?.site) q('#pubSite').value = err.data.site; // site créé malgré l'échec : on redéploiera dessus
      out.className = 'text-sm rounded-xl border border-red-500/40 bg-red-500/10 text-red-300 p-3';
      out.textContent = (err.code === 'auth' ? t('studio.publishAuth') + ' ' : '') + err.message;
    } finally {
      go.disabled = false; go.textContent = t('studio.publishGo');
    }
  };
}

/* ───────── Enregistrer dans un site importé ─────────
 * La page (version CDN, comme toute source de dev/) est déposée dans un site
 * accessible au compte ; root peut créer un site vide au passage. */

export async function siteDialog() {
  if (!S.page.some((b) => b.html)) return toast(t('studio.exportEmpty'), false);
  let sites;
  try { sites = await api('/sites'); } catch (e) { return toast(e.message, false); }
  const last = S.project.publish?.provider === 'site' ? S.project.publish : {};
  const field = 'w-full h-10 rounded-xl bg-ink-800 border border-ink-600 focus:border-vio-500 focus:ring-1 focus:ring-vio-500 outline-none text-sm px-3 select-text';
  const label = 'text-[10px] font-mono uppercase tracking-[0.15em] text-ink-400 block mb-2';
  const m = openModal({
    title: t('studio.siteTitle'), size: 'sm',
    body: `<form data-form class="space-y-4">
        <p class="text-xs text-ink-400 leading-relaxed">${esc(t('studio.siteHint'))}</p>
        <div><label class="${label}" for="siteSel">${esc(t('studio.siteSite'))}</label>
          <select id="siteSel" class="${field}">
            ${sites.map((s) => `<option value="${esc(s.id)}" ${s.id === last.site ? 'selected' : ''}>${esc(s.id)}${s.domain ? ' · ' + esc(s.domain) : ''}</option>`).join('')}
            ${S.cfg.root ? `<option value="__new__">${esc(t('studio.siteNew'))}</option>` : ''}
          </select></div>
        <div data-newsite class="hidden"><label class="${label}" for="siteName">${esc(t('studio.siteNewName'))}</label>
          <input id="siteName" maxlength="80" placeholder="${esc(S.brand.name || S.project.name)}" class="${field}"></div>
        <div><label class="${label}" for="sitePath">${esc(t('studio.sitePath'))}</label>
          <input id="sitePath" value="${esc(last.path || 'index.html')}" placeholder="index.html" class="${field}">
          <p data-exists class="hidden mt-1.5 text-[11px] text-amber-400">${esc(t('studio.siteExists'))}</p></div>
        <label data-ow class="hidden flex items-start gap-2 text-xs text-ink-300"><input id="siteOverwrite" type="checkbox" class="mt-0.5 accent-vio-500"><span>${esc(t('studio.siteOverwrite'))}</span></label>
        <p data-out class="hidden text-sm rounded-xl border p-3"></p>
        <button data-go class="w-full h-10 rounded-xl text-sm font-semibold btn-primary disabled:opacity-60 disabled:cursor-wait">${esc(t('studio.siteGo'))}</button>
      </form>`,
  });
  const q = (s) => m.el.querySelector(s);
  const sync = () => {
    const isNew = q('#siteSel').value === '__new__';
    q('[data-newsite]').classList.toggle('hidden', !isNew);
    const site = sites.find((s) => s.id === q('#siteSel').value);
    const path = q('#sitePath').value.trim().replace(/^\/+/, '') || 'index.html';
    const exists = !!site && (site.pages || []).includes(path); // pages peut être absent (site vide)
    q('[data-exists]').classList.toggle('hidden', !exists);
    q('[data-ow]').classList.toggle('hidden', !exists);
  };
  q('#siteSel').addEventListener('change', sync);
  q('#sitePath').addEventListener('input', sync);
  if (!sites.length && S.cfg.root) q('#siteSel').value = '__new__';
  try { sync(); } catch (e) { console.error(e); }
  q('[data-form]').onsubmit = async (e) => {
    e.preventDefault();
    const out = q('[data-out]'), go = q('[data-go]');
    go.disabled = true; out.classList.add('hidden');
    try {
      await flush();
      let site = q('#siteSel').value;
      if (site === '__new__') {
        const name = q('#siteName').value.trim() || S.brand.name || S.project.name;
        site = (await api('/sites/new', { method: 'POST', body: { name } })).id;
      }
      const r = await api(`/projects/${S.project.id}/site`, { method: 'POST', body: { site, path: q('#sitePath').value.trim(), overwrite: q('#siteOverwrite').checked } });
      S.project.rev = r.rev;
      S.project.publish = { provider: 'site', site: r.site, path: r.path, url: r.url };
      out.className = 'text-sm rounded-xl border border-emerald-500/40 bg-emerald-500/10 text-emerald-300 p-3';
      out.innerHTML = `${esc(t('studio.siteDone', { site: r.site, path: r.path }))}<span class="block mt-2 flex gap-3">
        <a href="${esc(r.url)}" target="_blank" rel="noopener" class="underline font-medium">${esc(t('studio.siteView'))}</a>
        <a href="/sites" class="underline font-medium">${esc(t('studio.siteManage'))}</a></span>`;
      toast(t('studio.siteSaved'));
    } catch (err) {
      out.className = 'text-sm rounded-xl border border-red-500/40 bg-red-500/10 text-red-300 p-3';
      out.textContent = err.message;
      if (err.status === 409) { q('[data-ow]').classList.remove('hidden'); q('[data-exists]').classList.remove('hidden'); }
    } finally { go.disabled = false; }
  };
}

/* ───────── Menu Export ───────── */

export function initExport() {
  const menu = $('#exportMenu'), btn = $('#btnExport');
  const toggle = (open) => {
    menu.classList.toggle('hidden', !open);
    btn.setAttribute('aria-expanded', String(open));
    if (open) $('#exportLangsHint').classList.toggle('hidden', !(S.seo.langs || []).some((l) => l && l !== (S.seo.lang || 'fr')));
  };
  btn.addEventListener('click', (e) => { e.stopPropagation(); toggle(menu.classList.contains('hidden')); });
  document.addEventListener('click', () => toggle(false));
  document.addEventListener('keydown', (e) => { if (e.key === 'Escape') toggle(false); });
  menu.addEventListener('click', (e) => {
    const item = e.target.closest('[data-export]'); if (!item) return;
    const kind = item.dataset.export;
    if (kind === 'copy') copyPage(); else if (kind === 'json') exportJSON(); else if (kind === 'publish') publishDialog(); else if (kind === 'site') siteDialog(); else exportAs(kind);
  });
  $('#btnProjects').addEventListener('click', () => projectsDialog().catch((e) => toast(e.message, false)));
}
