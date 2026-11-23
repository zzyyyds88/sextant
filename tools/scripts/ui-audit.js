// Measures the WebUI against the craft floor: contrast, touch targets, overflow.
//
// Run against a live daemon or the preview server:
//   node tools/scripts/ui-audit.js http://127.0.0.1:8099
//
// It prints one JSON line per viewport and exits non-zero if any check fails, so
// it can gate a change rather than just describe one.
const { chromium } = require('playwright');

const BASE = process.argv[2] || 'http://127.0.0.1:8099';
const EXEC = process.env.CHROME_PATH || undefined;

const VIEWPORTS = [
  { name: 'desktop-dark', width: 1280, height: 900, colorScheme: 'dark' },
  { name: 'desktop-light', width: 1280, height: 900, colorScheme: 'light' },
  { name: 'phone-dark', width: 412, height: 900, colorScheme: 'dark' },
];

function luminance(rgb) {
  const m = rgb.match(/\d+/g).map(Number).slice(0, 3).map(v => {
    v /= 255;
    return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4);
  });
  return 0.2126 * m[0] + 0.7152 * m[1] + 0.0722 * m[2];
}

(async () => {
  const browser = await chromium.launch(EXEC ? { executablePath: EXEC, args: ['--no-sandbox'] } : { args: ['--no-sandbox'] });
  let failures = 0;
  for (const vp of VIEWPORTS) {
    const ctx = await browser.newContext({
      viewport: { width: vp.width, height: vp.height },
      colorScheme: vp.colorScheme,
    });
    const page = await ctx.newPage();
    await page.goto(BASE + '/index.html', { waitUntil: 'domcontentloaded' });
    if (await page.locator('#tokenInput').isVisible()) {
      await page.fill('#tokenInput', process.env.CHOPIN_TOKEN || 'cpv1_preview');
      await page.click('#loginBtn');
      await page.waitForTimeout(2000);
    }
    const result = await page.evaluate(() => {
      const out = { lowContrast: [], smallTargets: [], overflow: [] };
      function bgOf(el) {
        let e = el;
        while (e) {
          const b = getComputedStyle(e).backgroundColor;
          if (b && b !== 'rgba(0, 0, 0, 0)' && b !== 'transparent') return b;
          e = e.parentElement;
        }
        return 'rgb(255,255,255)';
      }
      document.querySelectorAll('body *').forEach(el => {
        const cs = getComputedStyle(el);
        if (cs.visibility === 'hidden' || cs.display === 'none') return;
        const r = el.getBoundingClientRect();
        if (r.width < 2 || r.height < 2) return;
        if (el.children.length === 0) {
          const t = (el.textContent || '').trim();
          if (t) {
            const m = el.tagName === 'TEXTAREA' || el.tagName === 'INPUT'
              ? (el.placeholder || '') : t;
            if (m) {
              const c = el.tagName === 'TEXTAREA' || el.tagName === 'INPUT'
                ? (cs.placeholderColor || cs.color) : cs.color;
              out.lowContrast.push({ text: t.slice(0, 24), color: c, bg: bgOf(el) });
            }
          }
        }
        if (el.tagName === 'BUTTON' && r.height < 36) {
          out.smallTargets.push({ text: el.textContent.trim().slice(0, 16), h: Math.round(r.height) });
        }
        if (el.scrollWidth > el.clientWidth + 1 && el.children.length === 0) {
          out.overflow.push(el.textContent.trim().slice(0, 16));
        }
      });
      return out;
    });
    const tooLow = result.lowContrast
      .map(x => {
        const l1 = luminanceOf(x.color), l2 = luminanceOf(x.bg);
        return { ...x, ratio: +((Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05)).toFixed(2) };
      })
      .filter(x => x.ratio < 4.5);
    function luminanceOf(c) {
      const m = c.match(/\d+/g);
      if (!m) return 1;
      return luminance(c);
    }
    const line = {
      viewport: vp.name,
      texts: result.lowContrast.length,
      lowContrast: tooLow,
      smallTargets: result.smallTargets,
      overflow: result.overflow,
    };
    const bad = tooLow.length + result.smallTargets.length + result.overflow.length;
    if (bad) failures++;
    console.log(JSON.stringify(line));
    await ctx.close();
  }
  await browser.close();
  if (failures) {
    console.error('ui-audit: FAILED (' + failures + ' viewport(s))');
    process.exit(1);
  }
  console.log('ui-audit: OK');
})();
