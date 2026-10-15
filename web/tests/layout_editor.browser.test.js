const assert = require('node:assert/strict');
const { chromium } = require('playwright');
const { spawn, spawnSync } = require('node:child_process');
const path = require('node:path');
const fs = require('node:fs');
const os = require('node:os');
const net = require('node:net');
const http = require('node:http');

let baseURL = process.env.WLEDGER_URL || 'http://127.0.0.1:8080';
const email = process.env.WLEDGER_TEST_EMAIL || 'test@example.com';
const password = process.env.WLEDGER_TEST_PASSWORD || 'password123';
const executablePath = process.env.CHROMIUM_PATH || '/bin/chromium';

(async () => {
  if (process.env.WLEDGER_URL && process.env.WLEDGER_START_SERVER !== '0') throw new Error('set WLEDGER_START_SERVER=0 when targeting an external instance');
  if (process.env.WLEDGER_START_SERVER === '0') throw new Error('physical Locate acceptance requires the disposable app database and local mock WLED server; external controllers are not supported');
  let server;
  let mockController;
  const mockControllers = [];
  let mockPorts = [];
  const referenceMockControllers = [];
  const referenceControllerWrites = [];
  let mockHost;
  let mockPort;
  let scratchDir;
  let databasePath;
  let browser;
  const mockControllerRequests = [];
  const mockControllerWrites = [];
  try {
    if (process.env.WLEDGER_START_SERVER !== '0') {
      const mockControllerHandler = async (request, response) => {
        mockControllerRequests.push(`${request.method} ${request.url}`);
        response.setHeader('Content-Type', 'application/json');
        if (request.method === 'GET' && request.url === '/json/info') {
          response.end(JSON.stringify({ mac: '02:00:00:00:00:01', leds: { count: 200, rgbw: false } }));
          return;
        }
        if (request.method === 'GET' && request.url === '/json/cfg') {
          response.end(JSON.stringify({ hw: { led: { ins: [
            { start: 0, len: 100, rev: false, skip: 0, type: 22, pin: [2, -1, -1] },
            { start: 100, len: 100, rev: false, skip: 0, type: 22, pin: [4, -1, -1] },
          ] } } }));
          return;
        }
        if (request.method === 'GET' && request.url === '/json/state') {
          response.end(JSON.stringify({ on: true, seg: [
            { id: 10, start: 0, stop: 100, on: true, fx: 0, grp: 1, spc: 0, rev: false, mi: false, of: 0 },
            { id: 20, start: 100, stop: 200, on: true, fx: 0, grp: 1, spc: 0, rev: false, mi: false, of: 0 },
          ] }));
          return;
        }
        if (request.method === 'POST' && request.url === '/json/state') {
          let body = '';
          for await (const chunk of request) body += chunk;
          mockControllerWrites.push(JSON.parse(body));
          response.end(JSON.stringify({ success: true }));
          return;
        }
        response.statusCode = 404;
        response.end(JSON.stringify({ error: 'not found' }));
      };
      for (let index = 0; index < 6; index++) {
        const mock = http.createServer(mockControllerHandler);
        await new Promise((resolve, reject) => mock.once('error', reject).listen(0, '127.0.0.1', resolve));
        mockControllers.push(mock);
      }
      mockController = mockControllers[0];
      mockPorts = mockControllers.map(mock => mock.address().port);
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
      server = spawn(serverBinary, [], { cwd: serverCwd, env: { ...process.env, WLEDGER_INSECURE_COOKIES: '1', WLEDGER_PHYSICAL_OUTPUT_ENABLED: 'true', WLEDGER_PORT: String(port) }, stdio: 'ignore' });
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
    const controllerAssignmentPosts = [];
    const ledMappingPosts = [];
    const capacityPosts = [];
    const wiringPosts = [];
    const hardwareRequests = [];
    const pixelMappingPreviewRequests = [];
    const lifecyclePaths = [];
    page.on('pageerror', error => pageErrors.push({ url: page.url(), stack: error.stack || error.message }));
    page.on('console', message => { if (message.type() === 'error') consoleErrors.push(message.text()); });
    page.on('request', request => {
      if (request.method() === 'POST' && /\/storage\/cabinets\/\d+\/edit/.test(request.url())) layoutPosts.push({ url: request.url(), body: request.postData() || '' });
      if (request.method() === 'POST' && /\/storage\/cabinets\/\d+\/controller/.test(request.url())) controllerAssignmentPosts.push({ url: request.url(), body: request.postData() || '' });
      if (request.method() === 'POST' && /\/storage\/cabinets\/\d+\/drawers\/\d+\/led/.test(request.url())) ledMappingPosts.push({ url: request.url(), body: request.postData() || '' });
      if (request.method() === 'POST' && /\/storage\/cabinets\/\d+\/wiring/.test(request.url())) wiringPosts.push({ url: request.url(), body: request.postData() || '' });
      if (request.method() === 'POST' && /\/hardware\/\d+\/capacity/.test(request.url())) capacityPosts.push({ url: request.url(), body: request.postData() || '' });
      if (request.method() === 'GET' && /\/storage\/cabinets\/\d+\/drawers\/\d+\/pixel-map$/.test(new URL(request.url()).pathname)) pixelMappingPreviewRequests.push(new URL(request.url()).pathname);
      if (/\/hardware\/(?:off(?:\?|\/|$)|\d+\/locate(?:\?|\/|$))|\/parts\/\d+\/locate-bin|\/storage\/drawers\/\d+\/(?:locate|stop)|\/locate(?:\?|\/|$)|\/json\/(?:state|info)/.test(request.url())) hardwareRequests.push(request.url());
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

    await page.goto(`${baseURL}/hardware`);
    assert.match(await page.locator('body').innerText(), /Cabinets[\s\S]*Create Cabinet[\s\S]*Physical-first:[\s\S]*WLED controllers/);
    const advancedElectrical = page.locator('details').filter({ hasText: 'Advanced legacy electrical tools' });
    assert.equal(await advancedElectrical.count(), 1, 'legacy electrical tools must be clearly separated in an advanced section');
    await advancedElectrical.locator('summary').click();
    assert.match(await page.locator('body').innerText(), /Legacy containers, WLED segments and bin LED indices are not physical drawers or bins/);
    await page.getByRole('link', { name: 'Open Cabinets' }).click();
    await page.waitForURL(`${baseURL}/storage/cabinets`);
    const cabinetName = `Visual cabinet ${Date.now()}`;
    await page.locator('input[name=name]').fill(cabinetName);
    await page.getByRole('button', { name: 'Create cabinet' }).click();
    await page.waitForURL(/\/storage\/cabinets\/\d+\/edit$/);
    const cabinetID = page.url().match(/cabinets\/(\d+)\/edit$/)[1];
    const controllerSeed = spawnSync('python3', ['-c', `import json,sqlite3,sys; c=sqlite3.connect(sys.argv[1]); ports=json.loads(sys.argv[3]); c.executemany("INSERT INTO controllers(id,name,ip_address,port,led_count) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,ip_address=excluded.ip_address,port=excluded.port,led_count=excluded.led_count", [(99003+i, f"Persistent Mapping Test Controller {99003+i}", '127.0.0.1', ports[i], 0) for i in range(6)]); c.execute("INSERT INTO containers(id,name,controller_id,segment_id,led_start,led_count) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET led_start=excluded.led_start,led_count=excluded.led_count", (99004, "Test electrical string", 99003, 0, 0, 64)); c.commit(); c.close()`, databasePath, cabinetID, JSON.stringify(mockPorts)], { encoding: 'utf8' });
    assert.equal(controllerSeed.status, 0, controllerSeed.stderr);
    await page.goto(`${baseURL}/hardware`);
    await page.waitForLoadState('networkidle');
    assert.equal(await page.getByRole('link', { name: 'Edit controller' }).count(), 6, 'normal controller editing must be visible without expanding legacy tools');
    for (const port of mockPorts) assert.equal(await page.locator(`a[href="http://127.0.0.1:${port}"]`).count(), 1, 'controller links must include each unique configured HTTP port');
    const advancedGridLink = page.locator('details').filter({ hasText: 'Advanced legacy electrical tools' }).locator('a[href="/hardware/99003/grid"]');
    assert.equal(await advancedGridLink.count(), 1);
    assert.equal(await advancedGridLink.isVisible(), false, 'legacy grid tools should remain collapsed by default');
    assert.equal(mockControllerWrites.length, 0, 'controller listing must not send WLED writes');
    mockControllerRequests.length = 0;
    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    for (const id of [99003, 99004, 99005, 99006, 99007]) {
      await page.locator('label').filter({ hasText: `Persistent Mapping Test Controller ${id}` }).locator('input[type="checkbox"]').check();
    }
    assert.equal(await page.locator('label').filter({ hasText: 'Persistent Mapping Test Controller' }).locator('input[type="checkbox"]:checked').count(), 5);
    assert.equal(await page.locator('button[aria-label="Move controller up"]').count(), 5, 'all five controllers should have an explicit cabinet order');
    assert.equal(await page.locator('label').filter({ hasText: 'Persistent Mapping Test Controller 99008' }).locator('input[type="checkbox"]').isChecked(), false);
    const firstControllerSave = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === `/storage/cabinets/${cabinetID}/controller`);
    await page.getByRole('button', { name: 'Save controller order' }).click();
    const firstControllerSaveResponse = await firstControllerSave;
    if (firstControllerSaveResponse.status() !== 303) throw new Error(`initial controller order save failed (${firstControllerSaveResponse.status()}): ${await page.locator('body').innerText()}`);
    const firstControllerLocation = firstControllerSaveResponse.headers().location || '';
    if (!/controller_saved=1/.test(firstControllerLocation)) throw new Error(`initial controller order redirected to unexpected location ${firstControllerLocation}`);
    await page.waitForURL(/controller_saved=1/);
    assert.equal(controllerAssignmentPosts.length, 1);
    const postedControllerIDs = new URLSearchParams(controllerAssignmentPosts[0].body);
    assert.ok(postedControllerIDs.get('csrf_token'));
    assert.deepEqual(postedControllerIDs.getAll('controller_ids'), ['99003', '99004', '99005', '99006', '99007']);
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
    const backToDrawers = page.getByRole('button', { name: 'Back to drawers' });
    if (await backToDrawers.isVisible()) await backToDrawers.click();
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
    const configureControllerOutputs = async (controllerID, outputs) => {
      await page.goto(`${baseURL}/hardware/${controllerID}/edit`);
      const form = page.locator(`form[action="/hardware/${controllerID}/update"]`);
      const editor = form.locator('[x-data^="controllerOutputEditor"]');
      for (let index = 0; index < outputs.length; index++) {
        const output = outputs[index];
        await editor.getByRole('button', { name: 'Add output' }).click();
        await editor.locator('input[x-model="output.name"]').nth(index).fill(output.name);
        await editor.locator('input[x-model\\.number="output.pixel_count"]').nth(index).fill(String(output.pixels));
        if (output.pattern) {
          await editor.locator('select[x-model="output.pattern"]').nth(index).selectOption(output.pattern);
          await editor.locator('input[x-model\\.number="output.rows"]').nth(index).fill(String(output.rows));
          await editor.locator('input[x-model\\.number="output.columns"]').nth(index).fill(String(output.columns));
          if (output.pattern === 'serpentine_rows') {
            await editor.getByText('26→50', { exact: true }).waitFor();
            await editor.getByRole('button', { name: 'Next pixels' }).nth(index).click();
            await editor.getByText('41→35', { exact: true }).waitFor();
            await editor.getByRole('button', { name: 'Previous pixels' }).nth(index).click();
          }
        }
      }
      const saveResponse = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === `/hardware/${controllerID}/update`);
      await form.locator('button[type="submit"]').click();
      const response = await saveResponse;
      if (response.status() !== 303) throw new Error(`controller ${controllerID} output setup failed (${response.status()}): ${await page.locator('body').innerText()}`);
      await page.waitForURL(/controller_saved=1/);
    };
    await configureControllerOutputs(99003, [
      { name: 'Serpentine four-row string', pixels: 100, pattern: 'serpentine_rows', rows: 4, columns: 25 },
      { name: 'Cabinet return string', pixels: 100 },
    ]);
    await configureControllerOutputs(99004, [
      { name: 'Controller two string one', pixels: 100 },
      { name: 'Controller two string two', pixels: 100 },
    ]);

    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    await page.waitForSelector('svg g[role="button"][data-layout-entity="true"]');
    assert.equal(await page.locator('input[x-model="string.pixel_count"]').count(), 0, 'cabinet layout must not duplicate controller output capacity settings');
    const addPathButtons = () => page.getByRole('button', { name: '＋ Add path section', exact: true });
    const sectionPixels = () => page.locator('input[x-model="section.pixel_count"]');
    const clickPathPoints = async points => {
      await canvas.scrollIntoViewIfNeeded();
      for (const [x, y] of points) { const screen = await point(x, y); await page.mouse.click(screen.x, screen.y); }
      await canvas.evaluate(svg => svg.dispatchEvent(new MouseEvent('dblclick', { bubbles: true })));
    };
    const serpentineRows = [
      [[60, 10], [70, 10]], [[70, 15], [60, 15]], [[60, 20], [70, 20]], [[70, 25], [60, 25]],
    ];
    for (let index = 0; index < serpentineRows.length; index++) {
      await addPathButtons().first().click();
      await sectionPixels().last().fill('25');
      assert.equal(await page.getByText('Wiring path drawing').count(), 1, 'adding a path section should activate path drawing mode');
      await clickPathPoints(serpentineRows[index]);
      assert.equal(await page.getByText(new RegExp(`Section ${index + 1} · 25 pixels · 2 path points`)).count(), 1, `section ${index + 1} should retain its ordered path points`);
    }
    await page.locator('input[x-model="string.layout_complete"]').first().check();
    await page.getByRole('button', { name: 'Save output paths' }).click();
    await page.waitForURL(/wiring_(saved=1|error=)/);
    assert.equal(new URL(page.url()).searchParams.get('wiring_saved'), '1', await page.locator('body').innerText());
    assert.equal(wiringPosts.length, 1);
    const wiringPost = new URLSearchParams(wiringPosts[0].body);
    assert.ok(wiringPost.get('csrf_token'));
    const savedWiring = JSON.parse(wiringPost.get('wiring'));
    assert.deepEqual(savedWiring.configured_controller_ids, [99003, 99004]);
    assert.equal(savedWiring.strings[0].sections.reduce((sum, section) => sum + section.pixel_count, 0), 100);
    assert.deepEqual(savedWiring.strings[0].sections[1].points, [{ x: 70, y: 15 }, { x: 60, y: 15 }]);
    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    await page.evaluate(id => localStorage.setItem(`wledger-led-allocation-draft-cab-${id}`, JSON.stringify({ controllerId: 99003, allocations: { '1': { start: '3', count: '4' } } })), cabinetID);
    await page.reload();
    let editorData = JSON.parse(await page.locator('#layout-editor-data').textContent());
    const drawerTwoID = editorData.Drawers.find(item => item.Drawer.label === 'Drawer 2').Drawer.id;
    assert.equal(JSON.parse(editorData.Cabinet.layout_decorations_json).length, 4);
    const payloadBeforeError = () => JSON.parse(new URLSearchParams(layoutPosts.at(-1).body).get('layout'));
    assert.equal(payloadBeforeError().drawers.length, 4);
    assert.equal(payloadBeforeError().decorations.length, 4);

    await page.getByRole('button', { name: /1\. Main drawer/ }).click();
    const ledStart = page.locator('input[x-model="ledRangesFor(selectedDrawer())[0].start_pixel"]');
    const ledCount = page.locator('input[x-model="ledRangesFor(selectedDrawer())[0].pixel_count"]');
    assert.match(await page.locator('p.font-mono').innerText(), /Persistent Mapping Test Controller 99003.*cabinet 1–200.*Persistent Mapping Test Controller 99004.*cabinet 201–400/);
    assert.match(await page.locator('p.text-xs.text-warning[role=status]').innerText(), /legacy browser-local LED draft exists but was not imported/i);
    const selectedDrawerX = page.locator('input[x-model\\.number="selectedDrawer().x"]');
    await selectedDrawerX.fill(String(Number(await selectedDrawerX.inputValue()) + 1));
    await selectedDrawerX.press('Tab');
    await page.getByRole('button', { name: 'Save drawer pixels' }).click();
    assert.match(await page.locator('p[role=alert]').filter({ hasText: /Save or cancel physical layout/ }).innerText(), /Save or cancel physical layout/);
    await page.reload();
    await page.getByRole('button', { name: /1\. Main drawer/ }).click();
    await ledStart.fill('-1'); await ledCount.fill('4');
    assert.match(await page.getByText(/Cabinet pixel Start must be a whole number of at least 1/).innerText(), /at least 1/);
    await page.getByRole('button', { name: 'Save drawer pixels' }).click();
    assert.match(await page.locator('p[role=alert]').filter({ hasText: /Cabinet pixel Start must be a whole number/ }).innerText(), /at least 1/);
    await ledStart.fill('397'); await ledCount.fill('8');
    assert.match(await page.getByText(/exceeds cabinet capacity \(400 pixels\)/).innerText(), /exceeds cabinet capacity/);
    await page.getByRole('button', { name: 'Save drawer pixels' }).click();
    assert.match(await page.locator('p[role=alert]').filter({ hasText: /exceeds cabinet capacity/ }).innerText(), /exceeds cabinet capacity/);
    await ledStart.fill('26'); await ledCount.fill('1');
    assert.ok(await page.getByText('Persistent Mapping Test Controller 99003 local 50–50 · Serpentine four-row string pixels 50–50', { exact: false }).count(), 'saved serpentine wiring must preview the same transformed pixel the Locate engine selects');
    await ledStart.fill('195'); await ledCount.fill('16');
    assert.equal(await page.locator('output').first().innerText(), '210');
    assert.ok(await page.getByText('Persistent Mapping Test Controller 99003 local 195–200 · Cabinet return string pixels 95–100', { exact: false }).count(), 'second string pixels should remain string-local while controller pixels are local');
    assert.ok(await page.getByText('Persistent Mapping Test Controller 99004 local 1–10 · Controller two string one pixels 1–10', { exact: false }).count(), 'a cabinet allocation crossing controllers should restart controller-local numbering at 1');
    await page.getByRole('button', { name: 'Save drawer pixels' }).click();
    await page.waitForURL(/led_saved=1/);
    assert.equal(ledMappingPosts.length, 1);
    const postedMapping = new URLSearchParams(ledMappingPosts[0].body);
    assert.ok(postedMapping.get('csrf_token'));
    assert.equal(postedMapping.get('coordinate_space'), 'cabinet');
    assert.deepEqual(JSON.parse(postedMapping.get('allocations')).map(({ start_pixel, pixel_count }) => [start_pixel, pixel_count]), [[195, 16]]);
    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    await page.getByRole('button', { name: /1\. Main drawer/ }).click();
    assert.equal(await page.locator('input[x-model="ledRangesFor(selectedDrawer())[0].start_pixel"]').inputValue(), '195');
    assert.equal(await page.locator('input[x-model="ledRangesFor(selectedDrawer())[0].pixel_count"]').inputValue(), '16');
    await page.getByText('Advanced: additional non-contiguous ranges').click();
    await page.getByRole('button', { name: '＋ Add another range' }).click();
    await page.locator('input[x-model="range.start_pixel"]').fill('95');
    await page.locator('input[x-model="range.pixel_count"]').fill('11');
    await page.getByRole('button', { name: 'Save drawer pixels' }).click();
    await page.waitForURL(/led_saved=1/);
    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    await page.getByRole('button', { name: /1\. Main drawer/ }).click();
    await page.getByText('Advanced: additional non-contiguous ranges').click();
    await page.getByText('Persistent Mapping Test Controller 99003 local 76–81 · Serpentine four-row string pixels 76–81', { exact: false }).waitFor();
    assert.ok(pixelMappingPreviewRequests.some(path => path === `/storage/cabinets/${cabinetID}/drawers/${editorData.Drawers[0].Drawer.id}/pixel-map`), 'saved drawer preview must come from the Go topology mapping endpoint');
    await page.getByText('Advanced hardware diagnostics (read-only)').click();
    const discoveryStart = mockControllerRequests.length;
    await page.getByRole('button', { name: 'Read WLED output topology' }).click();
    await page.getByText(/Output 1: WLED start 0, length 100, forward, GPIO 2, physical pixels 1–100/).waitFor();
    await page.getByText(/Output 2: WLED start 100, length 100, forward, GPIO 4, physical pixels 101–200/).waitFor();
    assert.equal(await page.locator('span[title^="Selected drawer cabinet range"]').count(), 3, 'visual output map should mark drawer pixels in both outputs and the controller boundary');
    assert.deepEqual(mockControllerRequests.slice(discoveryStart), ['GET /json/info', 'GET /json/cfg'], 'output discovery must be read-only and must not inspect segments or issue writes');
    await page.getByRole('button', { name: /2\. Drawer 2/ }).click();
    const secondDrawerStart = page.locator('input[x-model="ledRangesFor(selectedDrawer())[0].start_pixel"]');
    const secondDrawerCount = page.locator('input[x-model="ledRangesFor(selectedDrawer())[0].pixel_count"]');
    await secondDrawerStart.fill('200'); await secondDrawerCount.fill('2');
    assert.match(await page.getByText(/Cabinet pixel ranges overlap/).innerText(), /overlap/);
    await page.getByRole('button', { name: 'Save drawer pixels' }).click();
    assert.match(await page.locator('p[role=alert]').filter({ hasText: /Cabinet pixel ranges overlap/ }).innerText(), /overlap/);
    await secondDrawerStart.fill('225'); await secondDrawerCount.fill('10');
    assert.ok(await page.getByText('Persistent Mapping Test Controller 99004 local 25–34 · Controller two string one pixels 25–34', { exact: false }).count(), 'the second controller preview should use local pixels 25–34 for cabinet pixels 225–234');
    await page.getByRole('button', { name: 'Save drawer pixels' }).click();
    await page.waitForURL(/led_saved=1/);
    const secondDrawerMappingPost = new URLSearchParams(ledMappingPosts.at(-1).body);
    assert.equal(secondDrawerMappingPost.get('coordinate_space'), 'cabinet');
    assert.deepEqual(JSON.parse(secondDrawerMappingPost.get('allocations')).map(({ start_pixel, pixel_count }) => [start_pixel, pixel_count]), [[225, 10]]);

    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    await page.getByRole('button', { name: 'Move controller up' }).nth(1).click();
    const controllerImpact = await page.locator('body').innerText();
    assert.match(controllerImpact, /Before:[\s\S]*Persistent Mapping Test Controller 99003 local 195–200 · Cabinet return string pixels 95–100 \+ Persistent Mapping Test Controller 99004 local 1–10 · Controller two string one pixels 1–10/);
    assert.match(controllerImpact, /After:[\s\S]*Persistent Mapping Test Controller 99004 local 195–200 · Controller two string two pixels 95–100 \+ Persistent Mapping Test Controller 99003 local 1–10 · Serpentine four-row string pixels 1–10/);
    const controllerConfirmation = page.locator('input[x-model="controllerMappingImpactConfirmed"]');
    await page.getByRole('button', { name: 'Save controller order' }).click();
    assert.match(await page.locator('p[role=alert]').filter({ hasText: /Review the before\/after mapping preview/ }).innerText(), /explicit confirmation/);
    assert.equal(controllerAssignmentPosts.length, 1, 'controller reorder must not submit before explicit confirmation');
    await controllerConfirmation.check();
    await page.getByRole('button', { name: 'Move controller down' }).first().click();
    await page.getByRole('button', { name: 'Move controller up' }).nth(1).click();
    await page.waitForFunction(() => !document.querySelector('input[x-model="controllerMappingImpactConfirmed"]')?.checked, null);
    await controllerConfirmation.check();
    await page.locator('[x-data="physicalLayoutEditor()"]')
      .evaluate(element => window.Alpine.$data(element).electricalRevision = 0);
    await page.getByRole('button', { name: 'Save controller order' }).click();
    await page.waitForURL(/controller_error=/);
    assert.match(await page.locator('p[role=alert]').filter({ hasText: /reload before saving/ }).innerText(), /reload before saving/);
    const unchangedOrder = spawnSync('python3', ['-c', 'import sqlite3,sys; c=sqlite3.connect(sys.argv[1]); rows=c.execute("SELECT controller_id FROM cabinet_controllers WHERE cabinet_id=? ORDER BY slot", (int(sys.argv[2]),)).fetchall(); print(rows); c.close()', databasePath, cabinetID], { encoding: 'utf8' });
    assert.equal(unchangedOrder.stdout.trim(), '[(99003,), (99004,), (99005,), (99006,), (99007,)]', 'a stale electrical revision must leave the saved controller order unchanged');

    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    assert.equal(await page.locator('input[x-model="string.pixel_count"]').count(), 0, 'cabinet layout must not duplicate controller output capacity settings');
    const outputRowsBeforeReorder = readOnlyRows(databasePath, 'SELECT display_order,name,pixel_count FROM controller_outputs WHERE controller_id=99003 ORDER BY display_order');
    await page.goto(`${baseURL}/hardware/99003/edit`);
    let outputEditor = page.locator('[x-data^="controllerOutputEditor"]');
    await outputEditor.getByRole('button', { name: 'Move output later' }).first().click();
    const reorderResponse = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/hardware/99003/update');
    await page.getByRole('button', { name: 'Save controller' }).click();
    assert.equal((await reorderResponse).status(), 409, 'output order changes affecting saved drawers must require explicit confirmation');
    assert.match(await page.locator('[role=alert]').innerText(), /saved drawer pixel destinations/);
    assert.deepEqual(readOnlyRows(databasePath, 'SELECT display_order,name,pixel_count FROM controller_outputs WHERE controller_id=99003 ORDER BY display_order'), outputRowsBeforeReorder, 'an unconfirmed output reorder must not mutate controller hardware settings');
    assert.equal(wiringPosts.length, 1, 'output settings must not be submitted through the cabinet wiring endpoint');
    await page.locator('input[name="confirm_mapping_impact"]').check();
    await page.getByRole('button', { name: 'Save controller' }).click();
    await page.waitForURL(/controller_saved=1/);
    assert.deepEqual(readOnlyRows(databasePath, 'SELECT display_order,name,pixel_count FROM controller_outputs WHERE controller_id=99003 ORDER BY display_order'), [[0, 'Cabinet return string', 100], [1, 'Serpentine four-row string', 100]]);
    assert.deepEqual(readOnlyRows(databasePath, 'SELECT start_pixel,pixel_count,display_order FROM drawer_led_allocations WHERE cabinet_id=? ORDER BY drawer_id,display_order', [cabinetID]).map(row => row.map(Number)), [[195, 16, 0], [95, 11, 1], [225, 10, 0]], 'confirming a wiring change must preserve the saved drawer range identities and quantities');

    await page.goto(`${baseURL}/hardware/99003/edit`);
    outputEditor = page.locator('[x-data^="controllerOutputEditor"]');
    await outputEditor.getByRole('button', { name: 'Move output earlier' }).nth(1).click();
    await page.locator('input[name="confirm_mapping_impact"]').check();
    await page.getByRole('button', { name: 'Save controller' }).click();
    await page.waitForURL(/controller_saved=1/);
    assert.deepEqual(readOnlyRows(databasePath, 'SELECT display_order,name,pixel_count FROM controller_outputs WHERE controller_id=99003 ORDER BY display_order'), outputRowsBeforeReorder, 'controller output order should be restorable in Hardware without changing drawer allocation records');

    await page.goto(`${baseURL}/hardware/99003/edit`);
    outputEditor = page.locator('[x-data^="controllerOutputEditor"]');
    await outputEditor.locator('select[x-model="output.pattern"]').first().selectOption('custom');
    await outputEditor.locator('select[x-model="output.custom_layout"]').first().selectOption('top-to-bottom-columns');
    await outputEditor.getByRole('button', { name: 'Generate order' }).first().click();
    assert.equal((await outputEditor.locator('textarea[x-model="output.custom_order_text"]').first().inputValue()).split(',')[1].trim(), '4', 'top-to-bottom layout generator must persist the exact custom permutation');
    await outputEditor.getByText('2→5', { exact: true }).waitFor();
    await outputEditor.locator('textarea[x-model="output.custom_order_text"]').first().fill('0, 0');
    const invalidPatternResponse = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/hardware/99003/update');
    await page.getByRole('button', { name: 'Save controller' }).click();
    assert.equal((await invalidPatternResponse).status(), 400, 'invalid custom output permutations must be rejected by server-side capability validation');
    assert.match(await page.locator('[role=alert]').innerText(), /invalid controller hardware configuration/);
    assert.deepEqual(readOnlyRows(databasePath, 'SELECT display_order,name,pixel_count FROM controller_outputs WHERE controller_id=99003 ORDER BY display_order'), outputRowsBeforeReorder);

    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    const wiringConfirmation = page.locator('input[x-model="wiringMappingImpactConfirmed"]');

    await page.getByRole('button', { name: 'Draw path' }).first().click();
    await canvas.scrollIntoViewIfNeeded();
    const pathEditPoint = await point(65, 12);
    await page.mouse.click(pathEditPoint.x, pathEditPoint.y);
    await canvas.evaluate(svg => svg.dispatchEvent(new MouseEvent('dblclick', { bubbles: true })));
    const pathImpact = await page.locator('body').innerText();
    assert.match(pathImpact, /Before:[\s\S]*existing physical path/);
    assert.match(pathImpact, /After:[\s\S]*draft physical path/);
    await page.getByRole('button', { name: 'Save output paths' }).click();
    assert.match(await page.locator('p[role=alert]').filter({ hasText: /Review the before\/after drawer pixel destinations/ }).innerText(), /confirm this wiring change/);
    assert.equal(wiringPosts.length, 1, 'physical path edits should remain a cabinet wiring save and require explicit review');
    await wiringConfirmation.check();
    await page.getByRole('button', { name: 'Save output paths' }).click();
    await page.waitForURL(/wiring_saved=1/);
    assert.equal(wiringPosts.length, 2);

    await page.goto(`${baseURL}/hardware/99003/edit`);
    let hardwareOutputEditor = page.locator('[x-data^="controllerOutputEditor"]');
    await hardwareOutputEditor.locator('input[x-model\\.number="output.pixel_count"]').first().fill('110');
    await hardwareOutputEditor.locator('input[x-model\\.number="output.rows"]').first().fill('5');
    await hardwareOutputEditor.locator('input[x-model\\.number="output.columns"]').first().fill('22');
    await page.getByText('210 total pixels', { exact: true }).waitFor();
    const capacityResponse = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/hardware/99003/update');
    await page.getByRole('button', { name: 'Save controller' }).click();
    assert.equal((await capacityResponse).status(), 409, 'controller capacity changes that remap drawers must require explicit confirmation');
    assert.match(await page.locator('[role=alert]').innerText(), /saved drawer pixel destinations/);
    assert.equal(Number(readOnlyRows(databasePath, 'SELECT led_count FROM controllers WHERE id=99003')[0][0]), 200, 'unconfirmed controller capacity changes must not persist');
    await page.locator('input[name="confirm_mapping_impact"]').check();
    await page.getByRole('button', { name: 'Save controller' }).click();
    await page.waitForURL(/controller_saved=1/);
    assert.equal(Number(readOnlyRows(databasePath, 'SELECT led_count FROM controllers WHERE id=99003')[0][0]), 210);

    await page.goto(`${baseURL}/hardware/99003/edit`);
    hardwareOutputEditor = page.locator('[x-data^="controllerOutputEditor"]');
    await hardwareOutputEditor.locator('input[x-model\\.number="output.pixel_count"]').first().fill('100');
    await hardwareOutputEditor.locator('input[x-model\\.number="output.rows"]').first().fill('4');
    await hardwareOutputEditor.locator('input[x-model\\.number="output.columns"]').first().fill('25');
    await page.locator('input[name="confirm_mapping_impact"]').check();
    await page.getByRole('button', { name: 'Save controller' }).click();
    await page.waitForURL(/controller_saved=1/);
    assert.equal(Number(readOnlyRows(databasePath, 'SELECT led_count FROM controllers WHERE id=99003')[0][0]), 200, 'restoring configured output length must restore total cabinet capacity');

    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    await page.getByRole('button', { name: 'Save layout' }).click();
    await page.waitForURL(`${baseURL}/storage/cabinets/${cabinetID}`);
    const savedPhysicalLayout = JSON.parse(new URLSearchParams(layoutPosts.at(-1).body).get('layout'));
    assert.ok(!('led_start' in savedPhysicalLayout.drawers[0]) && !('led_count' in savedPhysicalLayout.drawers[0]) && !('ledStart' in savedPhysicalLayout.drawers[0]) && !('ledCount' in savedPhysicalLayout.drawers[0]));
    const allocationCheck = spawnSync('python3', ['-c', 'import sqlite3,sys; c=sqlite3.connect(sys.argv[1]); rows=c.execute("SELECT start_pixel,pixel_count,display_order FROM drawer_led_allocations WHERE drawer_id=1 ORDER BY display_order,id").fetchall(); print(rows); c.close()', databasePath], { encoding: 'utf8' });
    assert.equal(allocationCheck.stdout.trim(), '[(195, 16, 0), (95, 11, 1)]');
    const secondaryAllocationCheck = spawnSync('python3', ['-c', 'import sqlite3,sys; c=sqlite3.connect(sys.argv[1]); rows=c.execute("SELECT start_pixel,pixel_count FROM drawer_led_allocations WHERE drawer_id=? ORDER BY display_order,id", (int(sys.argv[2]),)).fetchall(); print(rows); c.close()', databasePath, drawerTwoID], { encoding: 'utf8' });
    assert.equal(secondaryAllocationCheck.stdout.trim(), '[(225, 10)]');
    assert.ok(ledMappingPosts.every(post => new URLSearchParams(post.body).get('csrf_token')));
    const reassignmentCheck = spawnSync('python3', ['-c', 'import sqlite3,sys; c=sqlite3.connect(sys.argv[1]); c.execute("PRAGMA foreign_keys=ON");\ntry: c.execute("UPDATE cabinets SET controller_id=99999 WHERE id=?", (int(sys.argv[2]),)); c.commit(); print("unexpected")\nexcept sqlite3.IntegrityError: print("blocked"); c.close()', databasePath, cabinetID], { encoding: 'utf8' });
    assert.equal(reassignmentCheck.stdout.trim(), 'blocked');

    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
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

    await page.getByRole('button', { name: /Back(?: to drawers)?/ }).click();
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
    await page.getByRole('button', { name: 'Detach bin (keep inventory)' }).click();
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

    assert.equal(mockControllerRequests.filter(path => path === 'GET /json/cfg').length, 1, 'read-only output discovery should be the only configuration inspection');
    assert.equal(mockControllerRequests.filter(path => path === 'GET /json/info').length, 7, 'controller health badges may ping each configured endpoint, in addition to the explicit topology discovery');
    assert.ok(mockControllerRequests.every(path => path === 'GET /json/info' || path === 'GET /json/cfg'), 'editing controller/output/path configuration must never issue a WLED state request');
    assert.deepEqual(mockControllerWrites, [], 'configuration edits and output discovery must never write WLED');
    assert.deepEqual(hardwareRequests, [], 'ordinary cabinet editing must not issue a hardware action request');

    const physicalSeed = spawnSync('python3', ['-c', `import json,sqlite3,sys; c=sqlite3.connect(sys.argv[1]); c.execute("PRAGMA foreign_keys=ON"); cabinet=int(sys.argv[2]); drawers=c.execute("SELECT id FROM drawers WHERE cabinet_id=? AND archived=0 ORDER BY display_order,id", (cabinet,)).fetchall(); assert len(drawers)>=2, drawers; d1,d2=drawers[0][0],drawers[1][0]; c.execute("INSERT INTO bins(id,name,container_id,led_index,width) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name", (99005,"Browser second location",None,None,1)); c.execute("INSERT INTO bins(id,name,container_id,led_index,width) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name", (99006,"Browser unplaced location",None,None,1)); c.execute("INSERT INTO part_assignments(part_id,bin_id,quantity) VALUES(?,?,?) ON CONFLICT(part_id,bin_id) DO UPDATE SET quantity=excluded.quantity", (99002,99005,3)); c.execute("INSERT INTO part_assignments(part_id,bin_id,quantity) VALUES(?,?,?) ON CONFLICT(part_id,bin_id) DO UPDATE SET quantity=excluded.quantity", (99002,99006,1)); c.execute("INSERT INTO bin_drawer_attachments(bin_id,drawer_id,x,y,width,height,display_order) VALUES(?,?,?,?,?,?,?) ON CONFLICT(bin_id) DO UPDATE SET drawer_id=excluded.drawer_id", (99001,d1,1,1,5,5,0)); c.execute("INSERT INTO bin_drawer_attachments(bin_id,drawer_id,x,y,width,height,display_order) VALUES(?,?,?,?,?,?,?) ON CONFLICT(bin_id) DO UPDATE SET drawer_id=excluded.drawer_id", (99005,d2,1,1,5,5,0)); c.commit(); print(json.dumps({"drawer1":d1,"drawer2":d2})); c.close()`, databasePath, cabinetID], { encoding: 'utf8' });
    assert.equal(physicalSeed.status, 0, physicalSeed.stderr);
    const physicalDrawers = JSON.parse(physicalSeed.stdout.trim());

    mockControllerRequests.length = 0;
    await page.goto(`${baseURL}/hardware/99003/grid`);
    await page.locator('input[name="locate_mapping_verified"]').check();
    await page.locator('input[name="locate_ranges_reserved"]').check();
    await page.getByRole('button', { name: 'Save Locate safety setting' }).click();
    await page.waitForURL(`${baseURL}/hardware/99003/grid`);
    assert.match(await page.locator('body').innerText(), /Output configuration snapshot saved/);
    await page.goto(`${baseURL}/hardware/99004/grid`);
    await page.locator('input[name="locate_mapping_verified"]').check();
    await page.locator('input[name="locate_ranges_reserved"]').check();
    await page.getByRole('button', { name: 'Save Locate safety setting' }).click();
    await page.waitForURL(`${baseURL}/hardware/99004/grid`);
    assert.match(await page.locator('body').innerText(), /Output configuration snapshot saved/);
    assert.deepEqual(mockControllerRequests.slice(0, 4), ['GET /json/info', 'GET /json/cfg', 'GET /json/info', 'GET /json/cfg'], 'each assigned WLED controller must be attested independently');

    await page.goto(`${baseURL}/parts/99002`);

    const partLocationForms = page.locator('form[action="/parts/99002/locate-bin"]');
    assert.equal(await partLocationForms.count(), 3, 'part detail should show all three inventory locations');
    const binOneForm = partLocationForms.filter({ has: page.locator('input[name="bin_id"][value="99001"]') });
    const writeStart = mockControllerWrites.length;
    await binOneForm.locator('button[title="Locate this inventory location"]').click();
    await page.waitForURL(`${baseURL}/storage/drawers/${physicalDrawers.drawer1}?bin_id=99001`);
    await page.locator('.border-4.border-primary').waitFor();
    assert.match(await page.locator('.border-4.border-primary').innerText(), /#99001/);
    assert.equal(mockControllerWrites.length - writeStart, 4, 'cabinet pixels 195–210 and 95–105 should split across controllers, strings and WLED segments');
    const firstLocateCommands = mockControllerWrites.slice(writeStart, writeStart + 4).map(payload => {
      const segment = payload.seg[0];
      return [segment.id, segment.i[0], segment.i[1]];
    });
    assert.deepEqual(firstLocateCommands, [[10, 75, 81], [20, 0, 5], [20, 94, 100], [10, 0, 10]], 'serpentine output settings must map drawer logical pixels to exact controller-local WLED ranges');
    await page.getByRole('button', { name: 'Stop Locate / Turn Off Allocated LEDs' }).click();
    await page.waitForURL(`${baseURL}/storage/drawers/${physicalDrawers.drawer1}?stopped=1`);
    assert.equal(mockControllerWrites.slice(writeStart + 4, writeStart + 8).every(payload => payload.seg[0].i[2].every(channel => channel === 0)), true, 'Stop must black only the allocated ranges on every affected controller');

    await page.goto(`${baseURL}/parts/99002`);
    const binTwoForm = page.locator('form[action="/parts/99002/locate-bin"]').filter({ has: page.locator('input[name="bin_id"][value="99005"]') });
    const secondWriteStart = mockControllerWrites.length;
    await binTwoForm.locator('button[title="Locate this inventory location"]').click();
    await page.waitForURL(`${baseURL}/storage/drawers/${physicalDrawers.drawer2}?bin_id=99005`);
    await page.locator('.border-4.border-primary').waitFor();
    assert.match(await page.locator('.border-4.border-primary').innerText(), /#99005/);
    assert.equal(mockControllerWrites.length - secondWriteStart, 1);
    assert.deepEqual([mockControllerWrites[secondWriteStart].seg[0].id, mockControllerWrites[secondWriteStart].seg[0].i[0], mockControllerWrites[secondWriteStart].seg[0].i[1]], [10, 24, 34]);
    await page.getByRole('button', { name: 'Stop Locate / Turn Off Allocated LEDs' }).click();
    await page.waitForURL(`${baseURL}/storage/drawers/${physicalDrawers.drawer2}?stopped=1`);

    await page.goto(`${baseURL}/parts/99002`);
    const missingDrawerForm = page.locator('form[action="/parts/99002/locate-bin"]').filter({ has: page.locator('input[name="bin_id"][value="99006"]') });
    const writesBeforeInvalid = mockControllerWrites.length;
    await missingDrawerForm.locator('button[title="Locate this inventory location"]').click();
    await page.waitForURL(/\/parts\/99002\/locate-bin$/);
    assert.match(await page.locator('body').innerText(), /no physical drawer association/i);
    assert.equal(mockControllerWrites.length, writesBeforeInvalid, 'invalid physical location must not write to WLED');
    assert.equal(mockControllerRequests.filter(path => path.startsWith('POST ')).length, 10, 'mock should receive only scoped multi-controller activation and stop commands');

    const referenceCapacities = [200, 250, 150, 300, 100];
    const referenceControllerIDs = [99103, 99104, 99105, 99106, 99107];
    const referencePorts = [];
    for (let index = 0; index < referenceCapacities.length; index++) {
      const capacity = referenceCapacities[index];
      const half = capacity / 2;
      const controllerMock = http.createServer(async (request, response) => {
        response.setHeader('Content-Type', 'application/json');
        if (request.method === 'GET' && request.url === '/json/info') {
          response.end(JSON.stringify({ mac: `02:00:00:01:00:${String(index + 1).padStart(2, '0')}`, leds: { count: capacity, rgbw: false } }));
          return;
        }
        if (request.method === 'GET' && request.url === '/json/cfg') {
          response.end(JSON.stringify({ hw: { led: { ins: [
            { start: 0, len: half, rev: false, skip: 0, type: 22, pin: [2, -1, -1] },
            { start: half, len: half, rev: false, skip: 0, type: 22, pin: [4, -1, -1] },
          ] } } }));
          return;
        }
        if (request.method === 'GET' && request.url === '/json/state') {
          response.end(JSON.stringify({ on: true, seg: [
            { id: 10, start: 0, stop: half, on: true, fx: 0, grp: 1, spc: 0, rev: false, mi: false, of: 0 },
            { id: 20, start: half, stop: capacity, on: true, fx: 0, grp: 1, spc: 0, rev: false, mi: false, of: 0 },
          ] }));
          return;
        }
        if (request.method === 'POST' && request.url === '/json/state') {
          let body = '';
          for await (const chunk of request) body += chunk;
          referenceControllerWrites.push({ controller: index + 1, payload: JSON.parse(body) });
          response.end(JSON.stringify({ success: true }));
          return;
        }
        response.statusCode = 404;
        response.end(JSON.stringify({ error: 'not found' }));
      });
      await new Promise((resolve, reject) => controllerMock.once('error', reject).listen(0, '127.0.0.1', resolve));
      referenceMockControllers.push(controllerMock);
      referencePorts.push(controllerMock.address().port);
    }
    const referenceSeed = spawnSync('python3', ['-c', `import json,sqlite3,sys; c=sqlite3.connect(sys.argv[1]); c.execute("PRAGMA foreign_keys=ON"); configs=json.loads(sys.argv[2]); capacities=[200,250,150,300,100]; lengths=[[120,80],[125,125],[75,75],[150,150],[40,60]]; starts=[25,195,225,445,895]; counts=[10,16,10,16,16];
for i,(cfg,capacity) in enumerate(zip(configs,capacities)): c.execute("INSERT INTO controllers(id,name,ip_address,port,led_count) VALUES(?,?,?,?,?)", (cfg['id'],f'Reference Controller {i+1}', '127.0.0.1',cfg['port'],capacity));
c.execute("INSERT INTO cabinets(id,name,controller_id,coordinate_system,layout_revision,electrical_revision,display_order,archived,layout_width,layout_height) VALUES(99100,'Five-controller reference',99103,'grid',0,0,1,0,100,100)");
for i,(cfg,capacity) in enumerate(zip(configs,capacities)):
 c.execute("INSERT INTO cabinet_controllers(cabinet_id,controller_id,slot,strings_configured) VALUES(99100,?,?,1)", (cfg['id'],i));
 for j,length in enumerate(lengths[i]):
  name=f'Reference C{i+1} string {chr(65+j)}'; cur=c.execute("INSERT INTO cabinet_led_strings(cabinet_id,controller_id,name,display_order,pixel_count,layout_complete) VALUES(99100,?,?,?,?,1)", (cfg['id'],name,j,length)); sid=cur.lastrowid; first=length//2;
  for section,section_length in enumerate([first,length-first]):
   points='[{"x":5,"y":5},{"x":95,"y":5}]' if section==0 else '[{"x":95,"y":10},{"x":5,"y":10}]'; c.execute("INSERT INTO cabinet_led_path_sections(string_id,display_order,pixel_count,points_json) VALUES(?,?,?,?)", (sid,section,section_length,points));
 drawer=99111+i; bin_id=99121+i; c.execute("INSERT INTO drawers(id,cabinet_id,label,display_order,x,y,width,height,archived) VALUES(?,99100,?,?,?,0,10,10,0)", (drawer,f'Reference drawer {starts[i]}-{starts[i]+counts[i]-1}',i,i*15)); c.execute("INSERT INTO bins(id,name,container_id,led_index,width) VALUES(?,?,NULL,NULL,1)", (bin_id,f'Reference bin {i+1}')); c.execute("INSERT INTO bin_drawer_attachments(bin_id,drawer_id,x,y,width,height,display_order) VALUES(?,?,1,1,5,5,0)", (bin_id,drawer)); c.execute("INSERT INTO drawer_led_allocations(cabinet_id,drawer_id,start_pixel,pixel_count,display_order) VALUES(99100,?,?,?,0)", (drawer,starts[i],counts[i])); c.execute("INSERT INTO part_assignments(part_id,bin_id,quantity) VALUES(99002,?,1)", (bin_id,));
c.commit(); c.close()`, databasePath, JSON.stringify(referenceControllerIDs.map((id, index) => ({ id, port: referencePorts[index] })))], { encoding: 'utf8' });
    assert.equal(referenceSeed.status, 0, referenceSeed.stderr);

    await page.goto(`${baseURL}/storage/cabinets/99100/edit`);
    const referenceDestinations = [
      ['Reference drawer 25-34', 'Reference Controller 1 local 25–34 · Reference C1 string A pixels 25–34'],
      ['Reference drawer 195-210', 'Reference Controller 1 local 195–200 · Reference C1 string B pixels 75–80 → Reference Controller 2 local 1–10 · Reference C2 string A pixels 1–10'],
      ['Reference drawer 225-234', 'Reference Controller 2 local 25–34 · Reference C2 string A pixels 25–34'],
      ['Reference drawer 445-460', 'Reference Controller 2 local 245–250 · Reference C2 string B pixels 120–125 → Reference Controller 3 local 1–10 · Reference C3 string A pixels 1–10'],
      ['Reference drawer 895-910', 'Reference Controller 4 local 295–300 · Reference C4 string B pixels 145–150 → Reference Controller 5 local 1–10 · Reference C5 string A pixels 1–10'],
    ];
    for (const [drawerLabel, destination] of referenceDestinations) {
      await page.locator('aside button').filter({ hasText: drawerLabel }).first().click();
      assert.ok(await page.getByText(destination, { exact: false }).count(), `browser allocation preview missing ${destination}`);
    }

    const referenceDrawerIDs = [99111, 99112, 99113, 99114, 99115];
    const referenceBinIDs = [99121, 99122, 99123, 99124, 99125];
    for (let index = 0; index < referenceControllerIDs.length; index++) {
      await page.goto(`${baseURL}/hardware/${referenceControllerIDs[index]}/grid`);
      await page.locator('input[name="locate_mapping_verified"]').check();
      await page.locator('input[name="locate_ranges_reserved"]').check();
      await page.getByRole('button', { name: 'Save Locate safety setting' }).click();
      await page.waitForURL(`${baseURL}/hardware/${referenceControllerIDs[index]}/grid`);
      assert.match(await page.locator('body').innerText(), /Output configuration snapshot saved/);
    }

    const referenceExpectedCommands = [
      [[1, 10, 24, 34]],
      [[1, 20, 94, 100], [2, 10, 0, 10]],
      [[2, 10, 24, 34]],
      [[2, 20, 119, 125], [3, 10, 0, 10]],
      [[4, 20, 144, 150], [5, 10, 0, 10]],
    ];
    for (let index = 0; index < referenceBinIDs.length; index++) {
      await page.goto(`${baseURL}/parts/99002`);
      const form = page.locator('form[action="/parts/99002/locate-bin"]').filter({ has: page.locator(`input[name="bin_id"][value="${referenceBinIDs[index]}"]`) });
      const writeStart = referenceControllerWrites.length;
      await form.locator('button[title="Locate this inventory location"]').click();
      await page.waitForURL(`${baseURL}/storage/drawers/${referenceDrawerIDs[index]}?bin_id=${referenceBinIDs[index]}`);
      const activationWrites = referenceControllerWrites.slice(writeStart).map(({ controller, payload }) => {
        const segment = payload.seg[0];
        return [controller, segment.id, segment.i[0], segment.i[1], segment.i[2]];
      });
      assert.deepEqual(activationWrites, referenceExpectedCommands[index].map(([controller, segment, start, stop]) => [controller, segment, start, stop, [0, 0, 128]]), `mock WLED payloads for reference allocation ${index + 1}`);
      await page.getByRole('button', { name: 'Stop Locate / Turn Off Allocated LEDs' }).click();
      await page.waitForURL(`${baseURL}/storage/drawers/${referenceDrawerIDs[index]}?stopped=1`);
    }
    assert.equal(referenceControllerWrites.length, 16, 'five Locate activations and their scoped Stops should be recorded by local mock WLED controllers');

    const lifecycleSeed = spawnSync('python3', ['-c', `import sqlite3,sys; c=sqlite3.connect(sys.argv[1]); c.execute("PRAGMA foreign_keys=ON"); controllers=[(99201,'Lifecycle Controller 1',100,8000),(99202,'Lifecycle Controller 2',100,8001),(99203,'Lifecycle Controller 3',50,8002)];
for cid,name,capacity,port in controllers: c.execute("INSERT INTO controllers(id,name,ip_address,port,led_count) VALUES(?,?, '127.0.0.1',?,?)", (cid,name,port,capacity));
c.execute("INSERT INTO cabinets(id,name,controller_id,coordinate_system,electrical_revision,display_order,layout_width,layout_height) VALUES(99200,'Controller lifecycle cabinet',99201,'grid',5,2,100,100)");
for slot,cid in enumerate([99201,99202]):
 c.execute("INSERT INTO cabinet_controllers(cabinet_id,controller_id,slot,strings_configured) VALUES(99200,?,?,1)", (cid,slot)); cur=c.execute("INSERT INTO cabinet_led_strings(cabinet_id,controller_id,name,display_order,pixel_count,layout_complete) VALUES(99200,?,?,0,100,1)", (cid,f'Lifecycle string {slot+1}')); sid=cur.lastrowid;
 for section,length,points in [(0,50,'[{"x":5,"y":5},{"x":95,"y":5}]'),(1,50,'[{"x":95,"y":10},{"x":5,"y":10}]')]: c.execute("INSERT INTO cabinet_led_path_sections(string_id,display_order,pixel_count,points_json) VALUES(?,?,?,?)", (sid,section,length,points));
c.execute("INSERT INTO drawers(id,cabinet_id,label,display_order,x,y,width,height) VALUES(99210,99200,'Lifecycle drawer',0,0,0,10,10)"); c.execute("INSERT INTO drawer_led_allocations(cabinet_id,drawer_id,start_pixel,pixel_count,display_order) VALUES(99200,99210,95,11,0)"); c.commit(); c.close()`, databasePath], { encoding: 'utf8' });
    assert.equal(lifecycleSeed.status, 0, lifecycleSeed.stderr);
    const lifecycleSnapshot = () => {
      const result = spawnSync('python3', ['-c', 'import sqlite3,sys; c=sqlite3.connect(sys.argv[1]); print("revision="+str(c.execute("SELECT electrical_revision FROM cabinets WHERE id=99200").fetchone()[0])); print("members="+repr(c.execute("SELECT controller_id,slot,strings_configured FROM cabinet_controllers WHERE cabinet_id=99200 ORDER BY slot").fetchall())); print("strings="+repr(c.execute("SELECT controller_id,name,display_order,pixel_count FROM cabinet_led_strings WHERE cabinet_id=99200 ORDER BY controller_id,display_order").fetchall())); print("allocations="+repr(c.execute("SELECT drawer_id,start_pixel,pixel_count,display_order FROM drawer_led_allocations WHERE cabinet_id=99200 ORDER BY drawer_id,display_order").fetchall())); c.close()', databasePath], { encoding: 'utf8' });
      assert.equal(result.status, 0, result.stderr);
      return result.stdout.trim();
    };
    const lifecycleBefore = lifecycleSnapshot();
    const lifecyclePostStart = controllerAssignmentPosts.length;
    await page.goto(`${baseURL}/storage/cabinets/99200/edit`);
    const selectLifecycleController = async () => page.locator('label').filter({ hasText: 'Lifecycle Controller 3 (#99203)' }).locator('input[type="checkbox"]');
    await (await selectLifecycleController()).check();
    await page.getByRole('button', { name: 'Move controller up' }).nth(2).click();
    await page.locator('svg[aria-label="Interactive physical layout canvas"]')
      .evaluate(svg => svg.dispatchEvent(new MouseEvent('click', { bubbles: true })));
    await page.waitForFunction(() => window.Alpine.$data(document.querySelector('[x-data="physicalLayoutEditor()"]')).state.uiSelectionKind === '');
    const lifecyclePreview = await page.locator('[x-data="physicalLayoutEditor()"]')
      .evaluate(element => window.Alpine.$data(element).controllerMappingPreview());
    assert.deepEqual(lifecyclePreview, [{
      drawer: 'Lifecycle drawer', start: '95', count: '11', valid: true,
      before: ['Lifecycle Controller 1 local 95–100 · Lifecycle string 1 pixels 95–100', 'Lifecycle Controller 2 local 1–5 · Lifecycle string 2 pixels 1–5'],
      after: ['Lifecycle Controller 1 local 95–100 · Lifecycle string 1 pixels 95–100', 'Lifecycle Controller 3 local 1–5'],
    }]);
    assert.equal(await page.locator('input[x-model="controllerMappingImpactConfirmed"]').count(), 1, 'the visible before/after preview must provide an explicit impact confirmation');
    await page.getByRole('button', { name: 'Save controller order' }).click();
    assert.match(await page.locator('p[role=alert]').filter({ hasText: /explicit confirmation/ }).innerText(), /explicit confirmation/);
    assert.equal(controllerAssignmentPosts.length, lifecyclePostStart, 'addition with saved allocations must not submit before confirmation');
    assert.equal(lifecycleSnapshot(), lifecycleBefore, 'unconfirmed controller addition must preserve all persistent rows');
    await page.locator('input[x-model="controllerMappingImpactConfirmed"]').check();
    await page.locator('[x-data="physicalLayoutEditor()"]').evaluate(element => window.Alpine.$data(element).electricalRevision = 0);
    await page.getByRole('button', { name: 'Save controller order' }).click();
    await page.waitForURL(/controller_error=/);
    assert.match(await page.locator('p[role=alert]').filter({ hasText: /reload before saving/ }).innerText(), /reload before saving/);
    assert.equal(lifecycleSnapshot(), lifecycleBefore, 'stale-revision controller addition must preserve membership, wiring, allocations and revision');

    await page.goto(`${baseURL}/storage/cabinets/99200/edit`);
    await (await selectLifecycleController()).check();
    await page.getByRole('button', { name: 'Move controller up' }).nth(2).click();
    await page.locator('input[x-model="controllerMappingImpactConfirmed"]').check();
    await page.getByRole('button', { name: 'Save controller order' }).click();
    await page.waitForURL(/controller_saved=1/);
    const afterLifecycleAddition = lifecycleSnapshot();
    assert.match(afterLifecycleAddition, /revision=6/);
    assert.match(afterLifecycleAddition, /members=\[\(99201, 0, 1\), \(99203, 1, 1\), \(99202, 2, 1\)\]/);
        assert.match(afterLifecycleAddition, /strings=\[\(99201, 'Lifecycle string 1', 0, 100\), \(99202, 'Lifecycle string 2', 0, 100\), \(99203, 'Output 1', 0, 50\)\]/, 'assigning a legacy-capacity controller should project its output to cabinet path configuration');
    assert.match(afterLifecycleAddition, /allocations=\[\(99210, 95, 11, 0\)\]/);

    await page.goto(`${baseURL}/hardware/99203/edit`);
    const lifecycleOutputEditor = page.locator('[x-data^="controllerOutputEditor"]');
    await lifecycleOutputEditor.getByRole('button', { name: 'Remove output' }).click();
    const removeOutputResponse = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/hardware/99203/update');
    await page.getByRole('button', { name: 'Save controller' }).click();
    assert.equal((await removeOutputResponse).status(), 409, 'removing an assigned output that changes a saved drawer destination must require confirmation');
    assert.match(await page.locator('[role=alert]').innerText(), /saved drawer pixel destinations/);
    assert.equal(lifecycleSnapshot(), afterLifecycleAddition, 'unconfirmed output removal must preserve controller membership, wiring and allocations');
    await page.locator('input[name="confirm_mapping_impact"]').check();
    await page.getByRole('button', { name: 'Save controller' }).click();
    await page.waitForURL(/controller_saved=1/);
    assert.equal(Number(readOnlyRows(databasePath, 'SELECT led_count FROM controllers WHERE id=99203')[0][0]), 0);
    assert.equal(readOnlyRows(databasePath, 'SELECT id FROM cabinet_led_strings WHERE cabinet_id=99200 AND controller_id=99203').length, 0, 'removing the hardware output must remove its cabinet path projection');

    await page.goto(`${baseURL}/storage/cabinets/99200/edit`);
    await (await selectLifecycleController()).uncheck();
    const removalPreview = await page.locator('[x-data="physicalLayoutEditor()"]')
      .evaluate(element => window.Alpine.$data(element).controllerMappingPreview());
    assert.deepEqual(removalPreview, [], 'a zero-capacity output-free controller can be unassigned without moving the remaining ranges');
    const removePostStart = controllerAssignmentPosts.length;
    await page.getByRole('button', { name: 'Save controller order' }).click();
    await page.waitForURL(/controller_saved=1/);
    assert.equal(controllerAssignmentPosts.length, removePostStart + 1, 'the output-free controller should be removable after its hardware paths are cleared');
    const afterLifecycleRemoval = lifecycleSnapshot();
    assert.match(afterLifecycleRemoval, /revision=8/);
    assert.match(afterLifecycleRemoval, /members=\[\(99201, 0, 1\), \(99202, 1, 1\)\]/);
    assert.match(afterLifecycleRemoval, /strings=\[\(99201, 'Lifecycle string 1', 0, 100\), \(99202, 'Lifecycle string 2', 0, 100\)\]/);
    assert.match(afterLifecycleRemoval, /allocations=\[\(99210, 95, 11, 0\)\]/);
    const restoredLifecycleDestinations = await page.locator('[x-data="physicalLayoutEditor()"]')
      .evaluate(element => window.Alpine.$data(element).allocationDestinations({ start_pixel: '95', pixel_count: '11' }));
    assert.deepEqual(restoredLifecycleDestinations, ['Lifecycle Controller 1 local 95–100 · Lifecycle string 1 pixels 95–100', 'Lifecycle Controller 2 local 1–5 · Lifecycle string 2 pixels 1–5']);

    assert.ok(layoutPosts.length >= 4);
    assert.ok(layoutPosts.every(post => new URLSearchParams(post.body).get('csrf_token')));
    assert.ok(lifecyclePaths.every(url => !url.includes('undefined')));
    assert.deepEqual(pageErrors, [], JSON.stringify(pageErrors));
    const validationResponseErrors = consoleErrors.filter(message => message.includes('409 (Conflict)'));
    assert.equal(validationResponseErrors.length, 4, `only deliberate output-order, path, capacity and output-removal confirmation conflicts should be logged; got ${JSON.stringify(validationResponseErrors)}`);
    const invalidConfigurationResponses = consoleErrors.filter(message => message.includes('400 (Bad Request)'));
    assert.equal(invalidConfigurationResponses.length, 1, 'only the deliberately invalid custom output permutation should be logged as a bad request');
    const runtimeConsoleErrors = consoleErrors.filter(message => !message.includes("Blocked attempt to show a 'beforeunload' confirmation panel") && !message.includes('409 (Conflict)') && !message.includes('400 (Bad Request)'));
    assert.deepEqual(runtimeConsoleErrors, []);
    assert.equal(hardwareRequests.length, 15, 'only the expected Locate/Stop actions should reach hardware routes');
    console.log(JSON.stringify({ result: 'PASS', cabinetID, emptyID, archiveID, layoutSaves: layoutPosts.length, LEDMappingSaves: ledMappingPosts.length, hardwareRequests, lifecyclePaths, pageErrors, runtimeConsoleErrors, browserWarnings: consoleErrors.length - runtimeConsoleErrors.length }));
  } finally {
    if (browser) await browser.close();
    if (server) { server.kill('SIGTERM'); await new Promise(resolve => server.once('exit', resolve)); }
    for (const controller of mockControllers) if (controller.listening) await new Promise(resolve => controller.close(resolve));
    for (const controller of referenceMockControllers) await new Promise(resolve => controller.close(resolve));
    if (scratchDir) fs.rmSync(scratchDir, { recursive: true, force: true });
  }
})().catch(error => { console.error(error); process.exit(1); });

function readOnlyRows(databasePath, query, params = []) {
  const result = spawnSync('python3', ['-c', 'import json,sqlite3,sys; c=sqlite3.connect(sys.argv[1]); rows=c.execute(sys.argv[2],json.loads(sys.argv[3])).fetchall(); print(json.dumps(rows)); c.close()', databasePath, query, JSON.stringify(params)], { encoding: 'utf8' });
  if (result.status !== 0) throw new Error(`database read failed: ${result.stderr}`);
  return JSON.parse(result.stdout.trim());
}
