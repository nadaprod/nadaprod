// Thème commun landing ⇄ app : jetons appliqués, bascule ◐ mémorisée, Monaco
// qui suit, captures de chaque page de l'app en sombre et en clair.
// Suppose les données semées par run.sh (sites, demo).
import { harness } from './lib.mjs';

const h = await harness({ width: 1440 });
const { ev, nav, shot, check, sleep, login, noExceptions } = h;
const bg = () => ev(`getComputedStyle(document.body).backgroundColor`);
const setTheme = (t) => ev(`localStorage.setItem('nadaprod-theme', '${t}')`);

try {
  await login('web@l3dlp.com', 'smokepass123');
  for (const theme of ['dark', 'light']) {
    await setTheme(theme);
    await nav('/login'); await sleep(600);
    check(`${theme} : jetons sur /login`, (await bg()) === (theme === 'dark' ? 'rgb(10, 10, 9)' : 'rgb(250, 250, 247)'), await bg());
    await shot(`t-${theme}-01-login`);
    await nav('/sites'); await sleep(1500);
    check(`${theme} : /sites`, (await bg()) === (theme === 'dark' ? 'rgb(10, 10, 9)' : 'rgb(250, 250, 247)'));
    check(`${theme} : bouton principal vert acide`, (await ev(`getComputedStyle(document.querySelector('.btn-primary')).backgroundColor`)) === 'rgb(204, 255, 0)');
    await shot(`t-${theme}-02-sites`);
    await nav('/sites?open=demo'); await sleep(2000);
    await shot(`t-${theme}-03-sites-editor`);
    await nav('/editor'); await sleep(2500);
    await shot(`t-${theme}-04-studio`);
    await nav('/waiting-list'); await sleep(600);
    await shot(`t-${theme}-05-waiting-list`);
  }

  // Bascule ◐ depuis /sites, puis Monaco suit sans rechargement.
  await setTheme('dark');
  await nav('/sites?open=demo'); await sleep(2000);
  await ev(`document.querySelector('[data-mode=code]').click()`); await sleep(5000);
  const monacoDark = await ev(`!!document.querySelector('.monaco-editor.vs-dark')`);
  check('Monaco sombre en thème sombre', monacoDark);
  await ev(`toggleTheme()`); await sleep(500);
  check('bascule : thème clair mémorisé', (await ev(`document.documentElement.dataset.theme + '|' + localStorage.getItem('nadaprod-theme')`)) === 'light|light');
  check('Monaco suit la bascule', await ev(`!!document.querySelector('.monaco-editor.vs') && !document.querySelector('.monaco-editor.vs-dark')`));
  await shot('t-06-code-light');
  await ev(`toggleTheme()`); await sleep(500);
  check('retour au sombre', await ev(`!!document.querySelector('.monaco-editor.vs-dark')`));

  // Le landing lit les mêmes jetons.
  await nav('/'); await sleep(1000);
  check('landing : même fond sombre', (await bg()) === 'rgb(10, 10, 9)');
  await ev(`localStorage.removeItem('nadaprod-theme')`);
  noExceptions();
} catch (e) {
  h.fail(e);
} finally {
  h.done();
}
