'use strict';
// Disposable application + loopback WLED only. Never targets an existing instance.
const assert = require('node:assert/strict');
const { chromium } = require('playwright');
const { spawn, spawnSync } = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const http = require('node:http');
const net = require('node:net');

async function main() {
  if (process.env.WLEDGER_URL) throw new Error('External instances are prohibited by this test.');
  const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'wledger-led-centre-'));
  const root = path.resolve(__dirname, '../..');
  const hardware = [], writes = [], pageErrors = [], appRequests = [];
  let server, browser, mock;
  try {
    fs.symlinkSync(path.join(root, 'web'), path.join(scratch, 'web'));
    fs.symlinkSync(path.join(root, 'locales'), path.join(scratch, 'locales'));
    mock = http.createServer(async (req, res) => {
      hardware.push(req.method + ' ' + req.url);
      res.setHeader('Content-Type', 'application/json');
      if (req.url === '/json/info') return res.end(JSON.stringify({ ver: '0.15.0', mac: '02:00:00:00:00:21', leds: { count: 24, rgbw: false } }));
      if (req.url === '/json/cfg') return res.end(JSON.stringify({ hw: { led: { total: 24, ins: [{ start: 0, len: 24, rev: false, skip: 0, type: 22, pin: [2] }] } } }));
      if (req.url === '/json/state' && req.method === 'GET') return res.end(JSON.stringify({ on: true, bri: 128, seg: [{ id: 0, start: 0, stop: 24, on: true, fx: 0, grp: 1, spc: 0, rev: false, mi: false, of: 0 }] }));
      if (req.url === '/json/state' && req.method === 'POST') {
        let body = ''; for await (const chunk of req) body += chunk;
        writes.push(JSON.parse(body)); return res.end('{"success":true}');
      }
      res.writeHead(404); res.end('{}');
    });
    await new Promise(resolve => mock.listen(0, '127.0.0.1', resolve));
    const probe = net.createServer(); await new Promise(resolve => probe.listen(0, '127.0.0.1', resolve));
    const port = probe.address().port; await new Promise(resolve => probe.close(resolve));
    const origin = `http://127.0.0.1:${port}`;
    let binary = process.env.WLEDGER_TEST_BINARY;
    if (!binary) {
      binary = path.join(scratch, 'wledger');
      const build = spawnSync(process.env.GO_BIN || 'go', ['build', '-tags', 'fts5', '-o', binary, './cmd/server'], { cwd: root, encoding: 'utf8', timeout: 120000 });
      assert.equal(build.status, 0, build.stderr || String(build.error));
    }
    server = spawn(binary, [], { cwd: scratch, env: { ...process.env, WLEDGER_PORT: String(port), WLEDGER_INSECURE_COOKIES: '1', WLEDGER_PHYSICAL_OUTPUT_ENABLED: 'true' }, stdio: 'ignore' });
    for (let attempt = 0; ; attempt++) {
      if (server.exitCode !== null) throw new Error('Disposable server exited: ' + server.exitCode);
      try { if ((await fetch(origin + '/setup')).ok) break; } catch (_) {}
      if (attempt > 120) throw new Error('Disposable server did not become ready');
      await new Promise(resolve => setTimeout(resolve, 100));
    }
    browser = await chromium.launch({ headless: true, args: ['--no-sandbox'], ...(process.env.CHROMIUM_PATH ? { executablePath: process.env.CHROMIUM_PATH } : {}) });
    const page = await browser.newPage({ viewport: { width: 1440, height: 1100 } });
    page.on('pageerror', e => pageErrors.push(e.message));
    page.on('dialog', dialog => dialog.accept());
    // No external browser traffic, including CDNs. Alpine is supplied from the local test dependency.
    await page.route('**/*', route => {
      const url = route.request().url();
      if (url.startsWith(origin + '/')) return route.continue();
      if (url.includes('unpkg.com/alpinejs@')) return route.fulfill({ contentType: 'application/javascript', path: require.resolve('alpinejs/dist/cdn.min.js') });
      return route.fulfill({ contentType: 'application/javascript', body: '' });
    });
    page.on('request', req => { if (req.url().startsWith(origin + '/hardware/led-test-centre/')) appRequests.push({ method: req.method(), path: new URL(req.url()).pathname, body: req.postDataJSON() }); });
    await page.goto(origin + '/setup');
    await page.locator('form[action="/setup"] input[name="email"]').fill('led-centre@example.invalid');
    await page.locator('form[action="/setup"] input[name="password"]').fill('led-centre-disposable-123');
    await page.locator('form[action="/setup"] button').click(); await page.waitForURL(origin + '/');
    const db = path.join(scratch, 'data/wledger.db');
    function sql(statement, params = []) {
      const result = spawnSync('python3', ['-c', 'import sqlite3,sys,json; c=sqlite3.connect(sys.argv[1]); c.execute("PRAGMA foreign_keys=ON"); c.execute(sys.argv[2],json.loads(sys.argv[3])); c.commit(); c.close()', db, statement, JSON.stringify(params)], { encoding: 'utf8' });
      assert.equal(result.status, 0, result.stderr);
    }
    // Fixture writes are confined to the new scratch database. All controller endpoints are loopback mocks.
    sql("INSERT INTO controllers(id,name,ip_address,port,led_count) VALUES(1,'Loopback test controller','127.0.0.1',?,24)", [mock.address().port]);
    sql("INSERT INTO cabinets(id,name,controller_id) VALUES(1,'Test cabinet',1)");
    sql('INSERT INTO cabinet_controllers(cabinet_id,controller_id,slot) VALUES(1,1,0)');
    sql("INSERT INTO drawers(id,cabinet_id,label,x,y,width,height) VALUES(1,1,'Drawer A',0,0,10,10),(2,1,'Drawer B',12,0,10,10)");
    sql("INSERT INTO containers(id,name,controller_id,segment_id,led_start,led_count) VALUES(1,'Electrical fixture',1,0,0,24)");
    sql("INSERT INTO bins(id,name,container_id,led_index,width) VALUES(1,'Mapped bin',1,0,1)");
    sql('INSERT INTO bin_drawer_attachments(bin_id,drawer_id,x,y,width,height) VALUES(1,1,0,0,1,1)');
    sql('INSERT INTO drawer_led_allocations(cabinet_id,drawer_id,start_pixel,pixel_count,display_order) VALUES(1,1,1,3,0),(1,1,11,2,1)');
    await page.goto(origin + '/hardware/1/grid');
    const attestation = page.locator('form[action="/hardware/1/locate-mapping"]');
    await attestation.locator('input[name="locate_mapping_verified"]').check();
    await attestation.locator('input[name="locate_ranges_reserved"]').check();
    await attestation.locator('button[type="submit"]').click(); await page.waitForURL(origin + '/hardware/1/grid');
    const before = hardware.length;
    await page.goto(origin + '/hardware');
    await page.getByRole('link', { name: 'LED Test Centre', exact: true }).click();
    await page.waitForFunction(() => window.Alpine && document.querySelector('[x-data="ledTestCentre()"]') && Alpine.$data(document.querySelector('[x-data="ledTestCentre()"]')).data.controllers.length === 1);
    assert.equal(hardware.length, before, 'navigation/data must not contact WLED');
    assert.deepEqual(appRequests.map(r => r.path), ['/hardware/led-test-centre/data']);
    await page.getByLabel('Controller', { exact: true }).selectOption('1');
    await page.getByRole('button', { name: 'Pixel 0: assigned', exact: true }).click();
    const inspector = page.getByRole('region', { name: 'Selected pixel assignments' });
    assert.match(await inspector.innerText(), /Drawer-relative pixel \(0-based\): 0/);
    assert.equal(await inspector.getByRole('link', { name: /Cabinet:/ }).getAttribute('href'), '/storage/cabinets/1');
    assert.equal(await inspector.getByRole('link', { name: /Drawer:/ }).getAttribute('href'), '/storage/drawers/1');
    await page.getByRole('button', { name: 'Pixel 10: assigned', exact: true }).click();
    assert.match(await inspector.innerText(), /Drawer-relative pixel \(0-based\): 3/);
    assert.equal(hardware.length, before, 'pixel and controller selection must not contact WLED');
    await page.getByRole('button', { name: 'Test Connection (read-only)', exact: true }).click();
    await page.waitForFunction(() => !Alpine.$data(document.querySelector('[x-data="ledTestCentre()"]')).busy);
    assert.ok(hardware.length > before); assert.equal(writes.length, 0);
    await page.getByLabel('Output', { exact: true }).selectOption('0');
    await page.getByRole('button', { name: 'discovery', exact: true }).click();
    await page.getByLabel('Cabinet', { exact: true }).selectOption('1');
    await page.getByLabel('Drawer', { exact: true }).selectOption('1');
    await page.getByLabel('Count', { exact: true }).fill('9');
    async function command(buttonName) {
      const response = page.waitForResponse(r => r.url() === origin + '/hardware/led-test-centre/start');
      await page.getByRole('button', { name: buttonName, exact: true }).click();
      const result = await response; assert.equal(result.status(), 200, await result.text());
      const body = await result.json(); assert.ok(body.operation.ID);
      await page.waitForFunction(id => Alpine.$data(document.querySelector('[x-data="ledTestCentre()"]')).chosen === id, body.operation.ID);
      return appRequests.filter(r => r.path.endsWith('/start')).at(-1).body;
    }
    async function stop() {
      const response = page.waitForResponse(r => r.url() === origin + '/hardware/led-test-centre/stop');
      await page.getByRole('button', { name: 'Stop chosen operation', exact: true }).click();
      const result = await response; assert.equal(result.status(), 200, await result.text());
      await page.waitForFunction(() => !Alpine.$data(document.querySelector('[x-data="ledTestCentre()"]')).busy);
    }
    let body = await command('Flash individual pixel'); assert.equal(body.count, 1); assert.equal(body.mode, 'flash'); await stop();
    body = await command('Test / replay individual pixel'); assert.equal(body.count, 1); await stop();
    body = await command('Next pixel and test'); assert.equal(body.start, 1); await stop();
    body = await command('Previous pixel and test'); assert.equal(body.start, 0); await stop();
    body = await command('Individual pixel OFF'); assert.equal(body.count, 1); assert.equal(body.color, '#000000'); await stop();
    await page.getByLabel('Absolute pixel (0-based)', { exact: true }).fill('20');
    await page.getByRole('button', { name: 'Mark selected as first', exact: true }).click();
    await page.getByLabel('Absolute pixel (0-based)', { exact: true }).fill('21');
    await page.getByRole('button', { name: 'Mark selected as last', exact: true }).click();
    await page.getByRole('button', { name: 'Preview drawer mapping', exact: true }).click();
    await page.getByText('Append-only proposals', { exact: true }).waitFor({ state: 'visible' });
    await page.getByRole('checkbox', { name: /I reviewed these cabinet coordinates/ }).check();
    const mappingState = await page.locator('[x-data="ledTestCentre()"]').evaluate(el => { const s = Alpine.$data(el); return { confirmed: s.mappingConfirmed, busy: s.busy, conflicts: s.conflicts, preview: s.preview, error: s.error }; });
    assert.equal(mappingState.confirmed && !mappingState.busy && mappingState.conflicts.length === 0, true, JSON.stringify(mappingState));
    await page.getByRole('button', { name: 'Save mapping append', exact: true }).click();
    await page.getByRole('status').filter({ hasText: 'Mapping append saved.' }).waitFor();
    await page.getByLabel('Absolute pixel (0-based)', { exact: true }).fill('1');
    await page.getByLabel('Bin', { exact: true }).selectOption('1');
    await page.getByRole('button', { name: 'Save selected pixel to bin…', exact: true }).click();
    await page.getByRole('status').filter({ hasText: 'Bin pixel mapping saved.' }).waitFor();
    await page.getByRole('button', { name: 'Refresh saved data', exact: true }).click();
    await page.waitForFunction(() => !Alpine.$data(document.querySelector('[x-data="ledTestCentre()"]')).busy);
    assert.match(await inspector.innerText(), /Bin: Mapped bin.*relative pixel \(0-based\): 1/);
    await page.getByRole('button', {name: 'drawer', exact: true}).click();
    for (const label of ['drawer first', 'drawer last', 'drawer all', 'drawer flash', 'drawer sequence', 'drawer bins']) {
      body = await command(label); assert.equal(body.controller_id, 0); assert.equal(body.drawer_id, 1); await stop();
    }
    await page.getByLabel('Bin', {exact: true}).selectOption('1');
    body = await command('Test selected mapped bin'); assert.equal(body.bin_id, 1); await stop();
    body = await command('Test cabinet allocated pixels'); assert.equal(body.cabinet_id, 1); await stop();
    await page.getByRole('button', {name: 'discovery', exact: true}).click();
    await page.getByLabel('Bin', {exact: true}).selectOption('1');
    await page.getByRole('button', {name: 'Clear selected bin pixel override…', exact: true}).click();
    await page.getByRole('status').filter({hasText: 'Selected bin override cleared.'}).waitFor();
    await page.getByRole('button', {name: 'Save selected pixel to bin…', exact: true}).click();
    await page.getByRole('status').filter({hasText: 'Bin pixel mapping saved.'}).waitFor();
    // Database constraints correctly forbid overlap; inject inconsistent saved-data JSON to exercise conflict rendering.
    const conflictingData = await (await page.request.get(origin + '/hardware/led-test-centre/data')).json();
    conflictingData.cabinets[0].drawers[1].ranges = [{ start: 2, count: 1, physical: [{ controller_id: 1, first_pixel: 1, count: 1, relative_start: 0 }] }];
    await page.route(origin + '/hardware/led-test-centre/data', route => route.fulfill({ contentType: 'application/json', body: JSON.stringify(conflictingData) }));
    await page.getByRole('button', { name: 'Refresh saved data', exact: true }).click();
    await page.getByRole('button', { name: 'Pixel 1: conflict', exact: true }).waitFor();
    assert.match(await inspector.innerText(), /Drawer B/);
    const screenshot = process.env.WLEDGER_SCREENSHOT || path.join(os.tmpdir(), 'led-test-centre.png');
    await page.screenshot({ path: screenshot, fullPage: true });
    // Exercise partial-emergency rendering without relying on a timing-sensitive mock disconnection.
    await page.route(origin + '/hardware/led-test-centre/emergency-off', route => route.fulfill({ status: 502, contentType: 'application/json', body: JSON.stringify({ error: 'Emergency off incomplete; retry required', results: [{ ControllerID: 1, Acknowledged: false, Error: 'Local fixture timeout' }] }) }));
    await page.getByRole('button', { name: 'Emergency global off…', exact: true }).click();
    await page.getByLabel('Confirmation', { exact: true }).fill('EMERGENCY OFF');
    await page.getByRole('button', { name: 'Send global emergency off', exact: true }).click();
    await page.getByText(/Controller 1.*Local fixture timeout/).waitFor();
    await page.getByRole('button', {name: 'Cancel', exact: true}).click();
    await page.unroute(origin + '/hardware/led-test-centre/emergency-off');
    await page.unroute(origin + '/hardware/led-test-centre/data');
    await page.getByRole('button', {name: 'Emergency global off…', exact: true}).click();
    await page.getByLabel('Confirmation', {exact: true}).fill('EMERGENCY OFF');
    const emergencyResponse = page.waitForResponse(r => r.url() === origin + '/hardware/led-test-centre/emergency-off');
    await page.getByRole('button', {name: 'Send global emergency off', exact: true}).click();
    const emergency = await emergencyResponse; assert.equal(emergency.status(), 200, await emergency.text());
    assert.equal((await emergency.json()).results[0].Acknowledged, true);
    await page.waitForFunction(() => !Alpine.$data(document.querySelector('[x-data="ledTestCentre()"]')).busy);
    await page.reload();
    await page.waitForFunction(() => Alpine.$data(document.querySelector('[x-data="ledTestCentre()"]')).data.controllers.length === 1);
    await page.getByRole('region', {name: 'Emergency off outcomes'}).getByText(/acknowledged \(not physically verified\)/).waitFor();
    const beforeSettings = hardware.length;
    await page.goto(origin + '/settings');
    await page.locator('input[name="led_color_test_pixel"]').fill('rgb(12,34,56)');
    await page.locator('input[name="led_brightness_test_pixel"]').fill('77');
    await page.getByRole('button', {name: 'Save LED functions', exact: true}).click();
    await page.waitForURL(origin + '/settings');
    let saved = await (await page.request.get(origin + '/hardware/led-test-centre/data')).json();
    assert.deepEqual(saved.styles.test_pixel, {color: '#0C2238', brightness: 77});
    await page.getByRole('button', {name: 'Reset LED defaults', exact: true}).click();
    await page.waitForURL(origin + '/settings');
    saved = await (await page.request.get(origin + '/hardware/led-test-centre/data')).json();
    assert.deepEqual(saved.styles.test_pixel, {color: '#00FFFF', brightness: 128});
    assert.equal(hardware.length, beforeSettings, 'settings save/reset must not contact WLED');
    assert.deepEqual(pageErrors, []);
    console.log(JSON.stringify({ result: 'PASS', screenshot, realLocalAPI: ['data', 'inspect', 'start', 'stop', 'mapping-preview', 'mapping-save', 'bin-pixel'], hardwareRequests: hardware.length, mockWrites: writes.length, conflictRendering: 'stubbed inconsistent saved-data JSON', partialEmergencyResponse: 'stubbed HTTP 502', drawerOnlyContract: 'real drawer, mapped-bin and cabinet APIs passed', noExternalHardware: true }));
  } finally {
    if (browser) await browser.close();
    if (server && server.exitCode === null) { server.kill('SIGTERM'); await new Promise(resolve => server.once('exit', resolve)); }
    if (mock) await new Promise(resolve => mock.close(resolve));
    fs.rmSync(scratch, { recursive: true, force: true });
  }
}
main().catch(error => { console.error(error); process.exitCode = 1; });
