'use strict';
// Self-contained browser acceptance: disposable SQLite app and loopback-only WLED mock.
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
  const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'wledger-wiring-designer-'));
  const root = path.resolve(__dirname, '../..');
  const controllerRequests = [], controllerWrites = [], pageErrors = [];
  let server, browser, mock;
  try {
    fs.symlinkSync(path.join(root, 'web'), path.join(scratch, 'web'));
    fs.symlinkSync(path.join(root, 'locales'), path.join(scratch, 'locales'));
    mock = http.createServer(async (req, res) => {
      controllerRequests.push(`${req.method} ${req.url}`);
      res.setHeader('Content-Type', 'application/json');
      if (req.url === '/json/info') return res.end(JSON.stringify({ ver: '0.15.0', mac: '02:00:00:00:00:31', leds: { count: 24, rgbw: false } }));
      if (req.url === '/json/cfg') return res.end(JSON.stringify({ hw: { led: { total: 24, ins: [{ start: 0, len: 24, rev: false, skip: 0, type: 22, pin: [2] }] } } }));
      if (req.url === '/json/state' && req.method === 'GET') return res.end(JSON.stringify({ on: true, bri: 128, seg: [{ id: 0, start: 0, stop: 24, on: true, fx: 0, grp: 1, spc: 0, rev: false, mi: false, of: 0 }] }));
      if (req.url === '/json/state' && req.method === 'POST') { let body = ''; for await (const chunk of req) body += chunk; controllerWrites.push(JSON.parse(body)); return res.end('{"success":true}'); }
      res.writeHead(404); res.end('{}');
    });
    await new Promise(resolve => mock.listen(0, '127.0.0.1', resolve));
    const probe = net.createServer(); await new Promise(resolve => probe.listen(0, '127.0.0.1', resolve));
    const port = probe.address().port; await new Promise(resolve => probe.close(resolve));
    const origin = `http://127.0.0.1:${port}`;
    const binary = path.join(scratch, 'wledger');
    const build = spawnSync(process.env.GO_BIN || 'go', ['build', '-tags', 'fts5', '-o', binary, './cmd/server'], { cwd: root, encoding: 'utf8', timeout: 120000 });
    assert.equal(build.status, 0, build.stderr || String(build.error));
    server = spawn(binary, [], { cwd: scratch, env: { ...process.env, WLEDGER_PORT: String(port), WLEDGER_INSECURE_COOKIES: '1', WLEDGER_PHYSICAL_OUTPUT_ENABLED: 'true' }, stdio: 'ignore' });
    for (let attempt = 0; ; attempt++) {
      if (server.exitCode !== null) throw new Error(`Disposable server exited: ${server.exitCode}`);
      try { if ((await fetch(origin + '/setup')).ok) break; } catch (_) {}
      if (attempt > 120) throw new Error('Disposable server did not become ready');
      await new Promise(resolve => setTimeout(resolve, 100));
    }

    browser = await chromium.launch({ headless: true, args: ['--no-sandbox'], ...(process.env.CHROMIUM_PATH ? { executablePath: process.env.CHROMIUM_PATH } : {}) });
    const page = await browser.newPage({ viewport: { width: 1440, height: 1100 } });
    page.on('pageerror', error => pageErrors.push(error.message));
    // Block external CDN/network access; serve the pinned Alpine dependency locally.
    await page.route('**/*', route => {
      const url = route.request().url();
      if (url.startsWith(origin + '/')) return route.continue();
      if (url.includes('unpkg.com/alpinejs@')) return route.fulfill({ contentType: 'application/javascript', path: require.resolve('alpinejs/dist/cdn.min.js') });
      return route.fulfill({ contentType: 'application/javascript', body: '' });
    });
    await page.goto(origin + '/setup');
    await page.locator('form[action="/setup"] input[name="email"]').fill('wiring-designer@example.invalid');
    await page.locator('form[action="/setup"] input[name="password"]').fill('wiring-designer-disposable-123');
    await page.locator('form[action="/setup"] button').click(); await page.waitForURL(origin + '/');

    const database = path.join(scratch, 'data/wledger.db');
    function sql(statement, params = []) {
      const result = spawnSync('python3', ['-c', 'import sqlite3,sys,json; c=sqlite3.connect(sys.argv[1]); c.execute("PRAGMA foreign_keys=ON"); c.execute(sys.argv[2],json.loads(sys.argv[3])); c.commit(); c.close()', database, statement, JSON.stringify(params)], { encoding: 'utf8' });
      assert.equal(result.status, 0, result.stderr);
    }
    function rows(statement, params = []) {
      const result = spawnSync('python3', ['-c', 'import json,sqlite3,sys; c=sqlite3.connect(sys.argv[1]); print(json.dumps(c.execute(sys.argv[2],json.loads(sys.argv[3])).fetchall())); c.close()', database, statement, JSON.stringify(params)], { encoding: 'utf8' });
      assert.equal(result.status, 0, result.stderr); return JSON.parse(result.stdout.trim());
    }
    // Seed authoritative electrical outputs directly in the disposable database.
    sql('INSERT INTO controllers(id,name,ip_address,port,led_count) VALUES(1,\'Mapped fixture\',\'127.0.0.1\',?,24)', [mock.address().port]);
    sql('INSERT INTO controllers(id,name,ip_address,port,led_count) VALUES(2,\'Unmapped fixture\',\'127.0.0.1\',?,24)', [mock.address().port]);
    sql("INSERT INTO controller_outputs(controller_id,display_order,name,pixel_count,direction,pattern,rows,columns,custom_order_json) VALUES(1,0,'Mapped serpentine',24,'forward','serpentine_rows',4,6,'[]'),(2,0,'Unmapped strip',24,'forward','linear',1,1,'[]')");
    sql("INSERT INTO cabinets(id,name,controller_id,coordinate_system,layout_width,layout_height) VALUES(1,'Fixture cabinet',1,'grid',100,100)");
    sql('INSERT INTO cabinet_controllers(cabinet_id,controller_id,slot,strings_configured) VALUES(1,1,0,1)');
        sql("INSERT INTO cabinet_led_strings(cabinet_id,controller_id,name,display_order,pixel_count,controller_output_id) SELECT 1,1,'Saved output path',0,24,id FROM controller_outputs WHERE controller_id=1");
    sql("INSERT INTO drawers(id,cabinet_id,label,display_order,x,y,width,height) VALUES(1,1,'Drawer A',0,0,0,20,20)");
    sql("INSERT INTO containers(id,name,controller_id,segment_id,led_start,led_count) VALUES(1,'Legacy electrical string',1,0,0,24)");
    sql("INSERT INTO bins(id,name,container_id,led_index,width) VALUES(1,'Mapped bin',1,0,1)");
    sql('INSERT INTO bin_drawer_attachments(bin_id,drawer_id,x,y,width,height,display_order) VALUES(1,1,0,0,1,1,0)');
    sql('INSERT INTO drawer_led_allocations(cabinet_id,drawer_id,start_pixel,pixel_count,display_order) VALUES(1,1,1,8,0)');

    const open = async id => {
      await page.goto(`${origin}/hardware/${id}/wiring-designer`);
      await page.getByRole('heading', { name: 'Graphical LED Wiring Designer' }).waitFor();
      await page.waitForFunction(() => window.Alpine && Alpine.$data(document.querySelector('[x-data="wiringDesigner()"]'))?.data?.controller?.id);
    };
    const designerState = () => page.locator('[x-data="wiringDesigner()"]').evaluate(el => Alpine.$data(el));
    const canvas = page.locator('svg[aria-label="Controller output and pixel wiring drawing"]');

    const trafficBefore = controllerRequests.length;
    const inventoryBefore = JSON.stringify(rows('SELECT id,cabinet_id,label,x,y,width,height FROM drawers ORDER BY id')) + JSON.stringify(rows('SELECT id,name,container_id,led_index FROM bins ORDER BY id'));
    const mappedOutputIDs = rows('SELECT id FROM controller_outputs WHERE controller_id=1 ORDER BY display_order');
    await open(1);
    const unallocated = await (await page.request.get(origin + '/hardware/1/wiring-designer/pixel?pixel=23')).json();
    assert.deepEqual(unallocated.cabinet_addresses, [{cabinet_id:1,cabinet:'Fixture cabinet',pixel:19}], 'unallocated pixel retains cabinet addressing');
    await canvas.locator('circle[role="button"]').first().click({ force: true });
    await page.getByRole('button', { name: 'Zoom +', exact: true }).click();
    assert.match(await canvas.getAttribute('viewBox'), /960 640/);
    await page.getByRole('button', { name: 'Reset view' }).click();
    const beforePan = await canvas.getAttribute('viewBox');
    const box = await canvas.boundingBox();
    await page.mouse.move(box.x + box.width * .9, box.y + box.height * .9);
    await page.mouse.down(); await page.mouse.move(box.x + box.width * .88, box.y + box.height * .88); await page.mouse.up();
    assert.notEqual(await canvas.getAttribute('viewBox'), beforePan, 'dragging empty canvas pans the drawing');
    await page.getByRole('button', { name: 'Reset view' }).click();

    // Variable rows/columns, template and custom physical-pixel arrangement.
    const app = page.locator('[x-data="wiringDesigner()"]');
    await page.getByRole('button', { name: 'Irregular rows' }).click();
    await page.getByRole('button', { name: 'Custom pixels' }).click();
    assert.equal((await designerState()).editPixel, true);
    const draggablePixel = canvas.locator('circle[role="button"][data-logical="2"]');
    await draggablePixel.scrollIntoViewIfNeeded();
    const pixelBox = await draggablePixel.boundingBox();
    await page.mouse.move(pixelBox.x + pixelBox.width / 2, pixelBox.y + pixelBox.height / 2);
    await page.mouse.down(); await page.mouse.move(pixelBox.x + pixelBox.width / 2 + 24, pixelBox.y + pixelBox.height / 2 + 18, { steps: 4 }); await page.mouse.up();
    assert.equal((await designerState()).selected.pixel_positions.length, 1, 'dragging in custom-pixel mode records an individual visual position');
    await page.getByRole('button', { name: 'Custom pixels' }).click();
    const rowInputs = page.locator('input[type="number"][min="1"].w-24');
    while (await rowInputs.count() > 3) await page.getByRole('button', { name: 'Remove row' }).last().click();
    while (await rowInputs.count() < 3) await page.getByRole('button', { name: '＋ Add row' }).click();
    await rowInputs.nth(0).fill('8'); await rowInputs.nth(0).press('Tab');
    await rowInputs.nth(1).fill('7'); await rowInputs.nth(1).press('Tab');
    await page.locator('select[x-model="selected.axis"]').selectOption('columns');
    await page.locator('input[x-model="selected.sectionsText"]').fill('12, 12');
    await page.locator('input[x-model="selected.sectionsText"]').press('Tab');
    await page.locator('select[x-model\\.number="selected.rotation"]').selectOption('90');
    const sectionX = page.locator('input[x-model\\.number="section.x"]');
    await sectionX.nth(0).fill('12'); await sectionX.nth(0).press('Tab');
    await page.locator('input[x-model\\.number="section.rotation"]').nth(1).fill('30');
    await page.locator('input[x-model\\.number="section.rotation"]').nth(1).dispatchEvent('input');

    await page.getByRole('button', { name: '＋ Zero-pixel jumper' }).click();
    const canvasBox = await canvas.boundingBox();
    await page.mouse.click(canvasBox.x + canvasBox.width * .45, canvasBox.y + canvasBox.height * .75);
    await page.mouse.click(canvasBox.x + canvasBox.width * .48, canvasBox.y + canvasBox.height * .78);
    await page.getByRole('button', { name: 'Finish jumper' }).click();
    await page.locator('input[type="file"][accept="image/png,image/jpeg"]').setInputFiles({
      name: 'cabinet.png', mimeType: 'image/png',
      buffer: Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADUlEQVR4nGP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC', 'base64'),
    });
    await page.locator('input[x-model\\.number="layout.background.x"]').fill('20');
    await page.locator('input[x-model\\.number="layout.background.width"]').fill('1150');
    await page.locator('input[x-model\\.number="layout.background.height"]').fill('760');
    await page.getByRole('button', { name: 'Save graphical layout' }).click();
    await page.waitForFunction(() => { const state = Alpine.$data(document.querySelector('[x-data="wiringDesigner()"]')); return Boolean(state.notice || state.error); });
    const visualSaveState = await designerState();
    assert.equal(visualSaveState.error, '', `visual save failed: ${visualSaveState.error}`);
    assert.match(visualSaveState.notice, /Graphical layout saved/);
    const visualAfter = rows('SELECT direction,pixel_count,id FROM controller_outputs WHERE controller_id=1 ORDER BY display_order');
    assert.deepEqual(visualAfter, [['forward', 24, mappedOutputIDs[0][0]]], 'visual-only save preserves electrical topology and stable output ID');
    assert.equal(JSON.stringify(rows('SELECT id,cabinet_id,label,x,y,width,height FROM drawers ORDER BY id')) + JSON.stringify(rows('SELECT id,name,container_id,led_index FROM bins ORDER BY id')), inventoryBefore, 'visual layout edits preserve drawer/bin identities and coordinates');
    const saved = await designerState();
    assert.deepEqual(saved.selected.rows, [8, 7, 9]);
    assert.deepEqual(saved.selected.sections, [12, 12]);
    assert.equal(saved.selected.axis, 'columns'); assert.equal(saved.selected.rotation, 90);
    await page.reload(); await page.getByRole('heading', { name: 'Graphical LED Wiring Designer' }).waitFor();
    assert.ok(await canvas.locator('image').count(), 'raster background reopens');
    assert.ok(await canvas.locator('polyline').count() >= 1, 'zero-pixel jumper reopens');
    const reopened = await designerState();
    assert.deepEqual(reopened.outputs[0].rows, [8, 7, 9]);
    assert.deepEqual(reopened.outputs[0].sections, [12, 12]);
    assert.equal(reopened.outputs[0].section_layouts[0].x, 12);
    assert.equal(reopened.outputs[0].section_layouts[1].rotation, 30);
    assert.equal(reopened.outputs[0].rotation, 90);
        await page.screenshot({path: process.env.WLEDGER_DESIGNER_SCREENSHOT || '/tmp/wledger-wiring-designer.png', fullPage:true});

    // Pick a pixel: read-only inspector and generated Test Centre deep link; no hardware call.
    await canvas.locator('circle[role="button"]').first().click({ force: true });
    await page.getByText(/controller pixel .*0-based/).waitFor();
    const testHref = await page.getByRole('link', { name: 'Test selected pixel in LED Test Centre' }).getAttribute('href');
    assert.equal(new URL(testHref, origin).searchParams.get('controller_id'), '1');
    await page.goto(new URL(testHref, origin).href);
    await page.locator('select[aria-label="Controller"]').waitFor();
    assert.equal(await page.locator('select[aria-label="Controller"]').inputValue(), '1');
    assert.equal(controllerRequests.length, trafficBefore, 'designer and Test Centre pixel selection are read-only');
    assert.equal(controllerWrites.length, 0);

    // A stale visual revision is rejected; mapped electrical topology cannot be changed.
    const stale = await page.evaluate(async () => {
      const data = JSON.parse(document.querySelector('#wiring-designer-data')?.textContent || '{}');
      return data;
    });
    await open(1);
    const staleLayout = await designerState();
    const staleBody = JSON.stringify(staleLayout.layout);
    const firstSave = await page.evaluate(async body => {
      const csrf = document.querySelector('input[x-ref="csrf"]')?.value || '';
      return fetch('/hardware/1/wiring-designer', { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf }, body }).then(async r => ({ status: r.status, text: await r.text() }));
    }, staleBody);
    assert.equal(firstSave.status, 200, firstSave.text);
    const staleSave = await page.evaluate(async body => {
      const csrf = document.querySelector('input[x-ref="csrf"]')?.value || '';
      return fetch('/hardware/1/wiring-designer', { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf }, body }).then(async r => ({ status: r.status, text: await r.text() }));
    }, staleBody);
    assert.equal(staleSave.status, 409, `stale visual save must conflict: ${staleSave.text}`);
    const mappedReview = await page.evaluate(async () => {
      const data = JSON.parse(document.querySelector('#wiring-designer-data').textContent);
      const settings = structuredClone(data.settings); settings.outputs[0].direction = 'reverse';
      const csrf = document.querySelector('input[x-ref="csrf"]')?.value || '';
      const response = await fetch('/hardware/1/wiring-designer/review', { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf, Accept: 'application/json' }, body: JSON.stringify({ settings, expected_signature: data.layout.topology_signature }) });
      return { status: response.status, text: await response.text() };
    });
    assert.equal(mappedReview.status, 409, `mapped controller change must be blocked: ${mappedReview.text}`);

    // Unmapped controller review/apply: explicit confirmation required and output ID retained.
    await open(2);
    await page.getByRole('button', { name: 'Edit Wiring', exact: true }).click();
    await page.getByRole('button', { name: 'Direction: Forward' }).click();
    await page.getByRole('button', { name: 'Review wiring' }).click();
    await page.getByText('Review result', { exact: true }).waitFor();
    const electrical = await designerState();
    const stableOutputID = electrical.electricalReview.new_settings.outputs[0].id;
    assert.equal(electrical.electricalReview.old_settings.outputs[0].id, stableOutputID);
    assert.equal(electrical.electricalReview.new_settings.outputs[0].direction, 'reverse');
    const missingConfirmation = await page.evaluate(async review => {
      const csrf = document.querySelector('input[x-ref="csrf"]')?.value || '';
      const response = await fetch('/hardware/2/wiring-designer/apply', { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf }, body: JSON.stringify({ settings: review.new_settings, expected_signature: review.expected_signature }) });
      return { status: response.status, text: await response.text() };
    }, electrical.electricalReview);
    assert.equal(missingConfirmation.status, 400, `apply without exact typed confirmation must reject: ${missingConfirmation.text}`);
    await page.getByLabel('Type APPLY WIRING to apply reviewed electrical configuration').fill('APPLY WIRING');
    await page.getByRole('button', { name: 'APPLY WIRING', exact: true }).click();
    await page.waitForFunction(() => JSON.parse(document.querySelector('#wiring-designer-data').textContent).settings.outputs[0].direction === 'reverse');
    assert.deepEqual(rows('SELECT id,direction FROM controller_outputs WHERE controller_id=2'), [[stableOutputID, 'reverse']], 'successful unmapped reversal preserves stable output identity');
        await page.getByRole('button', {name:'Edit Wiring',exact:true}).click();
        await page.getByRole('button', {name:'＋ Add output',exact:true}).click();
        const electricalCounts=page.locator('input[x-model\\.number="output.pixel_count"]');
        await electricalCounts.nth(0).fill('12'); await electricalCounts.nth(0).press('Tab');
        await electricalCounts.nth(1).fill('12'); await electricalCounts.nth(1).press('Tab');
        await page.getByRole('button', {name:'Review wiring',exact:true}).click();
        await page.getByText('Review result',{exact:true}).waitFor();
        await page.getByLabel('Type APPLY WIRING to apply reviewed electrical configuration').fill('APPLY WIRING');
        await page.getByRole('button',{name:'APPLY WIRING',exact:true}).click();
        await page.waitForFunction(()=>JSON.parse(document.querySelector('#wiring-designer-data').textContent).settings.outputs.length===2);
        assert.equal(await canvas.locator('g[role="group"]').count(),2,'independent outputs render as separate graphical groups');
        const expandedOutputs=rows('SELECT id,pixel_count FROM controller_outputs WHERE controller_id=2 ORDER BY display_order');
        assert.equal(expandedOutputs[0][0],stableOutputID);assert.deepEqual(expandedOutputs.map(output=>output[1]),[12,12]);
    assert.deepEqual(rows('SELECT direction,pixel_count,id FROM controller_outputs WHERE controller_id=1 ORDER BY display_order'), [['forward', 24, mappedOutputIDs[0][0]]], 'mapped controller remains untouched');
    assert.deepEqual(pageErrors, [], JSON.stringify(pageErrors));
    assert.equal(controllerWrites.length, 0, 'no hardware writes are made');
    console.log(JSON.stringify({ result: 'PASS', scenarios: ['variable row/column layouts, templates, per-section transforms, output rotation, zoom/pan, custom-pixel toggle, jumper and raster round-trip', 'stale visual save rejection and electrical ID/mapping preservation', 'pixel inspector/Test Centre deep link without hardware writes', 'mapped electrical review rejection, missing confirmation rejection, unmapped reversal apply'] }));
  } finally {
    if (browser) await browser.close();
    if (server && server.exitCode === null) { server.kill('SIGTERM'); await new Promise(resolve => server.once('exit', resolve)); }
    if (mock) await new Promise(resolve => mock.close(resolve));
    fs.rmSync(scratch, { recursive: true, force: true });
  }
}
main().catch(error => { console.error(error); process.exitCode = 1; });
