// /sites : liste A→Z, palette « Projet en cours », éditeur, rôles, /drafts → /sites, anglais.
import { harness } from './lib.mjs';

const h = await harness({ width: 1440 });
const { ev, nav, shot, key, type, check, login, sleep, viewport, noExceptions } = h;

try {
  const ids = () => ev(`[...document.querySelectorAll('#siteList button.min-w-0[data-open]')].map(b=>b.querySelector('span').textContent)`);

  // ── root ──
  await login('web@l3dlp.com', 'smokepass123');
  await nav('/sites');
  check('liste: tous les sites, A→Z', JSON.stringify(await ids()) === JSON.stringify(['boulangerie-lyon','cafe-des-arts','landing-ete','portfolio-ines','zebra-studio']), JSON.stringify(await ids()));
  const counts = () => ev(`['sites','live','drafts'].map(k=>document.querySelector('[data-count='+k+']').textContent).join()`);
  const kpi = () => ev(`document.querySelector('[data-pages-n]').textContent + ' ' + document.querySelector('[data-pages-label]').textContent`);
  check('filtres : compteurs honnêtes (sans zéros ajoutés)', (await counts()) === '5,2,3', await counts());
  check('KPI pages (Tous) : total, libellé explicite', /^10 pages? (au total|in total)$/.test(await kpi()), await kpi());

  // Les chiffres sont les filtres : instantanés, A→Z, mémorisés dans l'adresse.
  const pressed = () => ev(`document.querySelector('[data-filter][aria-checked=true]').dataset.filter`);
  check('filtre par défaut : Tous', (await pressed()) === 'all');
  await ev(`document.querySelector('[data-filter=live]').click()`); await sleep(200);
  check('filtre En ligne : 2 sites A→Z', JSON.stringify(await ids()) === JSON.stringify(['boulangerie-lyon','portfolio-ines']), JSON.stringify(await ids()));
  check('KPI pages suit le filtre (En ligne)', /^4 (pages en ligne|live pages)$/.test(await kpi()), await kpi());
  check('filtre dans l\'adresse', (await ev(`location.search`)) === '?filtre=live');
  await nav('/sites?filtre=live'); await sleep(1200);
  check('filtre conservé au rechargement', (await pressed()) === 'live' && (await ids()).length === 2);
  await key('k', 'KeyK', { mod: 2 }); await sleep(300);
  check('palette ⌘K reste globale (5 sites) sous filtre', (await ev(`document.querySelectorAll('#paletteList [role=option]').length`)) === 5);
  await key('Escape', 'Escape', { vk: 27 }); await sleep(200);
  await ev(`document.querySelector('[data-filter=live]').focus()`);
  await key('ArrowRight', 'ArrowRight', { vk: 39 }); await sleep(200);
  check('clavier : → passe à Brouillons, 3 sites', (await pressed()) === 'drafts' && (await ids()).length === 3 && (await ev(`document.activeElement.dataset.filter`)) === 'drafts');
  check('KPI pages suit le filtre (Brouillons)', /^6 (pages en brouillon|draft pages)$/.test(await kpi()), await kpi());
  await ev(`document.querySelector('[data-filter=all]').click()`); await sleep(200);
  check('retour à Tous : adresse nettoyée, 5 sites', (await ev(`location.search`)) === '' && (await ids()).length === 5);

  // Tri : A→Z · Dernière édition · Pages · Création (plus récent / plus gros d'abord,
  // égalités → A→Z, date vide = la plus ancienne). Attentes calculées depuis /api/sites.
  await ev(`fetch('/api/sites/zebra-studio/pages', {method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify({name:'extra'})})`);
  await nav('/sites'); await sleep(1000);
  const expected = (key, only) => ev(`fetch('/api/sites').then(r => r.json()).then(list => {
    const az = (a, b) => a.id.localeCompare(b.id, undefined, { sensitivity: 'base' });
    const newest = (k) => (a, b) => (b[k] || '').localeCompare(a[k] || '') || az(a, b);
    const sorts = { az, edit: newest('updated_at'), pages: (a, b) => (b.pages || []).length - (a.pages || []).length || az(a, b), creation: newest('created_at') };
    const keep = { all: () => true, live: (s) => !!s.domain, drafts: (s) => !s.domain }[${JSON.stringify(only || 'all')}];
    return list.filter(keep).sort(sorts[${JSON.stringify(key)}]).map(s => s.id);
  })`);
  const sorted = () => ev(`document.querySelector('[data-sort][aria-checked=true]').dataset.sort`);
  check('tri par défaut : A→Z', (await sorted()) === 'az');
  for (const k of ['edit', 'pages', 'creation', 'az']) {
    await ev(`document.querySelector('[data-sort=${k}]').click()`); await sleep(200);
    const want = await expected(k);
    check(`tri ${k} : ordre attendu`, JSON.stringify(await ids()) === JSON.stringify(want), JSON.stringify(await ids()));
  }
  check('tri Pages : le site à 3 pages en tête', await ev(`(document.querySelector('[data-sort=pages]').click(), document.querySelector('#siteList [data-open] span').textContent)`) === 'zebra-studio');
  check('tri dans l\'adresse', (await ev(`location.search`)) === '?tri=pages');
  await nav('/sites?tri=pages'); await sleep(1200);
  check('tri conservé au rechargement', (await sorted()) === 'pages' && (await ids())[0] === 'zebra-studio');
  await ev(`document.querySelector('[data-filter=drafts]').click()`); await sleep(200);
  check('filtre + tri combinés (Brouillons × Pages)', JSON.stringify(await ids()) === JSON.stringify(await expected('pages', 'drafts')) && /filtre=drafts/.test(await ev(`location.search`)) && /tri=pages/.test(await ev(`location.search`)), (await ev(`location.search`)) + ' ' + JSON.stringify(await ids()));
  await key('k', 'KeyK', { mod: 2 }); await sleep(300);
  check('palette ⌘K reste A→Z sous tri', JSON.stringify(await ev(`[...document.querySelectorAll('#paletteList [role=option] p.font-mono')].map(p => p.firstChild.textContent)`)) === JSON.stringify(await expected('az')));
  await key('Escape', 'Escape', { vk: 27 }); await sleep(200);
  await ev(`document.querySelector('[data-sort=edit]').click()`); await sleep(200);
  const dated = await ev(`fetch('/api/sites').then(r => r.json()).then(l => l.filter(s => !s.domain && s.updated_at).length)`);
  check('tri Dernière édition : le fait affiché en ligne (si la date est connue)', (await ev(`document.querySelectorAll('#siteList [data-fact]').length`)) === dated, String(dated));
  await shot('s00b-sort');
  await ev(`document.querySelector('[data-sort=az]').click(); document.querySelector('[data-filter=all]').click()`); await sleep(200);
  check('retour A→Z / Tous : adresse nettoyée', (await ev(`location.search`)) === '');
  // État vide honnête : un compte qui n'a que des brouillons, filtre En ligne.
  await ev(`fetch('/api/users', {method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify({email:'bob@test.fr', password:'bobpass123'})}).then(() => fetch('/api/users/bob@test.fr', {method:'PUT', headers:{'Content-Type':'application/json'}, body: JSON.stringify({sites:['cafe-des-arts']})}))`);
  await login('bob@test.fr', 'bobpass123');
  await nav('/sites?filtre=live'); await sleep(1200);
  check('état vide par filtre', (await ids()).length === 0 && /domaine|domain/.test(await ev(`document.querySelector('#siteList').textContent`)), await ev(`document.querySelector('#siteList').textContent.trim()`));
  await shot('s00-filter-empty');
  await login('web@l3dlp.com', 'smokepass123');
  await nav('/sites'); await sleep(800);
  check('statut = glyphe immobile (disque plein en ligne / anneau brouillon)', (await ev(`document.querySelectorAll('#siteList [data-row] > span.bg-ink-100').length + '|' + document.querySelectorAll('#siteList [data-row] > span.border-ink-300').length + '|' + document.querySelectorAll('#siteList .animate-pulse').length`)) === '2|3|0');
  const none = await ev(`document.querySelector('#projectName').textContent`);
  check('case projet: aucun', none.includes('aucun') || none.includes('none'), none);
  check('root: import + nouveau site', !(await ev(`document.querySelector('[data-root]').classList.contains('hidden')`)));
  check('root: administration', !(await ev(`document.querySelector('#accountsCard').classList.contains('hidden')`)));
  check('pas de clé i18n brute', !(await ev(`/\\b(sites|drafts)\\.[a-z][A-Za-z]+\\b/.test(document.body.innerText)`)), await ev(`(document.body.innerText.match(/\\b(sites|drafts)\\.[a-z][A-Za-z]+\\b/)||[''])[0]`));
  await shot('s01-list-root');

  // palette : clic sur la case
  await ev(`document.querySelector('#btnProject').click()`); await sleep(300);
  check('palette: ouverte au clic, focus', (await ev(`document.querySelector('#palette').open && document.activeElement.id`)) === 'paletteInput');
  check('palette: tous les sites A→Z', (await ev(`[...document.querySelectorAll('#paletteList [role=option] p.font-mono')].map(p=>p.firstChild.textContent).join()`)) === 'boulangerie-lyon,cafe-des-arts,landing-ete,portfolio-ines,zebra-studio');
  check('palette: compteur', (await ev(`document.querySelector('#paletteCount').textContent`)) === '5');
  await shot('s02-palette-all');
  await type('CAFÉ');
  check('palette: recherche insensible casse/accents', (await ev(`document.querySelectorAll('#paletteList [role=option]').length`)) === 1);
  await key('Escape', 'Escape', { vk: 27 }); await sleep(200);
  check('palette: Échap ferme', !(await ev(`document.querySelector('#palette').open`)));

  // ⌘K + clavier : ↓ ↓ Entrée → 3e site (landing-ete)
  await key('k', 'KeyK', { mod: 2 }); await sleep(200);
  check('palette: Ctrl+K ouvre', await ev(`document.querySelector('#palette').open`));
  await key('ArrowDown', 'ArrowDown', { vk: 40 }); await key('ArrowDown', 'ArrowDown', { vk: 40 });
  await key('Enter', 'Enter', { vk: 13 }); await sleep(1200);
  // Monaco vient du CDN au premier passage : on attend l'atelier (8 s max).
  await ev(`new Promise(r => { const t0 = Date.now(); (function w() { if (CodeMode.active() === 'index.html' || Date.now() - t0 > 8000) r(); else setTimeout(w, 100); })(); })`);
  check('palette: Entrée ouvre l’éditeur', await ev(`!document.querySelector('#viewEdit').classList.contains('hidden') && CodeMode.active() === 'index.html' && !document.querySelector('#codeWrap').classList.contains('hidden')`));
  check('case projet: site ouvert', (await ev(`document.querySelector('#projectName').textContent`)) === 'landing-ete');
  const dom = () => ev(`(() => { const i = document.querySelector('#domainInput'), v = (s) => !document.querySelector(s).classList.contains('hidden'); return [v('#domainInput') ? (i.readOnly ? 'ro:' + i.value : 'edit') : '-', v('#btnDomain') ? 'brancher' : '', v('#btnOpenDomain') ? 'ouvrir' : '', v('#btnDetach') ? 'débrancher' : ''].filter(Boolean).join(' '); })()`);
  check('édition: domaine éditable (root)', (await dom()) === 'edit brancher', await dom());
  // Brancher → lecture seule + Ouvrir / Débrancher ; Débrancher → éditable à nouveau.
  await ev(`(() => { const i = document.querySelector('#domainInput'); i.value = 'landing.test'; document.querySelector('#btnDomain').click(); })()`); await sleep(900);
  check('domaine branché : lecture seule + Ouvrir + Débrancher', (await dom()) === 'ro:landing.test ouvrir débrancher', await dom());
  check('Ouvrir : https://domaine/ dans un nouvel onglet', (await ev(`(() => { const a = document.querySelector('#btnOpenDomain'); return a.href + '|' + a.target + '|' + a.rel + '|' + document.querySelector('#domainInput').getAttribute('aria-readonly'); })()`)) === 'https://landing.test/|_blank|noopener|true');
  await shot('s03b-domain-attached');
  await ev(`window.confirm = () => true; document.querySelector('#btnDetach').click()`); await sleep(900);
  check('débranché : champ éditable vide + Brancher', (await dom()) === 'edit brancher' && (await ev(`document.querySelector('#domainInput').value`)) === '', await dom());
  await shot('s03-editor');

  // « / » depuis l'éditeur, puce de page → autre site, page contact
  await ev(`document.activeElement.blur()`);
  await key('/', 'Slash', { text: '/' }); await sleep(200);
  await type('contact');
  check('palette: puces de page', (await ev(`document.querySelectorAll('#paletteList [data-page]').length`)) === 5);
  await shot('s04-palette-pages');
  await ev(`document.querySelector('#paletteList [data-page="contact.html"]').click()`); await sleep(1200);
  check('puce: ouvre boulangerie-lyon/contact.html', (await ev(`document.querySelector('#projectName').textContent + '|' + document.querySelector('#pageSelect').value`)) === 'boulangerie-lyon|contact.html');

  // mode code puis retour, enregistrement
  await ev(`document.querySelector('[data-mode=code]').click()`); await sleep(4000);
  check('édition: mode code (Monaco CDN)', await ev(`!document.querySelector('#codeWrap').classList.contains('hidden')`));
  await ev(`document.querySelector('[data-mode=visual]').click()`); await sleep(500);
  await ev(`document.querySelector('#btnSave').click()`); await sleep(1500);
  check('édition: enregistrement', (await ev(`document.querySelector('#toastMsg').textContent`)).length > 0, await ev(`document.querySelector('#toastMsg').textContent`));
  await ev(`document.querySelector('#btnBack').click()`); await sleep(600);
  check('retour: liste + case vide', !(await ev(`document.querySelector('#viewList').classList.contains('hidden')`)) && (await ev(`document.querySelector('#projectGlyph').innerHTML`)) === '');

  // ancienne URL /drafts + lien profond
  await nav('/drafts?open=cafe-des-arts&page=contact.html'); await sleep(800);
  check('/drafts → /sites, lien profond', (await ev(`location.pathname + '|' + document.querySelector('#projectName').textContent + '|' + document.querySelector('#pageSelect').value`)) === '/sites|cafe-des-arts|contact.html');

  // ── utilisateur simple ──
  await login('alice@test.fr', 'alicepass1');
  await nav('/sites');
  check('user: ses sites seulement, A→Z', JSON.stringify(await ids()) === JSON.stringify(['cafe-des-arts','portfolio-ines']));
  check('user: ni import, ni admin, ni suppression', await ev(`document.querySelector('[data-root]').classList.contains('hidden') && document.querySelector('#accountsCard').classList.contains('hidden') && !document.querySelector('[data-remove]')`));
  await ev(`document.querySelector('[data-open="portfolio-ines"]').click()`); await sleep(800);
  check('user: domaine en lecture seule + Ouvrir seulement', (await dom()) === 'ro:ines.design ouvrir', await dom());

  await ev(`localStorage.setItem('nadaprod-lang','en')`);
  await nav('/sites');
  check('en: textes traduits', (await ev(`document.querySelector('#btnProject').textContent`)).includes('Current project'));
  await shot('s05-user-en');
  await ev(`document.querySelector('#btnProject').click()`); await sleep(300);
  await shot('s06-palette-en');

  noExceptions();
} catch (e) {
  h.fail(e);
} finally {
  h.done();
}
