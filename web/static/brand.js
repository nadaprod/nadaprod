/* NADAPROD — comportements communs des pages publiques (landing, /debate) :
 * bascule FR/EN, bouton « Connexion » qui
 * devient « Mon compte » quand une session est ouverte.
 * À charger après /static/i18n.js et /static/theme.js (thème : theme.js).
 * Expose toggleLanguage(). */
(function () {
  'use strict';
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
