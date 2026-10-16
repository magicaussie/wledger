const assert = require('node:assert/strict');
const { wiringDesigner, wiringDesignerPure } = require('../static/js/wiring_designer.js');

function close(actual, expected, message) {
  assert.ok(Math.abs(actual - expected) < 1e-8, `${message}: got ${actual}, want ${expected}`);
}

const rows = wiringDesignerPure.rowPositions([3, 2], 'rows', 0, 0, 120, 90);
assert.equal(rows.length, 5);
assert.deepEqual(rows.map(p => p.index), [0, 1, 2, 3, 4]);
close(rows[3].x, rows[4].x + (108 / 3), 'unequal second row reverses serpentine order');
assert.ok(rows[3].y > rows[0].y, 'second row is lower than first');

const columns = wiringDesignerPure.rowPositions([2, 3], 'columns', 0, 0, 120, 90);
assert.equal(columns.length, 5);
assert.ok(columns[0].x < columns[2].x, 'second column is right of first');
assert.ok(columns[2].y > columns[3].y, 'odd serpentine column traverses upward');

const plain = {
  rows: [4], axis: 'rows', x: 0, y: 0, width: 120, height: 90, rotation: 0,
  section_layouts: [{ id: 'a', start: 0, count: 4, x: 0, y: 0, rotation: 0 }], pixel_positions: [],
};
const before = wiringDesignerPure.layoutPoints(plain);
const movedRotated = wiringDesignerPure.layoutPoints({
  ...plain,
  section_layouts: [{ ...plain.section_layouts[0], x: 13, y: -7, rotation: 90 }],
});
assert.equal(movedRotated.length, 4);
assert.notDeepEqual(movedRotated, before, 'section transform changes displayed pixel coordinates');
const beforeCentre = before.reduce((a, p) => ({ x: a.x + p.x / before.length, y: a.y + p.y / before.length }), { x: 0, y: 0 });
const afterCentre = movedRotated.reduce((a, p) => ({ x: a.x + p.x / movedRotated.length, y: a.y + p.y / movedRotated.length }), { x: 0, y: 0 });
close(afterCentre.x - beforeCentre.x, 13, 'section x offset');
close(afterCentre.y - beforeCentre.y, -7, 'section y offset');

const custom = wiringDesignerPure.layoutPoints({
  ...plain,
  pixel_positions: [{ index: 1, x: 80, y: 60 }],
});
close(custom[1].x, 80, 'custom pixel x');
close(custom[1].y, 60, 'custom pixel y');

const state = wiringDesigner();
state.electricalDraft = { controller_type: 'wled', connection_timeout_ms: 2000, outputs: [{ id: 8, display_order: 0, name: 'A', pixel_count: 2, direction: 'forward', pattern: 'custom', rows: 1, columns: 1, custom_order: [1, 0], custom_order_text: '1, 0' }] };
state.electricalConfirmation = 'APPLY WIRING';
state.invalidateElectricalReview();
assert.equal(state.electricalConfirmation, '', 'changing electrical draft invalidates typed confirmation');
state.electricalDraft.outputs[0].custom_order_text = '1, 0';
state.validateElectricalDraft();
const payload = state.electricalSettingsPayload();
assert.deepEqual(payload.outputs[0].custom_order, [1, 0]);
assert.equal(Object.hasOwn(payload.outputs[0], 'custom_order_text'), false, 'UI helper fields are omitted from strict backend JSON');
state.addElectricalOutput();
assert.equal(state.electricalDraft.outputs.length, 2);
state.toggleReverse(state.electricalDraft.outputs[0]);
assert.equal(state.electricalDraft.outputs[0].direction, 'reverse');
state.moveElectricalOutput(0, 1);
assert.equal(state.electricalDraft.outputs[0].name, 'Output 2');
assert.deepEqual(state.electricalDraft.outputs.map(o => o.display_order), [0, 1]);

for (const [pattern,custom,expected] of [
  ['linear',[],[0,1,2,3,4,5]],
  ['serpentine_rows',[],[0,1,2,5,4,3]],
  ['serpentine_columns',[],[0,3,4,1,2,5]],
  ['custom',[2,0,5,1,4,3],[2,0,5,1,4,3]],
]) {
  const config={pattern,rows:2,columns:3,pixel_count:6,direction:'forward',custom_order:custom};
  assert.deepEqual(Array.from({length:6},(_,i)=>wiringDesignerPure.physicalIndex(config,i)),expected);
  config.direction='reverse';
  assert.deepEqual(Array.from({length:6},(_,i)=>wiringDesignerPure.physicalIndex(config,i)),expected.map(i=>5-i));
}
const empty=wiringDesigner();empty.electricalDraft={outputs:null};empty.hydrateElectricalDraft();empty.addElectricalOutput();
assert.equal(empty.electricalDraft.outputs.length,1,'controllers without configured outputs can begin graphical setup');
const templates=wiringDesigner();templates.render=()=>{};templates.selected={count:24};templates.setTemplate('rectangle');
assert.equal(new Set(templates.selected.rows).size,1,'rectangle template has equal-length rows');
assert.equal(templates.selected.rows.reduce((sum,n)=>sum+n,0),24);
templates.selected={count:1};templates.setTemplate('irregular');assert.deepEqual(templates.selected.rows,[1]);
console.log('wiring designer pure and draft tests passed');
