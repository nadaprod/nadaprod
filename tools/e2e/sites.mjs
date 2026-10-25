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
  check('télémétrie', (await ev(`['sites','live','drafts','pages'].map(k=>document.querySelector('[data-count='+k+']').textContent).join()`)) === '05,02,03,10', await ev(`['sites','live','drafts','pages'].map(k=>document.querySelector('[data-count='+k+']').textContent).join()`));
  check('statut = glyphe (live pulse cyan / brouillon anneau)', (await ev(`document.querySelectorAll('#siteList .bg-cyan-400.animate-pulse').length + '|' + document.querySelectorAll('#siteList [class*="border-amber-400/80"]').length`)) === '2|3');
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
  check('palette: compteur', (await ev(`document.querySelector('#paletteCount').textContent`)) === '05/05');
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
  check('palette: Entrée ouvre l’éditeur', await ev(`!document.querySelector('#viewEdit').classList.contains('hidden') && CodeMode.active() === 'index.html' && !document.querySelector('#codeWrap').classList.contains('hidden')`));
  check('case projet: site ouvert', (await ev(`document.querySelector('#projectName').textContent`)) === 'landing-ete');
  check('édition: domaine éditable (root)', !(await ev(`document.querySelector('#domainInput').classList.contains('hidden')`)));
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
  check('user: domaine en lecture seule', (await ev(`document.querySelector('#domainInput').classList.contains('hidden') && document.querySelector('#domainRO').textContent`)) === 'ines.design');

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
