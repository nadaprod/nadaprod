/* i18n.js — traductions de l'app par fichiers JSON (/static/i18n/<lang>.json).
 *
 * Résolution : choix mémorisé (localStorage) sinon langue du navigateur,
 * puis en.json pour toute clé manquante dans la langue chargée. Le HTML
 * embarque le français en dur comme repli statique (zéro flash pour les
 * visiteurs francophones, contenu affichable même si le fetch échoue).
 *
 * Usage :
 *   - HTML : data-i18n="clé" (innerHTML), data-i18n-placeholder / -title /
 *     -aria-label / -alt / -value / -content = "clé" (attributs).
 *   - JS   : await I18N.ready puis t('clé') ou t('clé', {nom: valeur}).
 *   - Changement de langue : I18N.setLang('en') (persisté), I18N.onChange(cb).
 */
(function () {
  'use strict';
  var STORE = 'nadaprod-lang';
  var FALLBACK = 'en';
  var ATTRS = ['placeholder', 'title', 'aria-label', 'alt', 'value', 'content'];

  var dict = {};
  var listeners = [];

  function stored() {
    try { return localStorage.getItem(STORE); } catch (_) { return null; }
  }
  function detect() {
    var nav = (navigator.languages && navigator.languages[0]) || navigator.language || FALLBACK;
    return (stored() || nav).slice(0, 2).toLowerCase();
  }
  function fetchDict(lang) {
    return fetch('/static/i18n/' + lang + '.json')
      .then(function (r) { return r.ok ? r.json() : null; })
      .catch(function () { return null; });
  }

  var domReady = new Promise(function (resolve) {
    if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', resolve);
    else resolve();
  });

  function load(lang) {
    return Promise.all([
      fetchDict(FALLBACK),
      lang === FALLBACK ? null : fetchDict(lang),
      domReady,
    ]).then(function (r) {
      var fb = r[0], loc = r[1];
      dict = Object.assign({}, fb || {}, loc || {});
      I18N.lang = loc ? lang : FALLBACK;
      document.documentElement.lang = I18N.lang;
      apply(document);
      listeners.forEach(function (cb) { try { cb(I18N.lang); } catch (_) {} });
    });
  }

  function t(key, params) {
    var s = dict[key];
    if (s == null) return key; // clé manquante : elle s'affiche telle quelle, facile à repérer
    if (params) for (var k in params) s = s.split('{' + k + '}').join(params[k]);
    return s;
  }

  function apply(root) {
    root.querySelectorAll('[data-i18n]').forEach(function (el) {
      var s = dict[el.getAttribute('data-i18n')];
      if (s != null) el.innerHTML = s; // valeurs issues de nos propres JSON : HTML autorisé
    });
    ATTRS.forEach(function (attr) {
      root.querySelectorAll('[data-i18n-' + attr + ']').forEach(function (el) {
        var s = dict[el.getAttribute('data-i18n-' + attr)];
        if (s != null) el.setAttribute(attr, s);
      });
    });
  }

  var I18N = {
    lang: detect(),
    t: t,
    apply: apply,
    ready: null,
    setLang: function (lang) {
      lang = String(lang).slice(0, 2).toLowerCase();
      try { localStorage.setItem(STORE, lang); } catch (_) {}
      return load(lang);
    },
    onChange: function (cb) { listeners.push(cb); },
  };
  I18N.ready = load(I18N.lang);
  window.I18N = I18N;
  window.t = t;
})();
