// Landing / : polices locales, onglets (+ ancre partageable), langue, thème, mobile, mouvement réduit.
import { harness } from './lib.mjs';

const h = await harness({ width: 1440 });
const { ev, nav, shot, check, sleep, viewport, send, noExceptions } = h;

try {
  await nav('/'); await sleep(1500);
  await ev(`localStorage.removeItem('nadaprod-theme'); localStorage.setItem('nadaprod-lang', 'fr')`);
  await nav('/'); await sleep(1500);

  const fonts = await ev(`document.fonts.ready.then(() => ['900 40px "Big Shoulders Display"', 'italic 300 40px Fraunces', '400 16px Archivo', '500 12px "JetBrains Mono"'].map(f => document.fonts.check(f)))`);
  check('polices locales chargées', fonts.every(Boolean), JSON.stringify(fonts));
  check('héro : accent italique + condensé', await ev(`getComputedStyle(document.querySelector('#hero-title .accent')).fontStyle === 'italic' && getComputedStyle(document.querySelector('#hero-title')).fontFamily.includes('Big Shoulders')`));
  check('preuves chiffrées', (await ev(`[...document.querySelectorAll('.proof b')].map(b => b.textContent).join()`)) === '15,1,0,0');
  check('pas de clé i18n brute', !(await ev(`/\\bhome\\.[a-z][A-Za-z0-9_]+/.test(document.body.innerText)`)));
  await shot('h-01-home-light');

  await ev(`document.querySelectorAll('.nav-item')[1].click()`); await sleep(900);
  check('onglet Outil + ancre', (await ev(`location.hash + '|' + document.querySelectorAll('#tab-app .grid-item').length`)) === '#app|5');
  check('carte Langues : badge Nouveau', (await ev(`document.querySelector('.badge-new').textContent`)) === 'Nouveau');
  await shot('h-02-app');

  await nav('/#services'); await sleep(1200);
  check('ancre #services ouvre L\'Œuvre', await ev(`document.querySelector('#tab-services').classList.contains('active')`));
  check('pipeline en 6 étapes', (await ev(`document.querySelectorAll('.pipe [role=listitem]').length`)) === 6);

  await ev(`toggleTheme()`); await sleep(600);
  check('thème sombre mémorisé', (await ev(`localStorage.getItem('nadaprod-theme')`)) === 'dark');
  await shot('h-03-services-dark');

  await ev(`toggleLanguage()`); await sleep(1200);
  check('anglais', (await ev(`document.querySelector('#tab-services .display-text').textContent.trim()`)).toLowerCase().includes('self-contained'));
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
