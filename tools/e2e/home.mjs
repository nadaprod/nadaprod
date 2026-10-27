// Landing / : polices locales, onglets (+ ancre partageable), langue, thème, mobile, mouvement réduit.
import { harness } from './lib.mjs';

const h = await harness({ width: 1440 });
const { ev, nav, shot, check, sleep, viewport, send, key, noExceptions } = h;

try {
  await nav('/'); await sleep(1500);
  await ev(`localStorage.removeItem('nadaprod-theme'); localStorage.setItem('nadaprod-lang', 'fr')`);
  await nav('/'); await sleep(1500);

  // Chargées depuis le miroir local (le mono ne sert pas sur le premier onglet : on le demande).
  const fonts = await ev(`(() => { const F = ['400 40px "Permanent Marker"', 'italic 300 40px Fraunces', '400 16px Archivo', '400 12px "JetBrains Mono"']; return Promise.all(F.map(f => document.fonts.load(f))).then(() => F.map(f => document.fonts.check(f))); })()`);
  check('polices locales chargées', fonts.every(Boolean), JSON.stringify(fonts));
  check('héro : au marqueur seul (un geste)', await ev(`getComputedStyle(document.querySelector('#hero-title .accent')).fontFamily.startsWith('"Permanent Marker"') && getComputedStyle(document.querySelector('#hero-title')).fontFamily.startsWith('"Permanent Marker"')`));
  check('preuves chiffrées', (await ev(`[...document.querySelectorAll('.proof b')].map(b => b.textContent).join()`)) === '15,1,0,0');
  check('pas de clé i18n brute', !(await ev(`/\\bhome\\.[a-z][A-Za-z0-9_]+/.test(document.body.innerText)`)));
  await shot('h-01-home-light');

  check('une seule barre : pas de barre flottante en bas', await ev(`!document.querySelector('nav[aria-label=Sections], .nav-item') && !!document.querySelector('header .site-nav')`));
  check('boutons de la barre sans bordure', await ev(`[...document.querySelectorAll('header .nav-tool, header .nav-links a')].every(b => getComputedStyle(b).borderTopWidth === '0px')`));
  await ev(`document.querySelector('.nav-links [data-nav=app]').click()`); await sleep(900);
  check('onglet Outil + ancre : 5 rangées', (await ev(`location.hash + '|' + document.querySelectorAll('#tab-app .row').length`)) === '#app|5');
  check('épuré : ni texture, ni étiquette numérotée, ni badge', await ev(`!document.querySelector('.texture-overlay, .badge-new, .kicker') && ![...document.querySelectorAll('#tab-app .label')].some(l => /^\\d/.test(l.textContent))`));
  check('le vert surligne seulement la rubrique courante', await ev(`(() => { const cur = [...document.querySelectorAll('.nav-links a[aria-current=page]')]; return cur.length === 1 && cur[0].dataset.nav === 'app' && getComputedStyle(cur[0]).backgroundImage.includes('204, 255, 0'); })()`));
  await shot('h-02-app');

  await nav('/#services'); await sleep(1200);
  check('ancre #services ouvre L\'Œuvre', await ev(`document.querySelector('#tab-services').classList.contains('active')`));
  check('pipeline en 6 étapes', (await ev(`document.querySelectorAll('.pipe [role=listitem]').length`)) === 6);

  await ev(`toggleTheme()`); await sleep(600);
  check('thème sombre mémorisé', (await ev(`localStorage.getItem('nadaprod-theme')`)) === 'dark');
  await shot('h-03-services-dark');

  await ev(`toggleLanguage()`); await sleep(1200);
  check('anglais', (await ev(`document.querySelector('#tab-services .display-text').textContent.trim()`)).toLowerCase().includes('static'));
  await nav('/#app'); await sleep(1500);
  await shot('h-04-app-dark-en');

  await viewport(390, 844);
  await nav('/'); await sleep(1500);
  check('mobile : pas de défilement horizontal', await ev(`document.querySelector('main').scrollWidth <= innerWidth + 1`));
  check('mobile : logo + bouton menu seulement', await ev(`getComputedStyle(document.querySelector('.site-nav')).display === 'none' && getComputedStyle(document.querySelector('.nav-toggle')).display !== 'none'`));
  await shot('h-05-mobile');
  await ev(`document.querySelector('.nav-toggle').click()`); await sleep(400);
  const lines = await ev(`[...document.querySelectorAll('.site-nav.open .nav-links a, .site-nav.open .nav-tool')].map(a => Math.round(a.getBoundingClientRect().top))`);
  check('menu plein écran : un élément par ligne', lines.length === 8 && lines.every((y, i) => !i || y > lines[i - 1]), JSON.stringify(lines));
  check('menu : aria-expanded + focus dedans', await ev(`document.querySelector('.nav-toggle').getAttribute('aria-expanded') === 'true' && document.querySelector('.site-nav').contains(document.activeElement)`));
  await shot('h-06-mobile-menu');
  await key('Escape', 'Escape', { vk: 27 });
  check('Échap referme le menu', await ev(`!document.querySelector('.site-nav').classList.contains('open') && document.activeElement.classList.contains('nav-toggle')`));
  await ev(`document.querySelector('.nav-toggle').click()`); await sleep(300);
  await ev(`document.querySelector('.site-nav [data-nav=services]').click()`); await sleep(800);
  check('une rubrique ouvre l\'onglet et ferme le menu', await ev(`location.hash === '#services' && !document.querySelector('.site-nav').classList.contains('open')`));

  await send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-reduced-motion', value: 'reduce' }] });
  await nav('/#services'); await sleep(800);
  check('mouvement réduit : marquee figé', (await ev(`getComputedStyle(document.querySelector('.marquee-track')).animationName`)) === 'none');

  await ev(`localStorage.setItem('nadaprod-lang', 'fr'); localStorage.removeItem('nadaprod-theme')`);
  noExceptions();
} catch (e) {
  h.fail(e);
} finally {
  h.done();
}
