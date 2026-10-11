/* Studio NADAPROD — point d'entrée : charge le catalogue et le projet, relie
 * les vues au bus d'événements. */
import { S, $, api, on, emit, toast } from './core.js';
import { boot } from './store.js';
import { renderPreview, renderViewport } from './preview.js';
import { renderPalette, renderStructure, initStructure } from './structure.js';
import { renderInspector, switchTab, initPanels } from './panels.js';
import { initExport } from './exporter.js';
import { initPalette } from './palette.js';

on('render', () => { renderStructure(); renderPreview(); renderInspector(); });
on('project', () => { $('#projectLabel').textContent = S.project.name; });
on('added', () => { switchTab('generate'); $('#promptInput').focus(); });

document.addEventListener('click', (e) => {
  const vp = e.target.closest('.vp-btn');
  if (vp) { S.viewport = vp.dataset.vp; renderViewport(); }
});

(async function init() {
  await I18N.ready;
  initStructure(); initPanels(); initExport(); initPalette();
  try {
    [S.catalog, S.cfg] = await Promise.all([api('/blocks'), api('/studio/config')]);
    if (S.cfg.model) { $('#modelBadge').textContent = S.cfg.model; $('#modelBadge').classList.replace('hidden', 'flex'); }
    $('#exportCompiledHint').classList.toggle('hidden', !!S.cfg.compile);
    renderPalette();
    renderViewport();
    switchTab('generate');
    await boot();
  } catch (e) {
    console.error(e);
    toast(e.message || t('editor.backendDown'), false);
  }
  $('#boot').remove();
})();
