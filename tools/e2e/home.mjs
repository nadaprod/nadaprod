// Landing / : polices locales, onglets (+ ancre partageable), langue, thème, mobile, mouvement réduit.
import { harness } from './lib.mjs';

const h = await harness({ width: 1440 });
const { ev, nav, shot, check, sleep, viewport, send, noExceptions } = h;

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

  await ev(`document.querySelectorAll('.nav-item')[1].click()`); await sleep(900);
  check('onglet Outil + ancre : 5 rangées', (await ev(`location.hash + '|' + document.querySelectorAll('#tab-app .row').length`)) === '#app|5');
  check('épuré : ni texture, ni étiquette numérotée, ni badge', await ev(`!document.querySelector('.texture-overlay, .badge-new, .kicker') && ![...document.querySelectorAll('#tab-app .label')].some(l => /^\\d/.test(l.textContent))`));
  check('le vert ne marque que l\'onglet actif ici', await ev(`getComputedStyle(document.querySelector('.nav-item.active')).backgroundColor === 'rgb(204, 255, 0)' && getComputedStyle(document.querySelectorAll('.nav-item')[0]).backgroundColor !== 'rgb(204, 255, 0)'`));
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
  await shot('h-05-mobile');

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
