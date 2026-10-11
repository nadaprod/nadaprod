/* NADAPROD — script de l'aperçu du studio de blocs.
 *
 * Tourne DANS l'iframe d'aperçu, elle-même isolée (sandbox="allow-scripts",
 * origine opaque) : le HTML généré par l'IA ne peut ni lire les cookies de
 * l'app ni appeler son API. Tout passe par postMessage avec le studio.
 *
 * Studio → aperçu : np:sync, np:stream, np:select, np:mode, np:replace-el,
 *                   np:set-img, np:parent, np:delete-el, np:audit
 * Aperçu → studio : np:ready, np:block-click, np:el-selected, np:el-deselected,
 *                   np:block-html, np:audit-result
 */
(function () {
  'use strict';
  if (window.__np) return; // idempotent
  window.__np = true;

  var root = document.getElementById('np-root');
  var mode = 'block';      // 'block' : clic = sélection du bloc ; 'element' : retouche ciblée
  var selectedEl = null;   // élément sélectionné (mode element)
  var selectedBlock = null;

  function post(msg) { parent.postMessage(msg, '*'); } // origine opaque : pas de cible nommable
  function wrapperOf(node) { return node && node.closest ? node.closest('[data-np-block]') : null; }
  function wrapper(id) { return root.querySelector('[data-np-block="' + CSS.escape(id) + '"]'); }

  /* ───────── Rendu des blocs ───────── */

  // innerHTML n'exécute pas les <script> : on les recrée pour que les blocs
  // qui y ont droit (menu mobile du header, fermeture du bandeau) fonctionnent.
  function runScripts(el) {
    el.querySelectorAll('script').forEach(function (old) {
      var s = document.createElement('script');
      for (var i = 0; i < old.attributes.length; i++) s.setAttribute(old.attributes[i].name, old.attributes[i].value);
      s.textContent = old.textContent;
      old.replaceWith(s);
    });
  }

  function skeleton(label, hint) {
    var s = document.createElement('section');
    s.className = 'np-skeleton';
    var a = document.createElement('p'); a.className = 'np-skeleton-label'; a.textContent = label;
    var b = document.createElement('p'); b.className = 'np-skeleton-hint'; b.textContent = hint;
    s.appendChild(a); s.appendChild(b);
    return s;
  }

  function setBlock(w, b, hint) {
    if (b.html) {
      w.removeAttribute('data-np-empty');
      w.innerHTML = b.html;
      runScripts(w);
    } else {
      w.setAttribute('data-np-empty', '');
      w.replaceChildren(skeleton(b.label, hint));
    }
    w.__html = b.html;
  }

  // Réconciliation par identifiant : seuls les blocs modifiés sont re-rendus,
  // la position de défilement survit à chaque changement.
  function sync(data) {
    var keep = {};
    var prev = null;
    data.blocks.forEach(function (b) {
      keep[b.id] = true;
      var w = wrapper(b.id);
      if (!w) {
        w = document.createElement('div');
        w.setAttribute('data-np-block', b.id);
        w.__html = null;
      }
      if (w.__html !== b.html || w.__label !== b.label) { w.__label = b.label; setBlock(w, b, data.hint); }
      var want = prev ? prev.nextSibling : root.firstChild;
      if (w !== want) root.insertBefore(w, want);
      prev = w;
    });
    Array.prototype.slice.call(root.children).forEach(function (w) {
      if (!keep[w.getAttribute('data-np-block')]) w.remove();
    });
    if (selectedEl && !document.contains(selectedEl)) selectEl(null, true);
    markBlock(data.selectedId, false);
  }

  function markBlock(id, scroll) {
    selectedBlock = id || null;
    root.querySelectorAll('[data-np-current]').forEach(function (w) { w.removeAttribute('data-np-current'); });
    var w = id ? wrapper(id) : null;
    if (!w) return;
    w.setAttribute('data-np-current', '');
    if (scroll) w.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
  }

  /* ───────── Sélection d'élément (retouche ciblée) ───────── */

  function cleanHTML(node) {
    var clone = node.cloneNode(true);
    [clone].concat(Array.prototype.slice.call(clone.querySelectorAll('*'))).forEach(function (n) {
      if (!n.removeAttribute) return;
      n.removeAttribute('data-np-hover'); n.removeAttribute('data-np-selected'); n.removeAttribute('contenteditable');
    });
    return clone;
  }

  function blockHTML(w) { return cleanHTML(w).innerHTML.trim(); }

  function commit(w, reason) {
    if (!w) return;
    var html = blockHTML(w);
    w.__html = html;
    post({ type: 'np:block-html', id: w.getAttribute('data-np-block'), html: html, reason: reason });
  }

  function selectEl(el, silent) {
    if (selectedEl) { selectedEl.removeAttribute('data-np-selected'); selectedEl.removeAttribute('contenteditable'); }
    selectedEl = el;
    if (!el) { if (!silent) post({ type: 'np:el-deselected' }); return; }
    el.setAttribute('data-np-selected', '');
    var w = wrapperOf(el);
    post({
      type: 'np:el-selected',
      id: w.getAttribute('data-np-block'),
      tag: el.tagName.toLowerCase(),
      html: cleanHTML(el).outerHTML,
      isRoot: el.parentElement === w,
      isImg: el.tagName === 'IMG',
      photo: el.getAttribute('data-photo') || '',
      alt: el.getAttribute('alt') || '',
    });
  }

  function target(e) {
    var t = e.target;
    if (!(t instanceof Element)) return null;
    var w = wrapperOf(t);
    if (!w || t === w || w.hasAttribute('data-np-empty')) return null;
    return t;
  }

  document.addEventListener('mouseover', function (e) {
    if (mode !== 'element') return;
    var t = target(e); if (t && t !== selectedEl) t.setAttribute('data-np-hover', '');
  });
  document.addEventListener('mouseout', function (e) {
    if (e.target instanceof Element) e.target.removeAttribute('data-np-hover');
  });

  // Clic : jamais de navigation dans l'aperçu. Mode bloc ⇒ sélectionne le bloc ;
  // mode élément ⇒ sélectionne l'élément précis.
  document.addEventListener('click', function (e) {
    var w = wrapperOf(e.target);
    if (selectedEl && selectedEl.isContentEditable && selectedEl.contains(e.target)) return; // on édite du texte
    e.preventDefault(); e.stopPropagation();
    if (!w) return;
    if (mode === 'element') {
      var t = target(e);
      if (t) { t.removeAttribute('data-np-hover'); selectEl(t); }
    }
    post({ type: 'np:block-click', id: w.getAttribute('data-np-block') });
  }, true);

  // Double-clic (mode élément) : édition du texte en place.
  document.addEventListener('dblclick', function (e) {
    if (mode !== 'element') return;
    var t = target(e); if (!t) return;
    e.preventDefault();
    selectEl(t);
    t.setAttribute('contenteditable', 'true');
    t.focus();
  }, true);

  document.addEventListener('focusout', function (e) {
    var t = e.target;
    if (t instanceof Element && t.getAttribute('contenteditable') === 'true') {
      t.removeAttribute('contenteditable');
      var w = wrapperOf(t);
      if (w && blockHTML(w) !== w.__html) commit(w, 'edit');
      if (t === selectedEl) selectEl(t);
    }
  });

  document.addEventListener('submit', function (e) { e.preventDefault(); }, true);
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') { if (document.activeElement && document.activeElement.isContentEditable) document.activeElement.blur(); else selectEl(null); }
  });

  /* ───────── Audit qualité ───────── */

  function parseColor(s) {
    var m = /rgba?\(\s*([\d.]+)[ ,]+([\d.]+)[ ,]+([\d.]+)(?:[ ,/]+([\d.]+%?))?\s*\)/.exec(s || '');
    if (!m) return null;
    var a = m[4] === undefined ? 1 : (m[4].slice(-1) === '%' ? parseFloat(m[4]) / 100 : parseFloat(m[4]));
    return [parseFloat(m[1]), parseFloat(m[2]), parseFloat(m[3]), a];
  }
  function over(top, bottom) { // composition alpha de top sur bottom (opaque)
    var a = top[3];
    return [top[0] * a + bottom[0] * (1 - a), top[1] * a + bottom[1] * (1 - a), top[2] * a + bottom[2] * (1 - a), 1];
  }
  function luminance(c) {
    var f = function (v) { v /= 255; return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4); };
    return 0.2126 * f(c[0]) + 0.7152 * f(c[1]) + 0.0722 * f(c[2]);
  }
  function ratio(a, b) {
    var la = luminance(a), lb = luminance(b);
    return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
  }
  // Fond effectif : on remonte les ancêtres ; un dégradé ou une image de fond
  // rend le contraste incalculable ⇒ null (on ne signale que ce qu'on sait mesurer).
  function background(el) {
    var layers = [];
    for (var n = el; n && n !== document.documentElement; n = n.parentElement) {
      var cs = getComputedStyle(n);
      if (cs.backgroundImage && cs.backgroundImage !== 'none') return null;
      var c = parseColor(cs.backgroundColor);
      if (c && c[3] > 0) { layers.push(c); if (c[3] >= 1) break; }
    }
    var bg = [255, 255, 255, 1];
    for (var i = layers.length - 1; i >= 0; i--) bg = over(layers[i], bg);
    return bg;
  }
  function visible(el) {
    var cs = getComputedStyle(el), r = el.getBoundingClientRect();
    if (cs.display === 'none' || cs.visibility === 'hidden' || parseFloat(cs.opacity) === 0) return false;
    if (r.width < 2 || r.height < 2) return false; // sr-only et assimilés
    return true;
  }
  function hasOwnText(el) {
    for (var n = el.firstChild; n; n = n.nextSibling) if (n.nodeType === 3 && n.nodeValue.trim().length > 1) return true;
    return false;
  }
  function accessibleName(el) {
    if (el.getAttribute('aria-label') || el.getAttribute('aria-labelledby') || el.getAttribute('title')) return true;
    if ((el.textContent || '').trim()) return true;
    var img = el.querySelector('img[alt]:not([alt=""])');
    return !!img || !!el.querySelector('svg[aria-label], svg title');
  }
  function snippet(el) {
    var t = (el.textContent || '').replace(/\s+/g, ' ').trim();
    return t ? t.slice(0, 60) : '<' + el.tagName.toLowerCase() + '>';
  }

  function audit() {
    var blocks = [];
    var headings = [];
    Array.prototype.forEach.call(root.children, function (w) {
      if (w.hasAttribute('data-np-empty')) return;
      var issues = [];
      var add = function (kind, severity, el, extra) {
        if (issues.filter(function (i) { return i.kind === kind; }).length >= 6) return; // borne par type
        var it = { kind: kind, severity: severity, snippet: snippet(el) };
        if (extra) for (var k in extra) it[k] = extra[k];
        issues.push(it);
      };
      w.querySelectorAll('img').forEach(function (img) {
        if (!img.hasAttribute('alt') && img.getAttribute('role') !== 'presentation' && img.getAttribute('aria-hidden') !== 'true')
          add('img-alt', 'error', img, { snippet: (img.getAttribute('src') || '').slice(0, 60) });
      });
      w.querySelectorAll('a, button').forEach(function (el) {
        if (visible(el) && !accessibleName(el)) add('no-name', 'error', el);
      });
      w.querySelectorAll('input, textarea, select').forEach(function (el) {
        var type = (el.getAttribute('type') || '').toLowerCase();
        if (type === 'hidden' || type === 'submit' || type === 'button') return;
        var labelled = el.getAttribute('aria-label') || el.getAttribute('aria-labelledby') || el.closest('label') ||
          (el.id && w.querySelector('label[for="' + CSS.escape(el.id) + '"]'));
        if (!labelled) add('no-label', 'error', el, { snippet: el.getAttribute('placeholder') || el.getAttribute('name') || '<' + el.tagName.toLowerCase() + '>' });
      });
      w.querySelectorAll('*').forEach(function (el) {
        if (!hasOwnText(el) || !visible(el) || el.closest('script,style,svg')) return;
        var cs = getComputedStyle(el), fg = parseColor(cs.color), bg = background(el);
        if (!fg || !bg) return;
        var r = ratio(over(fg, bg), bg);
        var size = parseFloat(cs.fontSize), bold = parseInt(cs.fontWeight, 10) >= 700;
        var need = (size >= 24 || (size >= 18.66 && bold)) ? 3 : 4.5;
        if (r < need) add('contrast', r < need - 1.2 ? 'error' : 'warning', el, { ratio: Math.round(r * 10) / 10, need: need });
      });
      w.querySelectorAll('h1,h2,h3,h4,h5,h6').forEach(function (h) {
        headings.push({ level: parseInt(h.tagName[1], 10), id: w.getAttribute('data-np-block'), el: h });
      });
      blocks.push({ id: w.getAttribute('data-np-block'), issues: issues });
    });

    // Hiérarchie des titres, à l'échelle de la page.
    var byId = {}; blocks.forEach(function (b) { byId[b.id] = b; });
    var h1 = headings.filter(function (h) { return h.level === 1; });
    var page = [];
    if (headings.length && h1.length === 0) page.push({ kind: 'h1-missing', severity: 'warning' });
    h1.slice(1).forEach(function (h) { byId[h.id].issues.push({ kind: 'h1-multiple', severity: 'warning', snippet: snippet(h.el) }); });
    for (var i = 1; i < headings.length; i++) {
      if (headings[i].level > headings[i - 1].level + 1)
        byId[headings[i].id].issues.push({ kind: 'heading-skip', severity: 'warning', snippet: snippet(headings[i].el), from: headings[i - 1].level, to: headings[i].level });
    }
    post({ type: 'np:audit-result', blocks: blocks, page: page });
  }

  /* ───────── Ordres du studio ───────── */

  window.addEventListener('message', function (e) {
    if (e.source !== parent || !e.data || typeof e.data.type !== 'string') return;
    var d = e.data, w;
    switch (d.type) {
      case 'np:sync': sync(d); break;
      case 'np:stream': // génération en cours : HTML partiel, scripts non exécutés
        w = wrapper(d.id);
        if (w) { w.removeAttribute('data-np-empty'); w.innerHTML = d.html; w.__html = null; }
        break;
      case 'np:select': markBlock(d.id, d.scroll); break;
      case 'np:mode':
        mode = d.mode === 'element' ? 'element' : 'block';
        document.documentElement.setAttribute('data-np-mode', mode);
        if (mode === 'block') selectEl(null);
        break;
      case 'np:replace-el':
        if (selectedEl) {
          var tpl = document.createElement('template');
          tpl.innerHTML = d.html;
          var next = tpl.content.firstElementChild;
          w = wrapperOf(selectedEl);
          if (next && w) { selectedEl.replaceWith(next); selectedEl = null; selectEl(next); commit(w, 'retouch'); }
        }
        break;
      case 'np:set-img':
        if (selectedEl && selectedEl.tagName === 'IMG') {
          selectedEl.setAttribute('src', d.src);
          if (d.credit) selectedEl.setAttribute('data-photo-credit', d.credit);
          selectedEl.removeAttribute('srcset');
          commit(wrapperOf(selectedEl), 'image');
          selectEl(selectedEl);
        }
        break;
      case 'np:parent':
        if (selectedEl && selectedEl.parentElement && !selectedEl.parentElement.hasAttribute('data-np-block')) selectEl(selectedEl.parentElement);
        break;
      case 'np:delete-el':
        if (selectedEl) { w = wrapperOf(selectedEl); var el = selectedEl; selectEl(null); el.remove(); commit(w, 'edit'); }
        break;
      case 'np:audit': audit(); break;
    }
  });

  document.documentElement.setAttribute('data-np-mode', mode);
  post({ type: 'np:ready' });
})();
