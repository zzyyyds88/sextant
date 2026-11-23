// Headless check of /ui: log in, claim a session, and confirm the page shows a
// live frame. It exists because "the bundle loads" is not the same claim as
// "a browser can see and drive the phone".
import { chromium } from '/tmp/node_modules/playwright-core/index.mjs';

const base = process.env.BASE;
const token = process.env.TOKEN;

const errors = [];

// Clear any session a previous run left behind: a stale holder turns the claim
// into an expected 409 and the page correctly shows read-only instead.
import https from 'node:https';

function request(method, path) {
  const url = new URL(base + path);
  return new Promise((resolve, reject) => {
    const req = https.request({
      hostname: url.hostname, port: url.port, path: url.pathname,
      method, rejectUnauthorized: false,
      headers: { Authorization: 'Bearer ' + token },
    }, (res) => {
      let body = '';
      res.on('data', (c) => { body += c; });
      res.on('end', () => resolve({ status: res.statusCode, body }));
    });
    req.on('error', reject);
    req.end();
  });
}

const listed = JSON.parse((await request('GET', '/v1/sessions')).body || '{}');
for (const s of listed.active || []) {
  await request('DELETE', '/v1/sessions/' + encodeURIComponent(s.id));
}
const browser = await chromium.launch({
  executablePath: '/root/.cache/ms-playwright/chromium-1248/chrome-linux64/chrome',
  args: ['--ignore-certificate-errors', '--no-sandbox', '--disable-gpu'],
});
const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
page.on('console', (m) => {
  const loc = m.location();
  if (m.type() === 'error') errors.push(m.text() + ' @ ' + (loc.url || '?') + ':' + loc.lineNumber);
});
page.on('response', (res) => {
  if (res.status() >= 400) errors.push('HTTP ' + res.status() + ' ' + res.url());
});
page.on('requestfailed', (req) => errors.push('REQFAIL ' + req.url()));
page.on('request', (req) => { if (process.env.VERBOSE) console.error('REQ ' + req.url()); });
page.on('response', (res) => { if (process.env.VERBOSE) console.error('RES ' + res.status() + ' ' + res.url()); });
page.on('pageerror', (e) => errors.push(String(e)));

await page.goto(base + '/ui/', { waitUntil: 'networkidle' });
await page.fill('#tokenInput', token);
await page.click('#loginBtn');
await page.waitForSelector('#appView:not([hidden])', { timeout: 15000 });

// The version label is written after /v1/health returns; wait for it rather than
// reading the element once and calling the page broken.
await page.waitForFunction(() => {
  const v = document.querySelector('#version');
  return v && v.textContent && v.textContent.trim().length > 0;
}, { timeout: 20000 }).catch(() => {});

const version = (await page.textContent('#version')) || '';

// Claim a write session from the UI itself, so the check exercises the same path
// a person would.
await page.click('#claimBtn');
await page.waitForFunction(
  () => document.querySelector('#releaseBtn') && !document.querySelector('#releaseBtn').disabled,
  { timeout: 20000 },
);

// Wait for the video element to report real frames.
const frames = await page.evaluate(async () => {
  const v = document.querySelector('#video');
  const start = Date.now();
  let last = -1;
  let advance = 0;
  while (Date.now() - start < 20000) {
    if (v && !v.hidden && v.videoWidth > 0) {
      if (v.currentTime > last) { advance++; last = v.currentTime; }
      if (advance >= 3) return { ok: true, width: v.videoWidth, height: v.videoHeight, time: v.currentTime };
    }
    await new Promise((r) => setTimeout(r, 500));
  }
  return { ok: false, width: v ? v.videoWidth : 0, height: v ? v.videoHeight : 0, advance };
});

await page.screenshot({ path: process.env.SHOT || '/tmp/ui-shot.png', fullPage: false });

// The badge reflects the claim state, so it is read after the claim, not before.
const badge = (await page.textContent('#statusBadge')) || '';
const sessionInfo = await page.textContent('#sessionInfo').catch(() => '');
if (!version.trim()) { console.error('FAIL: the page never showed the daemon version'); process.exit(1); }
console.log(JSON.stringify({ version: version.trim(), badge: badge.trim(), frames, sessionInfo: sessionInfo.trim(), consoleErrors: errors }, null, 2));

await page.click('#releaseBtn').catch(() => {});
await browser.close();

if (!frames.ok) { console.error('FAIL: no live frames in the browser'); process.exit(1); }
if (errors.length) { console.error('FAIL: console errors: ' + errors.join(' | ')); process.exit(1); }
console.log('OK: browser saw live frames');
