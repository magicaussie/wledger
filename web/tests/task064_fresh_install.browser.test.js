const assert = require('node:assert/strict');
const crypto = require('node:crypto');
const { chromium } = require('playwright');
const { spawn, spawnSync } = require('node:child_process');
const fs = require('node:fs');
const http = require('node:http');
const net = require('node:net');
const os = require('node:os');
const path = require('node:path');

(async () => {
  const repoRoot = path.resolve(__dirname, '../..');
  const scratchDir = fs.mkdtempSync(path.join(os.tmpdir(), 'wledger-task064-fresh-'));
  const databasePath = path.join(scratchDir, 'data', 'wledger.db');
  const task065RestoreEnabled = process.env.WLEDGER_TASK065_RESTORE === '1';
  let restoreScratchDir;
  let restoreServer;
  let restoreContext;
  let restorePage;
  let restoreEvidence = { status: 'NOT RUN' };
  const mocks = [];
  const mockRequests = [];
  const mockWrites = [];
  const findings = [];
  let server;
  let browser;
  let baseURL;

  try {
    fs.symlinkSync(path.join(repoRoot, 'web'), path.join(scratchDir, 'web'));
    fs.symlinkSync(path.join(repoRoot, 'locales'), path.join(scratchDir, 'locales'));

    const capacities = [200, 250, 150, 300, 100];
    const controllerEndpoints = [];
    const mockBehaviors = capacities.map(() => ({ dropNextPostResponse: false, onDropAccepted: null }));
    const mockPixels = capacities.map((capacity, index) => Array.from({ length: capacity }, () => [20 + index, 30 + index, 40 + index]));
    const initialMockPixels = structuredClone(mockPixels);
    const ownedMockPixels = capacities.map(() => new Set());
    for (let index = 0; index < capacities.length; index++) {
      const capacity = capacities[index];
      const half = capacity / 2;
      const controllerIdentity = `mock-controller-${index + 1}`;
      const mock = http.createServer(async (request, response) => {
        const endpoint = `http://127.0.0.1:${mock.address().port}${request.url}`;
        const requestRecord = {
          timestamp: new Date().toISOString(), controller: index + 1, controllerIdentity,
          method: request.method, endpoint, payload: null,
          responseBehavior: 'pending', responseStatus: null,
        };
        mockRequests.push(requestRecord);
        response.setHeader('Content-Type', 'application/json');
        if (request.method === 'GET' && request.url === '/json/info') {
          requestRecord.responseBehavior = 'normal JSON response'; requestRecord.responseStatus = 200;
          response.end(JSON.stringify({ mac: `02:00:00:00:59:${String(index + 1).padStart(2, '0')}`, leds: { count: capacity, rgbw: false } }));
          return;
        }
        if (request.method === 'GET' && request.url === '/json/cfg') {
          requestRecord.responseBehavior = 'normal JSON response'; requestRecord.responseStatus = 200;
          response.end(JSON.stringify({ hw: { led: { ins: [
            { start: 0, len: half, rev: false, skip: 0, type: 22, pin: [2, -1, -1] },
            { start: half, len: half, rev: false, skip: 0, type: 22, pin: [4, -1, -1] },
          ] } } }));
          return;
        }
        if (request.method === 'GET' && request.url === '/json/state') {
          requestRecord.responseBehavior = 'normal JSON response'; requestRecord.responseStatus = 200;
          response.end(JSON.stringify({ on: true, seg: [
            { id: 10, start: 0, stop: half, on: true, fx: 0, grp: 1, spc: 0, rev: false, mi: false, of: 0 },
            { id: 20, start: half, stop: capacity, on: true, fx: 0, grp: 1, spc: 0, rev: false, mi: false, of: 0 },
          ] }));
          return;
        }
        if (request.method === 'POST' && request.url === '/json/state') {
          let body = '';
          for await (const chunk of request) body += chunk;
          requestRecord.payload = JSON.parse(body);
          for (const segmentCommand of requestRecord.payload.seg || []) {
            const segmentStart = segmentCommand.id === 10 ? 0 : segmentCommand.id === 20 ? half : null;
            const pixelCommand = segmentCommand.i;
            if (segmentStart === null || !Array.isArray(pixelCommand) || !Array.isArray(pixelCommand[2])) throw new Error(`unsupported targeted pixel command: ${JSON.stringify(segmentCommand)}`);
            for (let pixel = pixelCommand[0]; pixel < pixelCommand[1]; pixel++) {
              const controllerPixel = segmentStart + pixel;
              mockPixels[index][controllerPixel] = pixelCommand[2].slice();
              if (pixelCommand[2].some(channel => channel !== 0)) ownedMockPixels[index].add(controllerPixel);
            }
          }
          const writeRecord = { timestamp: requestRecord.timestamp, controller: index + 1, controllerIdentity, endpoint, payload: requestRecord.payload, responseBehavior: 'success acknowledgement', responseStatus: 200 };
          mockWrites.push(writeRecord);
          const behavior = mockBehaviors[index];
          if (behavior.dropNextPostResponse) {
            behavior.dropNextPostResponse = false;
            requestRecord.responseBehavior = 'command applied; response withheld until client disconnect';
            writeRecord.responseBehavior = requestRecord.responseBehavior;
            const accepted = behavior.onDropAccepted;
            behavior.onDropAccepted = null;
            if (accepted) accepted(requestRecord);
            await new Promise(resolve => response.once('close', resolve));
            requestRecord.responseBehavior = 'command applied; client disconnected before response';
            writeRecord.responseBehavior = requestRecord.responseBehavior;
            writeRecord.responseStatus = null;
            return;
          }
          requestRecord.responseBehavior = 'success acknowledgement'; requestRecord.responseStatus = 200;
          response.end(JSON.stringify({ success: true }));
          return;
        }
        requestRecord.responseBehavior = 'not found'; requestRecord.responseStatus = 404;
        response.statusCode = 404;
        response.end(JSON.stringify({ error: 'not found' }));
      });
      await new Promise((resolve, reject) => mock.once('error', reject).listen(0, '127.0.0.1', resolve));
      mocks.push(mock);
      controllerEndpoints.push({ host: '127.0.0.1', port: mock.address().port });
    }

    const appPort = await availablePort();
    baseURL = `http://127.0.0.1:${appPort}`;
    const binaryPath = path.join(scratchDir, 'wledger');
    const build = spawnSync(process.env.GO_BIN || 'go', ['build', '-tags', 'fts5', '-o', binaryPath, './cmd/server'], {
      cwd: repoRoot,
      env: { ...process.env, CGO_ENABLED: '1' },
      encoding: 'utf8',
    });
    if (build.status !== 0) throw new Error(`disposable server build failed: ${build.stderr}`);

    const startServer = (workingDirectory = scratchDir, port = appPort) => {
      const child = spawn(binaryPath, [], {
        cwd: workingDirectory,
        env: { ...process.env, WLEDGER_INSECURE_COOKIES: '1', WLEDGER_PORT: String(port) },
        stdio: ['ignore', 'pipe', 'pipe'],
      });
      let output = '';
      child.stdout.on('data', chunk => { output += chunk.toString(); });
      child.stderr.on('data', chunk => { output += chunk.toString(); });
      child.wledgerOutput = () => output;
      return child;
    };
    server = startServer();
    await waitForServer(baseURL, server);
    assert.ok(fs.statSync(path.join(scratchDir, 'app', 'uploads')).isDirectory(), 'first-install startup should create uploads only after recovery finds no journal');

    browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH || '/bin/chromium', headless: true, args: ['--no-sandbox'] });
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
    const page = await context.newPage();
    const pageErrors = [];
    const consoleErrors = [];
    const appMutations = [];
    page.on('pageerror', error => pageErrors.push({ url: page.url(), message: error.message }));
    page.on('console', message => { if (message.type() === 'error') consoleErrors.push({ url: page.url(), message: message.text() }); });
    page.on('request', request => {
      if (['POST', 'PUT', 'PATCH', 'DELETE'].includes(request.method()) && new URL(request.url()).origin === baseURL) {
        appMutations.push({ method: request.method(), path: new URL(request.url()).pathname });
      }
    });
    page.on('dialog', dialog => dialog.accept());

    await page.goto(`${baseURL}/setup`);
    assert.equal(new URL(page.url()).pathname, '/setup');
    const setupForm = page.locator('form[action="/setup"]');
    await setupForm.locator('input[name="email"]').fill('not-an-email');
    await setupForm.locator('input[name="password"]').fill('short');
    assert.equal(await setupForm.evaluate(form => form.checkValidity()), false, 'invalid first-time setup values must be blocked by browser validation');
    await setupForm.locator('input[name="email"]').fill('ui-only-admin@example.invalid');
    await setupForm.locator('input[name="password"]').fill('ui-only-safe-password-123');
    await setupForm.locator('button').click();
    await page.waitForURL(`${baseURL}/`);
    assert.match(await page.title(), /WLEDger/, 'dashboard should load immediately after setup');
    await page.locator('form[action="/logout"] button').click();
    await page.waitForURL(`${baseURL}/login`);
    await page.locator('form[action="/login"] input[name="email"]').fill('ui-only-admin@example.invalid');
    await page.locator('form[action="/login"] input[name="password"]').fill('incorrect-password');
    await page.locator('form[action="/login"] button').click();
    await page.waitForURL(`${baseURL}/login`);
    assert.match(await page.locator('body').innerText(), /Invalid email or password/);
    await page.goto(`${baseURL}/login`);
    await page.locator('form[action="/login"] input[name="email"]').fill('ui-only-admin@example.invalid');
    await page.locator('form[action="/login"] input[name="password"]').fill('ui-only-safe-password-123');
    await page.locator('form[action="/login"] button').click();
    await page.waitForURL(`${baseURL}/`);

    const emptyCounts = readOnlyCounts(databasePath);
    for (const table of ['controllers', 'cabinets', 'drawers', 'bins', 'containers', 'cabinet_controllers', 'bin_drawer_attachments', 'parts', 'part_assignments', 'cabinet_led_strings', 'cabinet_led_path_sections', 'drawer_led_allocations']) {
      assert.equal(emptyCounts[table], 0, `new-install table ${table} must begin empty`);
    }
    const migrationVersions = fs.readdirSync(path.join(repoRoot, 'sql/schema'))
      .map(name => name.match(/^(\d+)_.*\.sql$/))
      .filter(Boolean)
      .map(match => Number(match[1]))
      .sort((left, right) => left - right);
    const appliedMigrationVersions = readOnlyRows(databasePath, 'SELECT version_id FROM goose_db_version WHERE is_applied=1 AND version_id>0 ORDER BY version_id').map(row => Number(row[0]));
    assert.deepEqual(appliedMigrationVersions, migrationVersions, 'fresh startup should apply every repository migration exactly once and be current');

    await page.goto(`${baseURL}/hardware`);
    assert.match(await page.locator('body').innerText(), /No cabinets yet/i);

    const controllers = [];
    for (let index = 0; index < controllerEndpoints.length; index++) {
      await page.getByRole('button', { name: 'Add WLED Controller' }).click();
      const form = page.locator('#add_controller_modal form[action="/hardware"]');
      await form.locator('input[name="name"]').fill(`UI Controller ${index + 1}`);
      await form.locator('input[name="ip_address"]').fill(controllerEndpoints[index].host);
      await form.locator('input[name="port"]').fill(String(controllerEndpoints[index].port));
      await form.locator('button.btn-primary').click();
      await page.waitForURL(`${baseURL}/hardware`);
      const rows = readOnlyRows(databasePath, 'SELECT id, name, ip_address, port, led_count FROM controllers WHERE name = ? ORDER BY id', [`UI Controller ${index + 1}`]);
      assert.equal(rows.length, 1, `controller ${index + 1} should be created through its browser form`);
      controllers.push({ id: Number(rows[0][0]), name: rows[0][1], ip: rows[0][2], port: Number(rows[0][3]), capacity: Number(rows[0][4]) });
    }

    assert.ok(await page.locator('form[action^="/hardware/"][action$="/delete"]').count() >= 5);
    await page.goto(`${baseURL}/hardware/${controllers[0].id}/edit`);
    const editForm = page.locator(`form[action="/hardware/${controllers[0].id}/update"]`);
    await editForm.locator('input[name="name"]').fill('UI Controller A Edited');
    await editForm.locator('button[type="submit"]').click();
    await page.waitForURL(/controller_saved=1/);
    const editedController = readOnlyRows(databasePath, 'SELECT id,name,ip_address,port FROM controllers WHERE id=?', [controllers[0].id]);
    assert.deepEqual(editedController[0].map((value, index) => index === 0 || index === 3 ? Number(value) : value), [controllers[0].id, 'UI Controller A Edited', controllers[0].ip, controllers[0].port], 'controller edit must preserve its ID and endpoint');
    controllers[0].name = 'UI Controller A Edited';

    const controllerCountBeforeInvalid = Number(readOnlyRows(databasePath, 'SELECT COUNT(*) FROM controllers')[0][0]);
    await page.goto(`${baseURL}/hardware`);
    await page.getByRole('button', { name: 'Add WLED Controller' }).click();
    const invalidForm = page.locator('#add_controller_modal form[action="/hardware"]');
    await invalidForm.locator('input[name="name"]').fill('UI invalid port probe');
    await invalidForm.locator('input[name="ip_address"]').fill('127.0.0.1');
    await invalidForm.locator('input[name="port"]').fill('70000');
    assert.equal(await invalidForm.evaluate(form => form.checkValidity()), false, 'out-of-range port must fail form validation');
    const invalidPost = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/hardware');
    await invalidForm.evaluate(form => fetch(form.action, { method: 'POST', body: new FormData(form), redirect: 'manual' }));
    assert.equal((await invalidPost).status(), 400, 'server must reject invalid TCP ports even if browser validation is bypassed');
    assert.equal(Number(readOnlyRows(databasePath, 'SELECT COUNT(*) FROM controllers')[0][0]), controllerCountBeforeInvalid, 'invalid port must not create a controller');

    await page.goto(`${baseURL}/hardware`);
    await page.getByRole('button', { name: 'Add WLED Controller' }).click();
    const duplicateForm = page.locator('#add_controller_modal form[action="/hardware"]');
    await duplicateForm.locator('input[name="name"]').fill('UI duplicate endpoint probe');
    await duplicateForm.locator('input[name="ip_address"]').fill(controllers[0].ip);
    await duplicateForm.locator('input[name="port"]').fill(String(controllers[0].port));
    const duplicateResponse = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/hardware');
    await duplicateForm.locator('button.btn-primary').click();
    assert.equal((await duplicateResponse).status(), 400, 'duplicate controller endpoints must be rejected');
    assert.equal(readOnlyRows(databasePath, 'SELECT id FROM controllers WHERE name=?', ['UI duplicate endpoint probe']).length, 0);

    await page.goto(`${baseURL}/storage/cabinets`);
    const cabinetName = `UI-only cabinet ${Date.now()}`;
    await page.locator('form[action="/storage/cabinets"] input[name="name"]').fill(cabinetName);
    await page.getByRole('button', { name: 'Create cabinet' }).click();
    await page.waitForURL(/\/storage\/cabinets\/\d+\/edit$/);
    const cabinetID = Number(page.url().match(/cabinets\/(\d+)\/edit$/)[1]);
    const width = page.locator('input[x-model\\.number="state.width"]');
    const height = page.locator('input[x-model\\.number="state.height"]');
    await width.fill('200'); await width.press('Tab');
    await height.fill('120'); await height.press('Tab');

    const canvas = page.locator('svg[aria-label="Interactive physical layout canvas"]');
    const screenPoint = async (x, y) => canvas.evaluate((svg, point) => {
      const value = svg.createSVGPoint(); value.x = point.x; value.y = point.y;
      const screen = value.matrixTransform(svg.getScreenCTM());
      return { x: screen.x, y: screen.y };
    }, { x, y });
    const drag = async (x1, y1, x2, y2) => {
      const start = await screenPoint(x1, y1);
      const end = await screenPoint(x2, y2);
      await page.mouse.move(start.x, start.y);
      await page.mouse.down();
      await page.mouse.move(end.x, end.y, { steps: 8 });
      await page.mouse.up();
    };
    const drawers = [];
    for (let index = 0; index < 5; index++) {
      await page.getByRole('button', { name: 'Drawer', exact: true }).click();
      const x = 5 + index * 35;
      await drag(x, 10, x + 25, 28);
      const label = page.locator('input[x-model="selectedDrawer().label"]');
      const drawerLabel = `Installation drawer ${index + 1}`;
      await label.fill(drawerLabel);
      const selectedX = page.locator('input[x-model\\.number="selectedDrawer().x"]');
      const selectedY = page.locator('input[x-model\\.number="selectedDrawer().y"]');
      const selectedWidth = page.locator('input[x-model\\.number="selectedDrawer().width"]');
      const selectedHeight = page.locator('input[x-model\\.number="selectedDrawer().height"]');
      await selectedX.fill(String(x)); await selectedX.press('Tab');
      await selectedY.fill('10'); await selectedY.press('Tab');
      await selectedWidth.fill('25'); await selectedWidth.press('Tab');
      await selectedHeight.fill('18'); await selectedHeight.press('Tab');
      drawers.push(drawerLabel);
    }
    await page.getByRole('button', { name: 'Save layout' }).click();
    await page.waitForURL(`${baseURL}/storage/cabinets/${cabinetID}`);
    let cabinetRows = readOnlyRows(databasePath, 'SELECT name, layout_width, layout_height FROM cabinets WHERE id = ?', [cabinetID]);
    assert.deepEqual(cabinetRows[0], [cabinetName, 200, 120]);
    let drawerRows = readOnlyRows(databasePath, 'SELECT id, label, x, y, width, height FROM drawers WHERE cabinet_id = ? AND archived = 0 ORDER BY display_order, id', [cabinetID]);
    assert.equal(drawerRows.length, 5, 'five drawers should be saved through the editor');
    for (let index = 0; index < drawerRows.length; index++) assert.equal(drawerRows[index][1], drawers[index]);

    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    const membershipLabels = page.locator('label').filter({ hasText: /^UI Controller/ });
    for (const controller of controllers) {
      const checkbox = page.locator('label').filter({ hasText: `${controller.name} (#${controller.id})` }).locator('input[type="checkbox"]');
      await checkbox.check();
    }
    assert.equal(await page.locator('label').filter({ hasText: /UI Controller/ }).locator('input[type="checkbox"]:checked').count(), 5);
    await page.getByRole('button', { name: 'Save controller order' }).click();
    await page.waitForURL(/controller_saved=1/);
    const memberships = readOnlyRows(databasePath, 'SELECT controller_id, slot FROM cabinet_controllers WHERE cabinet_id = ? ORDER BY slot', [cabinetID]);
    assert.deepEqual(memberships.map(row => Number(row[0])), controllers.map(controller => controller.id));
    assert.deepEqual(memberships.map(row => Number(row[1])), [0, 1, 2, 3, 4]);

    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    assert.equal(await page.locator('button[aria-label="Move controller up"]').count(), 5);
    await page.getByRole('button', { name: 'Move controller down' }).first().click();
    await page.getByRole('button', { name: 'Save controller order' }).click();
    await page.waitForURL(/controller_saved=1/);
    let reordered = readOnlyRows(databasePath, 'SELECT controller_id FROM cabinet_controllers WHERE cabinet_id = ? ORDER BY slot', [cabinetID]).map(row => Number(row[0]));
    assert.deepEqual(reordered, [controllers[1].id, controllers[0].id, controllers[2].id, controllers[3].id, controllers[4].id]);
    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    await page.getByRole('button', { name: 'Move controller up' }).nth(1).click();
    await page.getByRole('button', { name: 'Save controller order' }).click();
    await page.waitForURL(/controller_saved=1/);
    reordered = readOnlyRows(databasePath, 'SELECT controller_id FROM cabinet_controllers WHERE cabinet_id = ? ORDER BY slot', [cabinetID]).map(row => Number(row[0]));
    assert.deepEqual(reordered, controllers.map(controller => controller.id), 'controller order should be restored through the UI');

    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    const configureButtons = page.getByRole('button', { name: 'Configure physical strings' });
    for (let index = 0; index < controllers.length; index++) {
      await configureButtons.first().click();
      const panel = page.locator('div.rounded.bg-base-100.p-2.space-y-2').filter({ hasText: `Controller ${index + 1}: ${controllers[index].name}` }).first();
      await panel.getByRole('button', { name: '＋ Add string' }).click();
      const stringName = `Physical string ${index + 1}`;
      const capacity = capacities[index];
      await panel.locator('input[x-model="string.name"]').fill(stringName);
      await panel.locator('input[x-model="string.pixel_count"]').fill(String(capacity));
      await panel.getByRole('button', { name: '＋ Add path section' }).click();
      await panel.getByRole('button', { name: '＋ Add path section' }).click();
      const sections = panel.locator('input[x-model="section.pixel_count"]');
      await sections.nth(0).fill(String(capacity / 2));
      await sections.nth(1).fill(String(capacity / 2));
      for (let sectionIndex = 0; sectionIndex < 2; sectionIndex++) {
        await panel.getByRole('button', { name: 'Draw path' }).nth(sectionIndex).click();
        await canvas.scrollIntoViewIfNeeded();
        const y = 20 + index * 18 + sectionIndex * 5;
        if (sectionIndex === 0) {
          const start = await screenPoint(10, y); const end = await screenPoint(190, y);
          await page.mouse.click(start.x, start.y); await page.mouse.click(end.x, end.y);
        } else {
          const start = await screenPoint(190, y); const end = await screenPoint(10, y);
          await page.mouse.click(start.x, start.y); await page.mouse.click(end.x, end.y);
        }
        await canvas.evaluate(svg => svg.dispatchEvent(new MouseEvent('dblclick', { bubbles: true })));
      }
      await panel.locator('input[x-model="string.layout_complete"]').check();
    }
    await page.getByRole('button', { name: 'Save strings and wiring paths' }).click();
    try {
      await page.waitForURL(/wiring_saved=1/, { timeout: 5000 });
    } catch (error) {
      throw new Error(`wiring save did not complete: url=${page.url()} body=${(await page.locator('body').innerText()).slice(-5000)} cause=${error.message}`);
    }
    const stringRows = readOnlyRows(databasePath, 'SELECT controller_id, pixel_count FROM cabinet_led_strings WHERE cabinet_id = ? ORDER BY controller_id, display_order', [cabinetID]);
    assert.deepEqual(stringRows.map(row => [Number(row[0]), Number(row[1])]), controllers.map((controller, index) => [controller.id, capacities[index]]));
    const sectionRows = readOnlyRows(databasePath, 'SELECT string_id, display_order, pixel_count, points_json FROM cabinet_led_path_sections WHERE string_id IN (SELECT id FROM cabinet_led_strings WHERE cabinet_id = ?) ORDER BY string_id, display_order', [cabinetID]);
    assert.equal(sectionRows.length, 10, 'each of the five UI-configured strings should have two directed path sections');
    assert.equal(sectionRows.reduce((total, row) => total + Number(row[2]), 0), 1000);

    const allocationInputs = [
      { drawer: 0, start: 25, count: 10 },
      { drawer: 1, start: 195, count: 16 },
      { drawer: 2, start: 225, count: 10 },
      { drawer: 3, start: 445, count: 16 },
      { drawer: 4, start: 895, count: 16 },
    ];
    for (const allocation of allocationInputs) {
      await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
      await page.locator('aside button').filter({ hasText: drawers[allocation.drawer] }).first().click();
      const startInput = page.locator('input[x-model="ledRangesFor(selectedDrawer())[0].start_pixel"]');
      const countInput = page.locator('input[x-model="ledRangesFor(selectedDrawer())[0].pixel_count"]');
      await startInput.fill(String(allocation.start));
      await countInput.fill(String(allocation.count));
      await page.getByRole('button', { name: 'Save drawer pixels' }).click();
      await page.waitForURL(/led_saved=1/);
    }
    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    await page.locator('aside button').filter({ hasText: drawers[0] }).first().click();
    await page.getByText('Advanced: additional non-contiguous ranges').click();
    await page.getByRole('button', { name: '＋ Add another range' }).click();
    await page.locator('input[x-model="range.start_pixel"]').fill('50');
    await page.locator('input[x-model="range.pixel_count"]').fill('5');
    await page.getByRole('button', { name: 'Save drawer pixels' }).click();
    await page.waitForURL(/led_saved=1/);
    const allocations = readOnlyRows(databasePath, 'SELECT drawer_id, start_pixel, pixel_count, display_order FROM drawer_led_allocations WHERE cabinet_id = ? ORDER BY drawer_id, display_order', [cabinetID]);
    assert.deepEqual(allocations.map(row => [Number(row[1]), Number(row[2])]), [[25, 10], [50, 5], [195, 16], [225, 10], [445, 16], [895, 16]]);

    const invalidAllocationCases = [
      { start: 0, count: 5, expected: /Start must be a whole number of at least 1/ },
      { start: 25, count: 10, expected: /ranges overlap/i },
      { start: 997, count: 5, expected: /exceeds cabinet capacity \(1000 pixels\)/ },
    ];
    for (const invalid of invalidAllocationCases) {
      await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
      await page.locator('aside button').filter({ hasText: drawers[4] }).first().click();
      await page.locator('input[x-model="ledRangesFor(selectedDrawer())[0].start_pixel"]').fill(String(invalid.start));
      await page.locator('input[x-model="ledRangesFor(selectedDrawer())[0].pixel_count"]').fill(String(invalid.count));
      assert.match(await page.locator('p').filter({ hasText: invalid.expected }).first().innerText(), invalid.expected);
      await page.getByRole('button', { name: 'Save drawer pixels' }).click();
      assert.match(await page.locator('p[role="alert"]').filter({ hasText: invalid.expected }).first().innerText(), invalid.expected);
      assert.deepEqual(readOnlyRows(databasePath, 'SELECT drawer_id, start_pixel, pixel_count, display_order FROM drawer_led_allocations WHERE cabinet_id = ? ORDER BY drawer_id, display_order', [cabinetID]), allocations, 'invalid allocations must leave saved database rows unchanged');
    }

    const stalePage = await context.newPage();
    await stalePage.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    await page.locator('aside button').filter({ hasText: drawers[3] }).first().click();
    await page.locator('input[x-model="ledRangesFor(selectedDrawer())[0].start_pixel"]').fill('446');
    await page.locator('input[x-model="ledRangesFor(selectedDrawer())[0].pixel_count"]').fill('15');
    await page.getByRole('button', { name: 'Save drawer pixels' }).click();
    await page.waitForURL(/led_saved=1/);
    await stalePage.locator('aside button').filter({ hasText: drawers[4] }).first().click();
    await stalePage.locator('input[x-model="ledRangesFor(selectedDrawer())[0].start_pixel"]').fill('896');
    await stalePage.locator('input[x-model="ledRangesFor(selectedDrawer())[0].pixel_count"]').fill('15');
    await stalePage.getByRole('button', { name: 'Save drawer pixels' }).click();
    await stalePage.waitForURL(/led_error=/);
    const staleRevisionError = new URL(stalePage.url()).searchParams.get('led_error');
    assert.ok(staleRevisionError, `stale allocation save should show an error: ${stalePage.url()}`);
    const afterStale = readOnlyRows(databasePath, 'SELECT drawer_id, start_pixel, pixel_count, display_order FROM drawer_led_allocations WHERE cabinet_id = ? ORDER BY drawer_id, display_order', [cabinetID]);
    assert.deepEqual(afterStale.map(row => row.map(Number)), [[1, 25, 10, 0], [1, 50, 5, 1], [2, 195, 16, 0], [3, 225, 10, 0], [4, 446, 15, 0], [5, 895, 16, 0]], 'stale save must not change Drawer 5 mapping');
    await stalePage.close();
    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    await page.locator('aside button').filter({ hasText: drawers[3] }).first().click();
    await page.locator('input[x-model="ledRangesFor(selectedDrawer())[0].start_pixel"]').fill('445');
    await page.locator('input[x-model="ledRangesFor(selectedDrawer())[0].pixel_count"]').fill('16');
    await page.getByRole('button', { name: 'Save drawer pixels' }).click();
    await page.waitForURL(/led_saved=1/);
    const finalAllocations = readOnlyRows(databasePath, 'SELECT drawer_id, start_pixel, pixel_count, display_order FROM drawer_led_allocations WHERE cabinet_id = ? ORDER BY drawer_id, display_order', [cabinetID]);
    assert.deepEqual(finalAllocations.map(row => row.map(Number)), [[1, 25, 10, 0], [1, 50, 5, 1], [2, 195, 16, 0], [3, 225, 10, 0], [4, 445, 16, 0], [5, 895, 16, 0]]);

    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    const layoutEditorData = JSON.parse(await page.locator('#layout-editor-data').textContent());
    assert.equal(layoutEditorData.Cabinet.name, cabinetName);
    assert.equal(layoutEditorData.Drawers.length, 5);

    const drawerIDs = drawerRows.map(row => Number(row[0]));
    const containersBeforePhysicalBins = Number(readOnlyRows(databasePath, 'SELECT COUNT(*) FROM containers')[0][0]);
    for (let drawerIndex = 0; drawerIndex < drawerIDs.length; drawerIndex++) {
      await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
      const drawerButton = page.locator('aside button').filter({ hasText: drawers[drawerIndex] }).first();
      await drawerButton.dblclick();
      await page.getByText('Physical bins', { exact: true }).waitFor();
      for (let localBin = 0; localBin < 2; localBin++) {
        const name = `UI Physical Bin ${drawerIndex * 2 + localBin + 1}`;
        await page.locator('input[x-model="newBinDraft.name"]').fill(name);
        await page.locator('input[x-model\\.number="newBinDraft.x"]').fill(localBin === 0 ? '1' : '10');
        await page.locator('input[x-model\\.number="newBinDraft.y"]').fill('1');
        await page.locator('input[x-model\\.number="newBinDraft.width"]').fill('6');
        await page.locator('input[x-model\\.number="newBinDraft.height"]').fill('6');
        await page.getByRole('button', { name: '＋ Add bin' }).click();
        await page.waitForURL(new RegExp(`drawer_id=${drawerIDs[drawerIndex]}&bin_id=\\d+`));
      }
    }
    const inventoryBins = readOnlyRows(databasePath, 'SELECT id, name, container_id, led_index FROM bins ORDER BY id');
    assert.equal(inventoryBins.length, 10, 'ten bins must be created through the independent physical-bin UI');
    assert.ok(inventoryBins.every(row => row[2] === null && row[3] === null), 'physical bin creation must not require legacy electrical containers or pixel indices');
    const binAttachments = readOnlyRows(databasePath, 'SELECT bin_id, drawer_id FROM bin_drawer_attachments ORDER BY bin_id');
    assert.equal(binAttachments.length, 10, 'physical bin creation should attach all ten bins to drawers');
    assert.deepEqual(binAttachments.map(row => Number(row[1])), drawerIDs.flatMap(id => [id, id]), 'two physical bins should attach to each drawer');
    assert.equal(Number(readOnlyRows(databasePath, 'SELECT COUNT(*) FROM containers')[0][0]), containersBeforePhysicalBins, 'physical bin creation must not create or modify electrical containers');
    for (const [binID] of inventoryBins) {
      const qr = await page.request.get(`${baseURL}/bin/${binID}/qr`);
      assert.equal(qr.status(), 200, `QR route for stable physical bin ${binID}`);
      assert.match(qr.headers()['content-type'], /image\/png/);
    }

    const createdParts = [];
    const testImage = Buffer.from(await page.evaluate(async () => {
      const element = document.createElement('canvas'); element.width = 2; element.height = 2;
      const context = element.getContext('2d'); context.fillStyle = '#4f46e5'; context.fillRect(0, 0, 2, 2);
      const blob = await new Promise(resolve => element.toBlob(resolve, 'image/png'));
      return Array.from(new Uint8Array(await blob.arrayBuffer()));
    }));
    for (let index = 0; index < 10; index++) {
      await page.goto(`${baseURL}/parts/new`);
      const form = page.locator('form[action="/parts"]');
      await form.locator('input[name="name"]').fill(`UI Part ${index + 1}`);
      await form.locator('textarea[name="description"]').fill(`Fresh-install acceptance part ${index + 1}`);
      form.locator('input[name="part_number"]').fill(`UI-064-${String(index + 1).padStart(3, '0')}`);
      if (index === 0) await form.locator('input[name="image"]').setInputFiles({ name: 'ui-install.png', mimeType: 'image/png', buffer: testImage });
      await form.locator('button[type="submit"]').click();
      await page.waitForURL(/\/parts\/\d+$/);
      const partID = Number(page.url().match(/\/parts\/(\d+)$/)[1]);
      await page.locator('button[onclick="stock_modal.showModal()"]').click();
      const binID = Number(inventoryBins[index][0]);
      const pickerButton = page.locator(`#visual_bin_picker button[data-bin-id="${binID}"]`);
      await pickerButton.waitFor({ state: 'visible' });
      await pickerButton.click();
      const stockForm = page.locator(`form[action="/parts/${partID}/assign"]`);
      await stockForm.locator('input[name="quantity"]').fill(index === 0 ? '8' : '5');
      const navigation = page.waitForNavigation();
      await stockForm.locator('button[type="submit"]').click();
      await navigation;
      createdParts.push({ id: partID, binID, name: `UI Part ${index + 1}` });
    }
    const partCount = Number(readOnlyRows(databasePath, 'SELECT COUNT(*) FROM parts')[0][0]);
    assert.equal(partCount, 10, 'all ten parts should be created through browser forms');
    const assignmentRows = readOnlyRows(databasePath, 'SELECT part_id, bin_id, quantity FROM part_assignments ORDER BY part_id, bin_id');
    assert.equal(assignmentRows.length, 10, 'all parts should receive browser-created stock assignments');
    for (let index = 0; index < createdParts.length; index++) {
      assert.deepEqual(assignmentRows[index].map(Number), [createdParts[index].id, createdParts[index].binID, index === 0 ? 8 : 5]);
    }

    const firstPart = createdParts[0];
    await page.goto(`${baseURL}/parts/${firstPart.id}`);
    const firstStockRow = page.locator('tr').filter({ hasText: inventoryBins[0][1] });
    await firstStockRow.locator('button[hx-post$="delta=1"]').click();
    await page.waitForFunction(name => Array.from(document.querySelectorAll('tr')).some(row => row.innerText.includes(name) && row.innerText.includes('9')), inventoryBins[0][1]);
    await firstStockRow.locator('button[hx-post$="delta=-1"]').click();
    await page.waitForFunction(name => Array.from(document.querySelectorAll('tr')).some(row => row.innerText.includes(name) && row.innerText.includes('8')), inventoryBins[0][1]);
    const sourceAssignment = Number(readOnlyRows(databasePath, 'SELECT id FROM part_assignments WHERE part_id = ? AND bin_id = ?', [firstPart.id, firstPart.binID])[0][0]);
    await page.locator(`button[onclick="move_stock_modal_${sourceAssignment}.showModal()"]`).click();
    const movePicker = page.locator(`#move_stock_modal_${sourceAssignment}_visual_bin_picker button[data-bin-id="${inventoryBins[1][0]}"]`);
    await movePicker.waitFor({ state: 'visible' });
    await movePicker.click();
    const moveNavigation = page.waitForNavigation();
    await page.locator(`button[form="move_stock_modal_${sourceAssignment}_form"]`).click();
    await moveNavigation;
    const movedAssignments = readOnlyRows(databasePath, 'SELECT bin_id, quantity FROM part_assignments WHERE part_id = ? ORDER BY bin_id', [firstPart.id]);
    assert.deepEqual(movedAssignments.map(row => row.map(Number)), [[Number(inventoryBins[1][0]), 8]], 'moving stock should preserve quantity and change its bin through the browser');

    await page.goto(`${baseURL}/parts`);
    await page.locator('#scan-target-q').fill('UI Part 10');
    await page.waitForFunction(() => new URL(location.href).searchParams.get('q') === 'UI Part 10' && document.body.innerText.includes('UI Part 10'));
    assert.doesNotMatch(await page.locator('#parts-grid').innerText(), /UI Part 1(?!0)/, 'search should filter to the matching part');

    await page.goto(`${baseURL}/settings`);
    const roleUsers = [
      { role: 'editor', email: 'task064-editor@example.invalid' },
      { role: 'viewer', email: 'task064-viewer@example.invalid' },
    ];
    for (const roleUser of roleUsers) {
      await page.locator('button[onclick="user_add_modal.showModal()"]:visible').click();
      const form = page.locator('form[action="/settings/users"]');
      await form.locator('input[name="email"]').fill(roleUser.email);
      await form.locator('input[name="temp_password"]').fill('task064-safe-password');
      await form.locator('select[name="role"]').selectOption(roleUser.role);
      await form.locator('button[type="submit"]').click();
      await page.waitForURL(`${baseURL}/settings`);
      roleUser.id = Number(readOnlyRows(databasePath, 'SELECT id FROM users WHERE email=? AND role=?', [roleUser.email, roleUser.role])[0][0]);
      assert.ok(roleUser.id, `${roleUser.role} should be created via the admin UI`);
    }

    const editorContext = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
    const editorPage = await editorContext.newPage();
    await editorPage.goto(`${baseURL}/login`);
    await editorPage.locator('form[action="/login"] input[name="email"]').fill(roleUsers[0].email);
    await editorPage.locator('form[action="/login"] input[name="password"]').fill('task064-safe-password');
    await editorPage.locator('form[action="/login"] button').click();
    await editorPage.waitForURL(`${baseURL}/force-reset`);
    await editorPage.locator('form[action="/force-reset"] input[name="new_password"]').fill('task064-editor-new-password');
    await editorPage.locator('form[action="/force-reset"] input[name="confirm_password"]').fill('task064-editor-new-password');
    await editorPage.locator('form[action="/force-reset"] button').click();
    await editorPage.waitForURL(`${baseURL}/`);
    await editorPage.goto(`${baseURL}/parts/new`);
    assert.equal(await editorPage.locator('form[action="/parts"]').count(), 1, 'editor may create permitted inventory');
    await editorPage.locator('form[action="/parts"] input[name="name"]').fill('Task 064 editor-created part');
    await editorPage.locator('form[action="/parts"] button[type="submit"]').click();
    await editorPage.waitForURL(/\/parts\/\d+$/);
    await editorPage.goto(`${baseURL}/hardware`);
    assert.equal(await editorPage.getByRole('button', { name: 'Add WLED controller' }).count(), 0, 'editor must not manage controllers');
    const editorToken = await editorPage.locator('meta[name="csrf-token"]').getAttribute('content');
    const editorControllerPost = await editorPage.evaluate(async token => {
      const body = new URLSearchParams({ csrf_token: token, name: 'Editor forbidden controller', ip_address: '127.0.0.1', port: '80' });
      return (await fetch('/hardware', { method: 'POST', body })).status;
    }, editorToken);
    assert.equal(editorControllerPost, 403, 'only administrators may create controllers');
    await editorContext.close();

    const viewerContext = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
    const viewerPage = await viewerContext.newPage();
    await viewerPage.goto(`${baseURL}/login`);
    await viewerPage.locator('form[action="/login"] input[name="email"]').fill(roleUsers[1].email);
    await viewerPage.locator('form[action="/login"] input[name="password"]').fill('task064-safe-password');
    await viewerPage.locator('form[action="/login"] button').click();
    await viewerPage.waitForURL(`${baseURL}/force-reset`);
    await viewerPage.locator('form[action="/force-reset"] input[name="new_password"]').fill('task064-viewer-new-password');
    await viewerPage.locator('form[action="/force-reset"] input[name="confirm_password"]').fill('task064-viewer-new-password');
    await viewerPage.locator('form[action="/force-reset"] button').click();
    await viewerPage.waitForURL(`${baseURL}/`);
    const viewerToken = await viewerPage.locator('meta[name="csrf-token"]').getAttribute('content');
    const viewerPartPost = await viewerPage.evaluate(async token => {
      const body = new URLSearchParams({ csrf_token: token, name: 'Viewer forbidden part' });
      return (await fetch('/parts', { method: 'POST', body })).status;
    }, viewerToken);
    assert.equal(viewerPartPost, 403, 'viewer must not create inventory via direct mutation requests');
    const viewerPartPage = await viewerPage.goto(`${baseURL}/parts/new`);
    assert.equal(viewerPartPage.status(), 403, 'viewer must not enter inventory mutation routes');
    await viewerPage.goto(`${baseURL}/hardware`);
    assert.equal(await viewerPage.getByRole('button', { name: 'Add WLED controller' }).count(), 0, 'viewer must not manage controllers');
    await viewerContext.close();

    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}`);
    assert.equal(await page.locator('label[for="main-drawer"]:visible').count(), 1, 'mobile navigation remains available at a narrow viewport');
    await page.setViewportSize({ width: 1440, height: 1000 });

    const persistedControllerCapacities = readOnlyRows(databasePath, 'SELECT id, led_count FROM controllers ORDER BY id').map(row => row.map(Number));
    let mappingAttestationsPassed = 0;
    let mappingAttestationFailure = null;
    for (const controller of controllers) {
      await page.goto(`${baseURL}/hardware/${controller.id}/grid`);
      const attestationForm = page.locator(`form[action="/hardware/${controller.id}/locate-mapping"]`);
      await attestationForm.locator('input[name="locate_mapping_verified"]').check();
      await attestationForm.locator('input[name="locate_ranges_reserved"]').check();
      const postResponse = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === `/hardware/${controller.id}/locate-mapping`);
      await attestationForm.locator('button[type="submit"]').click();
      const response = await postResponse;
      if (response.status() >= 400) {
        mappingAttestationFailure = { controller: controller.id, status: response.status(), body: await page.locator('body').innerText() };
        findings.push({
          severity: 'P1',
          workflow: 'Verify controller physical output capacity for Locate',
          url: page.url(),
          action: `Checked both Locate safety acknowledgements and submitted the UI form for ${controller.name}.`,
          expected: `Attestation of the loopback WLED controller reporting ${capacities[controller.id - 1]} pixels.`,
          actual: `HTTP ${response.status()}: ${mappingAttestationFailure.body.slice(-1200)}`,
          source: 'web/pages/hardware_grid.templ:25-40; internal/handler/physical_locate.go:125-141; internal/wled physical Locate mapping verification',
          stateChanged: false,
          blocksNextConfiguration: 'Yes for Locate/Stop on this controller; the safety attestation did not succeed, so no Locate command was sent.',
        });
        break;
      }
      await page.waitForURL(`${baseURL}/hardware/${controller.id}/grid`);
      mappingAttestationsPassed++;
    }

    let locateEvidence = { status: 'BLOCKED', reason: 'Controller mapping attestation did not pass for every controller; no Locate request was issued.' };
    if (mappingAttestationsPassed === controllers.length) {
      locateEvidence = await exerciseMockLocate(page, baseURL, firstPart.id, inventoryBins, mockWrites, mockRequests);
    }

    let crashRecoveryEvidence = { status: 'NOT RUN' };
    if (task065RestoreEnabled && mappingAttestationsPassed === controllers.length) {

      const crashWrites = [];
      const stopPageForDrawer = async () => {
        await page.goto(`${baseURL}/storage/drawers/1?bin_id=2`);
        const stopButton = page.getByRole('button', { name: 'Stop Locate / Turn Off Allocated LEDs' });
        if (await stopButton.count() !== 1) {
          throw new Error(`recovered Locate Stop control is missing; url=${page.url()} body=${(await page.locator('body').innerText()).slice(0, 3000)} durable=${JSON.stringify(readOnlyRows(databasePath, 'SELECT session_id,state,drawer_id,bin_id,owner_id FROM physical_locate_sessions'))}`);
        }
      };
      const waitForDroppedWrite = async () => {
        let resolveAccepted;
        const accepted = new Promise(resolve => { resolveAccepted = resolve; });
        mockBehaviors[0].dropNextPostResponse = true;
        mockBehaviors[0].onDropAccepted = resolveAccepted;
        return accepted;
      };
      const durableLocateState = () => readOnlyRows(databasePath, 'SELECT session_id, state, drawer_id, bin_id, owner_id FROM physical_locate_sessions WHERE state IN (\'activating\', \'active\', \'stopping\', \'partial\')');
      const durableTargetState = sessionID => readOnlyRows(databasePath, 'SELECT controller_id, may_have_received, commands_json FROM physical_locate_session_targets WHERE session_id=? ORDER BY controller_id', [sessionID]);
      const assertNoUnresolvedOwnership = label => {
        assert.deepEqual(durableLocateState(), [], `${label}: no active or partial Locate session should exist before injection`);
        const unresolved = Number(readOnlyRows(databasePath, 'SELECT COUNT(*) FROM physical_locate_session_targets WHERE may_have_received=1')[0][0]);
        assert.equal(unresolved, 0, `${label}: no target should retain an unresolved delivery marker before injection`);
      };
      const awaitMockDisconnect = async record => {
        for (let attempt = 0; attempt < 100; attempt++) {
          if (record.responseBehavior === 'command applied; client disconnected before response') return;
          await new Promise(resolve => setTimeout(resolve, 20));
        }
        throw new Error('mock WLED request did not record the killed client disconnect');
      };

      assertNoUnresolvedOwnership('activation delivery-loss scenario initial state');
      await page.goto(`${baseURL}/parts/${firstPart.id + 1}`);
      const locateAfterRestartForm = page.locator(`form[action="/parts/${firstPart.id + 1}/locate-bin"]`).filter({ has: page.locator(`input[name="bin_id"][value="${inventoryBins[1][0]}"]`) });
      const locateAccepted = waitForDroppedWrite();
      await submitFormWithoutWaiting(locateAfterRestartForm);
      const activationApplied = await locateAccepted;

      const firstProcess = server;
      firstProcess.kill('SIGKILL');
      await waitForProcessExit(firstProcess);
      await awaitMockDisconnect(activationApplied);

      assert.deepEqual(activationApplied.payload.seg.map(segment => segment.i), [[24, 34, [0, 0, 128]]], 'crash injection should occur after the first exact targeted activation command was applied');
      let activeRows = durableLocateState();
      assert.equal(activeRows.length, 1, `durable reservation after killed activation: ${JSON.stringify(readOnlyRows(databasePath, 'SELECT session_id,state,drawer_id,bin_id,owner_id FROM physical_locate_sessions'))}`);
      assert.equal(activeRows[0][1], 'activating', 'durable reservation must precede the blocked activation response');
      let activeTargets = durableTargetState(activeRows[0][0]);
      assert.equal(activeTargets.length, 1);
      assert.equal(Number(activeTargets[0][1]), 1, 'activation attempt marker must remain durable while delivery is uncertain');

      const activationSessionID = activeRows[0][0];
      const activationTargetBeforeCrash = activeTargets[0];
      server = startServer();

      await waitForServer(baseURL, server);

      const cleanupStartAfterActivationCrash = mockWrites.length;
      await stopPageForDrawer();

      const recoveryStopForm = page.locator('form[action="/storage/drawers/1/stop"]');
      const recoveryResponse = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/storage/drawers/1/stop');
      await submitFormWithoutWaiting(recoveryStopForm);
      await recoveryResponse;

      await page.goto(`${baseURL}/storage/drawers/1?stopped=1`);
      const recoveryWrites = mockWrites.slice(cleanupStartAfterActivationCrash);
      assert.equal(recoveryWrites.length, 2, 'recovery after process death should send both saved targeted cleanup ranges');
      assert.deepEqual(recoveryWrites.map(write => [write.controller, write.payload.seg[0].id, ...write.payload.seg[0].i]), [
        [1, 10, 24, 34, [0, 0, 0]], [1, 10, 49, 54, [0, 0, 0]],
      ], 'recovery cleanup should use only the persisted controller, segment and pixel spans');
      assert.ok(recoveryWrites.every(write => !Object.hasOwn(write.payload, 'on')));
      assert.deepEqual(durableLocateState(), [], 'confirmed recovery should release durable session ownership');
      assert.deepEqual(readOnlyRows(databasePath, 'SELECT state FROM physical_locate_sessions WHERE session_id=?', [activationSessionID]), [['stopped']], 'confirmed recovery should persist terminal stopped state');
      assert.deepEqual(durableTargetState(activationSessionID).map(row => Number(row[1])), [0], 'confirmed recovery should clear the target ownership marker');
      crashWrites.push({ scenario: 'SIGKILL after activation applied / before response', request: activationApplied, initialDurableTarget: activationTargetBeforeCrash, recoveredWrites: recoveryWrites });

      assertNoUnresolvedOwnership('Stop interruption scenario initial state');
      await page.goto(`${baseURL}/parts/${firstPart.id + 1}`);
      const locateAgainForm = page.locator(`form[action="/parts/${firstPart.id + 1}/locate-bin"]`).filter({ has: page.locator(`input[name="bin_id"][value="${inventoryBins[1][0]}"]`) });
      await locateAgainForm.locator('button[title="Locate this inventory location"]').click();
      await page.waitForURL(`${baseURL}/storage/drawers/1?bin_id=${inventoryBins[1][0]}`);
      await stopPageForDrawer();
      const stopAccepted = waitForDroppedWrite();
      const stopWriteStart = mockWrites.length;
      const stoppingForm = page.locator('form[action="/storage/drawers/1/stop"]');
      await submitFormWithoutWaiting(stoppingForm);
      const cleanupApplied = await stopAccepted;

      const secondProcess = server;
      secondProcess.kill('SIGKILL');
      await waitForProcessExit(secondProcess);
      await awaitMockDisconnect(cleanupApplied);

      assert.deepEqual(cleanupApplied.payload.seg[0].i, [24, 34, [0, 0, 0]], 'Stop crash must occur after the first owned range was cleaned');
      activeRows = durableLocateState();
      assert.equal(activeRows.length, 1, `durable Stop ownership after process termination: ${JSON.stringify(readOnlyRows(databasePath, 'SELECT session_id,state,drawer_id,bin_id,owner_id FROM physical_locate_sessions'))}`);
      assert.equal(activeRows[0][1], 'stopping', 'Stop state must be durable while cleanup is in flight');
      const stoppingSessionID = activeRows[0][0];
      activeTargets = durableTargetState(stoppingSessionID);
      assert.equal(Number(activeTargets[0][1]), 1, 'ownership must remain durable during uncertain cleanup delivery');

      server = startServer();
      await waitForServer(baseURL, server);
      const retryWriteStart = mockWrites.length;
      await stopPageForDrawer();
      const retryStopForm = page.locator('form[action="/storage/drawers/1/stop"]');
      const retryResponse = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/storage/drawers/1/stop');
      await submitFormWithoutWaiting(retryStopForm);
      await retryResponse;

      await page.goto(`${baseURL}/storage/drawers/1?stopped=1`);
      const partialCleanupBeforeRestart = mockWrites.slice(stopWriteStart, retryWriteStart);
      const retryCleanupWrites = mockWrites.slice(retryWriteStart);
      assert.equal(partialCleanupBeforeRestart.length, 1, 'the terminated Stop should have applied exactly the first cleanup request');
      assert.equal(retryCleanupWrites.length, 2, 'restart recovery should safely replay all saved cleanup ranges');
      assert.deepEqual(retryCleanupWrites.map(write => write.payload.seg[0].i), [[24, 34, [0, 0, 0]], [49, 54, [0, 0, 0]]]);
      assert.deepEqual(durableLocateState(), [], 'recovery after partial cleanup should release ownership only after acknowledgements');
      assert.deepEqual(readOnlyRows(databasePath, 'SELECT state FROM physical_locate_sessions WHERE session_id=?', [stoppingSessionID]), [['stopped']], 'recovered Stop should persist terminal stopped state');
      assert.deepEqual(durableTargetState(stoppingSessionID).map(row => Number(row[1])), [0]);
      assert.ok(retryCleanupWrites.every(write => !Object.hasOwn(write.payload, 'on')), 'recovery retry must never use GlobalOff');
      for (let controllerIndex = 0; controllerIndex < mockPixels.length; controllerIndex++) {
        for (let pixelIndex = 0; pixelIndex < mockPixels[controllerIndex].length; pixelIndex++) {
          if (ownedMockPixels[controllerIndex].has(pixelIndex)) {
            assert.deepEqual(mockPixels[controllerIndex][pixelIndex], [0, 0, 0], `owned pixel ${controllerIndex + 1}:${pixelIndex} should be black after confirmed cleanup`);
          } else {
            assert.deepEqual(mockPixels[controllerIndex][pixelIndex], initialMockPixels[controllerIndex][pixelIndex], `unrelated pixel ${controllerIndex + 1}:${pixelIndex} must retain its initial color`);
          }
        }
      }
      crashWrites.push({ scenario: 'SIGKILL during Stop after first cleanup applied / before response', request: cleanupApplied, durableTargetBeforeCrash: activeTargets[0], partialCleanupBeforeRestart, recoveredWrites: retryCleanupWrites });
      crashRecoveryEvidence = { status: 'PASS', processTermination: 'SIGKILL of disposable WLEDger server process', scenarios: crashWrites, unrelatedPixelsPreserved: true, ownedPixelsBlackAfterCleanup: true, noGlobalOff: true };
    }

    if (task065RestoreEnabled) {
      await page.goto(`${baseURL}/settings`);
      const downloadPromise = page.waitForEvent('download');
      await page.locator('a[href="/settings/backup/download"]').click();
      const download = await downloadPromise;
      const backupPath = path.join(scratchDir, 'task065-source-backup.zip');
      await download.saveAs(backupPath);
      const backupBytes = fs.readFileSync(backupPath);
      assert.ok(backupBytes.length > 0, 'authenticated source UI should export a nonempty archive');
      const archiveSummary = inspectBackupArchive(backupPath);
      assert.equal(archiveSummary.manifests.length, 1, 'archive should contain exactly one current manifest');
      assert.equal(archiveSummary.manifests[0], 'storage_data.json');
      assert.ok(archiveSummary.entries.includes('human_readable_parts.csv'));

      restoreScratchDir = fs.mkdtempSync(path.join(os.tmpdir(), 'wledger-task065-restore-'));
      fs.mkdirSync(path.join(restoreScratchDir, 'data'), { recursive: true });
      fs.mkdirSync(path.join(restoreScratchDir, 'uploads'), { recursive: true });
      fs.symlinkSync(path.join(repoRoot, 'web'), path.join(restoreScratchDir, 'web'));
      fs.symlinkSync(path.join(repoRoot, 'locales'), path.join(restoreScratchDir, 'locales'));
      const restoreDatabasePath = path.join(restoreScratchDir, 'data', 'wledger.db');
      const restorePort = await availablePort();
      const restoreURL = `http://127.0.0.1:${restorePort}`;
      restoreServer = startServer(restoreScratchDir, restorePort);
      await waitForServer(restoreURL, restoreServer);
      const freshDestinationCounts = readOnlyCounts(restoreDatabasePath);
      for (const table of ['controllers', 'cabinets', 'drawers', 'bins', 'parts', 'part_assignments', 'cabinet_led_strings', 'cabinet_led_path_sections', 'drawer_led_allocations']) {
        assert.equal(freshDestinationCounts[table], 0, `separate restore destination ${table} must be freshly initialized and empty`);
      }
      const destinationMigrationsBefore = readOnlyRows(restoreDatabasePath, 'SELECT version_id FROM goose_db_version WHERE is_applied=1 AND version_id>0 ORDER BY version_id').map(row => Number(row[0]));
      assert.deepEqual(destinationMigrationsBefore, migrationVersions, 'destination must independently apply every migration before restore');

      restoreContext = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
      restorePage = await restoreContext.newPage();
      const restorePageErrors = [];
      restorePage.on('pageerror', error => restorePageErrors.push({ url: restorePage.url(), message: error.message }));
      await restorePage.goto(`${restoreURL}/setup`);
      assert.equal(new URL(restorePage.url()).pathname, '/setup', 'separate destination must require its own initial setup');
      await restorePage.locator('form[action="/setup"] input[name="email"]').fill('task065-destination@example.invalid');
      await restorePage.locator('form[action="/setup"] input[name="password"]').fill('task065-destination-password-123');
      await restorePage.locator('form[action="/setup"] button').click();
      await restorePage.waitForURL(`${restoreURL}/`);
      await restorePage.goto(`${restoreURL}/settings`);
      await restorePage.locator('button[onclick="restore_modal.showModal()"]:visible').click();
      const restoreForm = restorePage.locator('form[hx-post="/settings/backup/restore"]');
      await restoreForm.locator('input[name="backup_file"]').setInputFiles(backupPath);
      await restoreForm.locator('input[type="checkbox"]').check();
      const wledWritesBeforeRestore = mockWrites.length;
      const restoreResponse = restorePage.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/settings/backup/restore');
      await restoreForm.locator('button[type="submit"]').click();
      const restoreHTTP = await restoreResponse;
      await restorePage.locator('#restore_result').getByText(/System restored successfully/).waitFor();
      assert.ok(restoreHTTP.status() < 400, `fresh-install UI restore returned HTTP ${restoreHTTP.status()}`);
      assert.equal(mockWrites.length, wledWritesBeforeRestore, 'backup restore must not send any WLED command');

      const sourceSnapshot = installationSnapshot(databasePath, cabinetID);
      const destinationSnapshot = installationSnapshot(restoreDatabasePath, cabinetID);
      assert.deepEqual(destinationSnapshot, sourceSnapshot, 'restored identities, layout, physical wiring, allocations, parts, images and stock quantities must match the source');
      const qrIdentityChecks = [];
      for (const [binID] of destinationSnapshot.bins) {
        const sourceQR = await page.request.get(`${baseURL}/bin/${binID}/qr`);
        const destinationQR = await restorePage.request.get(`${restoreURL}/bin/${binID}/qr`);
        assert.equal(sourceQR.status(), 200, `source QR identity route for bin ${binID}`);
        assert.equal(destinationQR.status(), 200, `restored QR identity route for bin ${binID}`);
        assert.match(destinationQR.headers()['content-type'], /image\/png/);
        assert.deepEqual(await destinationQR.body(), await sourceQR.body(), `QR identity for stable bin ID ${binID} should survive backup/restore`);
        qrIdentityChecks.push(Number(binID));
      }
      const settingsColumns = 'require_auth_for_read, locate_timeout_seconds, enable_locate_timeout, enable_debug_logs, color_locate, color_stock_ok, color_stock_low, color_stock_critical, color_error, inspiration_seeds_applied, supplier_cache_ttl_hours, default_currency';
      assert.deepEqual(readOnlyRows(restoreDatabasePath, `SELECT ${settingsColumns} FROM settings WHERE id=1`), readOnlyRows(databasePath, `SELECT ${settingsColumns} FROM settings WHERE id=1`), 'supported application settings should round-trip');
      const destinationMigrationsAfter = readOnlyRows(restoreDatabasePath, 'SELECT version_id FROM goose_db_version WHERE is_applied=1 AND version_id>0 ORDER BY version_id').map(row => Number(row[0]));
      assert.deepEqual(destinationMigrationsAfter, migrationVersions, 'restore must preserve the destination schema migration state');
      const imagePath = destinationSnapshot.parts.find(row => row[3])?.[3];
      assert.ok(imagePath, 'source browser flow should have created an uploaded part image');
      assert.ok(archiveSummary.entries.includes(`uploads/images/${path.basename(imagePath)}`), 'source backup should contain the part image asset');
      assert.ok(findUploadByName(path.join(restoreScratchDir, 'app', 'uploads'), path.basename(imagePath)), 'supported uploaded asset must exist in the independent destination filesystem');

      await restorePage.goto(`${restoreURL}/parts`);
      assert.ok((await restorePage.locator('body').innerText()).includes('UI Part 10'), 'restored inventory should be browseable');
      await restorePage.locator('#scan-target-q').fill('UI Part 10');
      await restorePage.waitForFunction(() => new URL(location.href).searchParams.get('q') === 'UI Part 10' && document.body.innerText.includes('UI Part 10'));
      assert.doesNotMatch(await restorePage.locator('#parts-grid').innerText(), /UI Part 1(?!0)/, 'restored inventory search should filter correctly');

      const destinationBeforeRestart = installationSnapshot(restoreDatabasePath, cabinetID);
      restoreServer.kill('SIGTERM');
      await new Promise(resolve => restoreServer.once('exit', resolve));
      restoreServer = startServer(restoreScratchDir, restorePort);
      await waitForServer(restoreURL, restoreServer);
      await restorePage.goto(`${restoreURL}/parts`);
      if (new URL(restorePage.url()).pathname === '/login') {
        await restorePage.locator('form[action="/login"] input[name="email"]').fill('ui-only-admin@example.invalid');
        await restorePage.locator('form[action="/login"] input[name="password"]').fill('ui-only-safe-password-123');
        await restorePage.locator('form[action="/login"] button').click();
        await restorePage.waitForURL(`${restoreURL}/`);
        await restorePage.goto(`${restoreURL}/parts`);
      }
      assert.ok((await restorePage.locator('body').innerText()).includes('UI Part 10'), 'restored inventory should remain browseable after app restart');
      assert.deepEqual(installationSnapshot(restoreDatabasePath, cabinetID), destinationBeforeRestart, 'independent restored database must persist unchanged across restart');
      assert.deepEqual(restorePageErrors, [], 'restore destination should have no browser runtime errors');

      const destinationUploadsPath = path.join(restoreScratchDir, 'app', 'uploads');
      const recoveryDirectory = restoreControlDirectory(destinationUploadsPath);
      const previousDatabaseGeneration = readOnlyRows(restoreDatabasePath, 'SELECT generation_id FROM restore_generation WHERE id=1')[0][0];
      const oldGenerationID = '066a0000000000000000000000000001';
      const oldCandidate = path.join(recoveryDirectory, `${oldGenerationID}.candidate`);
      const oldGeneration = path.join(recoveryDirectory, `${oldGenerationID}.previous`);
      fs.mkdirSync(recoveryDirectory, { recursive: true, mode: 0o700 });
      fs.mkdirSync(oldCandidate);
      fs.renameSync(destinationUploadsPath, oldGeneration);
      writePendingRestoreJournal(destinationUploadsPath, oldGenerationID, true);

      const restoreWritesBeforeStartupRecovery = mockWrites.length;
      await stopProcess(restoreServer);
      restoreServer = startServer(restoreScratchDir, restorePort);
      await waitForServer(restoreURL, restoreServer);
      assert.equal(fs.existsSync(path.join(recoveryDirectory, '.wledger-restore-journal.json')), false, 'startup rollback should retire the pending journal');
      assert.equal(fs.existsSync(oldCandidate), false, 'startup rollback should remove the uncommitted candidate');
      assert.equal(fs.existsSync(oldGeneration), false, 'startup rollback should restore the previous directory to active uploads');
      assert.ok(findUploadByName(destinationUploadsPath, path.basename(imagePath)), 'startup rollback must restore the old referenced image before serving');
      assert.deepEqual(readOnlyRows(restoreDatabasePath, 'SELECT generation_id FROM restore_generation WHERE id=1'), [[previousDatabaseGeneration]], 'rollback must keep the old committed generation marker');
      assert.equal(mockWrites.length, restoreWritesBeforeStartupRecovery, 'startup rollback must not send WLED commands');

      await stopProcess(restoreServer);
      restoreServer = null;
      const committedGenerationID = '066a0000000000000000000000000002';
      const committedCandidate = path.join(recoveryDirectory, `${committedGenerationID}.candidate`);
      const committedPrevious = path.join(recoveryDirectory, `${committedGenerationID}.previous`);
      fs.cpSync(destinationUploadsPath, committedCandidate, { recursive: true, errorOnExist: true });
      fs.renameSync(destinationUploadsPath, committedPrevious);
      fs.renameSync(committedCandidate, destinationUploadsPath);
      setRestoreGeneration(restoreDatabasePath, committedGenerationID);
      assert.deepEqual(readOnlyRows(restoreDatabasePath, 'SELECT generation_id FROM restore_generation WHERE id=1'), [[committedGenerationID]], 'roll-forward fixture must have a committed candidate marker');
      writePendingRestoreJournal(destinationUploadsPath, committedGenerationID, true);

      const restoreWritesBeforeRollForward = mockWrites.length;
      restoreServer = startServer(restoreScratchDir, restorePort);
      await waitForServer(restoreURL, restoreServer);
      assert.equal(fs.existsSync(path.join(recoveryDirectory, '.wledger-restore-journal.json')), false, 'startup roll-forward should retire the pending journal');
      assert.equal(fs.existsSync(committedPrevious), false, 'verified old generation should be safely cleaned after roll-forward');
      assert.ok(findUploadByName(destinationUploadsPath, path.basename(imagePath)), 'startup roll-forward must keep the committed candidate assets active');
      assert.equal(mockWrites.length, restoreWritesBeforeRollForward, 'startup roll-forward must not send WLED commands');

      restoreEvidence = {
        status: 'PASS',
        sourceDatabasePath: databasePath,
        destinationDatabasePath: restoreDatabasePath,
        destinationUploadsPath,
        archiveBytes: backupBytes.length,
        archiveSummary,
        independentDestinationMigrations: destinationMigrationsBefore,
        restoredSnapshot: destinationSnapshot,
        qrIdentityChecks,
        wledCommandsDuringRestore: mockWrites.length - wledWritesBeforeRestore,
        imagePath,
        restoredAfterRestart: true,
        startupRollbackWithMissingUploadsPath: true,
        startupRollForwardAfterCommit: true,
        noWLEDCommandsDuringStartupRecovery: true,
        searchAfterRestore: true,
        directSQLPopulation: false,
        destinationPageErrors: restorePageErrors,
      };
    }

    const persistedBeforeRestart = installationSnapshot(databasePath, cabinetID);
    let reloggedAfterRestart = false;
    server.kill('SIGTERM');
    await new Promise(resolve => server.once('exit', resolve));
    server = startServer();
    await waitForServer(baseURL, server);
    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    if (new URL(page.url()).pathname === '/login') {
      reloggedAfterRestart = true;
      await page.locator('input[name="email"]').fill('ui-only-admin@example.invalid');
      await page.locator('input[name="password"]').fill('ui-only-safe-password-123');
      await page.locator('form[action="/login"] button').click();
      await page.waitForURL(`${baseURL}/`);
      await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    }
    const postRestartData = JSON.parse(await page.locator('#layout-editor-data').textContent());
    assert.equal(postRestartData.Cabinet.name, cabinetName);
    assert.equal(postRestartData.Drawers.length, 5);
    const persistedAfterRestart = installationSnapshot(databasePath, cabinetID);
    assert.deepEqual(persistedAfterRestart, persistedBeforeRestart, 'all browser-created installation records must persist byte-for-value across app restart');

    assert.deepEqual(pageErrors, [], 'fresh installation pages must not raise JavaScript runtime errors');
    const invalidControllerConsoleErrors = consoleErrors.filter(error => error.url.endsWith('/hardware') && error.message.includes('400 (Bad Request)'));
    assert.equal(invalidControllerConsoleErrors.length, 2, `expected only the two deliberate invalid-controller responses: ${JSON.stringify(consoleErrors)}`);
    const nonValidationConsoleErrors = consoleErrors.filter(error => !invalidControllerConsoleErrors.includes(error));
    if (task065RestoreEnabled) {
      assert.deepEqual(nonValidationConsoleErrors.map(error => [new URL(error.url).pathname + new URL(error.url).search, error.message]).sort(), [
        ['/parts/2', 'Failed to load resource: net::ERR_CONNECTION_REFUSED'],
        ['/storage/drawers/1?bin_id=2', 'Failed to load resource: net::ERR_CONNECTION_REFUSED'],
      ], 'the only additional browser errors should be the two expected responses interrupted by SIGKILL');
    } else {
      assert.deepEqual(nonValidationConsoleErrors, [], `unexpected browser console errors remain a failure: ${JSON.stringify(nonValidationConsoleErrors)}`);
    }

    console.log(JSON.stringify({
      result: mappingAttestationsPassed === controllers.length && locateEvidence.status === 'PASS' ? 'PARTIAL' : 'BLOCKED',
      reason: 'Fresh setup, five controllers, cabinet/drawers, UI-created bins/placements, five-controller 1000-pixel wiring, reference allocations, ten parts, stock assignment/adjustment/move and restart persistence were browser-tested. Locate/Stop is reported separately based on mock-controller attestation and payload results.',
      databasePath,
      initialEmptyCounts: emptyCounts,
      appliedMigrationVersions,
      controllers,
      persistedControllerCapacities,
      cabinet: { id: cabinetID, name: cabinetName, dimensions: [200, 120], drawers: drawerRows },
      memberships,
      stringRows,
      sectionRows,
      allocations,
      finalAllocations,
      staleRevisionError,
      invalidAllocationCases: invalidAllocationCases.map(({ start, count }) => ({ start, count, rejectedWithoutMutation: true })),
      inventoryBins,
      binAttachments,
      createdParts,
      mappingAttestationsPassed,
      mappingAttestationFailure,
      locateEvidence,
      crashRecoveryEvidence,
      restoreEvidence,
      mockPixelPreservation: { initial: initialMockPixels, final: mockPixels, ownedPixels: ownedMockPixels.map(pixels => [...pixels].sort((a, b) => a - b)), unrelatedPreserved: crashRecoveryEvidence.status === 'PASS' },
      persistedBeforeRestart,
      persistedAfterRestart,
      reloggedAfterRestart,
      findings,
      browserPageErrors: pageErrors,
      browserConsoleErrors: consoleErrors,
      mockRequests,
      mockWrites,
      appMutations,
      uiOnlyWrites: true,
    }));
  } finally {
    if (restoreContext) await restoreContext.close();
    if (browser) await browser.close();
    for (const mock of mocks) if (mock.listening) await new Promise(resolve => mock.close(resolve));
    if (restoreServer) await stopProcess(restoreServer);
    if (restoreScratchDir) fs.rmSync(restoreScratchDir, { recursive: true, force: true });
    if (server) await stopProcess(server);
    fs.rmSync(scratchDir, { recursive: true, force: true });
  }
})().catch(error => { console.error(error); process.exit(1); });

