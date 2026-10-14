const assert = require('node:assert/strict');
const { chromium } = require('playwright');
const { spawn, spawnSync } = require('node:child_process');
const fs = require('node:fs');
const http = require('node:http');
const net = require('node:net');
const os = require('node:os');
const path = require('node:path');

(async () => {
  const repoRoot = path.resolve(__dirname, '../..');
  const scratchDir = fs.mkdtempSync(path.join(os.tmpdir(), 'wledger-ui-only-'));
  const databasePath = path.join(scratchDir, 'data', 'wledger.db');
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
    for (let index = 0; index < capacities.length; index++) {
      const capacity = capacities[index];
      const half = capacity / 2;
      const mock = http.createServer(async (request, response) => {
        mockRequests.push({ controller: index + 1, method: request.method, path: request.url });
        response.setHeader('Content-Type', 'application/json');
        if (request.method === 'GET' && request.url === '/json/info') {
          response.end(JSON.stringify({ mac: `02:00:00:00:59:${String(index + 1).padStart(2, '0')}`, leds: { count: capacity, rgbw: false } }));
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
          mockWrites.push({ controller: index + 1, payload: JSON.parse(body) });
          response.end(JSON.stringify({ success: true }));
          return;
        }
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

    const startServer = (enableMockOutput = false) => {
      const serverEnv = { ...process.env, WLEDGER_INSECURE_COOKIES: '1', WLEDGER_API_TOKEN: 'inventory-only-test-token', WLEDGER_PORT: String(appPort) };
      if (enableMockOutput) serverEnv.WLEDGER_PHYSICAL_OUTPUT_ENABLED = 'true';
      else delete serverEnv.WLEDGER_PHYSICAL_OUTPUT_ENABLED;
      return spawn(binaryPath, [], { cwd: scratchDir, env: serverEnv, stdio: 'ignore' });
    };
    server = startServer();
    await waitForServer(baseURL, server);

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
    await page.locator('form[action="/setup"] input[name="email"]').fill('ui-only-admin@example.invalid');
    await page.locator('form[action="/setup"] input[name="password"]').fill('ui-only-safe-password-123');
    await page.locator('form[action="/setup"] button').click();
    await page.waitForURL(`${baseURL}/`);

    for (const path of ['/global-off', '/parts/123/locate', '/bins/123/locate']) {
      const response = await fetch(`${baseURL}/api/v1${path}`, {
        method: 'POST',
        headers: { Authorization: 'Bearer inventory-only-test-token' },
      });
      const responseBody = await response.text();
      assert.equal(response.status, 409, `default-off API request ${path} must be rejected (body=${responseBody})`);
      assert.match(responseBody, /inventory-only mode/i);
    }
    assert.equal(mockWrites.length, 0, 'rejected API writes must not reach any loopback WLED state endpoint');

    const csrfToken = await page.locator('meta[name="csrf-token"]').getAttribute('content');
    const sessionCookies = (await context.cookies(baseURL)).map(cookie => `${cookie.name}=${cookie.value}`).join('; ');
    const blockedUIWrites = [];
    for (const path of ['/hardware/1/locate?bin_id=1', '/parts/1/locate', '/parts/1/locate-bin', '/storage/drawers/1/locate', '/hardware/off']) {
      const body = new URLSearchParams({ csrf_token: csrfToken, bin_id: '1' });
      const response = await fetch(`${baseURL}${path}`, {
        method: 'POST',
        headers: { Cookie: sessionCookies, 'Content-Type': 'application/x-www-form-urlencoded' },
        body,
      });
      blockedUIWrites.push({ path, status: response.status, body: await response.text() });
    }
    for (const blocked of blockedUIWrites) {
      assert.equal(blocked.status, 409, `direct browser POST ${blocked.path} must be rejected: ${blocked.body}`);
      assert.match(blocked.body, /inventory-only mode/i);
    }
    assert.equal(mockWrites.length, 0, 'rejected browser POSTs must not reach any loopback WLED state endpoint');
    assert.deepEqual(readOnlyRows(databasePath, 'SELECT count(*) FROM physical_locate_sessions'), [[0]], 'rejected Locate requests must not create durable ownership');

    const emptyCounts = readOnlyCounts(databasePath);
    for (const table of ['controllers', 'cabinets', 'drawers', 'bins', 'parts', 'part_assignments', 'cabinet_led_strings', 'cabinet_led_path_sections', 'drawer_led_allocations']) {
      assert.equal(emptyCounts[table], 0, `new-install table ${table} must begin empty`);
    }

    await page.goto(`${baseURL}/hardware`);
    assert.match(await page.locator('body').innerText(), /No cabinets yet/i);
    assert.match(await page.locator('body').innerText(), /LED Locate is unavailable in inventory-only mode/i);
    assert.equal(await page.getByRole('button', { name: /Turn Off All LEDs/i }).count(), 0, 'GlobalOff control must be absent by default');

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

    const controllerForms = await page.locator('form[action^="/hardware/"][action$="/delete"]').count();
    const controllerEditForms = await page.locator('form[action^="/hardware/"][action$="/update"]').count();
    const controllerEditLinks = await page.locator('a[href^="/hardware/"][href$="/edit"]').count();
    if (controllerEditForms === 0 && controllerEditLinks === 0) {
      findings.push({
        severity: 'P2',
        workflow: 'Edit controller settings',
        url: `${baseURL}/hardware`,
        action: 'Inspected each newly created controller card and available controls.',
        expected: 'A user-facing action to edit controller name, loopback address, port or settings.',
        actual: `No controller update form or edit link is rendered. Cards expose Configure, Export and Delete only (delete forms: ${controllerForms}).`,
        source: 'web/pages/hardware.templ:91-110; internal/router/router.go:220-228; internal/handler/hardware.go:45-78,122-137',
        stateChanged: false,
        blocksNextConfiguration: 'No; fresh records can be created with the intended address/port, but subsequent corrections require delete/recreate.',
      });
    }

    await page.getByRole('button', { name: 'Add WLED Controller' }).click();
    const invalidForm = page.locator('#add_controller_modal form[action="/hardware"]');
    await invalidForm.locator('input[name="name"]').fill('UI invalid port probe');
    await invalidForm.locator('input[name="ip_address"]').fill('127.0.0.1');
    await invalidForm.locator('input[name="port"]').fill('70000');
    await invalidForm.locator('button.btn-primary').click();
    await page.waitForURL(`${baseURL}/hardware`);
    const invalidPortRows = readOnlyRows(databasePath, 'SELECT id, port FROM controllers WHERE name = ?', ['UI invalid port probe']);
    if (invalidPortRows.length === 1) {
      findings.push({
        severity: 'P2',
        workflow: 'Reject invalid controller port',
        url: `${baseURL}/hardware`,
        action: 'Submitted a controller with loopback IP and TCP port 70000 using the Add WLED Controller form.',
        expected: 'Reject a port outside 1–65535 and leave controller records unchanged.',
        actual: `The browser form saved controller id ${invalidPortRows[0][0]} with port ${invalidPortRows[0][1]}.`,
        source: 'web/pages/hardware.templ:126-145; internal/handler/hardware.go:46-77',
        stateChanged: true,
        blocksNextConfiguration: 'No after cleanup; invalid probe record is removed using the normal UI below.',
      });
      await page.locator('details summary').filter({ hasText: 'Advanced legacy controller and segment tools' }).click();
      await page.locator(`form[action="/hardware/${invalidPortRows[0][0]}/delete"] button`).click();
      await page.waitForURL(`${baseURL}/hardware`);
      assert.equal(readOnlyRows(databasePath, 'SELECT id FROM controllers WHERE name = ?', ['UI invalid port probe']).length, 0, 'invalid probe should be removed through the browser');
    } else {
      findings.push({ workflow: 'Reject invalid controller port', status: 'REJECTED', evidence: 'The invalid port was not persisted.' });
    }

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

    await page.goto(`${baseURL}/hardware/${controllers[0].id}/grid`);
    await page.locator('input[x-model="currentContainer.name"]').fill('UI-created inventory-bin source');
    await page.locator('input[x-model\\.number="currentContainer.led_count"]').fill('10');
    await page.locator('input[x-model\\.number="currentContainer.config.rows"]').fill('2');
    await page.locator('input[x-model\\.number="currentContainer.config.cols"]').fill('5');
    await page.getByRole('button', { name: 'Auto Map (Linear)' }).click();
    await page.getByRole('button', { name: 'Save Changes' }).click();
    await page.getByRole('button', { name: 'Confirm', exact: true }).click();
    await page.waitForURL(`${baseURL}/hardware`);
    const inventoryBins = readOnlyRows(databasePath, 'SELECT id, name FROM bins ORDER BY id');
    assert.equal(inventoryBins.length, 10, 'the legacy grid browser form should create ten real inventory bins');

    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    for (let drawerIndex = 0; drawerIndex < 5; drawerIndex++) {
      const drawerButton = page.locator('aside button').filter({ hasText: drawers[drawerIndex] }).first();
      await drawerButton.dblclick();
      await page.getByText('Bin layout canvas').waitFor();
      const activeDrawerLabel = await page.locator('[x-data="physicalLayoutEditor()"]')
        .evaluate(element => window.Alpine.$data(element).activeDrawer()?.label);
      assert.equal(activeDrawerLabel, drawers[drawerIndex], `drawer ${drawerIndex + 1} should be active before adding bins`);

      const binSelect = page.locator('select[x-model="selectedAvailableBin"]');
      for (let localBin = 0; localBin < 2; localBin++) {
        const binID = Number(inventoryBins[drawerIndex * 2 + localBin][0]);
        await binSelect.selectOption(String(binID));
        await page.getByRole('button', { name: 'Draw bin' }).click();
        const offsetX = localBin === 0 ? 1 : 9;
        await canvas.scrollIntoViewIfNeeded();
        await drag(offsetX, 1, offsetX + 6, 7);
      }
      await page.getByRole('button', { name: /Back(?: to drawers)?/ }).click();
    }
    await page.getByRole('button', { name: 'Save layout' }).click();
    await page.waitForURL(`${baseURL}/storage/cabinets/${cabinetID}`);
    const binAttachments = readOnlyRows(databasePath, 'SELECT bin_id, drawer_id FROM bin_drawer_attachments ORDER BY bin_id');
    assert.equal(binAttachments.length, 10, `all ten bins should be placed through the graphical editor; saved rows: ${JSON.stringify(binAttachments)}`);
    const attachedDrawerIDs = new Set(binAttachments.map(row => Number(row[1])));
    assert.equal(attachedDrawerIDs.size, 5, 'bins should remain associated across all five drawers');

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
      await form.locator('input[name="part_number"]').fill(`UI-059-${String(index + 1).padStart(3, '0')}`);
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

    assert.equal(mockWrites.length, 0, 'controller configuration, wiring, allocation, inventory, stock and read-only mapping checks must remain available with output disabled and must not write WLED state');
    let locateEvidence = { status: 'BLOCKED', reason: 'Controller mapping attestation did not pass for every controller; no Locate request was issued.' };
    if (mappingAttestationsPassed === controllers.length) {
      server.kill('SIGTERM');
      await new Promise(resolve => server.once('exit', resolve));
      server = startServer(true);
      await waitForServer(baseURL, server);
      locateEvidence = await exerciseMockLocate(page, baseURL, firstPart.id, inventoryBins, mockWrites, mockRequests);
    }

    const persistedBeforeRestart = installationSnapshot(databasePath, cabinetID);
    let reloggedAfterRestart = false;
    server.kill('SIGTERM');
    await new Promise(resolve => server.once('exit', resolve));
    server = startServer(true);
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

    assert.deepEqual(pageErrors.filter(error => !error.message.includes("Identifier 'html5QrcodeScanner' has already been declared")), []);

    console.log(JSON.stringify({
      result: mappingAttestationsPassed === controllers.length && locateEvidence.status === 'PASS' ? 'PARTIAL' : 'BLOCKED',
      reason: 'Fresh setup, five controllers, cabinet/drawers, UI-created bins/placements, five-controller 1000-pixel wiring, reference allocations, ten parts, stock assignment/adjustment/move and restart persistence were browser-tested. Locate/Stop is reported separately based on mock-controller attestation and payload results.',
      databasePath,
      initialEmptyCounts: emptyCounts,
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
    if (browser) await browser.close();
    for (const mock of mocks) if (mock.listening) await new Promise(resolve => mock.close(resolve));
    if (server) {
      server.kill('SIGTERM');
      await new Promise(resolve => server.once('exit', resolve));
    }
    fs.rmSync(scratchDir, { recursive: true, force: true });
  }
})().catch(error => { console.error(error); process.exit(1); });

async function availablePort() {
  const probe = net.createServer();
  await new Promise((resolve, reject) => probe.once('error', reject).listen(0, '127.0.0.1', resolve));
  const port = probe.address().port;
  await new Promise(resolve => probe.close(resolve));
  return port;
}

async function waitForServer(baseURL, process) {
  for (let attempt = 0; attempt < 120; attempt++) {
    if (process.exitCode !== null) throw new Error(`disposable server exited with ${process.exitCode}`);
    try {
      const response = await fetch(`${baseURL}/setup`);
      if (response.ok) {
        if (process.exitCode !== null) throw new Error(`disposable server exited with ${process.exitCode}`);
        return;
      }
    } catch (error) {
      if (error.message.startsWith('disposable server exited')) throw error;
    }
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
  for (const table of ['controllers', 'cabinets', 'drawers', 'bins', 'parts', 'part_assignments', 'cabinet_led_strings', 'cabinet_led_path_sections', 'drawer_led_allocations']) {
    result[table] = Number(readOnlyRows(databasePath, `SELECT COUNT(*) FROM ${table}`)[0][0]);
  }
  return result;
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
    { partID: firstPartID, binID: Number(bins[1][0]), drawerID: 1, expected: [[1, 10, 24, 34], [1, 10, 49, 54]] },
    { partID: firstPartID + 2, binID: Number(bins[2][0]), drawerID: 2, expected: [[1, 20, 94, 100], [2, 10, 0, 10]] },
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
    assert.ok(!mockRequests.slice(requestStart).some(request => request.method === 'POST' && request.path === '/json/state' && mockWrites.some(write => write.payload.on === false)), 'no GlobalOff command should be sent');
    results.push({ binID: testCase.binID, activation, stop: stop.map(({ controller, payload }) => ({ controller, payload })) });
  }
  return { status: 'PASS', results };
}
