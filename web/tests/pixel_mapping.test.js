const assert = require("node:assert/strict");
const mapping = require("../static/js/pixel_mapping.js");

const cases = [
  { name: "left-to-right linear", output: { pixel_count: 6, direction: "forward", pattern: "linear" }, want: [0, 1, 2, 3, 4, 5] },
  { name: "right-to-left linear", output: { pixel_count: 6, direction: "reverse", pattern: "linear" }, want: [5, 4, 3, 2, 1, 0] },
  { name: "serpentine rows", output: { pixel_count: 6, rows: 2, columns: 3, pattern: "serpentine_rows" }, want: [0, 1, 2, 5, 4, 3] },
  { name: "reversed serpentine rows", output: { pixel_count: 6, direction: "reverse", rows: 2, columns: 3, pattern: "serpentine_rows" }, want: [5, 4, 3, 0, 1, 2] },
  { name: "serpentine columns", output: { pixel_count: 6, rows: 2, columns: 3, pattern: "serpentine_columns" }, want: [0, 3, 4, 1, 2, 5] },
  { name: "reversed serpentine columns", output: { pixel_count: 6, direction: "reverse", rows: 2, columns: 3, pattern: "serpentine_columns" }, want: [5, 2, 1, 4, 3, 0] },
  { name: "independently reversed rows and mixed directions", output: { pixel_count: 6, pattern: "custom", custom_order: [2, 1, 0, 3, 4, 5] }, want: [2, 1, 0, 3, 4, 5] },
  { name: "arbitrary custom permutation", output: { pixel_count: 6, direction: "reverse", pattern: "custom", custom_order: [0, 2, 1, 3, 5, 4] }, want: [5, 3, 4, 2, 0, 1] },
];

for (const test of cases) {
  assert.equal(mapping.validate(test.output), true, `${test.name} should validate`);
  const actual = Array.from({ length: 6 }, (_, logical) => mapping.physicalIndex(test.output, logical));
  assert.deepEqual(actual, test.want, `${test.name} exact logical-to-physical map`);
  for (let start = 0; start < 6; start++) {
    for (let count = 1; count <= 6 - start; count++) {
      const selected = Array.from({ length: count }, (_, index) => actual[start + index]).sort((a, b) => a - b);
      const unique = new Set(selected);
      assert.equal(unique.size, count, `${test.name} range ${start}+${count} must select distinct pixels`);
      assert.deepEqual([...unique].sort((a, b) => a - b), selected, `${test.name} range ${start}+${count} exact selected pixels`);
    }
  }
}

for (const output of [
  { pixel_count: 6, pattern: "custom", custom_order: [0, 0, 2, 3, 4, 5] },
  { pixel_count: 6, pattern: "serpentine_rows", rows: 2, columns: 2 },
  { pixel_count: 6, pattern: "unknown" },
]) {
  assert.equal(mapping.validate(output), false, "invalid mappings must fail closed");
}
assert.throws(() => mapping.physicalIndex(cases[0].output, 6), RangeError);
for (const [layout, want] of [
  ["row-major", [0, 1, 2, 3, 4, 5]],
  ["right-to-left-rows", [2, 1, 0, 5, 4, 3]],
  ["top-to-bottom-columns", [0, 2, 4, 1, 3, 5]],
  ["bottom-to-top-columns", [1, 3, 5, 0, 2, 4]],
  ["serpentine-rows", [0, 1, 2, 5, 4, 3]],
  ["serpentine-columns", [0, 3, 4, 1, 2, 5]],
]) {
  assert.deepEqual(mapping.generatePermutation(layout, 2, 3), want, `${layout} generated exact custom permutation`);
}
assert.throws(() => mapping.generatePermutation("top-to-bottom-columns", 0, 4), /between 1 and 100000 pixels/);
console.log(`pixel mapping: ${cases.length} exact patterns, all contiguous subranges, and custom wiring generators passed`);