async function submitFormWithoutWaiting(formLocator) {
  await formLocator.evaluate(form => {
    const body = new FormData(form);
    fetch(form.action, { method: form.method || 'POST', body, credentials: 'same-origin' }).catch(() => {});
  });
}

async function waitForProcessExit(process) {
  if (process.exitCode !== null || process.signalCode !== null) return;
  await new Promise(resolve => process.once('exit', resolve));
}

async function stopProcess(process) {
  if (!process || process.exitCode !== null || process.signalCode !== null) return;
  const exited = new Promise(resolve => process.once('exit', resolve));
  process.kill('SIGTERM');
  await exited;
}

function restoreControlDirectory(uploadsPath) {
  const root = path.dirname(path.resolve(uploadsPath));
  const digest = crypto.createHash('sha256').update(path.resolve(uploadsPath)).digest('hex').slice(0, 24);
  return path.join(root, `.wledger-restore-control-${digest}`);
}

function writePendingRestoreJournal(uploadsPath, id, hadUploads) {
  const control = restoreControlDirectory(uploadsPath);
  fs.mkdirSync(control, { recursive: true, mode: 0o700 });
  fs.writeFileSync(path.join(control, '.wledger-restore-journal.json'), JSON.stringify({ version: 1, id, had_uploads: hadUploads }), { mode: 0o600 });
}

