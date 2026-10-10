// Behavioural tests for the LED grid painter's coordinate-space logic.
//
// These exercise the Alpine component's pure methods directly by stubbing the
// small parts of the browser/Alpine environment the module touches. Run with:
//
//   node web/tests/grid_painter.test.js
//
// The goals are: (1) UI indices, payload indices and stored indices must use the
// same coordinate space, and (2) drawer-relative editing must stay within the
// owning drawer's allocation without ever being silently converted.

'use strict';

const assert = require('assert');
const path = require('path');

const SRC = path.join(__dirname, '..', 'static', 'js', 'grid_painter.js');

let passed = 0;
const failures = [];
function test(name, fn) {
    try {
        fn();
        passed++;
    } catch (err) {
        failures.push(`${name}: ${err.message}`);
    }
}

// --- harness ---------------------------------------------------------------

function makeFactory() {
    let factory = null;
    const listeners = {};
    globalThis.document = {
        addEventListener: (evt, cb) => {
            listeners[evt] = cb;
        },
        createElement: () => {
            let inner = '';
            return {
                set innerHTML(v) { inner = v; },
                get value() { return inner; },
            };
        },
        getElementById: (id) => (globalThis.__elements[id] || { textContent: '' }),
    };
    globalThis.Alpine = {
        data: (name, f) => { factory = f; },
    };
    delete require.cache[require.resolve(SRC)];
    require(SRC);
    listeners['alpine:init']();
    return factory;
}

function container(id, name, seg, start, count, cfg) {
    return {
        id,
        name,
        segment_id: seg,
        led_start: start,
        led_count: count,
        config_json: { String: JSON.stringify(cfg || { type: 'linear', total: count }), Valid: true },
    };
}

function bin(id, name, containerID, led, width, x, y) {
    return {
        id,
        name,
        container_id: containerID,
        led_index: { Int64: led, Valid: led !== null },
        width: { Int64: width, Valid: true },
        grid_x: { Int64: x, Valid: true },
        grid_y: { Int64: y, Valid: true },
    };
}

// Build a component and run init() against stubbed JSON scripts.
function make(space, containers, bins, canEdit) {
    const factory = makeFactory();
    globalThis.__elements = {
        'bin-data': { textContent: JSON.stringify(bins || []) },
        'cont-data': { textContent: JSON.stringify(containers || []) },
    };
    const comp = factory(1, 'bin-data', 'cont-data', space, canEdit === undefined ? true : canEdit);
    comp.init();
    return comp;
}

function exportedIndexFor(comp, name) {
    const rows = comp.exportBinData();
    const row = rows.find((r) => r.name === name);
    return row ? row.led_index : undefined;
}

// --- segment mode ----------------------------------------------------------

test('segment: load keeps segment-absolute index in UI and payload', () => {
    const comp = make('segment',
        [container(10, 'A', 0, 20, 10)],
        [bin(1, 'a1', 10, 23, 1, 0, 0)]);
    assert.strictEqual(comp.getCellLedLabel(0, 0), '24'); // 1-based display
    assert.strictEqual(exportedIndexFor(comp, 'a1'), 23); // stored == payload
});

test('segment: autoFill starts at the drawer allocation start', () => {
    const comp = make('segment', [container(10, 'A', 0, 20, 4, { type: 'grid', rows: 2, cols: 2 })], []);
    comp.autoFill('linear');
    const idx = comp.exportBinData().map((r) => r.led_index).sort((a, b) => a - b);
    assert.deepStrictEqual(idx, [20, 21, 22, 23]);
});

test('segment: getNextAvailableLedIndex dedupes across the whole segment', () => {
    const comp = make('segment',
        [container(10, 'A', 0, 0, 10), container(11, 'B', 0, 10, 10)],
        [bin(1, 'a1', 10, 0, 2, 0, 0)]); // occupies segment 0 and 1
    // Next free in segment 0 must skip the occupied 0,1 even though B is another drawer.
    assert.strictEqual(comp.getNextAvailableLedIndex(1), 2);
});

// --- drawer mode -----------------------------------------------------------

test('drawer: physical (segment) label is led_start + index; payload stays drawer-relative', () => {
    const comp = make('drawer',
        [container(10, 'A', 0, 10, 6)],
        [bin(1, 'a1', 10, 3, 2, 0, 0)]);
    assert.strictEqual(comp.getCellLedLabel(0, 0), '4-5'); // drawer-relative D
    assert.strictEqual(comp.getCellSegmentLabel(0, 0), '14-15'); // physical S
    assert.strictEqual(exportedIndexFor(comp, 'a1'), 3); // NOT the physical 13
});

test('drawer: rangeSummary distinguishes D and S', () => {
    const comp = make('drawer', [container(10, 'A', 0, 10, 6)], [bin(1, 'a1', 10, 3, 1, 0, 0)]);
    comp.onCellClick(0, 0); // select existing cell
    assert.strictEqual(comp.rangeSummary, 'D 4\u20134 \u00b7 S 14\u201314');
});

