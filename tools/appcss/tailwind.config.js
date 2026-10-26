// Config Tailwind des pages de l'app (web/{studio,sites,drafts,login,waiting-list}.html
// et les modules web/static/studio/*.js, qui produisent du HTML à classes Tailwind),
// compilée par bin/build.sh en web/static/tw.css (pas de CDN dans notre UI).
// Après tout ajout de classe dans ces pages : relancer bin/build.sh (ou la
// commande tailwindcss seule) pour régénérer tw.css.
//
// Identité commune avec le landing : les couleurs sont des variables CSS
// (web/static/tokens.css, thèmes clair et sombre) — ink = gris chauds,
// vio / indigo / cyan = le vert acide de NADAPROD. Les noms de classes de
// l'app restent ceux d'origine : c'est la palette qui a changé, pas le HTML.
// Coins presque carrés ; une seule ombre franche (le bouton principal).
const v = (name) => `rgb(var(--${name}) / <alpha-value>)`;
const ink = Object.fromEntries([950, 900, 850, 800, 700, 600, 400, 300, 200, 100].map((n) => [n, v('ink-' + n)]));
// Accent : en texte / trait = --acc-fg (vert en sombre, olive lisible en clair) ;
// la teinte 600 sert de fond (--acc-fill, toujours le vert acide).
const accent = { 200: v('acc-fg'), 300: v('acc-fg'), 400: v('acc-fg'), 500: v('acc-fg'), 600: v('acc-fill') };
// Statuts : les teintes claires (200–400, celles du texte et des pastilles) suivent
// le thème pour rester lisibles sur fond clair ; 500 et plus gardent Tailwind.
const status = (name) => ({ 200: v(name), 300: v(name), 400: v(name) });

module.exports = {
  content: [
    "./web/studio.html",
    "./web/static/studio/*.js",
    "./web/static/sites-code.js",
    "./web/static/sites-app.js",
    "./web/static/sites-explorer.js",
    "./web/sites.html",
    "./web/drafts.html",
    "./web/login.html",
    "./web/waiting-list.html",
  ],
  theme: {
    extend: {
      fontFamily: {
        display: ['"Permanent Marker"', 'sans-serif'],
        sans: ['Archivo', 'system-ui', 'sans-serif'],
        mono: ['"JetBrains Mono"', 'monospace'],
      },
      colors: { ink, vio: accent, indigo: accent, cyan: accent, emerald: status('ok'), amber: status('warn'), red: status('err'), acc: { fill: v('acc-fill'), on: v('acc-on'), fg: v('acc-fg') } },
      borderRadius: { sm: '1px', DEFAULT: '2px', md: '2px', lg: '2px', xl: '3px', '2xl': '4px', '3xl': '6px' },
      // Pas d'ombre dans l'app (doc/DESIGN.md, règle 4) : la seule ombre franche
      // est celle de .btn-primary. Les fenêtres tiennent par un filet + un voile.
      boxShadow: Object.fromEntries(['sm', 'DEFAULT', 'md', 'lg', 'xl', '2xl', 'inner'].map((k) => [k, '0 0 #0000'])),
    },
  },
};
