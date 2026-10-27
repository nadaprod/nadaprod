// Contraste réel des éléments cliquables visibles, page par page, dans les deux
// thèmes : couleur du texte contre le fond effectivement derrière (premier
// ancêtre opaque). Échoue sous 3:1 (boutons, liens) et liste les coupables.
// Suppose les données semées par run.sh (sites, demo).
import { harness } from './lib.mjs';

const h = await harness({ width: 1440 });
const { ev, nav, check, sleep, login, noExceptions } = h;

const AUDIT = `(() => {
  const rgb = (s) => { const m = s.match(/[\\d.]+/g); return m ? m.map(Number) : [0, 0, 0, 0]; };
  const lum = ([r, g, b]) => { const f = (c) => { c /= 255; return c <= .03928 ? c / 12.92 : ((c + .055) / 1.055) ** 2.4; }; return .2126 * f(r) + .7152 * f(g) + .0722 * f(b); };
  const ratio = (a, b) => { const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p); return (x + .05) / (y + .05); };
  const bgOf = (el) => {
    for (let n = el; n; n = n.parentElement) {
      const cs = getComputedStyle(n);
      // Surlignage en dégradé (rubrique courante) : la couleur pleine du dégradé est le fond du texte.
      const g = [...cs.backgroundImage.matchAll(/rgba?\\([^)]*\\)/g)].map((m) => rgb(m[0])).filter((c) => (c[3] ?? 1) > 0.5);
      if (g.length) return g[0];
      const c = rgb(cs.backgroundColor);
      if ((c[3] ?? 1) > 0.5) return c;
    }
    return rgb(getComputedStyle(document.documentElement).backgroundColor);
  };
  const bad = [];
  for (const el of document.querySelectorAll('button, a[href], [role=button], [role=tab], [role=option]')) {
    const r = el.getBoundingClientRect(), cs = getComputedStyle(el);
    if (!r.width || !r.height || cs.visibility === 'hidden' || cs.opacity === '0' || el.disabled) continue;
    if (!el.textContent.trim()) continue; // icône seule : traitée à part
    const fg = rgb(cs.color), k = ratio(fg, bgOf(el));
    if (k < 3) bad.push((el.id ? '#' + el.id : el.tagName.toLowerCase()) + ' « ' + el.textContent.trim().slice(0, 24) + ' » ' + k.toFixed(2));
  }
  return bad;
})()`;

try {
  await login('web@l3dlp.com', 'smokepass123');
  const pages = [['/', 'landing'], ['/debate', 'débat'], ['/login', 'connexion'], ['/waiting-list', 'liste d\'attente'],
    ['/sites', 'sites'], ['/sites?open=demo', 'éditeur (code)'], ['/editor', 'studio']];
  for (const theme of ['dark', 'light']) {
    await ev(`localStorage.setItem('nadaprod-theme', '${theme}')`);
    for (const [url, name] of pages) {
      await nav(url); await sleep(url.includes('open=') ? 3500 : 1500);
      const bad = await ev(AUDIT);
      check(`${theme} · ${name} : contraste des boutons ≥ 3:1`, bad.length === 0, bad.join(' | '));
    }
    // L'éditeur en mode visuel (secours) a ses propres boutons.
    await nav('/sites?open=demo'); await sleep(3500);
    await ev(`document.querySelector('[data-mode=visual]')?.click()`); await sleep(1500);
    const bad = await ev(AUDIT);
    check(`${theme} · éditeur (visuel) : contraste des boutons ≥ 3:1`, bad.length === 0, bad.join(' | '));
  }
  await ev(`localStorage.removeItem('nadaprod-theme')`);
  noExceptions();
} catch (e) {
  h.fail(e);
} finally {
  h.done();
}
