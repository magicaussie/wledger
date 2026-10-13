const assert = require('node:assert/strict');
const { chromium } = require('playwright');
const { spawn, spawnSync } = require('node:child_process');
const fs = require('node:fs');
const net = require('node:net');
const os = require('node:os');
const path = require('node:path');

const scannerMock = `
  window.__barcodeScannerStats = { constructors: 0, renders: 0, clears: 0 };
  window.Html5QrcodeScanner = class {
    constructor() { window.__barcodeScannerStats.constructors++; }
    render(onSuccess) { this.onSuccess = onSuccess; window.__barcodeScannerStats.renders++; }
    async clear() { window.__barcodeScannerStats.clears++; }
  };
`;

(async () => {
  const repoRoot = path.resolve(__dirname, '../..');
  const scratchDir = fs.mkdtempSync(path.join(os.tmpdir(), 'wledger-scanner-browser-'));
  let server;
  let browser;
  try {
    fs.symlinkSync(path.join(repoRoot, 'web'), path.join(scratchDir, 'web'));
    fs.symlinkSync(path.join(repoRoot, 'locales'), path.join(scratchDir, 'locales'));

    const probe = net.createServer();
    await new Promise((resolve, reject) => probe.once('error', reject).listen(0, '127.0.0.1', resolve));
    const port = probe.address().port;
    await new Promise(resolve => probe.close(resolve));
    const baseURL = `http://127.0.0.1:${port}`;
    const binaryPath = path.join(scratchDir, 'wledger');
    const build = spawnSync(process.env.GO_BIN || 'go', ['build', '-tags', 'fts5', '-o', binaryPath, './cmd/server'], {
      cwd: repoRoot,
      env: { ...process.env, CGO_ENABLED: '1' },
      encoding: 'utf8',
    });
    if (build.status !== 0) throw new Error(`server build failed: ${build.stderr}`);
    server = spawn(binaryPath, [], {
      cwd: scratchDir,
      env: { ...process.env, WLEDGER_INSECURE_COOKIES: '1', WLEDGER_PORT: String(port) },
      stdio: 'ignore',
    });
    let ready = false;
    for (let attempt = 0; attempt < 100; attempt++) {
      try {
        const response = await fetch(`${baseURL}/setup`);
        if (response.ok) { ready = true; break; }
      } catch {}
      if (server.exitCode !== null) throw new Error(`disposable server exited with ${server.exitCode}`);
      await new Promise(resolve => setTimeout(resolve, 100));
    }
    if (!ready) throw new Error('disposable server did not become ready');

    browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH || '/bin/chromium', headless: true, args: ['--no-sandbox'] });
    const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
    const pageErrors = [];
    const consoleErrors = [];
    page.on('pageerror', error => pageErrors.push(error.message));
    page.on('console', message => { if (message.type() === 'error') consoleErrors.push(message.text()); });
    await page.route('https://unpkg.com/html5-qrcode**', route => route.fulfill({
      status: 200,
      contentType: 'application/javascript',
      body: scannerMock,
    }));

    await page.goto(`${baseURL}/login`);
    if (new URL(page.url()).pathname === '/setup') {
      await page.locator('input[name=email]').fill('scanner-admin@example.invalid');
      await page.locator('input[name=password]').fill('safe-password-123');
      await page.locator('form button').click();
      await page.waitForURL(`${baseURL}/`);
    } else {
      await page.locator('input[name=email]').fill('scanner-admin@example.invalid');
      await page.locator('input[name=password]').fill('safe-password-123');
      await page.locator('form button').click();
      await page.waitForURL(`${baseURL}/`);
    }

    await page.goto(`${baseURL}/parts/new`);
    await assertGlobalScannerLoadedOnce(page);
    const firstPartName = `Scanner test part ${Date.now()}`;
    await page.locator('form[action="/parts"] input[name=name]').fill(firstPartName);
    await page.getByRole('button', { name: /Create Part/ }).click();
    await page.waitForURL(/\/parts\/\d+$/);
    const partID = Number(page.url().match(/\/parts\/(\d+)$/)[1]);

    await page.goto(`${baseURL}/parts/${partID}/edit`);
    await assertGlobalScannerLoadedOnce(page);
    await openCloseAndReopenScanner(page, 'barcode_input');
    let stats = await page.evaluate(() => window.__barcodeScannerStats);
    assert.deepEqual(stats, { constructors: 1, renders: 2, clears: 2 }, 'reopening a camera modal must reuse the one scanner instance');

    const editForm = page.locator(`form[action="/parts/${partID}/update"]`);
    await editForm.locator('input[name=name]').fill(`${firstPartName} edited`);
    await editForm.locator('input[name=name]').press('Tab');
    const htmxResponse = page.waitForResponse(response => response.url() === `${baseURL}/parts/new` && response.request().headers()['hx-request'] === 'true');
    await page.evaluate(() => window.htmx.ajax('GET', '/parts/new', { target: '#main-content', swap: 'innerHTML' }));
    await htmxResponse;
    await page.getByText('Add New Part').waitFor();
    await assertGlobalScannerLoadedOnce(page);
    await page.evaluate(() => {
      window.startGlobalScanner('barcode_input');
      window.startGlobalScanner('barcode_input');
    });
    await page.waitForFunction(() => window.__barcodeScannerStats.renders === 3);
    stats = await page.evaluate(() => window.__barcodeScannerStats);
    assert.deepEqual(stats, { constructors: 1, renders: 3, clears: 2 }, 'repeat modal initialization after HTMX navigation must not redeclare or construct another scanner');
    await page.locator('#global_scanner_modal').locator('button', { hasText: 'Cancel' }).click();
    await page.waitForFunction(() => window.__barcodeScannerStats.clears === 3);

    const duplicateScannerErrors = [...pageErrors, ...consoleErrors].filter(message => message.includes("Identifier 'html5QrcodeScanner' has already been declared"));
    assert.deepEqual(duplicateScannerErrors, []);
    assert.deepEqual(pageErrors, []);
    console.log(JSON.stringify({ result: 'PASS', partID, constructors: stats.constructors, renders: stats.renders, clears: stats.clears, modalCount: 1, barcodeScriptCount: 1, pageErrors, duplicateScannerErrors }));
  } finally {
    if (browser) await browser.close();
    if (server) { server.kill('SIGTERM'); await new Promise(resolve => server.once('exit', resolve)); }
    fs.rmSync(scratchDir, { recursive: true, force: true });
  }
})().catch(error => { console.error(error); process.exit(1); });

