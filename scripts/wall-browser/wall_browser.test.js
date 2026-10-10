// Playwright browser regression test for the WLEDger dashboard Wall.
//
// It drives the real router served on loopback by the companion harness
// (scripts/wall-browser/main.go) against a disposable, synthetic Wall fixture.
// It is an optional, manual regression: it needs Node, Playwright and a running
// harness, so it is not part of the Go CI suite.
//
// Usage:
//   go run -tags fts5 ./scripts/wall-browser -port 18080
//   DEMO_URL=http://127.0.0.1:18080 node scripts/wall-browser/wall_browser.test.js
//
// Set NODE_PATH to a Playwright install if it is not resolvable locally, e.g.
//   NODE_PATH=$(npm root -g) node scripts/wall-browser/wall_browser.test.js
//
// Screenshots and results.json are written to $SHOT_DIR (default ./screenshots).
const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

const BASE = process.env.DEMO_URL || 'http://127.0.0.1:18080';
const OUT = process.env.SHOT_DIR || path.join(__dirname, 'screenshots');
fs.mkdirSync(OUT, { recursive: true });

const results = [];
function check(name, ok, detail) {
  results.push({ name, ok: !!ok, detail: detail || '' });
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? '  -- ' + detail : ''}`);
}

const openDialogs = (page) => page.locator('dialog[open]').count();
const waitClosed = (page) =>
  page.waitForFunction(() => document.querySelectorAll('dialog[open]').length === 0, null, { timeout: 5000 });

(async () => {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await context.newPage();
  const pageErrors = [];
  page.on('pageerror', (e) => pageErrors.push(String(e && e.message)));

  // ---------------------------------------------------------------- login
  await page.goto(BASE + '/login', { waitUntil: 'domcontentloaded' });
  await page.fill('input[name="email"]', 'demo@example.test');
  await page.fill('input[name="password"]', 'demo-password-123');
  await Promise.all([
    page.waitForURL(BASE + '/', { timeout: 20000 }),
    page.click('form[action="/login"] button'),
  ]);
  check('login redirects to dashboard', page.url() === BASE + '/', page.url());

  await page.waitForFunction(() => window.Alpine !== undefined, null, { timeout: 20000 });
  await page.waitForSelector('text=Demo Wall', { timeout: 20000 });

  // ------------------------------------------------------- wall rendering
  const bodyText = await page.textContent('body');
  check('wall name rendered', bodyText.includes('Demo Wall'));
  check('container A rendered', bodyText.includes('Demo Drawer A'));
  check('container B rendered', bodyText.includes('Demo Drawer B'));
  check('representative bin rendered', bodyText.includes('R1C1'));

  const cardCount = await page.locator('button[aria-haspopup="dialog"]').count();
  check('two container cards', cardCount === 2, 'count=' + cardCount);

  const containerDialogs = page.locator('dialog.modal', { hasText: 'Demo Drawer' });
  check('two scoped container dialogs', (await containerDialogs.count()) === 2, 'count=' + (await containerDialogs.count()));

  const dupIds = await page.evaluate(() => {
    const seen = {}, dups = [];
    document.querySelectorAll('[id]').forEach((el) => {
      if (seen[el.id]) dups.push(el.id); else seen[el.id] = true;
    });
    return dups;
  });
  check('no duplicate element ids', dupIds.length === 0, dupIds.join(','));

  const cardAria = await page.locator('button[aria-haspopup="dialog"]').first().getAttribute('aria-label');
  check('card has localized OpenContainer aria-label', /^Open container: /.test(cardAria || ''), cardAria);

  await page.screenshot({ path: path.join(OUT, '01-dashboard-desktop.png'), fullPage: true });

  // ------------------------------------------------- modal open via click
  const cardA = page.getByRole('button', { name: /Demo Drawer A/ });
  await cardA.click();
  await page.waitForSelector('dialog[open]', { timeout: 5000 });
  check('card click opens exactly one modal', (await openDialogs(page)) === 1, 'open=' + (await openDialogs(page)));
  const modalText = await page.locator('dialog[open]').textContent();
  check('opened modal shows the container bins', modalText.includes('R1C1'));
  await page.waitForTimeout(500); // let the DaisyUI open transition settle
  await page.screenshot({ path: path.join(OUT, '02-modal-open.png') });

  // ------------------------------------------------- close via close button
  await page.click('dialog[open] button[aria-label="Close"]');
  await waitClosed(page);
  check('close button closes modal', (await openDialogs(page)) === 0);

  // ---------------------------------------------------- close via backdrop
  await cardA.click();
  await page.waitForSelector('dialog[open]', { timeout: 5000 });
  // Click a corner of the full-viewport backdrop, away from the centred modal-box.
  await page.mouse.click(20, 20);
  await waitClosed(page);
  check('backdrop click closes modal', (await openDialogs(page)) === 0);

  // -------------------------------------------------- keyboard accessibility
  // Reload for a clean focus order (a user landing on the page and tabbing).
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.waitForFunction(() => window.Alpine !== undefined, null, { timeout: 20000 });
  await page.waitForSelector('text=Demo Wall', { timeout: 20000 });
  await page.evaluate(() => document.activeElement && document.activeElement.blur());
  let focused = false;
  const seenFocus = [];
  for (let i = 0; i < 60; i++) {
    await page.keyboard.press('Tab');
    const info = await page.evaluate(() => {
      const el = document.activeElement;
      return el ? { tag: el.tagName, label: el.getAttribute && el.getAttribute('aria-label') } : null;
    });
    seenFocus.push(info ? (info.label || info.tag) : 'null');
    if (info && info.tag === 'BUTTON' && info.label && info.label.includes('Demo Drawer A')) { focused = true; break; }
  }
  check('card button reachable by Tab', focused, focused ? '' : seenFocus.slice(0, 8).join(' | '));
  if (focused) {
    await page.keyboard.press('Enter');
    await page.waitForSelector('dialog[open]', { timeout: 5000 });
    check('Enter on focused card opens modal', (await openDialogs(page)) === 1);
    await page.keyboard.press('Escape');
    await waitClosed(page);
    check('Escape closes modal', (await openDialogs(page)) === 0);
  }

  // --------------------------------------------------- empty container
  const cardB = page.getByRole('button', { name: /Demo Drawer B/ });
  await cardB.click();
  await page.waitForSelector('dialog[open]', { timeout: 5000 });
  const emptyText = await page.locator('dialog[open]').textContent();
  check('empty container shows localized empty state', emptyText.includes('No bins mapped to this container.'), emptyText.slice(0, 120));
  const emptyBinLinks = await page.locator('dialog[open] a[href^="/parts?bin="]').count();
  check('empty container renders no bin links', emptyBinLinks === 0, 'links=' + emptyBinLinks);
  await page.waitForTimeout(500);
  await page.screenshot({ path: path.join(OUT, '04-empty-container.png') });
  await page.keyboard.press('Escape');
  await waitClosed(page);

  // ------------------------------------------------------------- scrolling
  // A short viewport forces the tall 8x8 grid to overflow the modal-box.
  await page.setViewportSize({ width: 1280, height: 600 });
  await cardA.click();
  await page.waitForSelector('dialog[open]', { timeout: 5000 });
  const scrollInfo = await page.evaluate(() => {
    const box = document.querySelector('dialog[open] .modal-box');
    if (!box) return null;
    const before = box.scrollTop;
    box.scrollTop = box.scrollHeight;
    return { scrollHeight: box.scrollHeight, clientHeight: box.clientHeight, before, after: box.scrollTop };
  });
  check('modal-box content overflows (tall grid)', scrollInfo && scrollInfo.scrollHeight > scrollInfo.clientHeight + 5, JSON.stringify(scrollInfo));
  check('modal-box scrolls vertically', scrollInfo && scrollInfo.after > scrollInfo.before, JSON.stringify(scrollInfo));
  await page.waitForTimeout(500);
  await page.screenshot({ path: path.join(OUT, '03-modal-scrolled.png') });
  await page.keyboard.press('Escape');
  await waitClosed(page);
  await page.setViewportSize({ width: 1440, height: 900 });

  // ------------------------------------------------------- mobile viewport
  await page.setViewportSize({ width: 390, height: 844 });
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.waitForFunction(() => window.Alpine !== undefined, null, { timeout: 20000 });
  await page.waitForSelector('text=Demo Wall', { timeout: 20000 });
  await page.screenshot({ path: path.join(OUT, '05-mobile-dashboard.png'), fullPage: true });

  const mobileCard = page.getByRole('button', { name: /Demo Drawer A/ });
  await mobileCard.click();
  await page.waitForSelector('dialog[open]', { timeout: 5000 });
  await page.waitForTimeout(500); // let the DaisyUI open transition settle

  // Assert real geometry, not just the class: a bottom sheet must have its
  // modal-box bottom edge flush with the viewport bottom, and the dialog must
  // cover the viewport. A centred (modal-middle) box would leave a gap below.
  const mobileGeom = await page.evaluate(() => {
    const d = document.querySelector('dialog[open]');
    const box = d && d.querySelector('.modal-box');
    if (!d || !box) return null;
    const dr = d.getBoundingClientRect();
    const br = box.getBoundingClientRect();
    return {
      hasClass: d.classList.contains('modal-bottom'),
      vw: window.innerWidth,
      vh: window.innerHeight,
      dialogTop: dr.top,
      dialogBottom: dr.bottom,
      dialogWidth: dr.width,
      boxTop: br.top,
      boxBottom: br.bottom,
      boxWidth: br.width,
      boxHeight: br.height,
    };
  });
  check('mobile modal has modal-bottom class', mobileGeom && mobileGeom.hasClass, JSON.stringify(mobileGeom));
  check(
    'mobile modal-box is bottom-anchored (geometry)',
    mobileGeom && mobileGeom.boxHeight > 0 && mobileGeom.vh - mobileGeom.boxBottom <= 8,
    mobileGeom ? `vh=${mobileGeom.vh} boxBottom=${mobileGeom.boxBottom} gap=${mobileGeom.vh - mobileGeom.boxBottom}` : 'no geometry'
  );
  check(
    'mobile modal-box top gap exceeds bottom gap (bottom sheet)',
    mobileGeom && (mobileGeom.boxTop - mobileGeom.dialogTop) > (mobileGeom.dialogBottom - mobileGeom.boxBottom) + 20,
    mobileGeom ? `topGap=${(mobileGeom.boxTop - mobileGeom.dialogTop).toFixed(1)} bottomGap=${(mobileGeom.dialogBottom - mobileGeom.boxBottom).toFixed(1)}` : 'no geometry'
  );
  check(
    'mobile dialog covers the viewport',
    mobileGeom && mobileGeom.dialogTop <= 1 && mobileGeom.dialogBottom >= mobileGeom.vh - 1 && mobileGeom.dialogWidth >= mobileGeom.vw - 1,
    mobileGeom ? `dialog=${mobileGeom.dialogTop}..${mobileGeom.dialogBottom} w=${mobileGeom.dialogWidth} vw=${mobileGeom.vw}` : 'no geometry'
  );
  await page.screenshot({ path: path.join(OUT, '06-mobile-modal.png') });
  await page.keyboard.press('Escape');
  await waitClosed(page);

  // Contrast: at desktop width the same modal is centred (modal-middle), so its
  // box bottom must NOT be flush with the viewport bottom.
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.waitForFunction(() => window.Alpine !== undefined, null, { timeout: 20000 });
  await page.waitForSelector('text=Demo Wall', { timeout: 20000 });
  await page.getByRole('button', { name: /Demo Drawer A/ }).click();
  await page.waitForSelector('dialog[open]', { timeout: 5000 });
  await page.waitForTimeout(500);
  const desktopGeom = await page.evaluate(() => {
    const d = document.querySelector('dialog[open]');
    const box = d && d.querySelector('.modal-box');
    if (!d || !box) return null;
    const br = box.getBoundingClientRect();
    return { vh: window.innerHeight, boxTop: br.top, boxBottom: br.bottom };
  });
  check(
    'desktop modal-box is centred, not bottom-anchored',
    desktopGeom && desktopGeom.vh - desktopGeom.boxBottom > 20 && desktopGeom.boxTop > 20,
    desktopGeom ? `vh=${desktopGeom.vh} boxTop=${desktopGeom.boxTop} boxBottom=${desktopGeom.boxBottom}` : 'no geometry'
  );
  await page.keyboard.press('Escape');
  await waitClosed(page);

  check('no uncaught page errors', pageErrors.length === 0, pageErrors.join(' | ').slice(0, 300));

  await browser.close();

  const failed = results.filter((r) => !r.ok);
  console.log(`\nSUMMARY: ${results.length - failed.length}/${results.length} passed`);
  fs.writeFileSync(path.join(OUT, 'results.json'), JSON.stringify(results, null, 2));
  process.exit(failed.length === 0 ? 0 : 1);
})().catch((e) => { console.error('FATAL', e); process.exit(2); });
