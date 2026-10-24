// Harnais CDP minimal : chromium headless piloté par le protocole DevTools.
// Usage (un scénario) :
//   import { harness } from './lib.mjs';
//   const h = await harness({ width: 1440 });
//   try { …h.check('nom', condition, info)… } catch (e) { h.fail(e) } finally { h.done() }
// Arguments : node <scénario>.mjs <BASE> <dossier de captures>
import { spawn } from 'node:child_process';
import fs from 'node:fs';

export const BASE = process.argv[2] || 'http://127.0.0.1:18200';
export const OUT = process.argv[3] || '.';
export const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

export async function harness({ width = 1440, height = 900 } = {}) {
  fs.mkdirSync(OUT, { recursive: true });
  const port = 9333 + Math.floor(Math.random() * 500);
  const profile = fs.mkdtempSync(`${OUT}/profile-`);
  const chrome = spawn('chromium', ['--headless=new', '--no-sandbox', '--disable-gpu', `--remote-debugging-port=${port}`,
    `--window-size=${width},${height}`, `--user-data-dir=${profile}`, 'about:blank'], { stdio: 'ignore' });

  let ws, id = 0, fails = 0;
  const pending = new Map();
  const events = [];
  for (let i = 0; !ws && i < 50; i++) {
    try {
      const page = (await (await fetch(`http://127.0.0.1:${port}/json`)).json()).find((t) => t.type === 'page');
      const sock = new WebSocket(page.webSocketDebuggerUrl);
      await new Promise((res, rej) => { sock.onopen = res; sock.onerror = rej; });
      sock.onmessage = (m) => {
        const msg = JSON.parse(m.data);
        if (msg.id && pending.has(msg.id)) { pending.get(msg.id)(msg); pending.delete(msg.id); }
        else if (msg.method) events.push(msg);
      };
      ws = sock;
    } catch { await sleep(200); }
  }
  if (!ws) { chrome.kill(); throw new Error('chromium injoignable'); }

  const send = (method, params = {}) => new Promise((res) => { const i = ++id; pending.set(i, res); ws.send(JSON.stringify({ id: i, method, params })); });
  const h = {
    send, events, sleep,
    async ev(expr) {
      const r = await send('Runtime.evaluate', { expression: expr, awaitPromise: true, returnByValue: true });
      if (r.result.exceptionDetails) throw new Error('eval: ' + JSON.stringify(r.result.exceptionDetails).slice(0, 400));
      return r.result.result.value;
    },
    async nav(url) { await send('Page.navigate', { url: BASE + url }); await sleep(1500); },
    async shot(name) {
      const r = await send('Page.captureScreenshot', { format: 'png' });
      fs.writeFileSync(`${OUT}/${name}.png`, Buffer.from(r.result.data, 'base64'));
    },
    async key(k, code, extra = {}) {
      for (const type of ['keyDown', 'keyUp']) {
        await send('Input.dispatchKeyEvent', { type, key: k, code, windowsVirtualKeyCode: extra.vk, text: type === 'keyDown' ? extra.text : undefined, modifiers: extra.mod || 0 });
      }
      await sleep(150);
    },
    async type(text) { await send('Input.insertText', { text }); await sleep(250); },
    async viewport(w, hh = height) { await send('Emulation.setDeviceMetricsOverride', { width: w, height: hh, deviceScaleFactor: 1, mobile: false }); await sleep(300); },
    check(name, ok, info = '') { console.log(`${ok ? 'PASS' : 'FAIL'} ${name}${info ? ' — ' + info : ''}`); if (!ok) fails++; },
    async login(email, password) {
      await h.nav('/login');
      await h.ev(`fetch('/api/logout',{method:'POST'}).then(()=>fetch('/api/login',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({email:${JSON.stringify(email)},password:${JSON.stringify(password)}})})).then(r=>r.status)`);
    },
    noExceptions() {
      const errs = events.filter((e) => e.method === 'Runtime.exceptionThrown').map((e) => e.params.exceptionDetails.exception?.description?.split('\n')[0]);
      h.check('aucune exception JS', errs.length === 0, errs.join(' | '));
    },
    fail(e) { console.log('FAIL harness — ' + e.message); fails++; },
    done() {
      chrome.kill();
      fs.rmSync(profile, { recursive: true, force: true });
      console.log(fails ? `${fails} échec(s)` : 'tout est vert');
      process.exit(fails ? 1 : 0);
    },
  };
  await send('Page.enable');
  await send('Runtime.enable');
  await h.viewport(width, height);
  return h;
}