async function assertGlobalScannerLoadedOnce(page) {
  assert.equal(await page.locator('#global_scanner_modal').count(), 1, 'the base layout owns exactly one scanner modal');
  assert.equal(await page.locator('script[src="/static/js/barcode_scanner.js"]').count(), 1, 'the scanner controller script must appear once');
}

async function openCloseAndReopenScanner(page, inputID) {
  const openButton = page.locator(`button[onclick="startGlobalScanner('${inputID}')"]`);
  const modal = page.locator('#global_scanner_modal');
  await openButton.click();
  await page.waitForFunction(() => window.__barcodeScannerStats.renders === 1);
  assert.equal(await modal.evaluate(element => element.open), true);
  await modal.locator('button', { hasText: 'Cancel' }).click();
  await page.waitForFunction(() => window.__barcodeScannerStats.clears === 1);
  assert.equal(await modal.evaluate(element => element.open), false);
  await openButton.click();
  await page.waitForFunction(() => window.__barcodeScannerStats.renders === 2);
  await page.evaluate(() => window.startGlobalScanner('barcode_input'));
  await page.waitForTimeout(50);
  assert.equal(await page.evaluate(() => window.__barcodeScannerStats.renders), 2, 'repeated open must not render an already running camera scanner twice');
  await modal.locator('button', { hasText: 'Cancel' }).click();
  await page.waitForFunction(() => window.__barcodeScannerStats.clears === 2);
}
