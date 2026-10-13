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
  const scratchDir = fs.mkdtempSync(path.join(os.tmpdir(), 'wledger-csrf-browser-'));
  const databasePath = path.join(scratchDir, 'data', 'wledger.db');
  let server;
  let browser;
  try {
    fs.symlinkSync(path.join(repoRoot, 'web'), path.join(scratchDir, 'web'));
    fs.symlinkSync(path.join(repoRoot, 'locales'), path.join(scratchDir, 'locales'));

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
      env: { ...process.env, WLEDGER_INSECURE_COOKIES: '1', WLEDGER_PORT: String(port) },
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

    browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH || '/bin/chromium', headless: true, args: ['--no-sandbox'] });
    const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
    const page = await context.newPage();
    const pageErrors = [];
    const consoleErrors = [];
    const mutationRequests = [];
    page.on('pageerror', error => pageErrors.push(error.message));
    page.on('console', message => { if (message.type() === 'error') consoleErrors.push(message.text()); });
    page.on('request', request => {
      if (['POST', 'PUT', 'PATCH', 'DELETE'].includes(request.method()) && new URL(request.url()).origin === baseURL) {
        mutationRequests.push({ method: request.method(), url: request.url(), path: new URL(request.url()).pathname, headers: request.headers(), body: request.postData() || '' });
      }
    });

    await page.goto(`${baseURL}/setup`);
    const setupCSRFToken = await page.locator('form[action="/setup"] input[name="csrf_token"]').inputValue();
    assert.notEqual(setupCSRFToken, '');
    await page.locator('form[action="/setup"] input[name="email"]').fill('csrf-admin@example.invalid');
    await page.locator('form[action="/setup"] input[name="password"]').fill('safe-password-123');
    await page.locator('form[action="/setup"] button').click();
    await page.waitForURL(`${baseURL}/`);
    assert.notEqual(await page.locator('meta[name="csrf-token"]').getAttribute('content'), setupCSRFToken, 'setup rotates the pre-authentication token');

    assert.notEqual(await page.locator('form[action="/logout"] input[name="csrf_token"]').inputValue(), '');
    await page.locator('form[action="/logout"] button[type="submit"]').click();
    await page.goto(`${baseURL}/login`);
    const loginCSRFToken = await page.locator('form[action="/login"] input[name="csrf_token"]').inputValue();
    assert.notEqual(loginCSRFToken, '');
    await page.locator('form[action="/login"] input[name="email"]').fill('csrf-admin@example.invalid');
    await page.locator('form[action="/login"] input[name="password"]').fill('safe-password-123');
    await page.locator('form[action="/login"] button').click();
    await page.waitForURL(`${baseURL}/`);
    assert.notEqual(await page.locator('meta[name="csrf-token"]').getAttribute('content'), loginCSRFToken, 'login rotates the pre-authentication token');

    const forgedStatus = await page.evaluate(async () => {
      const response = await fetch('/parts', {
        method: 'POST',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: 'name=forged-without-csrf',
      });
      return response.status;
    });
    assert.equal(forgedStatus, 403, 'session-authenticated fetch without CSRF is rejected');
    assert.equal(query(databasePath, 'SELECT COUNT(*) FROM parts')[0][0], 0, 'rejected create did not mutate inventory');

    await page.goto(`${baseURL}/parts/new`);
    const createForm = page.locator('form[action="/parts"]');
    const createToken = await createForm.locator('input[name="csrf_token"]').inputValue();
    assert.notEqual(createToken, '');
    assert.equal(createToken, await page.locator('meta[name="csrf-token"]').getAttribute('content'));
    await createForm.locator('input[name="name"]').fill('CSRF workflow part');
    const pngBytes = Buffer.from(await page.evaluate(async () => {
      const canvas = document.createElement('canvas');
      canvas.width = 2; canvas.height = 2;
      const context = canvas.getContext('2d');
      context.fillStyle = '#4f46e5'; context.fillRect(0, 0, 2, 2);
      const blob = await new Promise(resolve => canvas.toBlob(resolve, 'image/png'));
      return Array.from(new Uint8Array(await blob.arrayBuffer()));
    }));
    await createForm.locator('input[name="image"]').setInputFiles({
      name: 'csrf-workflow.png',
      mimeType: 'image/png',
      buffer: pngBytes,
    });
    await createForm.locator('button[type="submit"]').click();
    await page.waitForURL(/\/parts\/\d+$/).catch(async error => { const request = mutationRequests.findLast(item => item.path === '/parts'); throw new Error(`part create did not redirect: url=${page.url()} body=${await page.locator('body').innerText()} request=${JSON.stringify(request)} cause=${error.message}`); });
    const partID = Number(page.url().match(/\/parts\/(\d+)$/)[1]);
    assert.match(await page.locator('body').innerText(), /CSRF workflow part/);
    assert.ok(query(databasePath, 'SELECT image_path FROM parts WHERE id=?', [partID])[0][0], 'multipart part image persisted');

    await page.goto(`${baseURL}/parts/${partID}/edit`);
    const editForm = page.locator(`form[action="/parts/${partID}/update"]`);
    assert.notEqual(await editForm.locator('input[name="csrf_token"]').inputValue(), '');
    await editForm.locator('input[name="name"]').fill('CSRF workflow part edited');
    await editForm.locator('button[type="submit"]').click();
    await page.waitForURL(`${baseURL}/parts/${partID}`);
    assert.match(await page.locator('body').innerText(), /CSRF workflow part edited/);

    const binID = seedStockBin(databasePath, partID);
    await page.goto(`${baseURL}/parts/${partID}`);
    const stockRow = page.locator('tr').filter({ hasText: 'CSRF browser stock bin' });
    await stockRow.locator('button[hx-post$="delta=1"]').click();
    await page.waitForFunction(({ path, id }) => {
      const row = Array.from(document.querySelectorAll('tr')).find(element => element.innerText.includes('CSRF browser stock bin'));
      return row && row.innerText.includes('3') && path && id;
    }, { path: baseURL, id: binID });
    assert.equal(query(databasePath, 'SELECT quantity FROM part_assignments WHERE part_id=? AND bin_id=?', [partID, binID])[0][0], 3);

    await page.goto(`${baseURL}/settings`);
    const settingsForm = page.locator('form[action="/settings"]');
    assert.notEqual(await settingsForm.locator('input[name="csrf_token"]').inputValue(), '');
    await settingsForm.locator('input[name="enable_debug_logs"]').check();
    await settingsForm.locator('button').click();
    await page.waitForLoadState('networkidle');
    assert.equal(query(databasePath, 'SELECT enable_debug_logs FROM settings LIMIT 1')[0][0], 1, 'settings mutation persisted');

    await page.locator('button[onclick="user_add_modal.showModal()"]:visible').click();
    const createUserForm = page.locator('form[action="/settings/users"]');
    assert.notEqual(await createUserForm.locator('input[name="csrf_token"]').inputValue(), '');
    await createUserForm.locator('input[name="email"]').fill('csrf-editor@example.invalid');
    await createUserForm.locator('input[name="temp_password"]').fill('temporary-password');
    await createUserForm.locator('select[name="role"]').selectOption('editor');
    await createUserForm.locator('button[type="submit"]').click();
    await page.waitForURL(`${baseURL}/settings`);
    const userID = query(databasePath, 'SELECT id FROM users WHERE email=?', ['csrf-editor@example.invalid'])[0][0];
    assert.ok(userID, 'test user was created through the browser form');

    page.on('dialog', dialog => dialog.accept());
    await page.locator(`form[action="/settings/users/${userID}/delete"] button`).click();
    await page.waitForURL(`${baseURL}/settings`);
    assert.equal(query(databasePath, 'SELECT COUNT(*) FROM users WHERE email=?', ['csrf-editor@example.invalid'])[0][0], 0, 'test user was deleted through the browser form');

    const downloadPromise = page.waitForEvent('download');
    await page.locator('a[href="/settings/backup/download"]').click();
    const download = await downloadPromise;
    const backupPath = path.join(scratchDir, 'csrf-browser-backup.zip');
    await download.saveAs(backupPath);
    assert.ok(fs.statSync(backupPath).size > 0);

    const beforeRejectedRestore = query(databasePath, 'SELECT COUNT(*) FROM parts')[0][0];
    const rejectedRestoreStatus = await page.evaluate(async backupBase64 => {
      const bytes = Uint8Array.from(atob(backupBase64), character => character.charCodeAt(0));
      const form = new FormData();
      form.append('backup_file', new File([bytes], 'backup.zip', { type: 'application/zip' }));
      const response = await fetch('/settings/backup/restore', { method: 'POST', body: form });
      return response.status;
    }, fs.readFileSync(backupPath).toString('base64'));
    assert.equal(rejectedRestoreStatus, 403, 'multipart restore without token is rejected before handler execution');
    assert.equal(query(databasePath, 'SELECT COUNT(*) FROM parts')[0][0], beforeRejectedRestore, 'rejected restore leaves inventory intact');

    await page.locator('button[onclick="restore_modal.showModal()"]:visible').click();
    const restoreForm = page.locator('form[hx-post="/settings/backup/restore"]');
    await restoreForm.locator('input[name="backup_file"]').setInputFiles(backupPath);
    await restoreForm.locator('input[type="checkbox"]').check();
    await restoreForm.locator('button[type="submit"]').click();
    await page.locator('#restore_result').getByText(/System restored successfully/).waitFor({ timeout: 30000 });
    assert.equal(query(databasePath, 'SELECT COUNT(*) FROM parts WHERE id=?', [partID])[0][0], 1, 'restore completed in the disposable database');

    const requestHasFormToken = pathPrefix => mutationRequests.some(request => request.path.startsWith(pathPrefix) && /name="csrf_token"|(?:^|&)csrf_token=/.test(request.body));
    const requestHasHeaderToken = pathPrefix => mutationRequests.some(request => request.path.startsWith(pathPrefix) && Boolean(request.headers['x-csrf-token']));
    assert.ok(requestHasFormToken(`/parts/${partID}/update`), 'multipart part edit submitted its hidden token');
    assert.ok(requestHasHeaderToken(`/parts/${partID}/stock/`), 'HTMX stock adjustment sent the CSRF header');
    assert.ok(requestHasFormToken('/settings'), 'settings form submitted its hidden token');
    assert.ok(requestHasFormToken('/settings/users'), 'user form submitted its hidden token');
    assert.ok(requestHasHeaderToken('/settings/backup/restore'), 'multipart HTMX restore sent the CSRF header');
    assert.ok(mutationRequests.every(request => !new URL(request.url).searchParams.has('csrf_token')), 'CSRF tokens never appear in request URLs');
    const knownDuplicateScannerErrors = [...pageErrors, ...consoleErrors].filter(message => message.includes("Identifier 'html5QrcodeScanner' has already been declared"));
    const expectedCSRFRejectionConsoleErrors = consoleErrors.filter(message => message.includes('server responded with a status of 403 (Forbidden)'));
    const unexpectedPageErrors = pageErrors;
    const unexpectedConsoleErrors = consoleErrors.filter(message => !message.includes('server responded with a status of 403 (Forbidden)'));
    assert.deepEqual(knownDuplicateScannerErrors, [], 'part create and edit pages must load the global scanner script only once');
    assert.deepEqual(unexpectedPageErrors, []);
    assert.deepEqual(unexpectedConsoleErrors, []);
    console.log(JSON.stringify({ result: 'PASS', partID, binID, userID, mutationRequests: mutationRequests.length, unexpectedPageErrors, unexpectedConsoleErrors, expectedCSRFRejectionConsoleErrors, knownDuplicateScannerErrors }));
  } finally {
    if (browser) await browser.close();
    if (server) { server.kill('SIGTERM'); await new Promise(resolve => server.once('exit', resolve)); }
    fs.rmSync(scratchDir, { recursive: true, force: true });
  }
})().catch(error => { console.error(error); process.exit(1); });

