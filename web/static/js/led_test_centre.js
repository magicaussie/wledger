(function (root) {
  'use strict';
  const base = '/hardware/led-test-centre';
  function integer(value, min, max, label) {
    if (value === undefined || value === null || typeof value === 'boolean' || (typeof value === 'string' && !value.trim())) throw new Error(label + ' is required.');
    const n = Number(value);
    if (!Number.isSafeInteger(n) || n < min || n > max) throw new Error(label + ' is outside the permitted range.');
    return n;
  }
  function bounds(controller, output) {
    const capacity = integer(controller?.led_count, 1, Number.MAX_SAFE_INTEGER, 'Controller capacity');
    if (!output) return { start: 0, count: capacity };
    const start = integer(output.start, 0, capacity - 1, 'Output start');
    const count = integer(output.length, 1, capacity - start, 'Output length');
    return { start, count };
  }
  function range(start, count, limits) {
    start = integer(start, limits.start, limits.start + limits.count - 1, 'Start pixel');
    count = integer(count, 1, limits.start + limits.count - start, 'Pixel count');
    return { start, count };
  }
  function pixels(limits, pixel) {
    const start = Math.max(limits.start, Math.min(Number(pixel) - 32, limits.start + limits.count - 96));
    return Array.from({ length: Math.min(96, limits.count) }, (_, i) => start + i);
  }
  function operation(raw) {
    return { id: raw.ID ?? raw.id ?? raw.SessionID ?? raw.session_id, controller: raw.ControllerID ?? raw.controller_id,
      state: raw.State ?? raw.state, error: raw.LastError ?? raw.last_error,
      last: raw.LastOperation ?? raw.last_operation, successful: raw.LastSuccessfulOperation ?? raw.last_successful_operation };
  }
  // Physical pieces already encode custom/reversed wiring. Never infer it from cabinet starts.
  function pixelInfo(data, controllerID, pixel) {
    const assignments = [];
    const bins = [];
    for (const cabinet of data.cabinets || []) {
      for (const drawer of cabinet.drawers || []) {
        const matchingBins = (drawer.bins || []).filter(bin => bin.assigned && Number(bin.controller_id) === Number(controllerID) && Number(bin.pixel) === pixel);
        for (const bin of matchingBins) bins.push({ ...bin, cabinet, drawer });
        for (const range of drawer.ranges || []) {
          for (const piece of range.physical || []) {
            const start = Number(piece.first_pixel);
            if (Number(piece.controller_id) !== Number(controllerID) || pixel < start || pixel >= start + Number(piece.count)) continue;
            assignments.push({ cabinet, drawer, relative: Number(piece.relative_start) + pixel - start, bins: matchingBins });
          }
        }
      }
    }
    const findings = (data.validation || []).filter(v => {
      if (v.severity !== 'error') return false;
      if (v.controller_id && Number(v.controller_id) !== Number(controllerID)) return false;
      if (v.bin_id) return bins.some(b => Number(b.id) === Number(v.bin_id));
      if (v.drawer_id) return assignments.some(a => Number(a.drawer.id) === Number(v.drawer_id));
      if (v.cabinet_id) return assignments.some(a => Number(a.cabinet.id) === Number(v.cabinet_id));
      return Number(v.controller_id) === Number(controllerID);
    });
    const conflict = assignments.length > 1 || bins.length > 1 || findings.length > 0 || bins.some(b => b.stale || !assignments.some(a => a.drawer.id === b.drawer.id && a.relative === Number(b.relative_pixel)));
    return { pixel, assignments, bins, findings, status: conflict ? 'conflict' : assignments.length || bins.length ? 'assigned' : 'unassigned' };
  }
  function proposals(rows) {
    if (!Array.isArray(rows) || !rows.length) throw new Error('Preview at least one mapping first.');
    return rows.map(row => ({ drawer_id: integer(row.drawer_id, 1, Number.MAX_SAFE_INTEGER, 'Drawer'),
      start: integer(row.start, 1, Number.MAX_SAFE_INTEGER, 'Cabinet start'),
      count: integer(row.count, 1, Number.MAX_SAFE_INTEGER - Number(row.start), 'Count') }));
  }
  function ledTestCentre() {
    return {
      tabs: ['controller', 'pixel', 'drawer', 'discovery', 'allocation', 'validation'], tab: 'controller',
      data: { controllers: [], cabinets: [], operations: [], validation: [], physical_output_enabled: false },
      controllerID: '', outputOrder: '', cabinetID: '', drawerID: '', binID: '', inspections: {},
      pixel: 0, count: 1, color: '#ffffff', brightness: 32, duration: 5, mode: 'solid',
      busy: false, error: '', notice: '', sent: '', acknowledged: '', human: false, chosen: '', pixelOperation: '',
      first: null, last: null, discoveryContext: '', preview: [], conflicts: [], mappingConfirmed: false,
      cabinetStart: 1, perDrawer: 1, allocationIDs: [], emergencyOpen: false, emergencyText: '', emergencyResults: [],
      get controller() { return (this.data.controllers || []).find(c => String(c.id) === String(this.controllerID)); },
      get inspection() { return this.inspections[this.controllerID]; },
      get outputs() { return this.inspection?.outputs || []; },
      get output() { return this.outputOrder === '' ? null : this.outputs.find(o => String(o.order) === String(this.outputOrder)); },
      get limits() { try { return bounds(this.controller, this.output); } catch (_) { return { start: 0, count: 0 }; } },
      get windowPixels() { return pixels(this.limits, Number.isSafeInteger(Number(this.pixel)) ? Number(this.pixel) : this.limits.start); },
      get pixelWindow() { return this.windowPixels.map(p => pixelInfo(this.data, this.controllerID, p)); },
      get selectedPixel() { return pixelInfo(this.data, this.controllerID, Number(this.pixel)); },
      applyStyle(name) {
        const style = this.data.styles?.[name];
        if (style) { this.color = style.color; this.brightness = style.brightness; }
      },
      get cabinet() { return (this.data.cabinets || []).find(c => String(c.id) === String(this.cabinetID)); },
      get drawers() { return this.cabinet?.drawers || []; },
      get drawer() { return this.drawers.find(d => String(d.id) === String(this.drawerID)); },
      get operations() { return (this.data.operations || []).map(operation); },
      get context() { return JSON.stringify([this.controllerID, this.outputOrder, this.cabinetID, this.drawerID]); },
      async init() { await this.refresh(); this.applyStyle('test_pixel'); },
      async request(path, body) {
        const options = { credentials: 'same-origin', headers: { Accept: 'application/json' } };
        if (body !== undefined) {
          const token = document.querySelector('meta[name="csrf-token"]')?.content;
          if (!token) throw new Error('Security token missing. Reload this page.');
          Object.assign(options, { method: 'POST', body: JSON.stringify(body) });
          Object.assign(options.headers, { 'Content-Type': 'application/json', 'X-CSRF-Token': token });
        }
        const response = await fetch(base + path, options);
        const value = await response.json();
        if (!response.ok || value.error) {
                  const error = new Error(value.error || 'Request failed (' + response.status + ').');
                  error.body = value; error.status = response.status;
                  throw error;
                }
        return value;
      },
      async act(fn) {
        if (this.busy) return;
        this.busy = true; this.error = ''; this.notice = '';
        try { await fn(); } catch (e) { this.error = e.message; } finally { this.busy = false; }
      },
      async refresh() {
        await this.act(async () => { this.data = await this.request('/data'); this.emergencyResults = this.data.emergency_results || []; this.invalidate(); });
      },
      invalidate() { this.human = false; this.preview = []; this.conflicts = []; this.mappingConfirmed = false; this.first = null; this.last = null; this.binID = ''; },
      controllerChanged() { this.outputOrder = ''; this.invalidate(); this.pixel = 0; },
      outputChanged() { this.invalidate(); this.pixel = this.limits.start; },
      async inspect() {
        await this.act(async () => {
          const id = integer(this.controllerID, 1, Number.MAX_SAFE_INTEGER, 'Controller');
          this.inspections[id] = await this.request('/inspect', { controller_id: id });
          this.outputOrder = ''; this.invalidate(); this.pixel = 0;
          this.notice = 'Read-only inspection complete. No physical behaviour has been verified.';
        });
      },
      move(delta) { this.pixel = Math.max(this.limits.start, Math.min(this.limits.start + this.limits.count - 1, Number(this.pixel) + delta)); },
      async testPixel(delta = 0, mode = 'solid', color = null) {
              if (this.busy) return;
              if (delta) this.move(delta);
              if (this.pixelOperation) {
                await this.stop(this.pixelOperation);
                if (this.error) return;
                this.pixelOperation = '';
              }
              await this.start('identify_pixel', false, color, { count: 1, mode });
            },
            async start(kind = 'test_pixel', all = false, overrideColor = null, overrides = {}) {
        await this.act(async () => {
          if (!this.data.physical_output_enabled) throw new Error('Physical output is disabled.');
          const isDrawer = kind.startsWith('drawer_') || kind === 'locate_drawer';
          const body = { color: overrideColor || this.color, brightness: integer(this.brightness, 0, 255, 'Brightness'),
            duration_seconds: integer(this.duration, 1, 3600, 'Duration'), mode: overrides.mode || this.mode, function: kind };
          if (kind === 'locate_cabinet') {
            Object.assign(body, {controller_id: 0, output_order: null, cabinet_id: integer(this.cabinetID, 1, Number.MAX_SAFE_INTEGER, 'Cabinet')});
          } else if (isDrawer) {
            // Drawer topology, including all its controllers, is resolved by the server.
            Object.assign(body, { controller_id: 0, output_order: null,
              drawer_id: integer(this.drawerID, 1, Number.MAX_SAFE_INTEGER, 'Drawer') });
            if (kind === 'drawer_bin') {
              const bin = this.drawer?.bins?.find(b => Number(b.id) === Number(this.binID));
              if (!bin?.assigned || bin.stale) throw new Error('Select a bin with a current explicit pixel mapping.');
              body.bin_id = integer(this.binID, 1, Number.MAX_SAFE_INTEGER, 'Bin');
            }
          } else {
            body.controller_id = integer(this.controllerID, 1, Number.MAX_SAFE_INTEGER, 'Controller');
            if (this.outputOrder !== '' && !this.output) throw new Error('Select an inspected output.');
            body.output_order = this.output ? Number(this.output.order) : null;
            Object.assign(body, range(all ? this.limits.start : this.pixel,
              all ? this.limits.count : overrides.count ?? (kind === 'identify_pixel' ? 1 : this.count), this.limits));
          }
          if (!/^#[0-9a-f]{6}$/i.test(body.color)) throw new Error('Choose a valid RGB colour.');
          if (!['solid', 'flash', 'sequence'].includes(body.mode)) throw new Error('Invalid mode.');
          this.sent = 'Command requested; acknowledgement pending. This is not proof of delivery or physical behaviour; a failed request may still have reached a controller.';
          this.acknowledged = ''; this.human = false;
          const result = await this.request('/start', body);
          if (result.operation) {
            const op = operation(result.operation); this.chosen = op.id;
                        if (kind === 'identify_pixel') this.pixelOperation = op.id;
            this.data.operations = [result.operation, ...(this.data.operations || []).filter(o => operation(o).id !== op.id)];
          }
          this.acknowledged = result.status || 'Server responded; inspect operation status. Physical behaviour not verified.';
        });
      },
      async stop(id) {
        await this.act(async () => {
          if (!id) throw new Error('Select an operation to stop.');
          const result = await this.request('/stop', { operation_id: id });
          this.notice = result.status || 'Stop response received. Refresh operations to check recovery state.';
          this.data = await this.request('/data');
        });
      },
      async emergency() {
        await this.act(async () => {
          if (this.emergencyText !== 'EMERGENCY OFF') throw new Error('Type EMERGENCY OFF exactly.');
          let result;
          try { result = await this.request('/emergency-off', { confirm: this.emergencyText }); }
          catch (error) {
            if (Array.isArray(error.body?.results)) this.emergencyResults = error.body.results;
            throw error;
          }
          this.emergencyResults = result.results || []; this.emergencyOpen = false; this.emergencyText = '';
          this.notice = 'Emergency-off results below are acknowledgements, not physical verification.';
          this.data = await this.request('/data');
        });
      },
      mark(which) { if (this.busy) return; this[which] = Number(this.pixel); this.discoveryContext = this.context; this.preview = []; this.mappingConfirmed = false; },
      async mappingPreview(discovery) {
        await this.act(async () => {
          let body;
          if (discovery) {
            if (!this.controller || this.discoveryContext !== this.context || this.first === null || this.last === null) throw new Error('Mark both endpoints for the current selection.');
            range(this.first, this.last - this.first + 1, this.limits);
            body = { cabinet_id: integer(this.cabinetID, 1, Number.MAX_SAFE_INTEGER, 'Cabinet'), controller_id: Number(this.controllerID),
              output_order: this.output ? Number(this.output.order) : null, first_pixel: this.first, last_pixel: this.last,
              drawer_ids: [integer(this.drawerID, 1, Number.MAX_SAFE_INTEGER, 'Drawer')] };
          } else {
            if (!this.allocationIDs.length) throw new Error('Select drawers in allocation order.');
            body = { cabinet_id: integer(this.cabinetID, 1, Number.MAX_SAFE_INTEGER, 'Cabinet'),
              drawer_ids: this.allocationIDs.map(id => integer(id, 1, Number.MAX_SAFE_INTEGER, 'Drawer')),
              start: integer(this.cabinetStart, 1, Number.MAX_SAFE_INTEGER, 'Cabinet start'),
              count_per_drawer: integer(this.perDrawer, 1, Number.MAX_SAFE_INTEGER, 'Count per drawer') };
          }
          this.preview = []; this.mappingConfirmed = false;
          const result = await this.request('/mapping-preview', body);
          this.preview = proposals(result.proposals); this.conflicts = result.conflicts || [];
        });
      },
      async saveMapping() {
        await this.act(async () => {
          if (!this.mappingConfirmed || this.conflicts.length) throw new Error('Resolve conflicts and explicitly confirm append.');
          const rows = proposals(this.preview);
          if (rows.some(r => !this.drawers.some(d => Number(d.id) === r.drawer_id))) throw new Error('Proposal drawer is not in the selected cabinet.');
          await this.request('/mapping-save', { cabinet_id: integer(this.cabinetID, 1, Number.MAX_SAFE_INTEGER, 'Cabinet'), proposals: rows, confirm: true });
          this.preview = []; this.mappingConfirmed = false; this.data = await this.request('/data'); this.notice = 'Mapping append saved.';
        });
      },
      async clearBin() {
        await this.act(async () => {
          if (!this.drawer?.bins?.some(b => Number(b.id) === Number(this.binID))) throw new Error('Select a bin in this drawer.');
          if (!root.confirm('Clear only this bin’s explicit pixel assignment? Locate will return to the existing whole-drawer fallback.')) return;
          await this.request('/bin-pixel', {bin_id: Number(this.binID), drawer_id: Number(this.drawerID), clear: true, confirm: true});
          this.data = await this.request('/data'); this.notice = 'Selected bin override cleared. Existing drawer ranges were preserved.';
        });
      },
      async saveBin() {
        await this.act(async () => {
          if (!this.drawer?.bins?.some(b => Number(b.id) === Number(this.binID))) throw new Error('Select a bin in this drawer.');
          range(this.pixel, 1, this.limits);
          if (!root.confirm('Save this controller pixel as the selected bin’s relative mapping?')) return;
          await this.request('/bin-pixel', { bin_id: Number(this.binID), drawer_id: Number(this.drawerID), controller_id: Number(this.controllerID), pixel: Number(this.pixel), confirm: true });
          this.data = await this.request('/data');
                    this.notice = 'Bin pixel mapping saved.';
        });
      }
    };
  }
  const api = { integer, bounds, range, pixels, operation, pixelInfo, proposals, ledTestCentre };
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
  root.ledTestCentre = ledTestCentre;
})(typeof window !== 'undefined' ? window : globalThis);
