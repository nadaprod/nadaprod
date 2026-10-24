// Studio : onglet Traduction contre le vrai serveur (clé Gemini factice ⇒ la traduction auto échoue proprement).
import { harness } from './lib.mjs';

const h = await harness({ width: 1440 });
const { ev, nav, shot, key, type, check, login, sleep, viewport, noExceptions } = h;

try {
  await login('web@l3dlp.com', 'smokepass123');
  const id = await ev(`(async () => {
    const r = await fetch('/api/projects', { method: 'POST', headers: {'Content-Type':'application/json'}, body: JSON.stringify({
      name: 'Atelier R&D', brand: { name: 'Nébula' }, seo: { title: 'Accueil', lang: 'fr', langs: ['en'] },
      blocks: [
        { id: 'h1', type: 'hero', html: '<section class="p-10 bg-slate-900 text-white"><h1 class="text-4xl font-bold">Bonjour R&amp;D</h1><p>Lisez <a href="#" class="underline">nos conditions</a>.</p><img src="https://placehold.co/80x40" alt="Une image"></section>' },
        { id: 'h2', type: 'cta', html: '<section class="p-8"><button class="px-4 py-2 bg-violet-600 text-white"><svg class="w-4 h-4 inline" viewBox="0 0 24 24"><path d="M5 12h14"/></svg> Commencer</button></section>' },
      ] }) });
    return (await r.json()).id; })()`);
  check('projet créé', typeof id === 'string' && id.length > 4, String(id));
  await nav('/editor/' + id); await sleep(1500);
  await ev(`document.querySelector('[data-tab=i18n]').click()`); await sleep(1200);
  check('onglet visible', !(await ev(`document.querySelector('#tab-i18n').classList.contains('hidden')`)));
  const langs = await ev(`document.querySelector('#trSource').textContent + '|' + [...document.querySelectorAll('#trTargets [data-tr-lang]')].map(b=>b.textContent.trim()).join()`);
  check('source FR + cible en', /^fr\|en/i.test(langs), langs);
  const n = await ev(`document.querySelectorAll('[data-tr-seg]').length`);
  check('5 segments (seo, titre, phrase, alt, bouton)', n === 5, String(n));
  const srcs = await ev(`[...document.querySelectorAll('[data-tr-seg] p')].map(p => p.textContent.trim())`);
  check('entités décodées à l\'affichage', srcs.some(s => s.includes('Bonjour R&D')) && !srcs.some(s => s.includes('&amp;')), JSON.stringify(srcs));
  check('marqueurs en puces', await ev(`[...document.querySelectorAll('[data-tr-seg] p')].some(p => p.innerHTML.includes('border-cyan-400'))`));
  await shot('r-01-tab');

  const rowOf = (txt) => `[...document.querySelectorAll('[data-tr-seg]')].find(r => r.querySelector('p').textContent.includes(${JSON.stringify(txt)}))`;
  await ev(`${rowOf('Bonjour R&D')}.querySelector('textarea').focus()`);
  await type('Hello R&D');
  await key('Enter', 'Enter', { vk: 13 }); await sleep(700);
  const saved = await ev(`fetch('/api/projects/${id}/i18n').then(r => r.json())`);
  const enTexts = Object.values(saved.translations.en).map(x => x.text + '/' + x.status);
  check('Entrée enregistre (encodé côté serveur)', enTexts.includes('Hello R&amp;D/human'), JSON.stringify(enTexts));
  check('focus passe au suivant', await ev(`document.activeElement.classList.contains('tr-input')`));

  await ev(`(() => { const t = ${rowOf('Lisez')}.querySelector('textarea'); t.focus(); t.value = 'Read our terms.'; })()`);
  await key('Enter', 'Enter', { vk: 13 }); await sleep(500);
  check('marqueurs invalides signalés', !(await ev(`${rowOf('Lisez')}.querySelector('[data-tr-err]').classList.contains('hidden')`)));
  await ev(`(() => { const t = ${rowOf('Lisez')}.querySelector('textarea'); t.focus(); t.value = 'Read <1>our terms</1>.'; })()`);
  await key('Enter', 'Enter', { vk: 13 }); await sleep(700);
  check('marqueurs corrects acceptés', await ev(`${rowOf('Lisez')}.querySelector('[data-tr-err]').classList.contains('hidden')`));

  await ev(`document.querySelector('[data-tr-preview=en]')?.click()`); await sleep(1800);
  check('aperçu traduit (badge)', !(await ev(`document.querySelector('#trPreviewBadge').classList.contains('hidden')`)));
  await shot('r-02-preview');
  await ev(`document.querySelector('#trPreviewBadge').click()`); await sleep(400);

  await ev(`document.querySelector('[data-tr-auto=en]')?.click()`); await sleep(4000);
  await shot('r-03-auto-error');

  const exp = await ev(`fetch('/api/projects/${id}/export', {method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify({format:'html', lang:'en'})}).then(async r => r.status + '|' + (await r.text()))`);
  check('export html en', exp.startsWith('200|') && exp.includes('<html lang="en">') && exp.includes('Hello R&amp;D') && exp.includes('Read <a'), exp.slice(0, 160).replace(/\s+/g, ' '));

  await ev(`fetch('/api/projects/${id}').then(r=>r.json()).then(p => fetch('/api/projects/${id}/blocks/h1', {method:'PUT', headers:{'Content-Type':'application/json'}, body: JSON.stringify({type:'hero', rev: p.rev, html:'<section class="p-4"><h2>Bonjour R&amp;D</h2><p>Tout nouveau</p></section>'})}))`);
  const after = await ev(`fetch('/api/projects/${id}/i18n').then(r => r.json())`);
  const keep = after.segments.find(s => s.src === 'Bonjour R&amp;D');
  check('régénération : traduction conservée', !!keep && after.translations.en[keep.hash]?.text === 'Hello R&amp;D');

  noExceptions();
} catch (e) {
  h.fail(e);
} finally {
  h.done();
}
