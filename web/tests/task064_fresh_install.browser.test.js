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
  const task067Enabled = process.env.WLEDGER_TASK067_LOCATE === '1';
  const inventoryOnly = process.env.WLEDGER_TASK064_INVENTORY_ONLY === '1';
  const task065RestoreEnabled = process.env.WLEDGER_TASK065_RESTORE === '1' || task067Enabled;
  let restoreScratchDir;
  let restoreServer;
  let restoreContext;
  let restorePage;
  let secondServer;
  let secondContext;
  let secondPage;
  let restoreEvidence = { status: 'NOT RUN' };
  let multiProcessEvidence = { status: 'NOT RUN' };
  const mocks = [];
  const mockRequests = [];
  const mockWrites = [];
  const findings = [];
  let acceptDialogs = true;
  let server;
  let browser;
  let baseURL;

  try {
    fs.symlinkSync(path.join(repoRoot, 'web'), path.join(scratchDir, 'web'));
    fs.symlinkSync(path.join(repoRoot, 'locales'), path.join(scratchDir, 'locales'));

    const capacities = [200, 250, 150, 300, 100];
    const outputCounts = [2, 1, 1, 1, 1];
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
        env: { ...process.env, WLEDGER_INSECURE_COOKIES: '1', WLEDGER_PHYSICAL_OUTPUT_ENABLED: inventoryOnly ? 'false' : 'true', WLEDGER_PORT: String(port) },
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
    page.on('dialog', dialog => acceptDialogs ? dialog.accept() : dialog.dismiss());

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

    await page.goto(`${baseURL}/categories`);
    await page.getByRole('link', { name: 'New category' }).click();
    const categoryImage = Buffer.from(await page.evaluate(() => {
      const canvas = document.createElement('canvas'); canvas.width = 4; canvas.height = 4;
      const context = canvas.getContext('2d'); context.fillStyle = '#3567aa'; context.fillRect(0, 0, 4, 4);
      return canvas.toDataURL('image/png').split(',')[1];
    }), 'base64');
    const categoryCreateForm = page.locator('form[action="/categories"]');
    await categoryCreateForm.locator('input[name="name"]').fill('Browser fasteners');
    await categoryCreateForm.locator('textarea[name="description"]').fill('Reusable mechanical hardware');
    await categoryCreateForm.locator('input[name="image"]').setInputFiles({ name: 'category.png', mimeType: 'image/png', buffer: categoryImage });
    await categoryCreateForm.locator('button[type="submit"]').click();
    await page.waitForURL(new RegExp(`${baseURL}/categories/\\d+$`));
    const categoryID = Number(page.url().match(/\/categories\/(\d+)$/)[1]);
    const categoryRows = readOnlyRows(databasePath, 'SELECT id,image_path FROM categories WHERE id=?', [categoryID]);
    assert.equal(categoryRows.length, 1, 'category should be created from the management form');
    const categoryImagePath = categoryRows[0][1];
    const categoryImageFile = path.join(scratchDir, 'app', 'uploads', categoryImagePath.replace(/^\/uploads\//, ''));
    assert.ok(fs.existsSync(categoryImageFile), 'category image upload should create the image asset');

    const attributeForm = page.locator(`form[action="/categories/${categoryID}/attributes"]`);
    await attributeForm.locator('input[name="name"]').fill('Grade');
    await attributeForm.locator('select[name="data_type"]').selectOption('text');
    await attributeForm.locator('textarea[name="options"]').fill('8.8\n10.9');
    await attributeForm.locator('input[name="required"]').check();
    await attributeForm.locator('button[type="submit"]').click();
    await page.waitForURL(`${baseURL}/categories/${categoryID}`);
    const attributeRows = readOnlyRows(databasePath, 'SELECT id FROM category_attributes WHERE category_id=? AND name=?', [categoryID, 'Grade']);
    assert.equal(attributeRows.length, 1, 'category-defined required specification should persist');
    const attributeID = Number(attributeRows[0][0]);

    await page.getByRole('link', { name: 'Add product' }).click();
    await page.waitForURL(`${baseURL}/parts/new?category_id=${categoryID}`);
    const categoryPartForm = page.locator('form[action="/parts"]');
    await categoryPartForm.locator('input[name="name"]').fill('Browser M8 bolt');
    await categoryPartForm.locator('input[name="sku"]').fill('BROWSER-BOLT-835');
    await categoryPartForm.locator('input[name="brand"]').fill('Browser Brand');
    await categoryPartForm.locator('input[name="model"]').fill('M8x35');
    await categoryPartForm.locator('input[name="material"]').fill('Steel');
    await categoryPartForm.locator('input[name="length_mm"]').fill('35');
    await categoryPartForm.locator('select[name="dimension_unit"]').selectOption('in');
    await categoryPartForm.locator('input[name="weight_g"]').fill('12.5');
    await categoryPartForm.locator('select[name="weight_unit"]').selectOption('lb');
    await categoryPartForm.locator('input[name="purchase_date"]').fill('2026-02-14');
    await categoryPartForm.locator('input[name="purchase_price"]').fill('0.42');
    await categoryPartForm.locator('input[name="product_url"]').fill('https://example.com/browser-bolt');
    await categoryPartForm.locator(`select[name="spec_${attributeID}"]`).selectOption('8.8');
    await categoryPartForm.locator('button[type="submit"]').click();
    await page.waitForURL(/\/parts\/\d+$/);
    const categoryPartID = Number(page.url().match(/\/parts\/(\d+)$/)[1]);
    const productCategoryRows = readOnlyRows(databasePath, 'SELECT pc.category_id,v.value_text FROM part_categories pc JOIN part_attribute_values v ON v.part_id=pc.part_id WHERE pc.part_id=? AND v.attribute_id=?', [categoryPartID, attributeID]);
    assert.deepEqual(productCategoryRows, [[categoryID, '8.8']], 'product category and custom specification should persist together');
    const standardDetails = readOnlyRows(databasePath, 'SELECT sku,brand,model,material,length_mm,dimension_unit,weight_g,weight_unit,purchase_date,purchase_price,product_url FROM part_details WHERE part_id=?', [categoryPartID]);
    assert.deepEqual(standardDetails, [['BROWSER-BOLT-835', 'Browser Brand', 'M8x35', 'Steel', 35, 'in', 12.5, 'lb', '2026-02-14', 0.42, 'https://example.com/browser-bolt']], 'general physical, measurement-unit and purchase fields should persist');
    await page.goto(`${baseURL}/categories/${categoryID}`);
    await Promise.all([
      page.waitForURL(`${baseURL}/labels/category/${categoryID}`, { waitUntil: 'domcontentloaded' }),
      page.locator(`a[href="/labels/category/${categoryID}"]`).click(),
    ]);
    assert.match(await page.locator('body').innerText(), /QR.*Code 128/s, 'category label must print both QR and Code 128');
    await page.goto(`${baseURL}/scan?q=${encodeURIComponent(`wledger:category:${categoryID}`)}`);
    await page.waitForURL(`${baseURL}/categories/${categoryID}`);
    await page.locator(`form[action="/categories/${categoryID}/delete"] button`).click();
    await page.waitForURL(`${baseURL}/categories`);
    assert.equal(Number(readOnlyRows(databasePath, 'SELECT COUNT(*) FROM parts WHERE id=?', [categoryPartID])[0][0]), 1, 'category deletion must preserve products');
    assert.equal(Number(readOnlyRows(databasePath, 'SELECT COUNT(*) FROM part_categories WHERE part_id=?', [categoryPartID])[0][0]), 0, 'deleted category must safely uncategorize products');
    assert.ok(!fs.existsSync(categoryImageFile), 'deleting a category should clean up its image asset');
    const csrfToken = await page.locator('meta[name="csrf-token"]').getAttribute('content');
    const deleteProductStatus = await page.evaluate(async ({ id, token }) => {
      const response = await fetch(`/parts/${id}/delete`, {
        method: 'POST', credentials: 'same-origin',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: new URLSearchParams({ csrf_token: token }),
      });
      return response.status;
    }, { id: categoryPartID, token: csrfToken });
    assert.equal(deleteProductStatus, 200, 'temporary category acceptance product should be removable');

    const emptyCounts = readOnlyCounts(databasePath);
    for (const table of ['controllers', 'controller_outputs', 'cabinets', 'drawers', 'bins', 'containers', 'cabinet_controllers', 'bin_drawer_attachments', 'parts', 'part_assignments', 'categories', 'part_categories', 'category_attributes', 'part_attribute_values', 'cabinet_led_strings', 'cabinet_led_path_sections', 'drawer_led_allocations']) {
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
      await form.locator('select[name="controller_type"]').selectOption(index === 0 ? 'wled-esp32' : 'wled');
      await form.locator('input[name="connection_timeout_ms"]').fill(index === 0 ? '5500' : '2000');
      const outputEditor = form.locator('[x-data^="controllerOutputEditor"]');
      await outputEditor.getByRole('button', { name: 'Add output' }).click();
      await outputEditor.locator('input[x-model="output.name"]').nth(0).fill(`Physical string ${index + 1}`);
      await outputEditor.locator('input[x-model\\.number="output.pixel_count"]').nth(0).fill(String(index === 0 ? 80 : capacities[index]));
      if (index === 0) {
        await outputEditor.getByRole('button', { name: 'Add output' }).click();
        await outputEditor.locator('input[x-model="output.name"]').nth(1).fill('Physical string 2');
        await outputEditor.locator('input[x-model\\.number="output.pixel_count"]').nth(1).fill('120');
        await outputEditor.locator('select[x-model="output.direction"]').nth(1).selectOption('reverse');
        await outputEditor.locator('select[x-model="output.pattern"]').nth(1).selectOption('serpentine_rows');
        await outputEditor.locator('input[x-model\\.number="output.rows"]').nth(1).fill('12');
        await outputEditor.locator('input[x-model\\.number="output.columns"]').nth(1).fill('10');
      }
      await form.locator('button.btn-primary').click();
      await page.waitForURL(`${baseURL}/hardware`);
      const rows = readOnlyRows(databasePath, 'SELECT id, name, ip_address, port, led_count, controller_type, connection_timeout_ms FROM controllers WHERE name = ? ORDER BY id', [`UI Controller ${index + 1}`]);
      assert.equal(rows.length, 1, `controller ${index + 1} should be created through its browser form`);
      assert.deepEqual([Number(rows[0][4]), rows[0][5], Number(rows[0][6])], [capacities[index], index === 0 ? 'wled-esp32' : 'wled', index === 0 ? 5500 : 2000], 'controller type, connection timeout and total output capacity should persist');
      const outputRows = readOnlyRows(databasePath, 'SELECT display_order,name,pixel_count,direction,pattern,rows,columns FROM controller_outputs WHERE controller_id=? ORDER BY display_order', [Number(rows[0][0])]);
      assert.equal(outputRows.length, outputCounts[index], 'configured output count should persist');
      if (index === 0) {
        assert.deepEqual(outputRows, [[0, 'Physical string 1', 80, 'forward', 'linear', 1, 1], [1, 'Physical string 2', 120, 'reverse', 'serpentine_rows', 12, 10]], 'controller outputs with differing lengths, direction and serpentine rows should persist in order');
      } else {
        assert.deepEqual(outputRows, [[0, `Physical string ${index + 1}`, capacities[index], 'forward', 'linear', 1, 1]], 'single-output controller definitions should persist');
      }
      controllers.push({ id: Number(rows[0][0]), name: rows[0][1], ip: rows[0][2], port: Number(rows[0][3]), capacity: Number(rows[0][4]) });
    }

    assert.ok(await page.locator('form[action^="/hardware/"][action$="/delete"]').count() >= 5);
    await page.goto(`${baseURL}/hardware/${controllers[0].id}/edit`);
    const editForm = page.locator(`form[action="/hardware/${controllers[0].id}/update"]`);
    await editForm.locator('input[name="name"]').fill('UI Controller A Edited');
    const editedOutput = editForm.locator('[x-data^="controllerOutputEditor"]');
    await editedOutput.locator('select[x-model="output.pattern"]').nth(1).selectOption('serpentine_columns');
    await editedOutput.locator('input[x-model\\.number="output.rows"]').nth(1).fill('10');
    await editedOutput.locator('input[x-model\\.number="output.columns"]').nth(1).fill('12');
    await editForm.locator('button[type="submit"]').click();
    await page.waitForURL(/controller_saved=1/);
    const editedController = readOnlyRows(databasePath, 'SELECT id,name,ip_address,port FROM controllers WHERE id=?', [controllers[0].id]);
    assert.deepEqual(editedController[0].map((value, index) => index === 0 || index === 3 ? Number(value) : value), [controllers[0].id, 'UI Controller A Edited', controllers[0].ip, controllers[0].port], 'controller edit must preserve its ID and endpoint');
    controllers[0].name = 'UI Controller A Edited';
    const editedOutputRows = readOnlyRows(databasePath, 'SELECT display_order,name,pixel_count,direction,pattern,rows,columns FROM controller_outputs WHERE controller_id=? ORDER BY display_order', [controllers[0].id]);
    assert.deepEqual(editedOutputRows, [[0, 'Physical string 1', 80, 'forward', 'linear', 1, 1], [1, 'Physical string 2', 120, 'reverse', 'serpentine_columns', 10, 12]], 'controller edit should persist output direction and serpentine configuration');
    await page.goto(`${baseURL}/hardware/${controllers[0].id}/edit`);
    const restoreDirectionForm = page.locator(`form[action="/hardware/${controllers[0].id}/update"]`);
    await restoreDirectionForm.locator('select[x-model="output.direction"]').nth(1).selectOption('forward');
    await restoreDirectionForm.locator('button[type="submit"]').click();
    await page.waitForURL(/controller_saved=1/);

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
    const cabinetOutputs = readOnlyRows(databasePath, 'SELECT controller_id,id,name,pixel_count,display_order FROM cabinet_led_strings WHERE cabinet_id=? ORDER BY controller_id,display_order', [cabinetID]);
    assert.equal(cabinetOutputs.length, outputCounts.reduce((sum, count) => sum + count, 0), 'cabinet assignment should project configured controller outputs as path rows');
    for (let index = 0; index < cabinetOutputs.length; index++) {
      const [controllerID, outputID, outputName, pixelCount] = cabinetOutputs[index];
      const controllerIndex = controllers.findIndex(controller => controller.id === controllerID);
      const controller = controllers[controllerIndex];
      const panel = page.locator('div.rounded.bg-base-100.p-2.space-y-2').filter({ hasText: `Controller ${controllerIndex + 1}: ${controller.name}` }).first();
      const outputPanel = panel.locator('div.rounded.border.border-base-300.p-2.space-y-2').filter({ hasText: outputName }).first();
      assert.ok(await outputPanel.count(), `output ${outputID} should be shown as a cabinet path target`);
      await outputPanel.getByRole('button', { name: '＋ Add path section' }).click();
      await outputPanel.getByRole('button', { name: '＋ Add path section' }).click();
      const sections = outputPanel.locator('input[x-model="section.pixel_count"]');
      await sections.nth(0).fill(String(pixelCount / 2));
      await sections.nth(1).fill(String(pixelCount / 2));
      for (let sectionIndex = 0; sectionIndex < 2; sectionIndex++) {
        await outputPanel.getByRole('button', { name: 'Draw path' }).nth(sectionIndex).click();
        await canvas.scrollIntoViewIfNeeded();
        const y = 20 + index * 10 + sectionIndex * 5;
        if (sectionIndex === 0) {
          const start = await screenPoint(10, y); const end = await screenPoint(190, y);
          await page.mouse.click(start.x, start.y); await page.mouse.click(end.x, end.y);
        } else {
          const start = await screenPoint(190, y); const end = await screenPoint(10, y);
          await page.mouse.click(start.x, start.y); await page.mouse.click(end.x, end.y);
        }
        await canvas.evaluate(svg => svg.dispatchEvent(new MouseEvent('dblclick', { bubbles: true })));
      }
      await outputPanel.locator('input[x-model="string.layout_complete"]').check();
    }
    await page.getByRole('button', { name: 'Save output paths' }).click();
    try {
      await page.waitForURL(/wiring_saved=1/, { timeout: 5000 });
    } catch (error) {
      throw new Error(`wiring save did not complete: url=${page.url()} body=${(await page.locator('body').innerText()).slice(-5000)} cause=${error.message}`);
    }
    const stringRows = readOnlyRows(databasePath, 'SELECT controller_id, pixel_count FROM cabinet_led_strings WHERE cabinet_id = ? ORDER BY controller_id, display_order', [cabinetID]);
    assert.deepEqual(stringRows.map(row => [Number(row[0]), Number(row[1])]), cabinetOutputs.map(row => [Number(row[0]), Number(row[3])]));
    const sectionRows = readOnlyRows(databasePath, 'SELECT string_id, display_order, pixel_count, points_json FROM cabinet_led_path_sections WHERE string_id IN (SELECT id FROM cabinet_led_strings WHERE cabinet_id = ?) ORDER BY string_id, display_order', [cabinetID]);
    assert.equal(sectionRows.length, outputCounts.reduce((sum, count) => sum + count, 0) * 2, 'each hardware-configured output should have two directed cabinet path sections');
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
    const uploadedImage = readOnlyRows(databasePath, 'SELECT image_path FROM parts WHERE id=?', [firstPart.id])[0][0];
    assert.ok(uploadedImage, 'part creation should persist the initial browser image upload');
    const imageFiles = webPath => {
      const name = path.basename(webPath);
      const base = path.parse(name).name;
      return [path.join(scratchDir, 'app', 'uploads', 'images', name), path.join(scratchDir, 'app', 'uploads', 'images', `${base}_thumb.jpg`)];
    };
    const assertImageFiles = (webPath, exists) => {
      for (const file of imageFiles(webPath)) assert.equal(fs.existsSync(file), exists, `${file} exists should be ${exists}`);
    };
    assertImageFiles(uploadedImage, true);

    const duplicateOwner = createdParts[1];
    await page.goto(`${baseURL}/parts/${duplicateOwner.id}/edit`);
    const duplicateOwnerForm = page.locator(`form[action="/parts/${duplicateOwner.id}/update"]`);
    await duplicateOwnerForm.locator('input[name="barcode_data"]').fill('UI-DUPLICATE-BARCODE');
    await duplicateOwnerForm.locator('button[type="submit"]').click();
    await page.waitForURL(`${baseURL}/parts/${duplicateOwner.id}`);

    await page.goto(`${baseURL}/parts/${firstPart.id}/edit`);
    let imageEditForm = page.locator(`form[action="/parts/${firstPart.id}/update"]`);
    await imageEditForm.locator('textarea[name="description"]').fill('Edited without changing the cover image');
    await imageEditForm.locator('button[type="submit"]').click();
    await page.waitForURL(`${baseURL}/parts/${firstPart.id}`);
    assert.equal(readOnlyRows(databasePath, 'SELECT image_path FROM parts WHERE id=?', [firstPart.id])[0][0], uploadedImage, 'ordinary edits should preserve the existing image reference');
    assertImageFiles(uploadedImage, true);

    await page.goto(`${baseURL}/parts/${firstPart.id}/edit`);
    imageEditForm = page.locator(`form[action="/parts/${firstPart.id}/update"]`);
    await imageEditForm.locator('input[name="image"]').setInputFiles({ name: 'replacement.png', mimeType: 'image/png', buffer: testImage });
    await imageEditForm.locator('button[type="submit"]').click();
    await page.waitForURL(`${baseURL}/parts/${firstPart.id}`);
    const replacementImage = readOnlyRows(databasePath, 'SELECT image_path FROM parts WHERE id=?', [firstPart.id])[0][0];
    assert.ok(replacementImage && replacementImage !== uploadedImage, 'replacement should persist a new image reference');
    assertImageFiles(uploadedImage, false);
    assertImageFiles(replacementImage, true);
    await page.locator(`img[src="${replacementImage}"]`).waitFor();

    const duplicatePartCount = Number(readOnlyRows(databasePath, 'SELECT COUNT(*) FROM parts')[0][0]);
    await page.goto(`${baseURL}/parts/new`);
    const duplicatePartForm = page.locator('form[action="/parts"]');
    await duplicatePartForm.locator('input[name="name"]').fill('Duplicate barcode should be rejected');
    await duplicatePartForm.locator('input[name="barcode_data"]').fill('UI-DUPLICATE-BARCODE');
    const duplicatePartResponse = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/parts');
    await duplicatePartForm.locator('button[type="submit"]').click();
    assert.equal((await duplicatePartResponse).status(), 409, 'duplicate product barcode should be rejected');
    assert.equal(Number(readOnlyRows(databasePath, 'SELECT COUNT(*) FROM parts')[0][0]), duplicatePartCount, 'duplicate create must not mutate the product table');

    await page.goto(`${baseURL}/parts/${firstPart.id}/edit`);
    imageEditForm = page.locator(`form[action="/parts/${firstPart.id}/update"]`);
    await imageEditForm.locator('input[name="remove_image"]').check();
    await imageEditForm.locator('input[name="barcode_data"]').fill('UI-DUPLICATE-BARCODE');
    const failedRemovalResponse = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === `/parts/${firstPart.id}/update`);
    await imageEditForm.locator('button[type="submit"]').click();
    assert.ok((await failedRemovalResponse).status() >= 400, 'failed duplicate update with image removal should return an error');
    assert.equal(readOnlyRows(databasePath, 'SELECT image_path FROM parts WHERE id=?', [firstPart.id])[0][0], replacementImage, 'failed update must preserve the database image reference');
    assertImageFiles(replacementImage, true);

    await page.goto(`${baseURL}/parts/${firstPart.id}/edit`);
    imageEditForm = page.locator(`form[action="/parts/${firstPart.id}/update"]`);
    await imageEditForm.locator('input[name="remove_image"]').check();
    await imageEditForm.locator('button[type="submit"]').click();
    await page.waitForURL(`${baseURL}/parts/${firstPart.id}`);
    assert.equal(readOnlyRows(databasePath, 'SELECT image_path FROM parts WHERE id=?', [firstPart.id])[0][0], null, 'explicit removal should clear the persisted image reference');
    assertImageFiles(replacementImage, false);
    assert.equal(await page.locator('figure img').count(), 0, 'part detail should stop displaying the removed image');

    await page.goto(`${baseURL}/parts/${duplicateOwner.id}/edit`);
    const retainedImageForm = page.locator(`form[action="/parts/${duplicateOwner.id}/update"]`);
    await retainedImageForm.locator('input[name="image"]').setInputFiles({ name: 'archive-image.png', mimeType: 'image/png', buffer: testImage });
    await retainedImageForm.locator('button[type="submit"]').click();
    await page.waitForURL(`${baseURL}/parts/${duplicateOwner.id}`);
    const retainedImage = readOnlyRows(databasePath, 'SELECT image_path FROM parts WHERE id=?', [duplicateOwner.id])[0][0];
    assert.ok(retainedImage, 'a separate image remains available for backup/restore acceptance');
    assertImageFiles(retainedImage, true);

    await page.goto(`${baseURL}/parts/${firstPart.id}`);
    const stockRowsBeforeInvalid = readOnlyRows(databasePath, 'SELECT id,bin_id,quantity FROM part_assignments WHERE part_id=? ORDER BY id', [firstPart.id]);
    const invalidAssignForm = page.locator(`form[action="/parts/${firstPart.id}/assign"]`);
    const invalidAssignResponse = await page.evaluate(async form => {
      const body = new FormData(form); body.set('bin_id', '0'); body.set('quantity', '0');
      return (await fetch(form.action, { method: 'POST', body, credentials: 'same-origin' })).status;
    }, await invalidAssignForm.elementHandle());
    assert.equal(invalidAssignResponse, 400, 'invalid stock assignment must be rejected');
    assert.deepEqual(readOnlyRows(databasePath, 'SELECT id,bin_id,quantity FROM part_assignments WHERE part_id=? ORDER BY id', [firstPart.id]), stockRowsBeforeInvalid, 'invalid assignment must not mutate existing stock');

    await page.goto(`${baseURL}/parts/${firstPart.id}`);
    const firstStockRow = page.locator('tr').filter({ hasText: inventoryBins[0][1] });
    await firstStockRow.locator('button[hx-post$="delta=1"]').click();
    await page.waitForFunction(name => Array.from(document.querySelectorAll('tr')).some(row => row.innerText.includes(name) && row.innerText.includes('9')), inventoryBins[0][1]);
    await firstStockRow.locator('button[hx-post$="delta=-1"]').click();
    await page.waitForFunction(name => Array.from(document.querySelectorAll('tr')).some(row => row.innerText.includes(name) && row.innerText.includes('8')), inventoryBins[0][1]);
    const sourceAssignment = Number(readOnlyRows(databasePath, 'SELECT id FROM part_assignments WHERE part_id = ? AND bin_id = ?', [firstPart.id, firstPart.binID])[0][0]);
    const moveModalID = `move_stock_modal_${sourceAssignment}`;
    await page.locator(`button[onclick="openStockMoveModal('${moveModalID}')"]`).click();
    await page.locator(`#${moveModalID}`).getByRole('button', { name: 'Use manual destination selection' }).click();
    const movePicker = page.locator(`#${moveModalID}_visual_bin_picker button[data-bin-id="${inventoryBins[1][0]}"]`);
    await movePicker.waitFor({ state: 'visible' });
    await movePicker.click();
    acceptDialogs = false;
    await page.locator(`button[form="${moveModalID}_form"]`).click();
    await page.waitForTimeout(100);
    assert.equal(new URL(page.url()).pathname, `/parts/${firstPart.id}`, 'cancelled manual stock move should leave the browser on the product');
    assert.deepEqual(readOnlyRows(databasePath, 'SELECT bin_id, quantity FROM part_assignments WHERE part_id = ? ORDER BY bin_id', [firstPart.id]).map(row => row.map(Number)), [[firstPart.binID, 8]], 'cancelled manual move must not change stock');
    acceptDialogs = true;
    const moveNavigation = page.waitForNavigation();
    await page.locator(`button[form="${moveModalID}_form"]`).click();
    await moveNavigation;
    const movedAssignments = readOnlyRows(databasePath, 'SELECT bin_id, quantity FROM part_assignments WHERE part_id = ? ORDER BY bin_id', [firstPart.id]);
    assert.deepEqual(movedAssignments.map(row => row.map(Number)), [[Number(inventoryBins[1][0]), 8]], 'moving stock should preserve quantity and change its bin through the browser');

    await page.goto(`${baseURL}/parts/${firstPart.id}`);
    await page.locator('button[onclick="stock_modal.showModal()"]:visible').click();
    const extraBin = page.locator(`#visual_bin_picker button[data-bin-id="${inventoryBins[2][0]}"]`);
    await extraBin.waitFor({ state: 'visible' });
    await extraBin.click();
    const extraStockForm = page.locator(`form[action="/parts/${firstPart.id}/assign"]`);
    await extraStockForm.locator('input[name="quantity"]').fill('3');
    const extraAssignNavigation = page.waitForNavigation();
    await extraStockForm.locator('button[type="submit"]').click();
    await extraAssignNavigation;
    let lifecycleAssignments = readOnlyRows(databasePath, 'SELECT id,bin_id,quantity FROM part_assignments WHERE part_id=? ORDER BY bin_id', [firstPart.id]);
    assert.deepEqual(lifecycleAssignments.map(row => row.map(Number)), [[lifecycleAssignments[0][0], Number(inventoryBins[1][0]), 8], [lifecycleAssignments[1][0], Number(inventoryBins[2][0]), 3]], 'manual assignment should add stock to a second physical bin');

    const removeAssignmentID = Number(lifecycleAssignments[1][0]);
    await page.goto(`${baseURL}/parts/${firstPart.id}`);

    const removeStockForm = page.locator(`form[action="/parts/${firstPart.id}/stock/${removeAssignmentID}/delete"]`);
    acceptDialogs = false;
    await removeStockForm.locator('button').click();
    await page.waitForTimeout(100);
    assert.deepEqual(readOnlyRows(databasePath, 'SELECT id,bin_id,quantity FROM part_assignments WHERE part_id=? ORDER BY bin_id', [firstPart.id]).map(row => row.map(Number)), lifecycleAssignments.map(row => row.map(Number)), 'cancelled stock assignment removal must not mutate either location');
    acceptDialogs = true;
    const removeStockNavigation = page.waitForNavigation();
    await removeStockForm.locator('button').click();
    await removeStockNavigation;
    lifecycleAssignments = readOnlyRows(databasePath, 'SELECT id,bin_id,quantity FROM part_assignments WHERE part_id=? ORDER BY bin_id', [firstPart.id]);
    assert.deepEqual(lifecycleAssignments.map(row => row.slice(1).map(Number)), [[Number(inventoryBins[1][0]), 8]], 'removing one assignment should preserve stock in the other physical bin');

    await page.getByRole('button', { name: 'Delete', exact: true }).click();
    const deletePartNavigation = page.waitForNavigation();
    await page.locator(`#delete_modal_${firstPart.id} form[action="/parts/${firstPart.id}/delete"] button[type="submit"]`).click();
    await deletePartNavigation;
    assert.equal(Number(readOnlyRows(databasePath, 'SELECT COUNT(*) FROM parts WHERE id=?', [firstPart.id])[0][0]), 0, 'confirmed product deletion should follow existing application rules');
    assert.deepEqual(readOnlyRows(databasePath, 'SELECT id FROM part_assignments WHERE part_id=?', [firstPart.id]), [], 'part deletion should remove its remaining stock assignment through existing database semantics');

    await page.goto(`${baseURL}/parts?bin=${inventoryBins[1][0]}`);
    assert.match(await page.locator('#parts-grid').innerText(), /UI Part 2/, 'physical-bin stock search should find the product assigned to that bin');
    assert.doesNotMatch(await page.locator('#parts-grid').innerText(), /UI Part 10/, 'physical-bin search should exclude products without stock in the selected bin');

    await page.goto(`${baseURL}/parts`);
    await page.locator('#scan-target-q').fill('UI Part 10');
    await page.waitForFunction(() => new URL(location.href).searchParams.get('q') === 'UI Part 10' && document.body.innerText.includes('UI Part 10'));
    assert.doesNotMatch(await page.locator('#parts-grid').innerText(), /UI Part 1(?!0)/, 'search after deletion should show the matching product, not the deleted product');

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
    if (inventoryOnly) {
      locateEvidence = { status: 'SKIPPED', reason: 'Inventory lifecycle acceptance keeps WLED output disabled and does not initiate Locate/Stop.' };
    } else if (mappingAttestationsPassed === controllers.length) {
      locateEvidence = await exerciseMockLocate(page, baseURL, firstPart.id, inventoryBins, mockWrites, mockRequests);
    }
    if (inventoryOnly) assert.equal(mockWrites.length, 0, 'inventory-only browser acceptance must not send WLED state writes');

    let crashRecoveryEvidence = { status: 'NOT RUN' };
    if (task065RestoreEnabled && !inventoryOnly && mappingAttestationsPassed === controllers.length) {

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

    if (task067Enabled) {
      const adminRows = readOnlyRows(databasePath, 'SELECT id,password_hash FROM users WHERE email=?', ['ui-only-admin@example.invalid']);
      assert.equal(adminRows.length, 1, 'multi-process fixture requires the existing synthetic administrator');
      const secondAdminEmail = 'task067-second-process-admin@example.invalid';
      const secondAdminID = executeSQLite(databasePath, 'INSERT INTO users(email,password_hash,role,change_password_required) VALUES (?,?,?,0)', [secondAdminEmail, adminRows[0][1], 'admin']);
      const secondProcessPort = await availablePort();
      const secondProcessURL = `http://127.0.0.1:${secondProcessPort}`;
      secondServer = startServer(scratchDir, secondProcessPort);
      await waitForServer(secondProcessURL, secondServer);
      secondContext = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
      secondPage = await secondContext.newPage();
      await secondPage.goto(`${secondProcessURL}/login`);
      await secondPage.locator('form[action="/login"] input[name="email"]').fill(secondAdminEmail);
      await secondPage.locator('form[action="/login"] input[name="password"]').fill('ui-only-safe-password-123');
      await secondPage.locator('form[action="/login"] button').click();
      await secondPage.waitForURL(`${secondProcessURL}/`);
      const sharedLocateRows = () => readOnlyRows(databasePath, 'SELECT session_id,state,drawer_id,bin_id,owner_id FROM physical_locate_sessions WHERE state IN (\'activating\',\'active\',\'stopping\',\'partial\')');
      const sharedTargetRows = sessionID => readOnlyRows(databasePath, 'SELECT controller_id,may_have_received,commands_json FROM physical_locate_session_targets WHERE session_id=? ORDER BY controller_id', [sessionID]);
      const assertNoSharedOwnership = label => {
        assert.deepEqual(sharedLocateRows(), [], `${label}: no active or partial Locate sessions should exist before injection`);
        assert.equal(Number(readOnlyRows(databasePath, 'SELECT COUNT(*) FROM physical_locate_session_targets WHERE may_have_received=1')[0][0]), 0, `${label}: no targets should retain uncertain-delivery ownership before injection`);
      };
      const awaitSharedMockDisconnect = async record => {
        for (let attempt = 0; attempt < 100; attempt++) {
          if (record.responseBehavior === 'command applied; client disconnected before response') return;
          await new Promise(resolve => setTimeout(resolve, 25));
        }
        throw new Error(`shared-process controller request did not observe client termination: ${JSON.stringify(record)}`);
      };

      assertNoSharedOwnership('before Task 067 multi-process Locate race');
      const multiProcessPixelsBefore = structuredClone(mockPixels);
      const processPIDs = [server.pid, secondServer.pid];
      const writesBeforeConcurrentLocate = mockWrites.length;
      let acceptedActivation;
      const activationAccepted = new Promise(resolve => { acceptedActivation = resolve; });
      mockBehaviors[0].dropNextPostResponse = true;
      mockBehaviors[0].onDropAccepted = record => acceptedActivation(record);

      const targetBinID = inventoryBins[1][0];
      const targetPartID = firstPart.id + 1;
      await Promise.all([
        page.goto(`${baseURL}/parts/${targetPartID}`),
        secondPage.goto(`${secondProcessURL}/parts/${targetPartID}`),
      ]);
      const locatePath = `/parts/${targetPartID}/locate-bin`;
      const requestA = page.waitForRequest(request => request.method() === 'POST' && new URL(request.url()).pathname === locatePath, { timeout: 15000 });
      const formA = page.locator(`form[action="${locatePath}"]`).filter({ has: page.locator(`input[name="bin_id"][value="${targetBinID}"]`) });
      const formB = secondPage.locator(`form[action="${locatePath}"]`).filter({ has: secondPage.locator(`input[name="bin_id"][value="${targetBinID}"]`) });
      await submitFormWithoutWaiting(formA);
      await requestA;
      const acceptedRequest = await Promise.race([
        activationAccepted,
        new Promise((_, reject) => setTimeout(() => reject(new Error('process A did not reach the shared mock WLED controller')), 10000)),
      ]);
      const activeConcurrentRows = sharedLocateRows();
      assert.equal(activeConcurrentRows.length, 1, `process A reservation count = ${activeConcurrentRows.length}: ${JSON.stringify(activeConcurrentRows)}`);
      assert.equal(activeConcurrentRows[0][1], 'activating');
      const concurrentSessionID = activeConcurrentRows[0][0];
      const concurrentOwnerID = Number(activeConcurrentRows[0][4]);
      assert.equal(concurrentOwnerID, Number(adminRows[0][0]), 'process A should own the deliberately first reserved operation');
      const ownerWasMainProcess = true;
      const concurrentTargets = sharedTargetRows(concurrentSessionID);
      assert.deepEqual(concurrentTargets.map(target => Number(target[1])), [1], 'attempt marker must commit before the shared controller accepts the POST');

      // Process B submits a conflicting Locate while A holds the acknowledged
      // activation response open and SQLite operation claim active.
      const requestB = secondPage.waitForRequest(request => request.method() === 'POST' && new URL(request.url()).pathname === locatePath, { timeout: 15000 });
      const locateResponseB = secondPage.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === locatePath, { timeout: 15000 });
      await submitFormWithoutWaiting(formB);
      await requestB;
      const losingLocateResponse = await locateResponseB;
      assert.equal(losingLocateResponse.status(), 409, `process B conflicting Locate response was ${losingLocateResponse.status()}`);
      assert.deepEqual(sharedLocateRows(), activeConcurrentRows, 'process B must not replace process A durable ownership');
      assert.equal(mockWrites.length, writesBeforeConcurrentLocate + 1, 'only process A may activate while its controller response is withheld');

      const stopPath = '/storage/drawers/1/stop';
      const foreignPage = ownerWasMainProcess ? secondPage : page;
      const foreignURL = ownerWasMainProcess ? secondProcessURL : baseURL;
      const processBStopResponse = foreignPage.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === stopPath);
      await submitPostWithoutWaiting(foreignPage, `${foreignURL}${stopPath}`);
      const rejectedForeignStop = await processBStopResponse;
      assert.equal(rejectedForeignStop.status(), 409, `second-process Stop response was ${rejectedForeignStop.status()}`);
      assert.deepEqual(sharedLocateRows(), activeConcurrentRows, 'a different owner/process must not change the active durable reservation');
      assert.equal(mockWrites.length, writesBeforeConcurrentLocate + 1, 'conflicting Locate/Stop from process B must not send additional WLED commands');

      const owningProcess = ownerWasMainProcess ? server : secondServer;
      if (!owningProcess) throw new Error(`could not identify the Locate-owning process for user ${concurrentOwnerID}`);
      owningProcess.kill('SIGKILL');
      await waitForProcessExit(owningProcess);
      await awaitSharedMockDisconnect(acceptedRequest);
      if (ownerWasMainProcess) server = null;
      else secondServer = null;

      const unresolvedAfterKill = sharedLocateRows();
      assert.equal(unresolvedAfterKill.length, 1, 'owner process SIGKILL must leave one durable unresolved session');
      assert.equal(unresolvedAfterKill[0][0], concurrentSessionID);
      assert.equal(unresolvedAfterKill[0][1], 'activating');
      assert.deepEqual(sharedTargetRows(concurrentSessionID).map(target => Number(target[1])), [1], 'owner process SIGKILL must preserve the possible-delivery marker');

      const controllerRow = readOnlyRows(databasePath, 'SELECT id,ip_address,port FROM controllers WHERE name=?', ['UI Controller A Edited'])[0];
      const controllerUpdateResponse = secondPage.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === `/hardware/${controllerRow[0]}/update`);
      await submitPostWithoutWaiting(secondPage, `${secondProcessURL}/hardware/${controllerRow[0]}/update`, { name: 'Unsafe concurrent endpoint change', ip_address: '192.0.2.99', port: '8080' });
      const rejectedEndpointChange = await controllerUpdateResponse;
      const endpointAfterRejectedChange = readOnlyRows(databasePath, 'SELECT ip_address,port FROM controllers WHERE id=?', [controllerRow[0]]);
      assert.equal(rejectedEndpointChange.status(), 409, `unresolved cross-process endpoint edit response was ${rejectedEndpointChange.status()}`);
      assert.deepEqual(endpointAfterRejectedChange, [[controllerRow[1], controllerRow[2]]], 'rejected cross-process endpoint edit must preserve the original recovery target');
      assert.equal(mockWrites.length, writesBeforeConcurrentLocate + 1, 'rejected cross-process endpoint edit must not write WLED');

      // The other process attempts recovery but cannot impersonate the saved owner.
      await foreignPage.goto(`${foreignURL}/storage/drawers/1?bin_id=${targetBinID}`);
      const foreignRetry = foreignPage.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === stopPath);
      await submitPostWithoutWaiting(foreignPage, `${foreignURL}${stopPath}`);
      const foreignRetryResponse = await foreignRetry;
      assert.equal(foreignRetryResponse.status(), 409, `foreign recovery request response was ${foreignRetryResponse.status()}`);
      assert.deepEqual(sharedLocateRows(), unresolvedAfterKill, 'foreign-owner recovery must fail closed without releasing durable ownership');
      assert.equal(mockWrites.length, writesBeforeConcurrentLocate + 1, 'foreign-owner recovery must not issue cleanup commands');

      // Restart the owning application process; its original durable owner can
      // now issue the saved targeted cleanup against the same loopback mock.
      const owningPage = ownerWasMainProcess ? page : secondPage;
      const owningURL = ownerWasMainProcess ? baseURL : secondProcessURL;
      if (ownerWasMainProcess) {
        server = startServer(scratchDir, appPort);
        await waitForServer(baseURL, server);
      } else {
        secondServer = startServer(scratchDir, secondProcessPort);
        await waitForServer(secondProcessURL, secondServer);
      }
      await owningPage.goto(`${owningURL}/storage/drawers/1?bin_id=${targetBinID}`);
      const ownerStopResponse = owningPage.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === stopPath);
      await submitFormWithoutWaiting(owningPage.locator(`form[action="${stopPath}"]`));
      const ownerStop = await ownerStopResponse;
      assert.ok(ownerStop.status() < 400, `restarted owner Stop response was ${ownerStop.status()}`);
      assert.deepEqual(sharedLocateRows(), [], 'restarted owner recovery should release the session only after confirmed cleanup');
      const multiProcessRecoveryWrites = mockWrites.slice(writesBeforeConcurrentLocate + 1);
      assert.deepEqual(multiProcessRecoveryWrites.map(write => write.payload.seg[0].i), [[24, 34, [0, 0, 0]], [49, 54, [0, 0, 0]]], 'restarted owner must use only the saved pixel ranges');
      assert.ok(multiProcessRecoveryWrites.every(write => !Object.hasOwn(write.payload, 'on')), 'cross-process recovery must never issue GlobalOff');
      for (let controllerIndex = 0; controllerIndex < mockPixels.length; controllerIndex++) {
        for (let pixelIndex = 0; pixelIndex < mockPixels[controllerIndex].length; pixelIndex++) {
          const owned = controllerIndex === 0 && ((pixelIndex >= 24 && pixelIndex < 34) || (pixelIndex >= 49 && pixelIndex < 54));
          if (owned) assert.deepEqual(mockPixels[controllerIndex][pixelIndex], [0, 0, 0], `owned shared-process pixel ${controllerIndex + 1}:${pixelIndex} should be black after recovery`);
          else assert.deepEqual(mockPixels[controllerIndex][pixelIndex], multiProcessPixelsBefore[controllerIndex][pixelIndex], `unrelated shared-process pixel ${controllerIndex + 1}:${pixelIndex} must be preserved`);
        }
      }
      multiProcessEvidence = {
        status: 'PASS',
        databasePath,
        uploadsPath: path.join(scratchDir, 'app', 'uploads'),
        processPorts: [appPort, secondProcessPort],
        processIDs: processPIDs,
        independentProcesses: true,
        simultaneousConflictingLocates: true,
        durableOwner: concurrentOwnerID,
        processAKilledBy: 'SIGKILL while mock had applied activation and withheld response',
        processBStopRejectedWithoutWLEDWrite: true,
        unresolvedControllerEndpointEditRejected: true,
        foreignRecoveryRejectedWithoutReleasingOwnership: true,
        restartedOwnerTargetedRecovery: multiProcessRecoveryWrites,
        framebufferUnrelatedPixelsPreserved: true,
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
    if (task067Enabled) {
      const expectedTask067ConsoleErrors = [
        ['/parts', 'Failed to load resource: the server responded with a status of 409 (Conflict)'],
        ['/parts/2', 'Failed to load resource: the server responded with a status of 400 (Bad Request)'],
        ['/parts/2/update', 'Failed to load resource: the server responded with a status of 500 (Internal Server Error)'],
        ['/parts/3', 'Failed to load resource: net::ERR_CONNECTION_REFUSED'],
        ['/parts/3', 'Failed to load resource: net::ERR_CONNECTION_REFUSED'],
        ['/storage/drawers/1', 'Failed to load resource: net::ERR_CONNECTION_REFUSED'],
      ];
      assert.deepEqual(nonValidationConsoleErrors.map(error => [new URL(error.url).pathname, error.message]).sort(), expectedTask067ConsoleErrors.sort(), 'browser console errors must be limited to deliberate invalid-input and interrupted mock-locate requests');

    } else if (inventoryOnly) {
      const expectedInventoryValidationErrors = [
        ['/parts', 'Failed to load resource: the server responded with a status of 409 (Conflict)'],
        ['/parts/2', 'Failed to load resource: the server responded with a status of 400 (Bad Request)'],
        ['/parts/2/update', 'Failed to load resource: the server responded with a status of 500 (Internal Server Error)'],
      ];
      assert.deepEqual(nonValidationConsoleErrors.map(error => [new URL(error.url).pathname, error.message]).sort(), expectedInventoryValidationErrors.sort(), 'browser errors should be limited to deliberate duplicate/invalid/failure non-mutation cases');

    } else if (task065RestoreEnabled) {
      const expectedInterruptedResponses = [
        ['/parts', 'Failed to load resource: the server responded with a status of 409 (Conflict)'],
        ['/parts/2', 'Failed to load resource: the server responded with a status of 400 (Bad Request)'],
        ['/parts/2/update', 'Failed to load resource: the server responded with a status of 500 (Internal Server Error)'],
        ['/parts/3', 'Failed to load resource: net::ERR_CONNECTION_REFUSED'],
        ['/storage/drawers/1?bin_id=2', 'Failed to load resource: net::ERR_CONNECTION_REFUSED'],
      ];
      assert.deepEqual(nonValidationConsoleErrors.map(error => [new URL(error.url).pathname + new URL(error.url).search, error.message]).sort(), expectedInterruptedResponses.sort(), 'additional browser errors should be limited to deliberate validation failures and requests interrupted by SIGKILL');
    } else {
      const expectedInventoryValidationErrors = [
        ['/parts', 'Failed to load resource: the server responded with a status of 409 (Conflict)'],
        ['/parts/2', 'Failed to load resource: the server responded with a status of 400 (Bad Request)'],
        ['/parts/2/update', 'Failed to load resource: the server responded with a status of 500 (Internal Server Error)'],
      ];
      assert.deepEqual(nonValidationConsoleErrors.map(error => [new URL(error.url).pathname, error.message]).sort(), expectedInventoryValidationErrors.sort(), 'browser errors should be limited to deliberate duplicate/invalid/failure non-mutation cases');
    }

    console.log(JSON.stringify({
      result: inventoryOnly ? 'PASS' : mappingAttestationsPassed !== controllers.length || locateEvidence.status !== 'PASS' ? 'BLOCKED' : task067Enabled && crashRecoveryEvidence.status === 'PASS' && restoreEvidence.status === 'PASS' && multiProcessEvidence.status === 'PASS' ? 'PASS' : 'PARTIAL',
      reason: inventoryOnly ? 'Inventory-only acceptance used a fresh disposable app with WLED output explicitly disabled; part image lifecycle, physical-bin stock lifecycle, search, restart persistence and separate-app backup/restore were exercised. Locate/Stop was intentionally skipped.' : task067Enabled ? 'Fresh setup, controller outputs, physical mappings, category/product lifecycle, five-controller Locate/Stop, SIGKILL recovery, backup/restore compatibility, independent process fencing and unrelated-pixel preservation all passed against isolated loopback simulators.' : 'Fresh setup, five controllers, cabinet/drawers, UI-created bins/placements, five-controller 1000-pixel wiring, reference allocations, ten parts, stock assignment/adjustment/move and restart persistence were browser-tested. Extended crash recovery, restore and multi-process cases run with test:task067-locate.',
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
      multiProcessEvidence,
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
    if (secondContext) await secondContext.close();
    if (browser) await browser.close();
    for (const mock of mocks) if (mock.listening) await new Promise(resolve => mock.close(resolve));
    if (restoreServer) await stopProcess(restoreServer);
    if (secondServer) await stopProcess(secondServer);
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

async function submitPostWithoutWaiting(page, url, fields = {}) {
  const token = await page.locator('meta[name="csrf-token"]').getAttribute('content');
  await page.evaluate(({ url, fields, token }) => {
    const body = new URLSearchParams({ ...fields, csrf_token: token });
    fetch(url, { method: 'POST', body, credentials: 'same-origin' }).catch(() => {});
  }, { url, fields, token });
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

function executeSQLite(databasePath, sql, parameters = []) {
  const script = 'import json,sqlite3,sys; c=sqlite3.connect(sys.argv[1]); cur=c.execute(sys.argv[2],json.loads(sys.argv[3])); c.commit(); print(json.dumps(cur.lastrowid)); c.close()';
  const result = spawnSync('python3', ['-c', script, databasePath, sql, JSON.stringify(parameters)], { encoding: 'utf8' });
  if (result.status !== 0) throw new Error(`disposable SQLite fixture write failed: ${result.stderr}`);
  return JSON.parse(result.stdout);
}

function executeSQLite(databasePath, sql, parameters = []) {
  const script = 'import json,sqlite3,sys; c=sqlite3.connect(sys.argv[1]); cur=c.execute(sys.argv[2],json.loads(sys.argv[3])); c.commit(); print(json.dumps(cur.lastrowid)); c.close()';
  const result = spawnSync('python3', ['-c', script, databasePath, sql, JSON.stringify(parameters)], { encoding: 'utf8' });
  if (result.status !== 0) throw new Error(`disposable SQLite fixture write failed: ${result.stderr}`);
  return JSON.parse(result.stdout);
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
  for (const table of ['controllers', 'controller_outputs', 'cabinets', 'drawers', 'bins', 'containers', 'cabinet_controllers', 'bin_drawer_attachments', 'parts', 'part_assignments', 'categories', 'part_categories', 'category_attributes', 'part_attribute_values', 'cabinet_led_strings', 'cabinet_led_path_sections', 'drawer_led_allocations']) {
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
    controllers: readOnlyRows(databasePath, 'SELECT id, name, ip_address, port, led_count, controller_type, connection_timeout_ms FROM controllers ORDER BY id'),
        outputs: readOnlyRows(databasePath, 'SELECT id,controller_id,display_order,name,pixel_count,direction,pattern,rows,columns,custom_order_json FROM controller_outputs ORDER BY controller_id,display_order'),
    cabinet: readOnlyRows(databasePath, 'SELECT id, name, layout_width, layout_height FROM cabinets WHERE id = ?', [cabinetID]),
    memberships: readOnlyRows(databasePath, 'SELECT controller_id, slot FROM cabinet_controllers WHERE cabinet_id = ? ORDER BY slot', [cabinetID]),
    drawers: readOnlyRows(databasePath, 'SELECT id, label, x, y, width, height FROM drawers WHERE cabinet_id = ? ORDER BY display_order, id', [cabinetID]),
    bins: readOnlyRows(databasePath, 'SELECT id, name, container_id, led_index FROM bins ORDER BY id'),
    attachments: readOnlyRows(databasePath, 'SELECT bin_id, drawer_id, x, y, width, height FROM bin_drawer_attachments ORDER BY bin_id'),
    strings: readOnlyRows(databasePath, 'SELECT id, controller_id, controller_output_id, name, display_order, pixel_count, layout_complete FROM cabinet_led_strings WHERE cabinet_id = ? ORDER BY controller_id, display_order', [cabinetID]),
    sections: readOnlyRows(databasePath, 'SELECT string_id, display_order, pixel_count, points_json FROM cabinet_led_path_sections WHERE string_id IN (SELECT id FROM cabinet_led_strings WHERE cabinet_id = ?) ORDER BY string_id, display_order', [cabinetID]),
    allocations: readOnlyRows(databasePath, 'SELECT drawer_id, start_pixel, pixel_count, display_order FROM drawer_led_allocations WHERE cabinet_id = ? ORDER BY drawer_id, display_order', [cabinetID]),
    parts: readOnlyRows(databasePath, 'SELECT id, name, part_number, image_path FROM parts ORDER BY id'),
    assignments: readOnlyRows(databasePath, 'SELECT part_id, bin_id, quantity FROM part_assignments ORDER BY part_id, bin_id'),
  };
}

async function exerciseMockLocate(page, baseURL, firstPartID, bins, mockWrites, mockRequests) {
  const testCases = [
    { partID: firstPartID + 1, binID: Number(bins[1][0]), drawerID: 1, expected: [[1, 10, 24, 34], [1, 10, 49, 54]] },
    { partID: firstPartID + 2, binID: Number(bins[2][0]), drawerID: 2, expected: [[1, 20, 49, 51], [1, 20, 69, 71], [1, 20, 89, 91], [2, 10, 0, 10]] },
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
