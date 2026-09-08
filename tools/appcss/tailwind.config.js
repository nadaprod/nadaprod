// Config Tailwind des pages de l'app (web/{index,sites,drafts,login,waiting-list}.html).
// Miroir du `tailwind.config` inline historique de ces pages — compilée par
// bin/build.sh en web/static/tw.css (fini cdn.tailwindcss.com dans notre UI,
// cohérent avec ce que le pipeline impose aux sites publiés).
// Après tout ajout de classe dans ces pages : relancer bin/build.sh (ou la
// commande tailwindcss seule) pour régénérer tw.css.
module.exports = {
  content: [
    "./web/index.html",
    "./web/sites.html",
    "./web/drafts.html",
    "./web/login.html",
    "./web/waiting-list.html",
  ],
  theme: {
    extend: {
      fontFamily: {
        display: ['"Space Grotesk"', 'sans-serif'],
        sans: ['Inter', 'sans-serif'],
        mono: ['"JetBrains Mono"', 'monospace'],
      },
      colors: {
        ink: { 950: '#07090D', 900: '#0A0C11', 850: '#0E1117', 800: '#131722', 700: '#1C2230', 600: '#2A3245', 400: '#5C687F', 300: '#8A94A8', 200: '#B9C0CF', 100: '#E7EAF1' },
        vio: { 500: '#8B5CF6', 400: '#A78BFA', 600: '#7C3AED' },
      },
    },
  },
};