test('drawer: autoFill fills the drawer from zero, not from led_start', () => {
    const comp = make('drawer',
        [container(10, 'A', 0, 40, 4, { type: 'grid', rows: 2, cols: 2 })], []);
    comp.autoFill('linear');
    const idx = comp.exportBinData().map((r) => r.led_index).sort((a, b) => a - b);
    assert.deepStrictEqual(idx, [0, 1, 2, 3]);
});

test('drawer: getNextAvailableLedIndex is drawer-local and skips used indices', () => {
    const comp = make('drawer',
        [container(10, 'A', 0, 0, 10), container(11, 'B', 0, 10, 10)],
        [bin(1, 'a1', 10, 0, 2, 0, 0), bin(2, 'b1', 11, 0, 1, 0, 0)]);
    // Drawer B's own index 0 is used, so next free is 1 (drawer A's usage is irrelevant).
    assert.strictEqual(comp.getNextAvailableLedIndex(1), 1);
    assert.strictEqual(comp.getNextAvailableLedIndex(0), 2);
});

test('drawer: range editing clamps to the allocation', () => {
    const comp = make('drawer', [container(10, 'A', 0, 5, 10)], [bin(1, 'a1', 10, 0, 1, 0, 0)]);
    comp.onCellClick(0, 0);
    comp.selectedStartLed = 5; // 1-based D start
    comp.selectedEndLed = 20; // beyond the drawer -> clamps
    comp.applyCellRange();
    const cell = comp.cells['0,0'];
    assert.strictEqual(cell.led_index, 4); // D start = 5-1
    assert.strictEqual(cell.width, 6); // [4,10)
});

test('drawer: toggleCell never reuses an occupied drawer index', () => {
    const comp = make('drawer', [container(10, 'A', 0, 0, 3, { type: 'linear', total: 3 })], []);
    comp.toggleCell(0, 0);
    comp.toggleCell(0, 1);
    const idx = comp.exportBinData().map((r) => r.led_index).sort((a, b) => a - b);
    assert.deepStrictEqual(idx, [0, 1]);
});

// --- unresolved mode -------------------------------------------------------

test('unresolved: painter cannot edit', () => {
    const comp = make('unresolved', [container(10, 'A', 0, 0, 4)], [], false);
    comp.toggleCell(0, 0);
    assert.deepStrictEqual(comp.exportBinData(), []);
});

// --- NULL vs index-0 preservation ------------------------------------------

// Convert exported painter rows back into the server bin shape (as templ.JSONScript
// would emit it) so a load/save/reload round trip can be simulated.
function exportedToServerBins(comp, rows) {
    return rows.map((r, i) => ({
        id: i + 1,
        name: r.name,
        container_id: comp.containers[r.container_index].id,
        led_index: { Int64: r.led_index === null ? 0 : r.led_index, Valid: r.led_index !== null },
        width: { Int64: r.width, Valid: true },
        grid_x: { Int64: r.x, Valid: true },
        grid_y: { Int64: r.y, Valid: true },
    }));
}

test('index 0 remains a real (mapped) assignment', () => {
    const comp = make('segment', [container(10, 'A', 0, 0, 10)], [bin(1, 'zero', 10, 0, 1, 0, 0)]);
    assert.strictEqual(comp.isMappedCell(0, 0), true);
    assert.strictEqual(comp.isUnmappedCell(0, 0), false);
    assert.strictEqual(comp.getCellLedLabel(0, 0), '1');
    assert.strictEqual(exportedIndexFor(comp, 'zero'), 0);
});

test('a NULL bin loads unmapped and renders without a coordinate', () => {
    const comp = make('segment', [container(10, 'A', 0, 0, 10)], [bin(1, 'u', 10, null, 1, 0, 0)]);
    assert.strictEqual(comp.isUnmappedCell(0, 0), true);
    assert.strictEqual(comp.isMappedCell(0, 0), false);
    assert.strictEqual(comp.getCellLedLabel(0, 0), '');
    assert.strictEqual(comp.getCellSegmentLabel(0, 0), '');
    assert.strictEqual(comp.getStartLed(0, 0), '');
});

test('NULL bin survives a load/save/reload round trip (segment)', () => {
    const containers = [container(10, 'A', 0, 0, 10)];
    const comp1 = make('segment', containers, [bin(1, 'zero', 10, 0, 1, 0, 0), bin(2, 'u', 10, null, 1, 1, 0)]);
    const rows1 = comp1.exportBinData();
    assert.strictEqual(rows1.find((r) => r.name === 'zero').led_index, 0);
    assert.strictEqual(rows1.find((r) => r.name === 'u').led_index, null);

    const comp2 = make('segment', containers, exportedToServerBins(comp1, rows1));
    const rows2 = comp2.exportBinData();
    assert.strictEqual(rows2.find((r) => r.name === 'zero').led_index, 0);
    assert.strictEqual(rows2.find((r) => r.name === 'u').led_index, null);
});

