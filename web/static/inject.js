/* NADAPROD — éditeur injecté. Un seul fichier, aucun état côté page. */
(function () {
  'use strict';
  if (window.__wp) return; // idempotent
  window.__wp = true;

  var selected = null;

  function el(e) {
    var t = e.target;
    if (!(t instanceof Element)) return null;
    if (t === document.documentElement || t === document.body) return null;
    if (t.closest('#__wp_inject,#__wp_style')) return null;
    return t;
  }

  function post(msg) { parent.postMessage(msg, location.origin); }

  function select(t) {
    if (selected) { selected.removeAttribute('data-wp-selected'); selected.removeAttribute('contenteditable'); }
    selected = t;
    if (t) {
      t.setAttribute('data-wp-selected', '');
      post({ type: 'wp:selected', tag: t.tagName.toLowerCase(), html: t.outerHTML });
    } else {
      post({ type: 'wp:deselected' });
    }
  }

  /* Survol */
  document.addEventListener('mouseover', function (e) { var t = el(e); if (t && t !== selected) t.setAttribute('data-wp-hover', ''); });
  document.addEventListener('mouseout', function (e) { var t = el(e); if (t) t.removeAttribute('data-wp-hover'); });

  /* Clic = sélectionner (et neutraliser liens/boutons en mode édition) */
  document.addEventListener('click', function (e) {
    var t = el(e); if (!t) return;
    if (selected && selected.contentEditable === 'true' && selected.contains(t)) return; // laisser éditer
    e.preventDefault(); e.stopPropagation();
    t.removeAttribute('data-wp-hover');
    select(t);
  }, true);

  /* Double-clic = édition du texte en place */
  document.addEventListener('dblclick', function (e) {
    var t = el(e); if (!t) return;
    e.preventDefault();
    select(t);
    t.setAttribute('contenteditable', 'true');
    t.focus();
  }, true);

  document.addEventListener('focusout', function (e) {
    if (e.target instanceof Element && e.target.getAttribute('contenteditable') === 'true') {
      e.target.removeAttribute('contenteditable');
      if (e.target === selected) post({ type: 'wp:selected', tag: selected.tagName.toLowerCase(), html: selected.outerHTML });
    }
  });

  document.addEventListener('keydown', function (e) { if (e.key === 'Escape') select(null); });
  document.addEventListener('input', function () { post({ type: 'wp:dirty' }); });

  /* Sérialisation : la page telle qu'elle sera écrite sur disque,
     sans le moindre artefact d'édition. */
  function serialize() {
    var clone = document.documentElement.cloneNode(true);
    clone.querySelectorAll('#__wp_inject,#__wp_style').forEach(function (n) { n.remove(); });
    clone.querySelectorAll('[data-wp-hover],[data-wp-selected],[contenteditable]').forEach(function (n) {
      n.removeAttribute('data-wp-hover');
      n.removeAttribute('data-wp-selected');
      n.removeAttribute('contenteditable');
    });
    return '<!DOCTYPE html>\n' + clone.outerHTML;
  }

  /* Ordres du parent (le studio) */
  window.addEventListener('message', function (e) {
    if (e.origin !== location.origin || !e.data || typeof e.data.type !== 'string') return;
    switch (e.data.type) {
      case 'wp:get-html':
        post({ type: 'wp:html', html: serialize() });
        break;
      case 'wp:replace': // résultat IA → remplace l'élément sélectionné
        if (selected) {
          var tmp = document.createElement('template');
          tmp.innerHTML = e.data.html;
          var next = tmp.content.firstElementChild;
          if (next) { selected.replaceWith(next); select(next); }
        }
        break;
      case 'wp:delete':
        if (selected) { var s = selected; select(null); s.remove(); }
        break;
      case 'wp:parent': // remonter la sélection d'un cran
        if (selected && selected.parentElement && selected.parentElement !== document.body) select(selected.parentElement);
        break;
    }
  });

  post({ type: 'wp:ready', title: document.title });
})();
