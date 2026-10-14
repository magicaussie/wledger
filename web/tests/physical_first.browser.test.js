const assert = require('node:assert/strict');
const { createHash } = require('node:crypto');
const { chromium } = require('playwright');
const { spawn, spawnSync } = require('node:child_process');
const fs = require('node:fs');
const http = require('node:http');
const net = require('node:net');
const os = require('node:os');
const path = require('node:path');

(async () => {
  const repoRoot = path.resolve(__dirname, '../..');
  const scratchDir = fs.mkdtempSync(path.join(os.tmpdir(), 'wledger-physical-first-'));
  let server;
  let browser;
  let wledMock;
  let databasePath;
  const wledRequests = [];
  const pageErrors = [];
  const consoleErrors = [];
  const mutations = [];
  try {
    fs.symlinkSync(path.join(repoRoot, 'web'), path.join(scratchDir, 'web'));
    fs.symlinkSync(path.join(repoRoot, 'locales'), path.join(scratchDir, 'locales'));

    wledMock = http.createServer((request, response) => {
      wledRequests.push(`${request.method} ${request.url}`);
      response.writeHead(404, { 'Content-Type': 'application/json' });
      response.end(JSON.stringify({ error: 'unexpected request to unassigned mock WLED' }));
    });
    await new Promise((resolve, reject) => wledMock.once('error', reject).listen(0, '127.0.0.1', resolve));
    const mockPort = wledMock.address().port;

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
      env: { ...process.env, WLEDGER_INSECURE_COOKIES: '1', WLEDGER_PHYSICAL_OUTPUT_ENABLED: 'true', WLEDGER_PORT: String(port) },
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
    if (!ready) throw new Error('disposable WLEDger server did not become ready');
    databasePath = path.join(scratchDir, 'data', 'wledger.db');

    browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH || '/bin/chromium', headless: true, args: ['--no-sandbox'] });
    const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
    page.on('pageerror', error => pageErrors.push(error.message));
    page.on('console', message => { if (message.type() === 'error') consoleErrors.push(message.text()); });
    page.on('request', request => {
      if (request.method() === 'POST' && new URL(request.url()).origin === baseURL) {
        mutations.push({ path: new URL(request.url()).pathname, body: request.postData() || '' });
      }
    });

    await page.goto(`${baseURL}/login`);
    if (new URL(page.url()).pathname === '/setup') {
      await page.locator('input[name=email]').fill('physical-first-admin@example.invalid');
      await page.locator('input[name=password]').fill('safe-password-123');
      await page.locator('form button').click();
      await page.waitForURL(`${baseURL}/`);
    } else {
      await page.locator('input[name=email]').fill('physical-first-admin@example.invalid');
      await page.locator('input[name=password]').fill('safe-password-123');
      await page.locator('form button').click();
      await page.waitForURL(`${baseURL}/`);
    }

    await page.goto(`${baseURL}/hardware`);
    const hardwareText = await page.locator('body').innerText();
    assert.match(hardwareText, /Create Cabinet[\s\S]*Physical-first:[\s\S]*Add Drawers[\s\S]*Add Bins[\s\S]*Assign Inventory/);
    assert.match(hardwareText, /No WLED controllers configured/);
    assert.equal(await page.locator('details summary', { hasText: 'Advanced legacy electrical tools' }).count(), 1);
    await page.getByRole('link', { name: 'Open Cabinets' }).click();
    await page.waitForURL(`${baseURL}/storage/cabinets`);

    const cabinetName = `UI physical cabinet ${Date.now()}`;
    await page.locator('input[name=name]').fill(cabinetName);
    await page.getByRole('button', { name: 'Create cabinet' }).click();
    await page.waitForURL(/\/storage\/cabinets\/\d+\/edit$/);
    const cabinetID = Number(page.url().match(/cabinets\/(\d+)\/edit$/)[1]);
    const canvas = page.locator('svg[aria-label="Interactive physical layout canvas"]');
    const point = async (x, y) => canvas.evaluate((svg, value) => {
      const p = svg.createSVGPoint(); p.x = value.x; p.y = value.y;
      const screen = p.matrixTransform(svg.getScreenCTM()); return { x: screen.x, y: screen.y };
    }, { x, y });
    const drag = async (x1, y1, x2, y2) => {
      const a = await point(x1, y1); const b = await point(x2, y2);
      await page.mouse.move(a.x, a.y); await page.mouse.down(); await page.mouse.move(b.x, b.y, { steps: 8 }); await page.mouse.up();
    };
    const drawDrawer = async (label, x1, y1, x2, y2) => {
      await page.getByRole('button', { name: 'Drawer', exact: true }).click();
      await drag(x1, y1, x2, y2);
      const labelInput = page.locator('input[x-model="selectedDrawer().label"]');
      await labelInput.fill(label);
      await labelInput.press('Tab');
      await page.getByRole('button', { name: 'Save layout' }).click();
      await page.waitForURL(`${baseURL}/storage/cabinets/${cabinetID}`).catch(async error => {
        const editorState = await page.locator('[x-data="physicalLayoutEditor()"]')
                  .evaluate(element => { const editor = window.Alpine.$data(element); return { dirty: editor.dirty, error: editor.error, state: editor.state, electrical: editor.hasUnsavedElectricalChanges(), payload: editor.$refs.layout.value }; });
                throw new Error(`physical drawer save did not redirect: url=${page.url()} mutations=${JSON.stringify(mutations)} pageErrors=${JSON.stringify(pageErrors)} consoleErrors=${JSON.stringify(consoleErrors)} editor=${JSON.stringify(editorState)} body=${await page.locator('body').innerText()} cause=${error.message}`);
      });
    };
    const openEditor = async () => page.goto(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    const openDrawer = async label => {
      await page.getByRole('button', { name: new RegExp(`\\d+\\. ${label}`) }).dblclick();
      await page.getByText('Physical bins', { exact: true }).waitFor();
    };
    const layoutData = async () => JSON.parse(await page.locator('#layout-editor-data').textContent());
    const addBin = async (name, placement = {}) => {
      await page.locator('input[x-model="newBinDraft.name"]').fill(name);
      for (const [field, value] of Object.entries(placement)) {
        await page.locator(`input[x-model\\.number="newBinDraft.${field}"]`).fill(String(value));
      }
      await page.getByRole('button', { name: '＋ Add bin' }).click();
    };
    const qrDigest = async id => {
      const response = await page.request.get(`${baseURL}/bin/${id}/qr`);
      assert.equal(response.status(), 200);
      assert.match(response.headers()['content-type'], /image\/png/);
      return createHash('sha256').update(await response.body()).digest('hex');
    };

    await drawDrawer('Drawer North', 10, 10, 30, 30);
    await openEditor();
    await drawDrawer('Drawer South', 40, 10, 60, 30);
    await openEditor();
    let layout = await layoutData();
    const north = layout.Drawers.find(item => item.Drawer.label === 'Drawer North').Drawer;
    const south = layout.Drawers.find(item => item.Drawer.label === 'Drawer South').Drawer;
    const northID = Number(north.id);
    const southID = Number(south.id);

    await openDrawer('Drawer North');
    await addBin('Physical Bin A', { x: 1, y: 1, width: 6, height: 6 });
    await page.waitForURL(new RegExp(`drawer_id=${northID}&bin_id=\\d+`));
    let binID = Number(new URL(page.url()).searchParams.get('bin_id'));
    assert.equal(await page.locator('input[x-model="selectedBin().name"]').inputValue(), 'Physical Bin A');
    const originalQR = await qrDigest(binID);
    let persisted = query(databasePath,
      'SELECT id,name,container_id,led_index,width,grid_x,grid_y FROM bins WHERE id=?', [binID]);
    assert.deepEqual(persisted, [[binID, 'Physical Bin A', null, null, null, null, null]], 'UI-created physical bin must have no legacy electrical association or indices');

    await page.getByRole('button', { name: 'Back to drawers' }).click();
    await openDrawer('Drawer South');
    await addBin('Physical Bin B', { x: 1, y: 1, width: 6, height: 6 });
    await page.waitForURL(new RegExp(`drawer_id=${southID}&bin_id=\\d+`));
    const secondBinID = Number(new URL(page.url()).searchParams.get('bin_id'));
    assert.notEqual(secondBinID, binID);

    await page.goto(`${baseURL}/storage/cabinets/${cabinetID}`);
    const cabinetBrowse = await page.locator('body').innerText();
    assert.match(cabinetBrowse, /Drawer North/);
    assert.match(cabinetBrowse, /Drawer South/);
    await page.goto(`${baseURL}/storage/drawers/${northID}`);
    const drawerBrowseText = await page.locator('body').innerText();
    assert.match(drawerBrowseText, /Bins: 1/);
    assert.match(drawerBrowseText, /PHYSICAL PLACEMENT/);
    assert.equal(await page.getByRole('button', { name: 'View Bin Layout' }).count(), 1);

    await page.goto(`${baseURL}/parts/new`);
    const partName = `Physical-first part ${Date.now()}`;
    await page.locator('form[action="/parts"] input[name=name]').fill(partName);
    await page.getByRole('button', { name: /Create Part/ }).click();
    await page.waitForURL(/\/parts\/\d+$/);
    const partID = Number(page.url().match(/\/parts\/(\d+)$/)[1]);
    await page.locator('button[onclick="stock_modal.showModal()"]:visible').click();
    const picker = page.locator('#visual_bin_picker');
    const physicalBinButton = picker.locator(`button[data-bin-id="${binID}"]`);
    await physicalBinButton.waitFor();
    assert.equal(await physicalBinButton.getAttribute('title'), 'Physical Bin A');
    await physicalBinButton.click();
    const stockForm = page.locator(`form[action="/parts/${partID}/assign"]`);
    await stockForm.locator('input[name=quantity]').fill('7');
    await stockForm.getByRole('button', { name: /Save Stock/ }).click();
    await page.waitForURL(`${baseURL}/parts/${partID}`);
    assert.match(await page.locator('table tbody').innerText(), /Physical Bin A/);
    assert.deepEqual(query(databasePath,
      'SELECT quantity FROM part_assignments WHERE part_id=? AND bin_id=?', [partID, binID]), [[7]]);

    await openEditor();
    await openDrawer('Drawer North');
    await page.locator('svg g[data-layout-entity="true"]').first().click();
    const binName = page.locator('input[x-model="selectedBin().name"]');
    const binX = page.locator('input[x-model\\.number="selectedBin().x"]');
    await binName.fill('Renamed Physical Bin A'); await binName.press('Tab');
    await binX.fill('2'); await binX.press('Tab');
    await page.getByRole('button', { name: 'Save layout' }).click();
    await page.waitForURL(`${baseURL}/storage/cabinets/${cabinetID}`);
    persisted = query(databasePath, 'SELECT id,name,container_id,led_index,width,grid_x,grid_y FROM bins WHERE id=?', [binID]);
    assert.deepEqual(persisted, [[binID, 'Renamed Physical Bin A', null, null, null, null, null]]);
    assert.deepEqual(query(databasePath,
      'SELECT x,y FROM bin_drawer_attachments WHERE bin_id=?', [binID]), [[2, 1]]);
    assert.deepEqual(query(databasePath,
      'SELECT quantity FROM part_assignments WHERE part_id=? AND bin_id=?', [partID, binID]), [[7]]);

    await openEditor();
    await openDrawer('Drawer North');
    { const screen = await point(4, 4); await page.mouse.click(screen.x, screen.y); }
    await page.getByText('Move bin to another drawer').click();
    await page.locator('select[x-model="moveTargetDrawerID"]').selectOption(String(southID));
    await page.locator('input[x-model\\.number="moveBinX"]').fill('8');
    await page.locator('input[x-model\\.number="moveBinY"]').fill('1');
    await page.getByRole('button', { name: 'Move bin', exact: true }).click();
    await page.waitForURL(new RegExp(`drawer_id=${southID}&bin_id=${binID}`));
    assert.equal(await page.locator('input[x-model="selectedBin().name"]').inputValue(), 'Renamed Physical Bin A');
    assert.deepEqual(query(databasePath,
      'SELECT bin_id,drawer_id,x,y FROM bin_drawer_attachments WHERE bin_id=?', [binID]), [[binID, southID, 8, 1]]);
    assert.deepEqual(query(databasePath,
      'SELECT quantity FROM part_assignments WHERE part_id=? AND bin_id=?', [partID, binID]), [[7]]);
    assert.equal(await qrDigest(binID), originalQR, 'moving and renaming must not change the bin QR identity');

    const beforeRejectedCreate = query(databasePath, 'SELECT COUNT(*) FROM bins')[0][0];
    await addBin('Overlapping bin');
    await page.waitForURL(/layout_error=/);
    assert.match(await page.locator('p[x-show="error"]').innerText(), /overlap/i);
    assert.equal(query(databasePath, 'SELECT COUNT(*) FROM bins')[0][0], beforeRejectedCreate, 'invalid position must not leave a partial bins row');
    assert.deepEqual(query(databasePath,
      'SELECT bin_id,drawer_id,x,y FROM bin_drawer_attachments WHERE bin_id=?', [binID]), [[binID, southID, 8, 1]]);

    await openEditor();
    await openDrawer('Drawer South');
    { const screen = await point(11, 4); await page.mouse.click(screen.x, screen.y); }
    const moveXInput = page.locator('input[x-model\\.number="selectedBin().x"]');
    await moveXInput.fill('99'); await moveXInput.press('Tab');
    await page.getByRole('button', { name: 'Save layout' }).click();
    await page.waitForURL(/layout_error=/);
    assert.deepEqual(query(databasePath,
      'SELECT x,y FROM bin_drawer_attachments WHERE bin_id=?', [binID]), [[8, 1]], 'invalid bin geometry edit must roll back');

    await openEditor();
    await openDrawer('Drawer South');
    { const screen = await point(11, 4); await page.mouse.click(screen.x, screen.y); }
    const detachDialogs = [];
    page.once('dialog', async dialog => { detachDialogs.push(dialog.message()); await dialog.accept(); });
    await page.getByRole('button', { name: 'Detach bin (keep inventory)' }).click();
    await page.waitForURL(`${baseURL}/storage/cabinets/${cabinetID}/edit`);
    assert.match(detachDialogs[0], /inventory bin, stock and electrical mapping remain/i);
    assert.deepEqual(query(databasePath, 'SELECT id,name FROM bins WHERE id=?', [binID]), [[binID, 'Renamed Physical Bin A']]);
    assert.equal(query(databasePath, 'SELECT COUNT(*) FROM bin_drawer_attachments WHERE bin_id=?', [binID])[0][0], 0);
    assert.deepEqual(query(databasePath,
      'SELECT quantity FROM part_assignments WHERE part_id=? AND bin_id=?', [partID, binID]), [[7]]);
    assert.equal(await qrDigest(binID), originalQR, 'detaching placement must not change the bin QR identity');

    await page.goto(`${baseURL}/parts/${partID}`);
    await page.locator('button[onclick="stock_modal.showModal()"]:visible').click();
    const unplaced = page.locator('#visual_bin_picker [data-bin-group="unplaced"]');
    await unplaced.locator(`button[data-bin-id="${binID}"]`).waitFor();
    assert.equal(await unplaced.locator(`button[data-bin-id="${binID}"]`).getAttribute('title'), 'Renamed Physical Bin A');

    const finalState = query(databasePath,
      'SELECT (SELECT COUNT(*) FROM controllers),(SELECT COUNT(*) FROM containers),(SELECT controller_id FROM cabinets WHERE id=?),(SELECT container_id IS NULL AND led_index IS NULL FROM bins WHERE id=?),(SELECT quantity FROM part_assignments WHERE part_id=? AND bin_id=?)',
      [cabinetID, binID, partID, binID]);
    assert.deepEqual(finalState, [[0, 0, null, 1, 7]], 'physical-first records should remain independent of all WLED/electrical data');
    assert.deepEqual(wledRequests, [], `layout-only UI contacted the mock WLED endpoint: ${wledRequests.join(', ')}`);
    assert.ok(mutations.some(item => item.path === `/storage/cabinets/${cabinetID}/drawers/${northID}/bins`), 'bin creation must use an ordinary CSRF-protected UI form');
    assert.ok(mutations.some(item => item.path === `/storage/cabinets/${cabinetID}/bins/${binID}/move`), 'bin movement must use an ordinary UI route');
    assert.ok(mutations.filter(item => item.path.startsWith('/storage/cabinets/')).every(item => /csrf_token/.test(item.body)), 'physical layout writes must submit CSRF tokens');
    assert.deepEqual(pageErrors, []);
    assert.deepEqual(consoleErrors, []);
    console.log(JSON.stringify({ result: 'PASS', cabinetID, northID, southID, binID, secondBinID, partID, quantity: 7, wledRequests, pageErrors, consoleErrors, uiMutations: mutations.length }));
  } finally {
    if (browser) await browser.close();
    if (server) { server.kill('SIGTERM'); await new Promise(resolve => server.once('exit', resolve)); }
    if (wledMock) await new Promise(resolve => wledMock.close(resolve));
    fs.rmSync(scratchDir, { recursive: true, force: true });
  }
})().catch(error => { console.error(error); process.exit(1); });

function query(databasePath, sql, parameters = []) {
  const result = spawnSync('python3', ['-c', 'import json,sqlite3,sys; c=sqlite3.connect(sys.argv[1]); rows=c.execute(sys.argv[2], json.loads(sys.argv[3])).fetchall(); print(json.dumps(rows)); c.close()', databasePath, sql, JSON.stringify(parameters)], { encoding: 'utf8' });
  if (result.status !== 0) throw new Error(`read-only SQLite assertion failed: ${result.stderr}`);
  return JSON.parse(result.stdout);
}