test('NULL bin survives a load/save/reload round trip (drawer)', () => {
    const containers = [container(10, 'A', 0, 10, 6)];
    const comp1 = make('drawer', containers, [bin(1, 'zero', 10, 0, 1, 0, 0), bin(2, 'u', 10, null, 1, 1, 0)]);
    const rows1 = comp1.exportBinData();
    assert.strictEqual(rows1.find((r) => r.name === 'zero').led_index, 0);
    assert.strictEqual(rows1.find((r) => r.name === 'u').led_index, null);

    const comp2 = make('drawer', containers, exportedToServerBins(comp1, rows1));
    const rows2 = comp2.exportBinData();
    assert.strictEqual(rows2.find((r) => r.name === 'zero').led_index, 0);
    assert.strictEqual(rows2.find((r) => r.name === 'u').led_index, null);
    assert.strictEqual(comp2.getCellSegmentLabel(0, 0), '11'); // physical S for index 0 (start 10)
    assert.strictEqual(comp2.getCellSegmentLabel(0, 1), ''); // unmapped has no physical label
});

test('an ordinary save does not assign LEDs to unmapped bins', () => {
    const comp = make('drawer', [container(10, 'A', 0, 0, 10)],
        [bin(1, 'zero', 10, 0, 1, 0, 0), bin(2, 'u', 10, null, 1, 1, 0)]);
    const rows = comp.exportBinData(); // no painting, auto-fill or range editing
    assert.strictEqual(rows.find((r) => r.name === 'zero').led_index, 0);
    assert.strictEqual(rows.find((r) => r.name === 'u').led_index, null);
});

test('NULL does not occupy an LED index; index 0 does', () => {
    const onlyNull = make('drawer', [container(10, 'A', 0, 0, 10)], [bin(1, 'u', 10, null, 1, 0, 0)]);
    assert.strictEqual(onlyNull.getNextAvailableLedIndex(0), 0);

    const nullPlusZero = make('drawer', [container(10, 'A', 0, 0, 10)],
        [bin(1, 'zero', 10, 0, 1, 0, 0), bin(2, 'u', 10, null, 1, 1, 0)]);
    assert.strictEqual(nullPlusZero.getNextAvailableLedIndex(0), 1);
});

test('painting an unmapped bin explicitly assigns it a real LED', () => {
    const comp = make('drawer', [container(10, 'A', 0, 10, 5)], [bin(1, 'u', 10, null, 1, 0, 0)]);
    comp.onCellClick(0, 0); // select the unmapped cell
    assert.strictEqual(comp.rangeSummary, 'Unassigned');
    comp.selectedStartLed = 3; // drawer-relative index 2
    comp.selectedEndLed = 4; // width 2
    comp.applyCellRange();
    assert.strictEqual(comp.isMappedCell(0, 0), true);
    assert.strictEqual(comp.cells['0,0'].led_index, 2);
    assert.strictEqual(exportedIndexFor(comp, 'u'), 2);
});

test('the Remove action clears a bin from the grid payload (existing UI semantics)', () => {
    const comp = make('segment', [container(10, 'A', 0, 0, 10)], [bin(1, 'a1', 10, 3, 1, 0, 0)]);
    comp.onCellClick(0, 0);
    comp.unmapCell();
    // The Remove action deletes the grid cell; the bin is omitted from the payload
    // rather than being remapped to index 0.
    assert.deepStrictEqual(comp.exportBinData(), []);
});

test('auto-fill assigns sequentially and is not offset by unmapped bins', () => {
    const comp = make('drawer',
        [container(10, 'A', 0, 40, 4, { type: 'grid', rows: 2, cols: 2 })],
        [bin(1, 'u', 10, null, 1, 0, 0)]);
    comp.autoFill('linear');
    const idx = comp.exportBinData().map((r) => r.led_index).sort((a, b) => a - b);
    assert.deepStrictEqual(idx, [0, 1, 2, 3]);
});

test('segment mode: an unmapped bin does not occupy a segment index', () => {
    const comp = make('segment', [container(10, 'A', 0, 0, 10)],
        [bin(1, 'zero', 10, 0, 1, 0, 0), bin(2, 'u', 10, null, 1, 1, 0)]);
    assert.strictEqual(comp.getNextAvailableLedIndex(0), 1); // 0 is used; the NULL bin reserves nothing
});

// --- run -------------------------------------------------------------------

if (failures.length > 0) {
    console.error(`\n${failures.length} test(s) failed:\n` + failures.map((f) => '  - ' + f).join('\n'));
    console.error(`\n${passed} passed, ${failures.length} failed`);
    process.exit(1);
}
console.log(`grid_painter: all ${passed} behavioural tests passed`);
