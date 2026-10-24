/* NADAPROD — comportements communs des pages publiques (landing, /debate) :
 * thème clair/sombre mémorisé, bascule FR/EN, bouton « Connexion » qui
 * devient « Mon compte » quand une session est ouverte.
 * À charger dans le <head>, après /static/i18n.js (le thème s'applique avant
 * le premier rendu : pas de flash). Expose toggleTheme() et toggleLanguage(). */
(function () {
  'use strict';
  try {
    const th = localStorage.getItem('nadaprod-theme');
    if (th) document.documentElement.dataset.theme = th;
  } catch (_) {}

  // Thème : choix mémorisé, sinon celui du système.
  window.toggleTheme = function () {
    const html = document.documentElement;
    const dark = html.dataset.theme ? html.dataset.theme === 'dark' : matchMedia('(prefers-color-scheme: dark)').matches;
    html.dataset.theme = dark ? 'light' : 'dark';
    try { localStorage.setItem('nadaprod-theme', html.dataset.theme); } catch (_) {}
  };

  window.toggleLanguage = function () { I18N.setLang(I18N.lang === 'fr' ? 'en' : 'fr'); };
  const langLabel = () => {
    const b = document.getElementById('lang-btn');
    if (b) b.textContent = I18N.lang.toUpperCase();
  };
  I18N.onChange(langLabel);
  I18N.ready.then(langLabel);

  // Session déjà ouverte ⇒ « Connexion » devient « Mon compte » (le libellé suit la langue).
  Promise.all([I18N.ready, fetch('/api/me')]).then(([, r]) => {
    const btn = document.getElementById('auth-btn');
    if (!r.ok || !btn) return;
    btn.href = '/sites';
    btn.setAttribute('data-i18n-aria-label', 'home.nav_account');
    btn.setAttribute('aria-label', t('home.nav_account'));
    const label = btn.querySelector('.auth-label');
    label.setAttribute('data-i18n', 'home.nav_account');
    label.textContent = t('home.nav_account');
  }).catch(() => {});
})();
