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
  let databasePath;
  let browser;
  try {
    if (process.env.WLEDGER_START_SERVER !== '0') {
      const repoRoot = path.resolve(__dirname, '../..');
      let serverCwd;
      let serverBinary;
      const probe = net.createServer();
      await new Promise((resolve, reject) => probe.once('error', reject).listen(0, '127.0.0.1', resolve));
      const port = probe.address().port;
      await new Promise(resolve => probe.close(resolve));
      baseURL = `http://127.0.0.1:${port}`;
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
      server = spawn(serverBinary, [], { cwd: serverCwd, env: { ...process.env, WLEDGER_INSECURE_COOKIES: '1', WLEDGER_PORT: String(port) }, stdio: 'ignore' });
      let ready = false;
      for (let attempt = 0; attempt < 100; attempt++) {
        try { const response = await fetch(baseURL + '/login'); if (response.ok) { ready = true; break; } } catch {}
        if (server.exitCode !== null) throw new Error(`disposable server exited with ${server.exitCode}`);
        await new Promise(resolve => setTimeout(resolve, 200));
      }
      if (!ready) throw new Error('disposable WLEDger server did not become ready');
      databasePath = path.join(serverCwd, 'data', 'wledger.db');
      const seed = spawnSync('python3', ['-c', `import sqlite3,sys; c=sqlite3.connect(sys.argv[1]); c.execute("INSERT INTO bins(id,name,container_id,led_index) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name", (99001, "Browser spare bin", None, None)); c.execute("INSERT INTO parts(id,name) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name", (99002, "Browser test part")); c.execute("INSERT INTO part_assignments(part_id,bin_id,quantity) VALUES(?,?,?) ON CONFLICT(part_id,bin_id) DO UPDATE SET quantity=excluded.quantity", (99002, 99001, 4)); c.commit(); c.close()`, databasePath], { encoding: 'utf8' });
      if (seed.status !== 0) throw new Error(`failed to seed disposable browser inventory: ${seed.stderr}`);
    }

    browser = await chromium.launch({ executablePath, headless: true, args: ['--no-sandbox'] });
    const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
    const pageErrors = [];
    const consoleErrors = [];
    const layoutPosts = [];
    const lifecyclePaths = [];
    page.on('pageerror', error => pageErrors.push(error.message));
    page.on('console', message => { if (message.type() === 'error') consoleErrors.push(message.text()); });
    page.on('request', request => {
      if (request.method() === 'POST' && /\/storage\/cabinets\/\d+\/edit/.test(request.url())) layoutPosts.push({ url: request.url(), body: request.postData() || '' });
      if (request.method() === 'POST' && request.url().includes('/storage/cabinets/')) lifecyclePaths.push(new URL(request.url()).pathname);
    });
    page.on('dialog', dialog => dialog.accept());

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
    const cabinetName = `Visual cabinet ${Date.now()}`;
    await page.locator('input[name=name]').fill(cabinetName);
    await page.getByRole('button', { name: 'Create cabinet' }).click();
    await page.waitForURL(/\/storage\/cabinets\/\d+\/edit$/);
    const cabinetID = page.url().match(/cabinets\/(\d+)\/edit$/)[1];
    const controllerSeed = spawnSync('python3', ['-c', `import sqlite3,sys; c=sqlite3.connect(sys.argv[1]); c.execute("INSERT INTO controllers(id,name,ip_address,port,led_count) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,led_count=excluded.led_count", (99003, "Draft Test Controller", "192.0.2.99", 80, 64)); c.execute("INSERT INTO containers(id,name,controller_id,segment_id,led_start,led_count) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET led_start=excluded.led_start,led_count=excluded.led_count", (99004, "Test electrical string", 99003, 0, 0, 64)); c.execute("UPDATE cabinets SET controller_id=? WHERE id=?", (99003, int(sys.argv[2]))); c.commit(); c.close()`, databasePath, cabinetID], { encoding: 'utf8' });
    assert.equal(controllerSeed.status, 0, controllerSeed.stderr);
    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    const canvas = page.locator('svg[aria-label="Interactive physical layout canvas"]');
    const point = async (x, y) => canvas.evaluate((svg, value) => {
      const p = svg.createSVGPoint(); p.x = value.x; p.y = value.y;
      const screen = p.matrixTransform(svg.getScreenCTM()); return { x: screen.x, y: screen.y };
    }, { x, y });
    const drag = async (x1, y1, x2, y2) => {
      const a = await point(x1, y1); const b = await point(x2, y2);
      await page.mouse.move(a.x, a.y); await page.mouse.down(); await page.mouse.move(b.x, b.y, { steps: 8 }); await page.mouse.up();
    };
    const drawerX = page.locator('input[x-model\\.number="selectedDrawer().x"]');
    const drawerY = page.locator('input[x-model\\.number="selectedDrawer().y"]');
    const drawerWidth = page.locator('input[x-model\\.number="selectedDrawer().width"]');
    const drawerHeight = page.locator('input[x-model\\.number="selectedDrawer().height"]');

    await page.getByRole('button', { name: 'Drawer', exact: true }).click();
    await drag(10, 10, 30, 30);
    assert.equal(await canvas.getAttribute('viewBox'), '0 0 100 100');
    await page.locator('input[x-model="selectedDrawer().label"]').fill('Main drawer');

assert.equal(Number(await drawerX.inputValue()), 10);
    assert.equal(Number(await drawerY.inputValue()), 10);
    assert.equal(Number(await drawerWidth.inputValue()), 20);
    assert.equal(Number(await drawerHeight.inputValue()), 20);

    await page.getByRole('button', { name: 'Select', exact: true }).click();
    await drag(20, 20, 25, 20);
    assert.equal(Number(await drawerX.inputValue()), 15);
    await drag(35, 30, 40, 35);
    assert.equal(Number(await drawerWidth.inputValue()), 25);
    assert.equal(Number(await drawerHeight.inputValue()), 25);
    await drawerWidth.fill('24'); await drawerWidth.press('Tab');
    await page.getByRole('button', { name: 'Undo' }).click();
    assert.equal(Number(await drawerWidth.inputValue()), 25);
    await page.getByRole('button', { name: 'Redo' }).click();
    assert.equal(Number(await drawerWidth.inputValue()), 24);

    const snapToggle = page.locator('input[x-model="snap"]');
    const gridToggle = page.locator('input[x-model="showGrid"]');
    await gridToggle.uncheck();
    await gridToggle.check();
    await page.getByRole('button', { name: 'Zoom in' }).click();
    assert.equal(await page.locator('text=125% zoom').count(), 1);
    await page.getByRole('button', { name: 'Reset' }).click();
    await page.getByRole('button', { name: 'Pan', exact: true }).click();
    const beforePan = await canvas.getAttribute('viewBox');
    await drag(90, 90, 85, 85);
    assert.notEqual(await canvas.getAttribute('viewBox'), beforePan);
    await page.getByRole('button', { name: 'Reset' }).click();

    await page.locator('input[x-model\\.number="gridSize"]').fill('10');
    await page.locator('input[x-model\\.number="gridSize"]').press('Tab');
    await page.getByRole('button', { name: 'Drawer', exact: true }).click();
    await drag(11, 56, 31, 76);
    assert.equal(Number(await page.locator('input[x-model\\.number="selectedDrawer().y"]').inputValue()), 60);
    const secondDrawerX = page.locator('input[x-model\\.number="selectedDrawer().x"]');
    assert.equal(Number(await secondDrawerX.inputValue()), 10);
    await snapToggle.uncheck();
    await drag(52, 56, 70, 73);
    const thirdDrawerX = page.locator('input[x-model\\.number="selectedDrawer().x"]');
    assert.ok(Math.abs(Number(await thirdDrawerX.inputValue()) - 52) < 1);

    await page.getByRole('button', { name: 'Dummy outline' }).click(); await drag(75, 60, 90, 90);
    await page.getByRole('button', { name: 'Rectangle', exact: true }).click(); await drag(75, 42, 82, 48);
    await page.getByRole('button', { name: 'Line', exact: true }).click(); await drag(75, 35, 90, 35);
    await page.getByRole('button', { name: 'Label', exact: true }).click(); const labelPoint = await point(90, 50); await page.mouse.click(labelPoint.x, labelPoint.y);
    await canvas.focus(); await page.keyboard.press('Delete');
    await page.waitForFunction(() => document.querySelectorAll('svg g[data-layout-entity="true"]').length === 6);
    await page.keyboard.press('Control+z');
    await page.waitForFunction(() => document.querySelectorAll('svg g[data-layout-entity="true"]').length === 7);
    await page.keyboard.press('Control+z');
    await page.waitForFunction(() => document.querySelectorAll('svg g[data-layout-entity="true"]').length === 6);
    await page.keyboard.press('Control+y');
    await page.waitForFunction(() => document.querySelectorAll('svg g[data-layout-entity="true"]').length === 7);
    assert.equal(await page.locator('svg g[data-layout-entity="true"]').count(), 7);
    await page.keyboard.press('Control+z');
    await page.keyboard.press('Control+y');

    await page.getByRole('button', { name: 'Drawer', exact: true }).click();
    await drag(20, 15, 30, 25);
    await page.getByRole('button', { name: 'Save layout' }).click();
    await page.waitForURL(/layout_error=/);
    assert.match(await page.locator('.text-sm.text-error[role=alert]').innerText(), /overlap/i);
    await page.getByRole('button', { name: /4\. Drawer 4/ }).click();
    await page.locator('input[x-model\\.number="selectedDrawer().x"]').fill('45');
    await page.locator('input[x-model\\.number="selectedDrawer().x"]').press('Tab');
    await page.locator('input[x-model\\.number="selectedDrawer().y"]').fill('10');
    await page.locator('input[x-model\\.number="selectedDrawer().y"]').press('Tab');
    await page.getByRole('button', { name: 'Save layout' }).click();
    await page.waitForURL(`${baseURL}/storage/cabinets/${cabinetID}`);
    const drawerLinks = page.locator('a[href^="/storage/drawers/"]');
    assert.equal(await drawerLinks.evaluateAll(links => new Set(links.map(link => link.getAttribute('href'))).size), 4);
    assert.equal(await drawerLinks.first().evaluate(element => getComputedStyle(element).position), 'absolute');

    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    await page.getByRole('button', { name: /4\. Drawer 4/ }).click();
    await page.locator('input[x-model\\.number="selectedDrawer().x"]').fill('99');
    await page.locator('input[x-model\\.number="selectedDrawer().x"]').press('Tab');
    await page.getByRole('button', { name: 'Save layout' }).click();
    await page.waitForURL(/layout_error=/);
    assert.match(await page.locator('.text-sm.text-error[role=alert]').innerText(), /outside cabinet bounds/i);
    assert.equal(Number(await page.locator('input[x-model\\.number="selectedDrawer().x"]').inputValue()), 99, 'invalid edit should be retained');
    await page.locator('input[x-model\\.number="selectedDrawer().x"]').fill('45');
    await page.locator('input[x-model\\.number="selectedDrawer().x"]').press('Tab');
    await page.getByRole('button', { name: 'Save layout' }).click();
    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}`);
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    assert.ok(await canvas.isVisible());
    assert.ok(await page.getByRole('button', { name: 'Drawer', exact: true }).isVisible());
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= 390));
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    await page.waitForSelector('svg g[role="button"][data-layout-entity="true"]');
    let editorData = JSON.parse(await page.locator('#layout-editor-data').textContent());
    assert.equal(JSON.parse(editorData.Cabinet.layout_decorations_json).length, 4);
    const payloadBeforeError = () => JSON.parse(new URLSearchParams(layoutPosts.at(-1).body).get('layout'));
    assert.equal(payloadBeforeError().drawers.length, 4);
    assert.equal(payloadBeforeError().decorations.length, 4);

    await page.getByRole('button', { name: /1\. Main drawer/ }).click();
    const ledStart = page.locator('input[x-model="ledDraftFor(selectedDrawer()).start"]');
    const ledCount = page.locator('input[x-model="ledDraftFor(selectedDrawer()).count"]');

    assert.equal(await ledStart.inputValue(), '');
    assert.equal(await ledCount.inputValue(), '');
    assert.match(await page.locator('p').filter({ hasText: 'LED Controller:' }).innerText(), /Draft Test Controller/);
    await ledStart.fill('-1'); await ledCount.fill('4');
    assert.match(await page.getByText(/LED Start must be a non-negative/).innerText(), /non-negative/);
    await page.getByRole('button', { name: 'Save LED draft' }).click();
    assert.match(await page.locator('p.text-xs.text-error[role=alert]').innerText(), /non-negative/);
    await ledStart.fill('60'); await ledCount.fill('10');
    assert.match(await page.getByText(/beyond controller/).innerText(), /beyond controller/);
    await page.getByRole('button', { name: 'Save LED draft' }).click();
    assert.match(await page.locator('p.text-xs.text-error[role=alert]').innerText(), /beyond controller/);
    await ledStart.fill('3'); await ledCount.fill('4');
    await page.getByRole('button', { name: 'Save LED draft' }).click();
    assert.equal(await page.getByText('LED draft saved in this browser only.').count(), 1);
    await page.getByRole('button', { name: /2\. Drawer 2/ }).click();
    const secondLedStart = page.locator('input[x-model="ledDraftFor(selectedDrawer()).start"]');
    const secondLedCount = page.locator('input[x-model="ledDraftFor(selectedDrawer()).count"]');
    await secondLedStart.fill('5'); await secondLedCount.fill('4');
    assert.match(await page.getByText(/overlaps Main drawer/).innerText(), /overlaps Main drawer/);
    await page.getByRole('button', { name: 'Save LED draft' }).click();
    assert.match(await page.locator('p.text-xs.text-error[role=alert]').innerText(), /overlaps Main drawer/);
    await secondLedStart.fill(''); await secondLedCount.fill('');
    await page.getByRole('button', { name: 'Save layout' }).click();
    await page.waitForURL(`${baseURL}/storage/cabinets/${cabinetID}`);
    const savedPhysicalLayout = JSON.parse(new URLSearchParams(layoutPosts.at(-1).body).get('layout'));
    assert.ok(!('led_start' in savedPhysicalLayout.drawers[0]) && !('led_count' in savedPhysicalLayout.drawers[0]) && !('ledStart' in savedPhysicalLayout.drawers[0]) && !('ledCount' in savedPhysicalLayout.drawers[0]));
    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    await page.getByRole('button', { name: /1\. Main drawer/ }).click();
    assert.equal(await page.locator('input[x-model="ledDraftFor(selectedDrawer()).start"]').inputValue(), '3');
    assert.equal(await page.locator('input[x-model="ledDraftFor(selectedDrawer()).count"]').inputValue(), '4');
    const breakController = spawnSync('python3', ['-c', 'import sqlite3,sys; c=sqlite3.connect(sys.argv[1]); c.execute("UPDATE cabinets SET controller_id=99999 WHERE id=?", (int(sys.argv[2]),)); c.commit(); c.close()', databasePath, cabinetID], { encoding: 'utf8' });
    assert.equal(breakController.status, 0, breakController.stderr);
    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    await page.getByRole('button', { name: /1\. Main drawer/ }).click();
    assert.match(await page.locator('p').filter({ hasText: 'Cabinet references missing LED controller' }).innerText(), /#99999/);
    await page.locator('input[x-model="ledDraftFor(selectedDrawer()).start"]').fill('3');
    await page.locator('input[x-model="ledDraftFor(selectedDrawer()).count"]').fill('4');
    await page.getByRole('button', { name: 'Save LED draft' }).click();
    assert.match(await page.locator('p.text-xs.text-error[role=alert]').innerText(), /missing LED controller/);
    const restoreController = spawnSync('python3', ['-c', 'import sqlite3,sys; c=sqlite3.connect(sys.argv[1]); c.execute("UPDATE cabinets SET controller_id=99003 WHERE id=?", (int(sys.argv[2]),)); c.commit(); c.close()', databasePath, cabinetID], { encoding: 'utf8' });
    assert.equal(restoreController.status, 0, restoreController.stderr);
    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    await page.getByRole('button', { name: /1\. Main drawer/ }).click();
    assert.equal(await page.locator('input[x-model="ledDraftFor(selectedDrawer()).start"]').inputValue(), '3');

    await page.getByRole('button', { name: /1\. Main drawer/ }).dblclick();
    await page.getByText('Bin layout canvas').waitFor();
    assert.equal(await canvas.getAttribute('viewBox'), '0 0 24 25');
    const binSelect = page.locator('select[x-model="selectedAvailableBin"]');
    await binSelect.selectOption('99001');
    await page.locator('input[x-model="snap"]').check();
    await page.getByRole('button', { name: 'Draw bin' }).click();

    await drag(1, 1, 6, 6);

    await page.waitForFunction(() => document.querySelectorAll('svg g[role="button"][data-layout-entity="true"]').length === 1);
    assert.equal(await page.locator('svg g[role="button"][data-layout-entity="true"]').count(), 1);
    const binX = page.locator('input[x-model\\.number="selectedBin().x"]');
    const binWidth = page.locator('input[x-model\\.number="selectedBin().width"]');
    assert.equal(Number(await binWidth.inputValue()), 5);
    await page.getByRole('button', { name: 'Select', exact: true }).click();
    await drag(2, 2, 7, 2);
    assert.equal(Number(await binX.inputValue()), 5);
    await drag(10, 5, 15, 10);
    assert.equal(Number(await binWidth.inputValue()), 10);
    await page.keyboard.press('Control+z');
    assert.equal(await page.locator('svg g[role="button"][data-layout-entity="true"]').count(), 1);
    await page.waitForFunction(() => Number(Array.from(document.querySelectorAll('input')).find(input => input.getAttribute('x-model.number') === 'selectedBin().width')?.value) === 5);
    assert.equal(Number(await binWidth.inputValue()), 5);
    await page.keyboard.press('Control+y');
    await page.waitForFunction(() => Number(Array.from(document.querySelectorAll('input')).find(input => input.getAttribute('x-model.number') === 'selectedBin().width')?.value) === 10);
    assert.equal(Number(await binWidth.inputValue()), 10);
    await page.getByRole('button', { name: 'Save layout' }).click();
    await page.waitForURL(`${baseURL}/storage/cabinets/${cabinetID}`);
    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    await page.getByRole('button', { name: /1\. Main drawer/ }).dblclick();
    assert.equal(await page.locator('svg g[role="button"][data-layout-entity="true"]').count(), 1);
    await page.locator('svg g[role="button"][data-layout-entity="true"]').first().click();
    assert.equal(Number(await page.locator('input[x-model\\.number="selectedBin().x"]').inputValue()), 5);
    assert.equal(Number(await page.locator('input[x-model\\.number="selectedBin().width"]').inputValue()), 10);

    await page.getByRole('button', { name: 'Back', exact: true }).click();
    await page.getByRole('button', { name: /1\. Main drawer/ }).click();
    await page.getByRole('button', { name: 'Delete', exact: true }).click();
    assert.match(await page.locator('.text-sm.text-error[role=alert]').innerText(), /remove or relocate/i);
    await page.keyboard.press('Escape');
    await page.getByRole('button', { name: 'Delete cabinet' }).click();
    await page.waitForURL(/layout_error=/);
    assert.match(await page.locator('.text-sm.text-error[role=alert]').innerText(), /drawer/i);

    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    await page.getByRole('button', { name: /1\. Main drawer/ }).dblclick();
    await page.locator('svg g[role="button"][data-layout-entity="true"]').first().click();
    await page.getByRole('button', { name: 'Remove placement' }).click();
    await page.waitForURL(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    await page.getByRole('button', { name: /1\. Main drawer/ }).dblclick();
    assert.equal(await page.locator('select[x-model="selectedAvailableBin"] option[value="99001"]').count(), 1);
    const stockCheck = spawnSync('python3', ['-c', 'import sqlite3,sys; c=sqlite3.connect(sys.argv[1]); r=c.execute("SELECT pa.quantity,b.container_id,b.led_index FROM part_assignments pa JOIN bins b ON b.id=pa.bin_id WHERE pa.part_id=99002 AND pa.bin_id=99001").fetchone(); e=c.execute("SELECT id,controller_id,led_start,led_count FROM containers WHERE id=99004").fetchone(); cabinet=c.execute("SELECT controller_id FROM cabinets WHERE id=1").fetchone(); print("|".join("NULL" if x is None else str(x) for x in r)+";"+"|".join(str(x) for x in e)+";"+"|".join(str(x) for x in cabinet)); c.close()', databasePath], { encoding: 'utf8' });
    assert.equal(stockCheck.stdout.trim(), '4|NULL|NULL;99004|99003|0|64;99003');

    await page.goto(`${baseURL}/storage/cabinets`);
    const emptyName = `Empty cabinet ${Date.now()}`;
    await page.locator('input[name=name]').fill(emptyName);
    await page.getByRole('button', { name: 'Create cabinet' }).click();
    await page.waitForURL(/\/storage\/cabinets\/\d+\/edit$/);
    const emptyID = page.url().match(/cabinets\/(\d+)\/edit$/)[1];
    await page.getByRole('button', { name: 'Delete cabinet' }).click();
    await page.waitForURL(`${baseURL}/storage/cabinets`);

    await page.locator('input[name=name]').fill(`Archived cabinet ${Date.now()}`);
    await page.getByRole('button', { name: 'Create cabinet' }).click();
    await page.waitForURL(/\/storage\/cabinets\/\d+\/edit$/);
    const archiveID = page.url().match(/cabinets\/(\d+)\/edit$/)[1];
    await page.getByRole('button', { name: 'Drawer', exact: true }).click(); await drag(10, 10, 20, 20);
    await page.getByRole('button', { name: 'Save layout' }).click(); await page.waitForURL(`${baseURL}/storage/cabinets/${archiveID}`);
    await page.goto(`${baseURL}/storage/cabinets/${archiveID}/edit`);
    await page.locator('svg g[role="button"][data-layout-entity="true"]').first().click();
    await page.getByRole('button', { name: 'Archive', exact: true }).click();
    await page.waitForURL(`${baseURL}/storage/cabinets/${archiveID}/edit`);
    await page.keyboard.press('Escape');
    await page.getByRole('button', { name: 'Archive cabinet' }).click();
    await page.waitForURL(`${baseURL}/storage/cabinets`);

    assert.ok(layoutPosts.length >= 4);
    assert.ok(layoutPosts.every(post => new URLSearchParams(post.body).get('csrf_token')));
    assert.ok(lifecyclePaths.every(url => !url.includes('undefined')));
    assert.deepEqual(pageErrors, []);
    const runtimeConsoleErrors = consoleErrors.filter(message => !message.includes("Blocked attempt to show a 'beforeunload' confirmation panel"));
    assert.deepEqual(runtimeConsoleErrors, []);
    console.log(JSON.stringify({ result: 'PASS', cabinetID, emptyID, archiveID, layoutSaves: layoutPosts.length, lifecyclePaths, pageErrors, runtimeConsoleErrors, browserWarnings: consoleErrors.length - runtimeConsoleErrors.length }));
  } finally {
    if (browser) await browser.close();
    if (server) { server.kill('SIGTERM'); await new Promise(resolve => server.once('exit', resolve)); }
    if (scratchDir) fs.rmSync(scratchDir, { recursive: true, force: true });
  }
})().catch(error => { console.error(error); process.exit(1); });