function setRestoreGeneration(databasePath, generationID) {
  const script = 'import sqlite3,sys; c=sqlite3.connect(sys.argv[1]); c.execute("UPDATE restore_generation SET generation_id=? WHERE id=1",(sys.argv[2],)); c.commit(); c.close()';
  const result = spawnSync('python3', ['-c', script, databasePath, generationID], { encoding: 'utf8' });
  if (result.status !== 0) throw new Error(`failed to set disposable restore generation: ${result.stderr}`);
}

async function availablePort() {
  const probe = net.createServer();
  await new Promise((resolve, reject) => probe.once('error', reject).listen(0, '127.0.0.1', resolve));
  const port = probe.address().port;
  await new Promise(resolve => probe.close(resolve));
  return port;
}

async function waitForServer(baseURL, process) {
  for (let attempt = 0; attempt < 120; attempt++) {
    try {
      const response = await new Promise(resolve => {
        const timeout = setTimeout(() => resolve(null), 1000);
        fetch(`${baseURL}/setup`).then(
          result => { clearTimeout(timeout); resolve(result); },
          () => { clearTimeout(timeout); resolve(null); },
        );
      });
      if (response?.ok) return;
    } catch {}
    if (process.exitCode !== null) throw new Error(`disposable server exited with ${process.exitCode}: ${process.wledgerOutput?.() || ''}`);
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  throw new Error('disposable WLEDger server did not become ready');
}

function readOnlyRows(databasePath, sql, parameters = []) {
  const script = 'import json,sqlite3,sys; c=sqlite3.connect("file:"+sys.argv[1]+"?mode=ro",uri=True); c.execute("PRAGMA query_only=ON"); rows=c.execute(sys.argv[2],json.loads(sys.argv[3])).fetchall(); print(json.dumps(rows)); c.close()';
  const result = spawnSync('python3', ['-c', script, databasePath, sql, JSON.stringify(parameters)], { encoding: 'utf8' });
  if (result.status !== 0) throw new Error(`read-only SQLite query failed: ${result.stderr}`);
  return JSON.parse(result.stdout);
}

function readOnlyCounts(databasePath) {
  const result = {};
  for (const table of ['controllers', 'cabinets', 'drawers', 'bins', 'containers', 'cabinet_controllers', 'bin_drawer_attachments', 'parts', 'part_assignments', 'cabinet_led_strings', 'cabinet_led_path_sections', 'drawer_led_allocations']) {
    result[table] = Number(readOnlyRows(databasePath, `SELECT COUNT(*) FROM ${table}`)[0][0]);
  }
  return result;
}

function inspectBackupArchive(archivePath) {
  const script = 'import json,sys,zipfile; z=zipfile.ZipFile(sys.argv[1]); names=z.namelist(); manifests=[n for n in names if n in ("storage_data.json","restore_data.json")]; storage=json.loads(z.read("storage_data.json")) if "storage_data.json" in names else {}; print(json.dumps({"entries":names,"manifests":manifests,"version":storage.get("version")}))';
  const result = spawnSync('python3', ['-c', script, archivePath], { encoding: 'utf8' });
  if (result.status !== 0) throw new Error(`read-only backup archive inspection failed: ${result.stderr}`);
  return JSON.parse(result.stdout);
}

function findUploadByName(root, fileName) {
  let found = false;
  if (!fs.existsSync(root)) return false;
  for (const entry of fs.readdirSync(root, { withFileTypes: true })) {
    const fullPath = path.join(root, entry.name);
    if (entry.isDirectory()) found ||= findUploadByName(fullPath, fileName);
    else if (entry.name === fileName) found = true;
    if (found) break;
  }
  return found;
}

function installationSnapshot(databasePath, cabinetID) {
  return {
    controllers: readOnlyRows(databasePath, 'SELECT id, name, ip_address, port, led_count FROM controllers ORDER BY id'),
    cabinet: readOnlyRows(databasePath, 'SELECT id, name, layout_width, layout_height FROM cabinets WHERE id = ?', [cabinetID]),
    memberships: readOnlyRows(databasePath, 'SELECT controller_id, slot FROM cabinet_controllers WHERE cabinet_id = ? ORDER BY slot', [cabinetID]),
    drawers: readOnlyRows(databasePath, 'SELECT id, label, x, y, width, height FROM drawers WHERE cabinet_id = ? ORDER BY display_order, id', [cabinetID]),
    bins: readOnlyRows(databasePath, 'SELECT id, name, container_id, led_index FROM bins ORDER BY id'),
    attachments: readOnlyRows(databasePath, 'SELECT bin_id, drawer_id, x, y, width, height FROM bin_drawer_attachments ORDER BY bin_id'),
    strings: readOnlyRows(databasePath, 'SELECT id, controller_id, name, display_order, pixel_count, layout_complete FROM cabinet_led_strings WHERE cabinet_id = ? ORDER BY controller_id, display_order', [cabinetID]),
    sections: readOnlyRows(databasePath, 'SELECT string_id, display_order, pixel_count, points_json FROM cabinet_led_path_sections WHERE string_id IN (SELECT id FROM cabinet_led_strings WHERE cabinet_id = ?) ORDER BY string_id, display_order', [cabinetID]),
    allocations: readOnlyRows(databasePath, 'SELECT drawer_id, start_pixel, pixel_count, display_order FROM drawer_led_allocations WHERE cabinet_id = ? ORDER BY drawer_id, display_order', [cabinetID]),
    parts: readOnlyRows(databasePath, 'SELECT id, name, part_number, image_path FROM parts ORDER BY id'),
    assignments: readOnlyRows(databasePath, 'SELECT part_id, bin_id, quantity FROM part_assignments ORDER BY part_id, bin_id'),
  };
}

async function exerciseMockLocate(page, baseURL, firstPartID, bins, mockWrites, mockRequests) {
  const testCases = [
    { partID: firstPartID + 1, binID: Number(bins[1][0]), drawerID: 1, expected: [[1, 10, 24, 34], [1, 10, 49, 54]] },
    { partID: firstPartID + 2, binID: Number(bins[2][0]), drawerID: 2, expected: [[1, 20, 94, 100], [2, 10, 0, 10]] },
    { partID: firstPartID + 4, binID: Number(bins[4][0]), drawerID: 3, expected: [[2, 10, 24, 34]] },
    { partID: firstPartID + 6, binID: Number(bins[6][0]), drawerID: 4, expected: [[2, 20, 119, 125], [3, 10, 0, 10]] },
    { partID: firstPartID + 8, binID: Number(bins[8][0]), drawerID: 5, expected: [[4, 20, 144, 150], [5, 10, 0, 10]] },
  ];
  const results = [];
  for (const testCase of testCases) {
    await page.goto(`${baseURL}/parts/${testCase.partID}`);
    const locateForm = page.locator(`form[action="/parts/${testCase.partID}/locate-bin"]`).filter({ has: page.locator(`input[name="bin_id"][value="${testCase.binID}"]`) });
    const writeStart = mockWrites.length;
    const requestStart = mockRequests.length;
    await locateForm.locator('button[title="Locate this inventory location"]').click();
    await page.waitForURL(`${baseURL}/storage/drawers/${testCase.drawerID}?bin_id=${testCase.binID}`);
    const activation = mockWrites.slice(writeStart).map(({ controller, payload }) => {
      const segment = payload.seg?.[0];
      return [controller, segment?.id, segment?.i?.[0], segment?.i?.[1], segment?.i?.[2]];
    });
    assert.deepEqual(activation, testCase.expected.map(([controller, segment, start, end]) => [controller, segment, start, end, [0, 0, 128]]));
    const stopWriteStart = mockWrites.length;
    await page.getByRole('button', { name: 'Stop Locate / Turn Off Allocated LEDs' }).click();
    await page.waitForURL(`${baseURL}/storage/drawers/${testCase.drawerID}?stopped=1`);
    const stop = mockWrites.slice(stopWriteStart).map(({ controller, payload }) => ({ controller, payload }));
    assert.equal(stop.length, testCase.expected.length);
    for (const { controller, payload } of stop) {
      assert.ok(testCase.expected.some(expected => expected[0] === controller), 'Stop must not target an unrelated controller');
      assert.ok(payload.seg?.length, 'Stop should use targeted segment commands');
      assert.notEqual(payload.on, false, 'Stop must not use GlobalOff');
      assert.ok(payload.seg.every(segment => Array.isArray(segment.i) && segment.i[2].every(channel => channel === 0)), 'Stop must black only the requested pixel interval');
    }
    const stopCommands = stop.map(({ controller, payload }) => {
      const segment = payload.seg[0];
      return [controller, segment.id, segment.i[0], segment.i[1], segment.i[2]];
    });
    assert.deepEqual(stopCommands, testCase.expected.map(([controller, segment, start, end]) => [controller, segment, start, end, [0, 0, 0]]), 'Stop must target exactly the Locate ranges and no neighboring pixels');
    assert.ok(!mockRequests.slice(requestStart).some(request => request.method === 'POST' && request.path === '/json/state' && mockWrites.some(write => write.payload.on === false)), 'no GlobalOff command should be sent');
    results.push({ binID: testCase.binID, activation, stop: stop.map(({ controller, payload }) => ({ controller, payload })) });
  }
  return { status: 'PASS', results };
}
