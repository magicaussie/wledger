'use strict';
// Isolated setup-flow browser acceptance; all services and data are disposable/loopback-only.
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
  const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'wledger-controller-setup-'));
  const root = path.resolve(__dirname, '../..');
  const wledRequests = [], pageErrors = [];
  let server, browser, mock;
  try {
    fs.symlinkSync(path.join(root, 'web'), path.join(scratch, 'web'));
    fs.symlinkSync(path.join(root, 'locales'), path.join(scratch, 'locales'));
    mock = http.createServer((req, res) => {
      wledRequests.push(`${req.method} ${req.url}`);
      res.setHeader('Content-Type', 'application/json');
      if (req.method === 'GET' && req.url === '/json/info') return res.end(JSON.stringify({ ver: '0.15.0', leds: { count: 33 } }));
      if (req.method === 'GET' && req.url === '/json/cfg') return res.end(JSON.stringify({ hw: { led: { total: 33, ins: [
        { start: 0, len: 24, pin: [2], rev: true }, { start: 24, len: 9, pin: [4], rev: false },
      ] } } }));
      res.writeHead(404); res.end('{}');
    });
    await new Promise(resolve => mock.listen(0, '127.0.0.1', resolve));
    const probe = net.createServer(); await new Promise(resolve => probe.listen(0, '127.0.0.1', resolve));
    const port = probe.address().port; await new Promise(resolve => probe.close(resolve));
    const origin = `http://127.0.0.1:${port}`;
    const binary = path.join(scratch, 'wledger');
    const build = spawnSync(process.env.GO_BIN || 'go', ['build', '-tags', 'fts5', '-o', binary, './cmd/server'], { cwd: root, encoding: 'utf8', timeout: 120000 });
    assert.equal(build.status, 0, build.stderr || String(build.error));
    server = spawn(binary, [], { cwd: scratch, env: { ...process.env, WLEDGER_PORT: String(port), WLEDGER_INSECURE_COOKIES: '1', WLEDGER_PHYSICAL_OUTPUT_ENABLED: 'false' }, stdio: 'ignore' });
    for (let attempt = 0; ; attempt++) {
      if (server.exitCode !== null) throw new Error(`Disposable server exited: ${server.exitCode}`);
      try { if ((await fetch(origin + '/setup')).ok) break; } catch (_) {}
      if (attempt >= 120) throw new Error('Disposable server did not become ready');
      await new Promise(resolve => setTimeout(resolve, 100));
    }

    browser = await chromium.launch({ headless: true, args: ['--no-sandbox'], ...(process.env.CHROMIUM_PATH ? { executablePath: process.env.CHROMIUM_PATH } : {}) });
    const page = await browser.newPage({ viewport: { width: 1440, height: 1100 } });
    page.on('pageerror', error => pageErrors.push(error.message));
    await page.route('**/*', route => {
      const url = route.request().url();
      if (url.startsWith(origin + '/')) return route.continue();
      if (url.includes('unpkg.com/alpinejs@')) return route.fulfill({ contentType: 'application/javascript', path: require.resolve('alpinejs/dist/cdn.min.js') });
      return route.fulfill({ contentType: 'application/javascript', body: '' });
    });
    await page.goto(origin + '/setup');
    await page.locator('form[action="/setup"] input[name="email"]').fill('controller-setup@example.invalid');
    await page.locator('form[action="/setup"] input[name="password"]').fill('controller-setup-disposable-123');
    await page.locator('form[action="/setup"] button').click(); await page.waitForURL(origin + '/');

    const database = path.join(scratch, 'data/wledger.db');
    const rows = statement => {
      const result = spawnSync('python3', ['-c', 'import json,sqlite3,sys; c=sqlite3.connect(sys.argv[1]); print(json.dumps(c.execute(sys.argv[2]).fetchall())); c.close()', database, statement], { encoding: 'utf8' });
      assert.equal(result.status, 0, result.stderr); return JSON.parse(result.stdout.trim());
    };
    await page.goto(origin + '/hardware');
    const add = page.getByRole('link', { name: 'Add WLED controller' });
    await add.waitFor();
    assert.equal(await page.locator('#add_controller_modal').count(), 0, 'legacy add modal is removed');
    await add.click(); await page.waitForURL(origin + '/hardware/new');
    const setupRoute = await page.reload();
    assert.equal(setupRoute?.status(), 200, `GET /hardware/new is not ready: ${setupRoute?.status()}`);
    await page.getByRole('heading', { name: 'Add WLED controller' }).waitFor({ timeout: 5000 });
    assert.equal(await page.getByText(/Rows\s*[×x]\s*Columns/i).count(), 0, 'step one has no grid-configuration requirement');
    const initialControllers = rows('SELECT id FROM controllers');
    await page.getByLabel('Controller name').fill('Manual controller');
    await page.getByLabel('IP address or host').fill('127.0.0.1');
    await page.getByLabel('HTTP port').fill(String(mock.address().port));
    await page.getByRole('button', { name: 'Continue to Wiring Designer' }).click();
    await page.getByText('Step 2 of 2 · Wiring').waitFor();
    assert.deepEqual(rows('SELECT id FROM controllers'), initialControllers, 'continuing manually does not create a database row');
    await page.getByRole('button', { name: '＋ Add output' }).click();
    const draft = page.locator('[x-data="wiringDesigner()"]');
    const countInputs = page.locator('input[x-model\\.number="output.count"]');
    await countInputs.nth(0).fill('24'); await countInputs.nth(0).press('Tab');
    await countInputs.nth(1).fill('9'); await countInputs.nth(1).press('Tab');
    await page.getByRole('button', { name: '＋ Add row' }).click();
    const localPixelTraffic = wledRequests.length;
    await page.locator('svg[aria-label="Controller output and pixel wiring drawing"] circle[role="button"]').first().click({ force: true });
    await page.getByText(/Draft setup address/).waitFor();
    assert.equal(wledRequests.length, localPixelTraffic, 'draft pixel inspection never calls WLED');
    const draftState = await draft.evaluate(el => Alpine.$data(el));
    assert.deepEqual(draftState.outputs.map(output => output.id), [-1, -2]);
    assert.deepEqual(draftState.layout.outputs.map(output => output.output_id), [-1, -2]);
    assert.deepEqual(draftState.electricalDraft.outputs.map(output => output.id), [-1, -2]);
    assert.deepEqual(draftState.outputs.map(output => output.count), [24, 9]);
        assert.deepEqual(draftState.outputs.map(output => output.rows), [[24], [8, 1]], 'the two visual templates retain unequal row layouts');
    await page.getByRole('button', { name: 'Save controller and layout' }).first().click();
    await page.waitForURL(/\/hardware\/\d+\/wiring-designer$/);
    await page.getByRole('heading', { name: 'Graphical LED Wiring Designer' }).waitFor();
    const created = rows('SELECT id,name FROM controllers');
    assert.equal(created.length, initialControllers.length + 1, 'final save creates exactly one controller');
    const createdID = Number(new URL(page.url()).pathname.split('/')[2]);
    assert.equal(Number(created.at(-1)[0]), createdID);
    assert.deepEqual(rows(`SELECT display_order,pixel_count,direction,pattern FROM controller_outputs WHERE controller_id=${createdID} ORDER BY display_order`), [[0, 24, 'forward', 'linear'], [1, 9, 'forward', 'linear']], 'controller electrical outputs retain each draft ID mapping and default to linear');
    await page.goto(origin + '/hardware');
    await page.getByRole('link', { name: 'Edit Wiring', exact: true }).first().click();
    await page.getByRole('heading', { name: 'Graphical LED Wiring Designer' }).waitFor();
    const edited = await draft.evaluate(el => Alpine.$data(el));
    assert.equal(edited.outputs.length, 2, 'saved wiring opens in the same designer');
    assert.deepEqual(edited.outputs.map(output => output.count), [24, 9]);

    // Discovery is explicit, reports hardware data, and does not infer physical reversal.
    await page.goto(origin + '/hardware'); await page.getByRole('link', { name: 'Add WLED controller' }).click();
    await page.getByLabel('Controller name').fill('Discovered controller');
    await page.getByLabel('IP address or host').fill('127.0.0.1');
    await page.getByLabel('HTTP port').fill(String(mock.address().port));
    const beforeDiscovery = rows('SELECT id FROM controllers').length;
    await page.getByRole('button', { name: 'Discover Controller' }).click();
    await page.getByText('Discovered controller', { exact: true }).waitFor();
    assert.deepEqual(rows('SELECT id FROM controllers').length, beforeDiscovery, 'discovery is not a save');
    assert.ok(wledRequests.includes('GET /json/info') && wledRequests.includes('GET /json/cfg'));
    await page.getByRole('button', { name: 'Continue to Wiring Designer' }).click();
    const discovered = await draft.evaluate(el => Alpine.$data(el));
    assert.deepEqual(discovered.outputs.map(output => [output.id, output.count, output.direction]), [[-1, 24, 'forward'], [-2, 9, 'forward']], 'discovered outputs receive distinct draft IDs and stay forward by default');

    // Offline discovery must remain recoverable into the manual flow.
    await page.goto(origin + '/hardware'); await page.getByRole('link', { name: 'Add WLED controller' }).click();
    await page.getByLabel('Controller name').fill('Offline controller');
    await page.getByLabel('IP address or host').fill('127.0.0.1');
    await page.getByLabel('HTTP port').fill('1');
    await page.getByRole('button', { name: 'Discover Controller' }).click();
    await page.waitForFunction(() => Alpine.$data(document.querySelector('[x-data="wiringDesigner()"]'))?.error);
    await page.getByRole('button', { name: 'Continue to Wiring Designer' }).click();
    await page.getByText('Step 2 of 2 · Wiring').waitFor();
    assert.deepEqual(rows('SELECT id FROM controllers').length, beforeDiscovery, 'offline/manual continuation remains unsaved');
        await page.getByRole('button', {name:'Save controller and layout',exact:true}).first().click();
        await page.waitForURL(/\/hardware\/\d+\/wiring-designer$/);
        assert.equal(rows("SELECT count(*) FROM controllers WHERE name='Offline controller'")[0][0],1,'offline setup can be saved completely without discovery');
        assert.ok(wledRequests.every(request=>request==='GET /json/info'||request==='GET /json/cfg'),'setup never writes WLED state');
    assert.deepEqual(pageErrors, [], JSON.stringify(pageErrors));
    console.log(JSON.stringify({ result: 'PASS', scenarios: ['manual setup without intermediate database writes', 'two draft outputs with unequal counts and row layouts, final save and same-designer edit', 'local draft pixel inspection without WLED requests', 'explicit info/config discovery without saving or inferring reverse', 'offline discovery error with manual continuation'] }));
  } finally {
    if (browser) await browser.close();
    if (server && server.exitCode === null) { server.kill('SIGTERM'); await new Promise(resolve => server.once('exit', resolve)); }
    if (mock) await new Promise(resolve => mock.close(resolve));
    fs.rmSync(scratch, { recursive: true, force: true });
  }
}
main().catch(error => { console.error(error); process.exitCode = 1; });
