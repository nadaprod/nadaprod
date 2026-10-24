// Éditeur de sites : explorateur de fichiers (≥ 1280 px), bouton Panneaux, ⌘B / ⌘⇧B.
import { harness } from './lib.mjs';

const h = await harness({ width: 1600 });
const { ev, nav, shot, key, type, check, login, sleep, viewport, noExceptions } = h;

try {
  await login('web@l3dlp.com', 'smokepass123');
  await ev(`localStorage.removeItem('np-panes'); localStorage.removeItem('np-explorer-w')`);
  await nav('/sites?open=demo'); await sleep(2000);
  const vis = (sel) => ev(`(() => { const el = document.querySelector(${JSON.stringify(sel)}); return !!el && el.offsetParent !== null; })()`);
  check('explorateur visible (écran large)', await vis('#explorer'));
  check('liste déroulante masquée', !(await vis('#pageSelect')));
  const items = await ev(`[...document.querySelectorAll('[data-x-path]')].map(e => e.dataset.xPath)`);
  check('arbre : dossiers d\'abord, puis fichiers', JSON.stringify(items) === JSON.stringify(['css/', 'fr/', 'js/', 'contact.html', 'index.html']), JSON.stringify(items));
  check('fichier actif surligné', (await ev(`document.querySelector('[data-x-path][aria-selected=true]')?.dataset.xPath`)) === 'index.html');
  await shot('x-01-explorer');

  // dossier : clic déplie ; page d'un sous-dossier : s'ouvre
  await ev(`document.querySelector('[data-x-path="fr/"]').click()`); await sleep(200);
  check('dossier déplié', (await ev(`document.querySelector('[data-x-path="fr/"]').getAttribute('aria-expanded')`)) === 'true');
  await ev(`document.querySelector('[data-x-path="fr/index.html"]').click()`); await sleep(1200);
  check('page ouverte depuis l\'arbre', (await ev(`document.querySelector('#frame').src`)).endsWith('/edit/demo/fr/index.html'));
  check('actif = fr/index.html', (await ev(`document.querySelector('[data-x-path][aria-selected=true]')?.dataset.xPath`)) === 'fr/index.html');

  // clavier
  await ev(`document.querySelector('[data-x-path][tabindex="0"]').focus()`);
  await key('ArrowLeft', 'ArrowLeft', { vk: 37 });
  check('← remonte au dossier parent', (await ev(`document.activeElement.dataset.xPath`)) === 'fr/');

  // filtre
  await ev(`(() => { const f = document.querySelector('[data-x-filter]'); f.value = 'css'; f.dispatchEvent(new Event('input')); })()`); await sleep(200);
  check('filtre à plat', JSON.stringify(await ev(`[...document.querySelectorAll('[data-x-path]')].map(e => e.dataset.xPath)`)) === JSON.stringify(['css/extra.css', 'css/site.css']));
  // CSS : bascule en mode code et ouvre l'onglet
  await ev(`document.querySelector('[data-x-path="css/extra.css"]').click()`); await sleep(5000);
  const codeOn = await vis('#codeWrap');
  check('CSS → mode code', codeOn);
  if (codeOn) {
    check('onglet css/extra.css actif', (await ev(`document.querySelector('#cmTabs [aria-selected=true]')?.dataset.tab`)) === 'css/extra.css');
    check('explorateur suit l\'onglet actif', (await ev(`document.querySelector('[data-x-path][aria-selected=true]')?.dataset.xPath`)) === 'css/extra.css');
  }
  await ev(`(() => { const f = document.querySelector('[data-x-filter]'); f.value = ''; f.dispatchEvent(new Event('input')); })()`);
  await shot('x-02-code');

  // bouton panneaux : tout masquer puis restaurer (mode code : explorateur + aperçu)
  await ev(`document.querySelector('#btnPanes').click()`); await sleep(300);
  check('focus : explorateur masqué', !(await vis('#explorer')));
  check('focus : aperçu masqué', codeOn ? !(await vis('#cmPane')) : true);
  check('bouton pressé', (await ev(`document.querySelector('#btnPanes').getAttribute('aria-pressed')`)) === 'true');
  await shot('x-03-focus');
  await ev(`document.querySelector('#btnPanes').click()`); await sleep(300);
  check('restauration', (await vis('#explorer')) && (codeOn ? await vis('#cmPane') : true));

  // ⌘B / ⌘⇧B (Ctrl en headless)
  await key('b', 'KeyB', { mod: 2, vk: 66 });
  check('Ctrl+B masque l\'explorateur', !(await vis('#explorer')));
  await key('b', 'KeyB', { mod: 2, vk: 66 });
  check('Ctrl+B le réaffiche', await vis('#explorer'));
  if (codeOn) {
    await key('B', 'KeyB', { mod: 10, vk: 66 });
    check('Ctrl+Shift+B masque l\'aperçu', !(await vis('#cmPane')));
    await key('B', 'KeyB', { mod: 10, vk: 66 });
  }
  // mémorisé après rechargement
  await ev(`document.querySelector('#btnPanes').click()`); await sleep(200);
  await nav('/sites?open=demo'); await sleep(2000);
  check('disposition mémorisée (explorateur masqué)', !(await vis('#explorer')));
  await ev(`document.querySelector('#btnPanes').click()`); await sleep(300);
  check('mode visuel : explorateur + retouche IA restaurés', (await vis('#explorer')) && (await vis('#editAside')));

  // écran moyen : pas d'explorateur, liste déroulante
  await viewport(1100, 800); await sleep(500);
  check('écran moyen : explorateur absent, liste visible', !(await vis('#explorer')) && (await vis('#pageSelect')));
  await shot('x-04-medium');

  noExceptions();
} catch (e) {
  h.fail(e);
} finally {
  h.done();
}
