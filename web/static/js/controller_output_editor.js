window.controllerOutputEditor = function (initialJSON) {
  let initial = [];
  try {
    initial = JSON.parse(initialJSON || "[]");
  } catch {
    initial = [];
  }

  return {
    outputs: initial.map((output, index) => ({
      ...output,
      id: Number(output.id) || 0,
      display_order: index,
      pixel_count: Number(output.pixel_count) || 1,
      rows: Number(output.rows) || 1,
      columns: Number(output.columns) || 1,
      custom_order: Array.isArray(output.custom_order) ? output.custom_order : [],
      custom_order_text: Array.isArray(output.custom_order) ? output.custom_order.join(",") : "",
    })),
    totalPixels() {
      return this.outputs.reduce((sum, output) => sum + (Number(output.pixel_count) || 0), 0);
    },
    addOutput() {
      this.outputs.push({
        id: 0,
        display_order: this.outputs.length,
        name: `Output ${this.outputs.length + 1}`,
        pixel_count: 1,
        direction: "forward",
        pattern: "linear",
        rows: 1,
        columns: 1,
        custom_order: [],
        custom_order_text: "",
      });
    },
    removeOutput(index) {
      this.outputs.splice(index, 1);
      this.normalizeOrder();
    },
    moveOutput(index, delta) {
      const destination = index + delta;
      if (destination < 0 || destination >= this.outputs.length) return;
      const [output] = this.outputs.splice(index, 1);
      this.outputs.splice(destination, 0, output);
      this.normalizeOrder();
    },
    normalizeOrder() {
      this.outputs.forEach((output, index) => { output.display_order = index; });
    },
    parseCustomOrder(output) {
      const raw = String(output.custom_order_text || "").trim();
      output.custom_order = raw === "" ? [] : raw.split(/[\s,;]+/).filter(Boolean).map(value => Number(value));
    },
    previewWidth(output) {
      const total = this.totalPixels();
      return total > 0 ? `${Math.max(1, Number(output.pixel_count) || 0) / total * 100}%` : "0%";
    },
  };
};