function query(databasePath, sql, parameters = []) {
  const result = spawnSync('python3', ['-c', 'import json,sqlite3,sys; c=sqlite3.connect(sys.argv[1]); rows=c.execute(sys.argv[2], json.loads(sys.argv[3])).fetchall(); print(json.dumps(rows)); c.close()', databasePath, sql, JSON.stringify(parameters)], { encoding: 'utf8' });
  if (result.status !== 0) throw new Error(`SQLite query failed: ${result.stderr}`);
  return JSON.parse(result.stdout);
}

function seedStockBin(databasePath, partID) {
  const result = spawnSync('python3', ['-c', 'import sqlite3,sys; c=sqlite3.connect(sys.argv[1]); c.execute("PRAGMA foreign_keys=ON"); cur=c.execute("INSERT INTO bins(name,container_id,led_index,width) VALUES(?,NULL,NULL,1)", ("CSRF browser stock bin",)); bin_id=cur.lastrowid; c.execute("INSERT INTO part_assignments(part_id,bin_id,quantity) VALUES(?,?,2)", (int(sys.argv[2]),bin_id)); c.commit(); print(bin_id); c.close()', databasePath, String(partID)], { encoding: 'utf8' });
  if (result.status !== 0) throw new Error(`stock fixture seed failed: ${result.stderr}`);
  return Number(result.stdout.trim());
}
