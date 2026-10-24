// /debate : chiffres lus sur /api/stats, hypothèses réglables, état « mesure jeune »,
// anglais, thème sombre, mobile, mouvement réduit.
import { harness } from './lib.mjs';

const h = await harness({ width: 1440 });
const { ev, nav, shot, check, sleep, viewport, send, noExceptions } = h;

try {
  await nav('/debate'); await sleep(500);
  await ev(`localStorage.removeItem('nadaprod-debate-hyp'); localStorage.removeItem('nadaprod-theme'); localStorage.setItem('nadaprod-lang', 'fr')`);
  await nav('/debate'); await sleep(1800);

  check('API lue : mesure en direct', (await ev(`document.querySelector('#statusLive').textContent`)).includes('direct'));
  check('date de début affichée', (await ev(`document.querySelector('#statusSince').textContent`)).startsWith('Mesure démarrée'));
  const reqs = await ev(`document.querySelector('[data-v="ai.requests"]').textContent`);
  check('chiffres rendus (pas de tiret)', reqs !== '—', reqs);
  check('faits de l\'outil rendus', (await ev(`[...document.querySelectorAll('.facts b')].every(b => b.textContent !== '—')`)));
  check('graphiques : 2 SVG avec axe', (await ev(`document.querySelectorAll('.chart svg line.base').length`)) === 2);
  check('tableau jour par jour', (await ev(`document.querySelectorAll('#dailyRows tr').length`)) >= 1);
  check('verdict non vide', (await ev(`document.querySelector('#rVerdict').textContent.length`)) > 10);
  // « Mesure jeune » : affiché exactement quand les données le justifient.
  const young = await ev(`fetch('/api/stats').then(r => r.json()).then(d => (Date.now() - new Date(d.since + 'T00:00:00')) / 864e5 < 7 || (d.ai.requests < 20 && d.static.pages < 1000))`);
  check('mesure jeune signalée si (et seulement si) c\'est le cas', young === await ev(`!document.querySelector('#early').hidden`), 'jeune=' + young);
  await shot('d-01-debate');

  // Les hypothèses recalculent le bilan et sont mémorisées.
  const before = await ev(`document.querySelector('#oWh').textContent + '|' + document.querySelector('#rAi').textContent`);
  await ev(`(() => { const r = document.querySelector('#hWh'); r.value = '2.4'; r.dispatchEvent(new Event('input')); })()`);
  const after = await ev(`document.querySelector('#oWh').textContent + '|' + document.querySelector('#rAi').textContent`);
  check('curseur : valeur et bilan recalculés', before !== after && after.startsWith('2,4'), before + ' → ' + after);
  check('hypothèse mémorisée', (await ev(`JSON.parse(localStorage.getItem('nadaprod-debate-hyp')).wh`)) === 2.4);
  await ev(`document.querySelector('.balance').scrollIntoView()`); await sleep(300);
  await shot('d-02-balance');

  await ev(`toggleTheme()`); await sleep(400);
  await ev(`toggleLanguage()`); await sleep(1200);
  check('anglais', (await ev(`document.querySelector('.display-text').textContent`)).toLowerCase().includes('facts'));
  check('anglais : statut traduit', (await ev(`document.querySelector('#statusLive').textContent`)).toLowerCase().includes('live'));
  await ev(`window.scrollTo(0, 0); document.querySelector('main').scrollTo(0, 0)`);
  await shot('d-03-dark-en');

  await viewport(390, 844);
  await nav('/debate'); await sleep(1500);
  check('mobile : pas de défilement horizontal', await ev(`document.querySelector('main').scrollWidth <= innerWidth + 1`));
  await shot('d-04-mobile');

  await send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-reduced-motion', value: 'reduce' }] });
  await nav('/debate'); await sleep(800);
  check('mouvement réduit : pastille figée', (await ev(`getComputedStyle(document.querySelector('.status .live'), '::before').animationName`)) === 'none');

  // Lien depuis le landing.
  await nav('/#services'); await sleep(1000);
  check('landing → /debate', (await ev(`document.querySelector('a.debate-link')?.getAttribute('href')`)) === '/debate');

  await ev(`localStorage.setItem('nadaprod-lang', 'fr'); localStorage.removeItem('nadaprod-theme'); localStorage.removeItem('nadaprod-debate-hyp')`);
  noExceptions();
} catch (e) {
  h.fail(e);
} finally {
  h.done();
}
