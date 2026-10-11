const assert = require('node:assert/strict');
const { chromium } = require('playwright');
const { spawn, spawnSync } = require('node:child_process');
const path = require('node:path');
const fs = require('node:fs');
const os = require('node:os');
const net = require('node:net');

let baseURL = process.env.WLEDGER_URL || 'http://127.0.0.1:8080';
const email = process.env.WLEDGER_TEST_EMAIL || 'test@example.com';
const password = process.env.WLEDGER_TEST_PASSWORD || 'password123';
const executablePath = process.env.CHROMIUM_PATH || '/bin/chromium';

(async () => {
  if (process.env.WLEDGER_URL && process.env.WLEDGER_START_SERVER !== '0') throw new Error('set WLEDGER_START_SERVER=0 when targeting an external instance');
  let server;
  let scratchDir;
  if (process.env.WLEDGER_START_SERVER !== '0') {
    const repoRoot = path.resolve(__dirname, '../..');
    let serverCwd;
    let serverBinary;
    let port;
    if (!process.env.WLEDGER_URL) {
      const probe = net.createServer();
      await new Promise((resolve, reject) => probe.once('error', reject).listen(0, '127.0.0.1', resolve));
      port = probe.address().port;
      await new Promise(resolve => probe.close(resolve));
      baseURL = `http://127.0.0.1:${port}`;
    }
    if (process.env.WLEDGER_SERVER_BINARY) {
      serverCwd = path.resolve(process.env.WLEDGER_SERVER_CWD || path.dirname(process.env.WLEDGER_SERVER_BINARY));
      serverBinary = path.resolve(process.env.WLEDGER_SERVER_BINARY);
    } else {
      scratchDir = fs.mkdtempSync(path.join(os.tmpdir(), 'wledger-layout-browser-'));
      serverCwd = scratchDir;
      serverBinary = path.join(scratchDir, 'wledger');
      fs.symlinkSync(path.join(repoRoot, 'web'), path.join(scratchDir, 'web'));
      fs.symlinkSync(path.join(repoRoot, 'locales'), path.join(scratchDir, 'locales'));
      const build = spawnSync(process.env.GO_BIN || 'go', ['build', '-tags', 'fts5', '-o', serverBinary, './cmd/server'], { cwd: repoRoot, env: { ...process.env, CGO_ENABLED: '1' }, encoding: 'utf8' });
      if (build.status !== 0) throw new Error(`failed to build browser-test server: ${build.stderr}`);
    }
    server = spawn(serverBinary, [], { cwd: serverCwd, env: { ...process.env, WLEDGER_INSECURE_COOKIES: '1', ...(port ? { WLEDGER_PORT: String(port) } : {}) }, stdio: 'ignore' });
    let ready = false;
    for (let attempt = 0; attempt < 80; attempt++) {
      try { const response = await fetch(baseURL + '/login'); if (response.ok) { ready = true; break; } } catch {}
      await new Promise(resolve => setTimeout(resolve, 250));
    }
    if (!ready) throw new Error('disposable WLEDger server did not become ready');
    const dbPath = path.join(serverCwd, 'data', 'wledger.db');
    const seed = spawnSync('python3', ['-c', `import sqlite3,sys; c=sqlite3.connect(sys.argv[1]); c.execute("DELETE FROM bin_drawer_attachments WHERE bin_id=99001"); c.execute("INSERT INTO bins(id,name,container_id,led_index) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name", (99001, "Browser spare bin", None, None)); c.execute("INSERT INTO parts(id,name) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name", (99002, "Browser test part")); c.execute("INSERT INTO part_assignments(part_id,bin_id,quantity) VALUES(?,?,?) ON CONFLICT(part_id,bin_id) DO UPDATE SET quantity=excluded.quantity", (99002, 99001, 4)); c.commit(); c.close()`, dbPath], { encoding: 'utf8' });
    if (seed.status !== 0) throw new Error(`failed to seed disposable browser bin: ${seed.stderr}`);
  }
  const browser = await chromium.launch({ executablePath, headless: true, args: ['--no-sandbox'] });
  const page = await browser.newPage();
  const pageErrors = [];
  const consoleErrors = [];
  const layoutPosts = [];
  const lifecyclePosts = [];
  page.on('pageerror', error => pageErrors.push(error.message));
  page.on('console', message => { if (message.type() === 'error') consoleErrors.push(`${page.url()}: ${message.text()}`); });
  page.on('request', request => {
    if (request.method() === 'POST' && /\/storage\/cabinets\/\d+\/edit/.test(request.url())) {
      layoutPosts.push({ url: request.url(), body: request.postData() || '' });
    }
    if (request.method() === 'POST' && request.url().includes('/storage/cabinets/')) lifecyclePosts.push({ url: request.url(), body: request.postData() || '' });
  });
  page.on('dialog', dialog => dialog.accept());

  try {
    await page.goto(`${baseURL}/login`);
    let authenticated = false;
    if (new URL(page.url()).pathname === '/setup') {
      await page.locator('input[name=email]').fill(email);
      await page.locator('input[name=password]').fill(password);
      await page.locator('form button').click();
      await page.waitForURL(`${baseURL}/`);
      authenticated = true;
    }
    if (!authenticated) {
      await page.locator('input[name=email]').fill(email);
      await page.locator('input[name=password]').fill(password);
      await page.locator('form button').click();
      await page.waitForURL(`${baseURL}/`);
    }

    await page.goto(`${baseURL}/storage/cabinets`);
    const cabinetName = `Browser acceptance ${Date.now()}`;
    await page.locator('input[name=name]').fill(cabinetName);
    await page.getByRole('button', { name: 'Create cabinet' }).click();
    await page.waitForURL(/\/storage\/cabinets\/\d+\/edit$/);
    const cabinetID = page.url().match(/cabinets\/(\d+)\/edit$/)[1];

    await page.getByRole('button', { name: 'Add drawer' }).click();
    const drawerLabel = page.locator('input[x-model="drawer.label"]').first();
    await drawerLabel.waitFor({ state: 'visible' });
    await drawerLabel.fill('Browser drawer');
    const binSelect = page.locator('select[x-model="drawer.selectedBin"]').first();
    if (await binSelect.locator('option').count() > 1) {
      await binSelect.selectOption('99001');
      await page.getByRole('button', { name: 'Add bin' }).click();
    }
    await page.getByRole('button', { name: 'Save layout' }).click();
    await page.waitForURL(`${baseURL}/storage/cabinets/${cabinetID}`);
    const drawerLink = page.locator('a[href^="/storage/drawers/"]').first();
    assert.equal(await drawerLink.count(), 1);
    assert.equal(await drawerLink.evaluate(element => getComputedStyle(element).position), 'absolute');
        assert.equal(await drawerLink.evaluate(element => element.style.width), '10%');
        assert.equal(await drawerLink.evaluate(element => element.style.height), '10%');
    const drawerURL = new URL(await drawerLink.getAttribute('href'), baseURL).href;
    await page.goto(drawerURL);
    assert.equal(await page.getByRole('link', { name: 'Browser test part' }).count(), 1);
    await page.getByRole('button', { name: 'List', exact: true }).click();
    assert.equal(await page.getByRole('button', { name: 'List', exact: true }).getAttribute('aria-pressed'), 'true');
    await page.getByRole('button', { name: 'View Bin Layout' }).click();
    assert.equal(await page.getByRole('button', { name: 'View Bin Layout' }).getAttribute('aria-pressed'), 'true');
    assert.ok(await page.locator('[style*="aspect-ratio"]').count());
    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    assert.match(layoutPosts.at(-1).url, new RegExp(`/storage/cabinets/${cabinetID}/edit$`));
    const encodedForm = new URLSearchParams(layoutPosts.at(-1).body);
    const submittedLayout = JSON.parse(encodedForm.get('layout'));
    assert.equal(submittedLayout.id, Number(cabinetID));
    assert.equal(submittedLayout.drawers[0].label, 'Browser drawer');
    assert.equal(submittedLayout.drawers[0].bins[0].id, 99001);
    assert.ok(encodedForm.get('csrf_token'), 'layout save must include CSRF token');

    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    assert.equal(await page.locator('input[x-model="drawer.label"]').first().inputValue(), 'Browser drawer');
    if (await page.getByRole('button', { name: 'Remove placement' }).count()) {
      await page.getByRole('button', { name: 'Remove placement' }).click();
      await page.waitForURL(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
      assert.equal(await page.locator('select[x-model="drawer.selectedBin"] option[value="99001"]').count(), 1);
    }
    await page.locator('input[x-model="drawer.label"]').first().fill('Edited drawer');
    await page.getByRole('button', { name: 'Save layout' }).click();
    await page.waitForURL(`${baseURL}/storage/cabinets/${cabinetID}`);
    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    assert.equal(await page.locator('input[x-model="drawer.label"]').first().inputValue(), 'Edited drawer');
    await page.locator('form[action*="/storage/cabinets/"]').evaluate(form => { form.removeAttribute('x-on:submit'); form.querySelector('input[name=layout]').value = '{'; });
    await page.getByRole('button', { name: 'Save layout' }).click();
    await page.waitForURL(/layout_error=/);
    assert.match(await page.locator('[x-text="error"]').innerText(), /invalid layout data/i);
    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);

    await page.getByRole('button', { name: 'Add drawer' }).click();
    await page.getByRole('button', { name: 'Save layout' }).click();
    await page.waitForURL(/layout_error=/);
    assert.match(await page.locator('[x-text="error"]').innerText(), /overlap/i);

    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    await page.getByRole('button', { name: 'Delete cabinet' }).click();
    await page.waitForURL(/layout_error=/);
    assert.match(await page.locator('[x-text="error"]').innerText(), /drawer/i);

    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    await page.getByRole('button', { name: 'Delete', exact: true }).first().click();
    await page.waitForURL(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    assert.equal(await page.locator('input[x-model="drawer.label"]').count(), 0);
    await page.getByRole('button', { name: 'Delete cabinet' }).click();
    await page.waitForURL(`${baseURL}/storage/cabinets`);

    await page.goto(`${baseURL}/storage/cabinets`);
    const archiveName = `Browser archive ${Date.now()}`;
    await page.locator('input[name=name]').fill(archiveName);
    await page.getByRole('button', { name: 'Create cabinet' }).click();
    await page.waitForURL(/\/storage\/cabinets\/\d+\/edit$/);
    const archiveCabinetID = page.url().match(/cabinets\/(\d+)\/edit$/)[1];
    await page.getByRole('button', { name: 'Add drawer' }).click();
    await page.getByRole('button', { name: 'Save layout' }).click();
    await page.waitForURL(`${baseURL}/storage/cabinets/${archiveCabinetID}`);
    await page.goto(`${baseURL}/storage/cabinets/${archiveCabinetID}/edit`);
    await page.getByRole('button', { name: 'Archive', exact: true }).click();
    await page.waitForURL(`${baseURL}/storage/cabinets/${archiveCabinetID}/edit`);
    assert.equal(await page.locator('input[x-model="drawer.label"]').count(), 1);
    await page.getByRole('button', { name: 'Archive cabinet' }).click();
    await page.waitForURL(`${baseURL}/storage/cabinets`);
    assert.equal(await page.getByText(archiveName, { exact: true }).count(), 0);

    assert.deepEqual(pageErrors, []);
    const runtimeConsoleErrors = consoleErrors.filter(message => !message.includes("Blocked attempt to show a 'beforeunload' confirmation panel"));
    assert.deepEqual(runtimeConsoleErrors, []);
    assert.ok(lifecyclePosts.length >= 4, `expected lifecycle requests, got ${lifecyclePosts}`);
    assert.ok(lifecyclePosts.every(post => !post.url.includes('undefined')));
    assert.ok(lifecyclePosts.every(post => { const parts = new URL(post.url).pathname.split('/'); return parts[1] === 'storage' && parts[2] === 'cabinets' && /^\d+$/.test(parts[3]); }));
    assert.ok(lifecyclePosts.every(post => new URLSearchParams(post.body).get('csrf_token')), 'lifecycle POSTs must include CSRF tokens');
    console.log(JSON.stringify({ result: 'PASS', cabinetID, archiveCabinetID, saves: layoutPosts.length, lifecyclePaths: lifecyclePosts.map(post => new URL(post.url).pathname), pageErrors, runtimeConsoleErrors, browserWarnings: consoleErrors.length - runtimeConsoleErrors.length }));
  } finally {
    await browser.close();
    if (server) {
      server.kill('SIGTERM');
      await new Promise(resolve => server.once('exit', resolve));
    }
    if (scratchDir) fs.rmSync(scratchDir, { recursive: true, force: true });
  }
})().catch(error => { console.error(error); process.exit(1); });
