/* NADAPROD — comportements communs des pages publiques (landing, /debate) :
 * bascule FR/EN, bouton « Connexion » qui
 * devient « Mon compte » quand une session est ouverte.
 * À charger après /static/i18n.js et /static/theme.js (thème : theme.js).
 * Expose toggleLanguage(). */
(function () {
  'use strict';
  // Barre de navigation : rubrique courante + menu plein écran sur mobile.
  window.markNav = function (current) {
    document.querySelectorAll('.nav-links [data-nav]').forEach((a) => {
      if (a.dataset.nav === current) a.setAttribute('aria-current', 'page'); else a.removeAttribute('aria-current');
    });
  };
  const nav = () => document.getElementById('site-nav');
  const toggleBtn = () => document.querySelector('.nav-toggle');
  window.closeMenu = function () {
    const n = nav(), b = toggleBtn();
    if (!n || !n.classList.contains('open')) return;
    n.classList.remove('open');
    document.body.classList.remove('menu-open');
    b.setAttribute('aria-expanded', 'false');
    b.setAttribute('aria-label', t('home.menu'));
    b.focus();
  };
  function openMenu() {
    const n = nav(), b = toggleBtn();
    n.classList.add('open');
    document.body.classList.add('menu-open');
    b.setAttribute('aria-expanded', 'true');
    b.setAttribute('aria-label', t('home.menuClose'));
    n.querySelector('a, button')?.focus();
  }
  document.addEventListener('DOMContentLoaded', () => {
    const b = toggleBtn();
    if (!b) return;
    b.addEventListener('click', () => (nav().classList.contains('open') ? closeMenu() : openMenu()));
    nav().addEventListener('click', (e) => { if (e.target.closest('a')) closeMenu(); });
    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') closeMenu();
      // Tab reste dans le menu ouvert (le bouton ✕ compris).
      if (e.key === 'Tab' && nav().classList.contains('open')) {
        const items = [b, ...nav().querySelectorAll('a, button')];
        const i = items.indexOf(document.activeElement);
        const next = e.shiftKey ? (i <= 0 ? items.length - 1 : i - 1) : (i === items.length - 1 ? 0 : i + 1);
        e.preventDefault(); items[next].focus();
      }
    });
    addEventListener('resize', () => { if (innerWidth > 900) closeMenu(); });
    if (document.body.dataset.page) markNav(document.body.dataset.page);
  });

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
