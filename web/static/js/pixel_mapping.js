(function (root) {
  function validate(output) {
    const pixelCount = Number(output?.pixel_count ?? output?.PixelCount);
    const direction = output?.direction ?? output?.Direction ?? "forward";
    const pattern = output?.pattern ?? output?.Pattern ?? "linear";
    const rows = Number(output?.rows ?? output?.Rows) || 1;
    const columns = Number(output?.columns ?? output?.Columns) || 1;
    const custom = output?.custom_order ?? output?.CustomOrder ?? [];
    if (!Number.isSafeInteger(pixelCount) || pixelCount < 1 || pixelCount > 100000) return false;
    if (direction !== "forward" && direction !== "reverse") return false;
    if (!Number.isSafeInteger(rows) || !Number.isSafeInteger(columns) || rows < 1 || columns < 1) return false;
    if (pattern === "linear") return custom.length === 0;
    if (pattern === "serpentine_rows" || pattern === "serpentine_columns") {
      return custom.length === 0 && rows * columns === pixelCount;
    }
    if (pattern !== "custom" || !Array.isArray(custom) || custom.length !== pixelCount) return false;
    const seen = new Uint8Array(pixelCount);
    for (const value of custom) {
      if (!Number.isSafeInteger(value) || value < 0 || value >= pixelCount || seen[value]) return false;
      seen[value] = 1;
    }
    return true;
  }

  function physicalIndexUnchecked(output, logicalIndex) {
    const pixelCount = Number(output.pixel_count ?? output.PixelCount);
    const direction = output.direction ?? output.Direction ?? "forward";
    const pattern = output.pattern ?? output.Pattern ?? "linear";
    const rows = Number(output.rows ?? output.Rows) || 1;
    const columns = Number(output.columns ?? output.Columns) || 1;
    const custom = output.custom_order ?? output.CustomOrder ?? [];
    let mapped = logicalIndex;
    if (pattern === "serpentine_rows") {
      const row = Math.floor(logicalIndex / columns);
      let column = logicalIndex % columns;
      if (row % 2 === 1) column = columns - 1 - column;
      mapped = row * columns + column;
    } else if (pattern === "serpentine_columns") {
      let row = Math.floor(logicalIndex / columns);
      const column = logicalIndex % columns;
      if (column % 2 === 1) row = rows - 1 - row;
      mapped = column * rows + row;
    } else if (pattern === "custom") {
      mapped = custom[logicalIndex];
    }
    return direction === "reverse" ? pixelCount - 1 - mapped : mapped;
  }

  function physicalIndex(output, logicalIndex) {
    if (!validate(output)) throw new Error("invalid physical pixel mapping");
    const pixelCount = Number(output.pixel_count ?? output.PixelCount);
    if (!Number.isSafeInteger(logicalIndex) || logicalIndex < 0 || logicalIndex >= pixelCount) {
      throw new RangeError("logical pixel is outside the configured output");
    }
    return physicalIndexUnchecked(output, logicalIndex);
  }

  function physicalIndices(output, logicalStart, count) {
    if (!validate(output)) throw new Error("invalid physical pixel mapping");
    const pixelCount = Number(output.pixel_count ?? output.PixelCount);
    if (!Number.isSafeInteger(logicalStart) || !Number.isSafeInteger(count) || logicalStart < 0 || count < 1 || logicalStart + count > pixelCount) {
      throw new RangeError("logical pixel range is outside the configured output");
    }
    return Array.from({ length: count }, (_, offset) => physicalIndexUnchecked(output, logicalStart + offset));
  }

  function generatePermutation(layout, rowsValue, columnsValue) {
    const rows = Number(rowsValue), columns = Number(columnsValue);
    if (!Number.isSafeInteger(rows) || !Number.isSafeInteger(columns) || rows < 1 || columns < 1 || rows * columns > 100000) {
      throw new RangeError("rows and columns must define between 1 and 100000 pixels");
    }
    const count = rows * columns;
    if (layout === "manual" || layout === "mixed") return null;
    return Array.from({ length: count }, (_, logical) => {
      const row = Math.floor(logical / columns);
      const column = logical % columns;
      switch (layout) {
        case "row-major": return logical;
        case "right-to-left-rows": return row * columns + columns - 1 - column;
        case "top-to-bottom-columns": return column * rows + row;
        case "bottom-to-top-columns": return column * rows + rows - 1 - row;
        case "serpentine-rows": return row * columns + (row % 2 === 1 ? columns - 1 - column : column);
        case "serpentine-columns": return column * rows + (column % 2 === 1 ? rows - 1 - row : row);
        default: throw new Error("unsupported custom pixel pattern");
      }
    });
  }

  const api = Object.freeze({ validate, physicalIndex, physicalIndices, generatePermutation });
  root.WLEDgerPixelMapping = api;
  if (typeof module !== "undefined" && module.exports) module.exports = api;
})(globalThis);
