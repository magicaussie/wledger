'use strict';
const assert = require('node:assert/strict');
const { integer, bounds, range, pixels, operation, pixelInfo, proposals, ledTestCentre } = require('../static/js/led_test_centre.js');

async function main() {
  for (const v of ['', ' ', null, undefined, false, NaN, Infinity, 1.5, -1]) assert.throws(() => integer(v, 0, 10, 'value'));
  assert.equal(integer('0', 0, 10, 'value'), 0);
  const limits = bounds({ led_count: 1000 }, { start: 100, length: 20 });
  assert.deepEqual(limits, { start: 100, count: 20 });
  assert.deepEqual(range(119, 1, limits), { start: 119, count: 1 });
  for (const args of [[99, 1], [119, 2], [100, 0], [100.5, 1]]) assert.throws(() => range(...args, limits));
  assert.throws(() => bounds({ led_count: 100 }, { start: 90, length: 20 }));
  assert.equal(pixels({ start: 0, count: 1000000000 }, 999999999).length, 96);
  assert.deepEqual(pixels(limits, 119), Array.from({ length: 20 }, (_, i) => i + 100));
  assert.deepEqual(pixels({ start: 0, count: 0 }, 0), []);
  assert.equal(operation({ ID: 'actual-id', SessionID: 'legacy', ControllerID: 7 }).id, 'actual-id');
  assert.equal(operation({ SessionID: 'session', ControllerID: 7 }).id, 'session');
  assert.equal(operation({ session_id: 'tagged', last_error: 'partial' }).error, 'partial');
  assert.deepEqual(proposals([{ drawer_id: '5', start: '1', count: '3', ignored: true }]), [{ drawer_id: 5, start: 1, count: 3 }]);
  assert.throws(() => proposals([])); assert.throws(() => proposals([{ drawer_id: 1, start: 0, count: 1 }]));

  const mapping = { cabinets: [{ id: 2, name: 'Cabinet A', drawers: [{ id: 5, label: 'Drawer A', ranges: [
    { start: 20, count: 2, physical: [{ controller_id: 7, first_pixel: 101, count: 1, relative_start: 0 }, { controller_id: 7, first_pixel: 100, count: 1, relative_start: 1 }] },
    { start: 50, count: 2, physical: [{ controller_id: 8, first_pixel: 0, count: 2, relative_start: 2 }] }
  ], bins: [{ id: 6, label: 'Bin A', assigned: true, controller_id: 7, pixel: 100, relative_pixel: 1, stale: false }] }] }], validation: [] };
  assert.equal(pixelInfo(mapping, 7, 100).status, 'assigned');
  assert.equal(pixelInfo(mapping, 7, 100).assignments[0].relative, 1, 'reverse wiring must use physical pieces');
  assert.equal(pixelInfo(mapping, 7, 101).assignments[0].relative, 0, 'relative zero is valid');
  assert.equal(pixelInfo(mapping, 8, 1).assignments[0].relative, 3, 'multi-range relative offset preserved');
  assert.equal(pixelInfo(mapping, 9, 100).status, 'unassigned');
  assert.equal(pixelInfo(mapping, 7, 999).status, 'unassigned');
  const drawer = mapping.cabinets[0].drawers[0];
  drawer.bins[0].stale = true; assert.equal(pixelInfo(mapping, 7, 100).status, 'conflict'); drawer.bins[0].stale = false;
  drawer.bins.push({ ...drawer.bins[0], id: 9 }); assert.equal(pixelInfo(mapping, 7, 100).status, 'conflict'); drawer.bins.pop();
  drawer.ranges.push({ physical: [{ controller_id: 7, first_pixel: 100, count: 1, relative_start: 4 }] });
  assert.equal(pixelInfo(mapping, 7, 100).assignments.length, 2); assert.equal(pixelInfo(mapping, 7, 100).status, 'conflict'); drawer.ranges.pop();
  mapping.validation.push({ severity: 'error', controller_id: 7, drawer_id: 5, message: 'overlap' });
  assert.equal(pixelInfo(mapping, 7, 101).status, 'conflict'); assert.equal(pixelInfo(mapping, 8, 1).status, 'assigned');

  // The transport is stubbed: no real network or hardware is used by this suite.
  const calls = [];
  const fixture = () => ({ controllers: [{ id: 7, led_count: 1000 }], cabinets: [{ id: 2, drawers: [{ id: 5, ranges: [{ start: 10, count: 2 }], bins: [{ id: 6, assigned: true, relative_pixel: 0, controller_id: 7, pixel: 100, stale: false }] }] }], operations: [], validation: [], styles: { test_pixel: { color: '#112233', brightness: 0 } }, physical_output_enabled: true });
  let responder = path => path === '/data' ? fixture() : {};
  const state = ledTestCentre();
  state.request = async (path, body) => { calls.push({ path, body }); return responder(path, body); };
  await state.init();
  assert.deepEqual(calls.map(c => c.path), ['/data']);
  state.controllerID = '7'; state.cabinetID = '2'; state.drawerID = '5'; state.binID = '6';
  state.inspections[7] = { outputs: [{ order: 0, start: 100, length: 20 }] }; state.outputOrder = '0'; state.pixel = 119;
  state.applyStyle('test_pixel'); assert.equal(state.brightness, 0);
  responder = path => path === '/data' ? fixture() : path === '/start' ? { operation: { ID: 'op-1', State: 'active' }, status: 'Controller acknowledged command; physical behaviour not verified' } : {};
  await state.start();
  assert.deepEqual(calls.at(-1).body, { controller_id: 7, output_order: 0, start: 119, count: 1, color: '#112233', brightness: 0, duration_seconds: 5, mode: 'solid', function: 'test_pixel' });
  assert.equal(state.chosen, 'op-1'); assert.equal(state.human, false);
  state.count = 2; const n = calls.length; await state.start(); assert.equal(calls.length, n); assert.match(state.error, /range/);
  state.count = 1; await state.start('drawer_all'); assert.equal(calls.at(-1).body.drawer_id, 5); assert.equal(calls.at(-1).body.bin_id, undefined);
    state.controllerID = ''; state.pixel = -100; state.count = -2; state.outputOrder = 'stale';
    for (const f of ['locate_drawer', 'drawer_first', 'drawer_last', 'drawer_bins', 'drawer_sequence', 'drawer_flash', 'drawer_all']) {
      await state.start(f);
      assert.deepEqual(calls.at(-1).body, { controller_id: 0, output_order: null, drawer_id: 5, color: '#112233', brightness: 0, duration_seconds: 5, mode: 'solid', function: f });
    }
    await state.start('drawer_bin'); assert.equal(calls.at(-1).body.bin_id, 6);
    state.binID = '99'; const invalidBinN = calls.length; await state.start('drawer_bin'); assert.equal(calls.length, invalidBinN);
    state.binID = '6'; state.controllerID = '7'; state.outputOrder = '0'; state.pixel = 119; state.count = 999;
    await state.start('identify_pixel'); assert.equal(calls.at(-1).body.count, 1);
    await state.testPixel(0, 'flash'); assert.equal(calls.at(-1).body.count, 1); assert.equal(calls.at(-1).body.mode, 'flash');
    await state.testPixel(0, 'solid', '#000000'); assert.equal(calls.at(-1).body.color, '#000000');
    await state.testPixel(-1); assert.equal(calls.at(-1).body.start, 118);
    await state.testPixel(1); assert.equal(calls.at(-1).body.start, 119);
    state.count = 1;
  await state.start('test_pixel', true, '#000000'); assert.equal(calls.at(-1).body.start, 100); assert.equal(calls.at(-1).body.count, 20);
  state.data.physical_output_enabled = false; const disabledN = calls.length; await state.start(); assert.equal(calls.length, disabledN);
  state.data.physical_output_enabled = true;
  state.pixel = 100; state.mark('first'); state.pixel = 119; state.mark('last');
  responder = () => ({ proposals: [{ drawer_id: 5, start: 1, count: 20 }], conflicts: [] });
  await state.mappingPreview(true);
  assert.deepEqual(calls.at(-1).body, { cabinet_id: 2, controller_id: 7, output_order: 0, first_pixel: 100, last_pixel: 119, drawer_ids: [5] });
  const previewN = calls.length; await state.saveMapping(); assert.equal(calls.length, previewN); assert.match(state.error, /confirm/);
  state.mappingConfirmed = true; state.conflicts = ['overlap']; await state.saveMapping(); assert.equal(calls.length, previewN);
  state.conflicts = []; state.mappingConfirmed = true;
  responder = path => path === '/data' ? fixture() : {};
  await state.saveMapping();
  const save = calls.find(c => c.path === '/mapping-save');
  assert.deepEqual(save.body, { cabinet_id: 2, proposals: [{ drawer_id: 5, start: 1, count: 20 }], confirm: true });
  assert.deepEqual(state.drawer.ranges, [{ start: 10, count: 2 }]);
  state.outputOrder = ''; const staleN = calls.length; await state.mappingPreview(true); assert.equal(calls.length, staleN);
  state.allocationIDs = ['5']; state.cabinetStart = 21; state.perDrawer = 2;
  responder = () => ({ proposals: [{ drawer_id: 5, start: 21, count: 2 }], conflicts: [] });
  await state.mappingPreview(false); assert.deepEqual(calls.at(-1).body, { cabinet_id: 2, drawer_ids: [5], start: 21, count_per_drawer: 2 });
  responder = path => path === '/data' ? fixture() : {};
  await state.stop('op-1'); assert.deepEqual(calls.at(-2), { path: '/stop', body: { operation_id: 'op-1' } });
  const emergencyN = calls.length; await state.emergency(); assert.equal(calls.length, emergencyN);
  state.emergencyText = 'EMERGENCY OFF'; responder = path => path === '/data' ? fixture() : { results: [{ ControllerID: 7, Acknowledged: false, Error: 'timeout' }] };
  await state.emergency(); assert.equal(state.emergencyResults[0].Acknowledged, false);
  assert.deepEqual(calls.at(-2).body, { confirm: 'EMERGENCY OFF' });
  global.confirm = () => true;
  state.binID = '6'; state.pixel = 0; responder = path => path === '/data' ? fixture() : {}; await state.saveBin(); assert.deepEqual(calls.at(-2).body, { bin_id: 6, drawer_id: 5, controller_id: 7, pixel: 0, confirm: true });
  await state.clearBin(); assert.deepEqual(calls.at(-2).body, {bin_id: 6, drawer_id: 5, clear: true, confirm: true});
    await state.start('locate_cabinet'); assert.deepEqual(calls.at(-1).body, {controller_id: 0, output_order: null, cabinet_id: 2, color: '#112233', brightness: 0, duration_seconds: 5, mode: 'solid', function: 'locate_cabinet'});
    delete global.confirm;

  const transport = ledTestCentre();
  global.document = { querySelector: () => ({ content: 'test-token' }) };
  global.fetch = async (url, options) => { calls.push({ url, options }); return { ok: true, json: async () => ({}) }; };
  await transport.request('/data'); assert.equal(calls.at(-1).options.method, undefined);
  await transport.request('/stop', { operation_id: 'test' });
  assert.equal(calls.at(-1).options.headers['X-CSRF-Token'], 'test-token');
  assert.equal(calls.at(-1).options.credentials, 'same-origin');
  global.document.querySelector = () => null;
  await assert.rejects(() => transport.request('/stop', {}), /Security token/);
  global.fetch = async () => ({ ok: false, status: 409, json: async () => ({ error: 'Conflict; retry Stop' }) });
  await assert.rejects(() => transport.request('/data'), /retry Stop/);
  const results = [{ ControllerID: 7, Acknowledged: false, Error: 'timeout' }];
  global.fetch = async () => ({ ok: false, status: 502, json: async () => ({ error: 'Emergency off incomplete', results }) });
  global.document.querySelector = () => ({ content: 'test-token' });
  transport.emergencyText = 'EMERGENCY OFF'; transport.emergencyOpen = true;
  await transport.emergency(); assert.deepEqual(transport.emergencyResults, results); assert.match(transport.error, /incomplete/); assert.equal(transport.emergencyOpen, true);
  delete global.fetch; delete global.document;
  console.log('LED Test Centre: pure validation, state transitions and stubbed API safety checks passed.');
}
main().catch(error => { console.error(error); process.exitCode = 1; });
