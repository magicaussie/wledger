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
      custom_layout: output.custom_layout || "manual",
      custom_order_error: "",
      preview_start: Number(output.preview_start) || 0,
    })),
    serverPreview: [],
    previewError: "",
    previewPending: false,
    previewSequence: 0,
    previewTimer: null,
    previewOrder: 0,
    init() {
      this.$watch("outputs", () => this.queueMappingPreview());
      const form = this.$el.closest("form");
      form?.querySelectorAll('[name="controller_type"], [name="connection_timeout_ms"]').forEach(input => input.addEventListener("change", () => this.queueMappingPreview()));
      this.queueMappingPreview();
    },
    totalPixels() {
      return this.outputs.reduce((sum, output) => sum + (Number(output.pixel_count) || 0), 0);
    },
    queueMappingPreview() {
      clearTimeout(this.previewTimer);
      for (const output of this.outputs) {
        const count = Number(output.pixel_count) || 0;
        const maxStart = Math.max(0, Math.ceil(count / 40 - 1) * 40);
        output.preview_start = Math.max(0, Math.min(maxStart, Number(output.preview_start) || 0));
      }
      this.previewSequence++;
      this.serverPreview = [];
      this.previewError = "";
      this.previewPending = true;
      const sequence = this.previewSequence;
      this.previewTimer = setTimeout(() => this.refreshMappingPreview(sequence), 120);
    },
    async refreshMappingPreview(sequence) {
      const form = this.$el.closest("form");
      const csrfToken = document.querySelector('meta[name="csrf-token"]')?.content || "";
      const settings = {
        controller_type: form?.querySelector('[name="controller_type"]')?.value || "wled",
        connection_timeout_ms: Number(form?.querySelector('[name="connection_timeout_ms"]')?.value) || 2000,
        preview_order: this.previewOrder,
        preview_start: Number(this.outputs.find(output => Number(output.display_order) === Number(this.previewOrder))?.preview_start) || 0,
        outputs: this.outputs.map(({ custom_order_text, custom_layout, custom_order_error, preview_start, ...output }) => output),
      };
      try {
        const response = await fetch("/hardware/output-mapping-preview", {
          method: "POST",
          headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfToken, Accept: "application/json" },
          body: JSON.stringify(settings),
        });
        const data = await response.json();
        if (sequence !== this.previewSequence) return;
        if (!response.ok) throw new Error(data.error || `Pixel preview failed (${response.status}).`);
        this.serverPreview = data;
        // The server always returns page 0 for every non-selected output, so
        // reset their retained page offsets to match. Otherwise an output that
        // was paged earlier keeps its "41-80 / 100" label while the pixel list
        // beneath it shows page-0 entries.
        for (const output of this.outputs) {
          if (Number(output.display_order) !== Number(this.previewOrder)) output.preview_start = 0;
        }
      } catch (error) {
        if (sequence === this.previewSequence) this.previewError = error?.message || "Canonical pixel preview is unavailable.";
      } finally {
        if (sequence === this.previewSequence) this.previewPending = false;
      }
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
        custom_layout: "manual",
        custom_order_error: "",
        preview_start: 0,
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
      output.custom_order_error = "";
    },
    generateCustomOrder(output) {
      const mapping = window.WLEDgerPixelMapping;
      try {
        const order = mapping.generatePermutation(output.custom_layout, output.rows, output.columns);
        if (order === null) throw new Error("Mixed directions require an explicit custom permutation in the text field.");
        if (order.length !== Number(output.pixel_count)) throw new Error("Rows × columns must equal the output pixel count.");
        output.pattern = "custom";
        output.custom_order = order;
        output.custom_order_text = order.join(", ");
        output.custom_order_error = "";
      } catch (error) {
        output.custom_order_error = error?.message || "Could not generate a custom pixel permutation.";
      }
    },
    previewWidth(output) {
      const total = this.totalPixels();
      return total > 0 ? `${Math.max(1, Number(output.pixel_count) || 0) / total * 100}%` : "0%";
    },
    previewPixels(output) {
      const preview = this.serverPreview.find(item => Number(item.display_order) === Number(output.display_order));
      return preview?.pixels || [];
    },
    previewPage(output, delta) {
      const pixelCount = Number(output.pixel_count) || 0;
      const current = Number(output.preview_start) || 0;
      const maxStart = Math.max(0, Math.ceil(pixelCount / 40 - 1) * 40);
      output.preview_start = Math.max(0, Math.min(maxStart, current + delta * 40));
      this.previewOrder = Number(output.display_order);
      this.queueMappingPreview();
    },
    previewPageLabel(output) {
      const count = Number(output.pixel_count) || 0;
      const start = Number(output.preview_start) || 0;
      return count > 0 ? `${start + 1}–${Math.min(start + 40, count)} / ${count}` : "No pixels";
    },
  };
};
