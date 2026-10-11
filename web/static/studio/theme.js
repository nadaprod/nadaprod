/* Studio NADAPROD — design system du projet.
 *
 * Le thème définit des JETONS (primary-*, accent-*, font-display, font-sans,
 * rounded-brand) dans la config Tailwind de la page. Les blocs générés s'y
 * réfèrent : changer de thème est donc instantané, sans regénération. Les
 * échelles de couleur sont calculées ici (seule implémentation) et stockées
 * avec le projet ; le serveur se contente de les valider à l'export.
 */
import { S } from './core.js';

export const RADII = { none: '0px', sm: '0.25rem', md: '0.5rem', lg: '0.75rem', xl: '1.25rem', full: '9999px' };
const STOPS = [50, 100, 200, 300, 400, 500, 600, 700, 800, 900, 950];
// La couleur choisie EST le palier 600 (celui des boutons et aplats de marque).
// Paliers plus clairs : part du chemin vers le quasi-blanc ; plus sombres : fraction de sa clarté.
const TINT = { 50: 1, 100: 0.9, 200: 0.76, 300: 0.58, 400: 0.38, 500: 0.18 };
const SHADE = { 700: 0.82, 800: 0.66, 900: 0.52, 950: 0.34 };
const HEX = /^#[0-9a-fA-F]{6}$/;
const FAMILY = /^[A-Za-z0-9][A-Za-z0-9 ]{0,48}$/;

export function defaultTheme() {
  return {
    enabled: false, mode: '', primary: '#7C3AED', accent: '#0EA5E9',
    font_display: '', font_sans: '', radius: 'lg', style: '', scales: {},
  };
}

export function normalizeTheme(raw) {
  const th = { ...defaultTheme(), ...(raw && typeof raw === 'object' ? raw : {}) };
  if (!HEX.test(th.primary)) th.primary = '#7C3AED';
  if (!HEX.test(th.accent)) th.accent = '#0EA5E9';
  if (!RADII[th.radius]) th.radius = 'lg';
  if (!['', 'light', 'dark'].includes(th.mode)) th.mode = '';
  th.scales = { primary: scale(th.primary), accent: scale(th.accent) };
  return th;
}

function hexToHsl(hex) {
  const n = parseInt(hex.slice(1), 16);
  const r = (n >> 16) / 255, g = ((n >> 8) & 255) / 255, b = (n & 255) / 255;
  const max = Math.max(r, g, b), min = Math.min(r, g, b), d = max - min;
  let h = 0;
  if (d) {
    if (max === r) h = ((g - b) / d) % 6; else if (max === g) h = (b - r) / d + 2; else h = (r - g) / d + 4;
    h *= 60; if (h < 0) h += 360;
  }
  const l = (max + min) / 2;
  return [h, d ? d / (1 - Math.abs(2 * l - 1)) : 0, l];
}
function hslToHex(h, s, l) {
  const c = (1 - Math.abs(2 * l - 1)) * s, x = c * (1 - Math.abs(((h / 60) % 2) - 1)), m = l - c / 2;
  const [r, g, b] = h < 60 ? [c, x, 0] : h < 120 ? [x, c, 0] : h < 180 ? [0, c, x] : h < 240 ? [0, x, c] : h < 300 ? [x, 0, c] : [c, 0, x];
  return '#' + [r, g, b].map((v) => Math.round((v + m) * 255).toString(16).padStart(2, '0')).join('');
}

/* Échelle 50→950 à partir d'une couleur : même teinte, la couleur choisie au
 * palier 600, les autres paliers éclaircis ou assombris autour d'elle. */
export function scale(hex) {
  const [h, s, l] = hexToHsl(hex);
  const out = {};
  for (const stop of STOPS) {
    if (stop === 600) out[stop] = hex.toLowerCase();
    else if (stop < 600) out[stop] = hslToHex(h, stop <= 100 ? s * 0.9 : s, l + (0.97 - l) * TINT[stop]);
    else out[stop] = hslToHex(h, stop >= 900 ? s * 0.9 : s, l * SHADE[stop]);
  }
  return out;
}

/* Miroir de export.TailwindConfig (Go) : même structure, mêmes validations. */
export function tailwindConfig(th = S.theme) {
  if (!th?.enabled) return null;
  const extend = { colors: { primary: th.scales.primary, accent: th.scales.accent }, borderRadius: { brand: RADII[th.radius] } };
  const fonts = {};
  const stack = (f) => [`"${f}"`, 'ui-sans-serif', 'system-ui', 'sans-serif'];
  if (FAMILY.test(th.font_display)) fonts.display = stack(th.font_display);
  if (FAMILY.test(th.font_sans)) fonts.sans = stack(th.font_sans);
  if (Object.keys(fonts).length) extend.fontFamily = fonts;
  return { theme: { extend } };
}

export function families(th = S.theme) {
  if (!th?.enabled) return [];
  return [...new Set([th.font_display, th.font_sans].filter((f) => FAMILY.test(f)))];
}

const FILES = { 400: 'regular.ttf', 500: '500.ttf', 600: '600.ttf', 700: '700.ttf' };

/* @font-face servis par le miroir local /webfonts (zéro requête Google dans l'app). */
export function fontFaceCSS(th = S.theme) {
  return families(th).map((fam) => {
    const font = S.cfg.fonts.find((f) => f.family === fam);
    return (font?.weights || []).map((w) =>
      `@font-face{font-family:'${fam}';font-style:normal;font-weight:${w};font-display:swap;src:url('${location.origin}/webfonts/${encodeURIComponent(fam)}/${FILES[w]}') format('truetype');}`).join('');
  }).join('');
}

export const googleFontsHref = (th = S.theme) =>
  'https://fonts.googleapis.com/css2?' + families(th).map((f) => 'family=' + f.replace(/ /g, '+') + ':wght@400;500;600;700').join('&') + '&display=swap';

/* Ce que le serveur attend dans une requête de génération (gemini.Theme). */
export function themeForPrompt(th = S.theme) {
  if (!th?.enabled) return null;
  const { enabled, mode, primary, accent, font_display, font_sans, radius, style } = th;
  return { enabled, mode, primary, accent, font_display, font_sans, radius, style };
}
