/* NADAPROD — thème clair / sombre, commun à toutes les pages (landing, /debate,
 * app). À charger dans le <head> : le thème mémorisé s'applique avant le
 * premier rendu (pas de flash). Sans choix mémorisé, le système décide.
 *   NPTheme.dark()            le thème courant est-il sombre ?
 *   NPTheme.toggle()          bascule et mémorise (localStorage 'nadaprod-theme')
 *   NPTheme.onChange(fn)      fn(dark) à chaque changement (bascule ou système)
 *   NPTheme.monaco(monaco)    déclare les thèmes Monaco, renvoie celui à utiliser,
 *                             et suit ensuite les changements tout seul
 * toggleTheme() reste exposé pour les boutons existants. */
(function () {
  'use strict';
  const html = document.documentElement;
  const KEY = 'nadaprod-theme';
  try { const th = localStorage.getItem(KEY); if (th) html.dataset.theme = th; } catch (_) {}

  const system = matchMedia('(prefers-color-scheme: dark)');
  const dark = () => html.dataset.theme ? html.dataset.theme === 'dark' : system.matches;
  const listeners = [];
  const emit = () => listeners.forEach((fn) => { try { fn(dark()); } catch (_) {} });
  system.addEventListener('change', () => { if (!html.dataset.theme) emit(); });

  function toggle() {
    html.dataset.theme = dark() ? 'light' : 'dark';
    try { localStorage.setItem(KEY, html.dataset.theme); } catch (_) {}
    emit();
  }

  // Monaco : mêmes valeurs que tokens.css (Monaco ne lit pas les variables CSS).
  const MONACO = {
    'nadaprod-dark': { base: 'vs-dark', colors: {
      'editor.background': '#0f0f0e', 'editorGutter.background': '#0f0f0e', 'editor.lineHighlightBackground': '#161615',
      'editorLineNumber.foreground': '#6c6c66', 'editorLineNumber.activeForeground': '#c9c9c1',
      'editor.selectionBackground': '#30302c', 'editorWidget.background': '#161615', 'editorCursor.foreground': '#ccff00' } },
    'nadaprod-light': { base: 'vs', colors: {
      'editor.background': '#ffffff', 'editorGutter.background': '#ffffff', 'editor.lineHighlightBackground': '#f3f3ee',
      'editorLineNumber.foreground': '#8a8a82', 'editorLineNumber.activeForeground': '#33332f',
      'editor.selectionBackground': '#ebebe5', 'editorWidget.background': '#f3f3ee', 'editorCursor.foreground': '#4d6600' } },
  };
  let monacoBound = false;
  function monaco(m) {
    for (const [name, th] of Object.entries(MONACO)) m.editor.defineTheme(name, { base: th.base, inherit: true, rules: [], colors: th.colors });
    if (!monacoBound) { monacoBound = true; listeners.push((d) => m.editor.setTheme(d ? 'nadaprod-dark' : 'nadaprod-light')); }
    return dark() ? 'nadaprod-dark' : 'nadaprod-light';
  }

  window.NPTheme = { dark, toggle, onChange: (fn) => listeners.push(fn), monaco };
  window.toggleTheme = toggle;
})();
