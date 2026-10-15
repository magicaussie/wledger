function physicalLayoutEditor() {
  return {
    state: {},
    lastStateJSON: '',
    bins: [],
    controller: null,
    controllers: [],
    assignedControllers: [],
    controllerError: '',
    controllerDraft: [],
    capacityDrafts: {},
    wiringStrings: [],
    configuredControllerIDs: [],
    wiringSaveError: '',
    wiringSaveNotice: '',
    wiringMappingImpactConfirmed: false,
    controllerMappingImpactConfirmed: false,
    diagnosticControllerID: 0,
    selectedStringKey: '',
    activePathSectionKey: '',
    physicalOutputLayout: null,
    outputDiscoveryError: '',
    controllerSaveNotice: '',
    controllerSaveError: '',
    ledRanges: {},
    persistedLedRanges: {},
    canonicalAllocationMappings: {},
    canonicalMappingRequests: {},
    electricalRevision: 0,
    allowElectricalNavigation: false,
    capacitySaveNotice: '',
    capacitySaveError: '',
    ledSaveNotice: '',
    ledSaveError: '',
    legacyLedDraftNotice: '',
    mode: 'cabinet',
    tool: 'select',
    selection: { kind: '', key: '' },
    activeDrawerKey: '',
    selectedAvailableBin: '',
    newBinDraft: { name: '', x: 1, y: 1, width: 5, height: 5 },
    moveTargetDrawerID: '',
    moveBinX: 0,
    moveBinY: 0,
    pointerOp: null,
    gridSize: 5,
    snap: true,
    showGrid: true,
    zoom: 1,
    pan: { x: 0, y: 0 },
    handles: ['nw', 'ne', 'sw', 'se'],
    dirty: false,
    saving: false,
    error: '',
    undoStack: [],
    redoStack: [],
    propertySnapshot: null,
    draftKey: '',

    init() {
      this.error = new URLSearchParams(window.location.search).get('layout_error') || '';
      const data = JSON.parse(document.getElementById('layout-editor-data').textContent);
      const cabinet = data.Cabinet;
      this.controller = data.Controller || null;
      this.controllers = data.Controllers || [];
      this.assignedControllers = data.AssignedControllers || [];
      this.controllerError = data.ControllerError || '';
      this.assignedControllers.sort((a, b) => Number(a.Slot) - Number(b.Slot));
      this.controllerDraft = this.assignedControllers.map(item => Number(item.Controller.id));
      this.configuredControllerIDs = this.assignedControllers.filter(item => Number(item.StringsConfigured) === 1).map(item => Number(item.Controller.id));
      this.capacityDrafts = Object.fromEntries(this.controllers.map(controller => [controller.id, String(controller.led_count)]));
      this.diagnosticControllerID = this.controllerDraft[0] || 0;
      const wiringData = data.Wiring || { Controllers: [], Total: 0 };
      this.wiringStrings = (wiringData.Controllers || []).flatMap(controller => (controller.Strings || []).map(item => {
        const record = item.Record;
        return {
          id: Number(record.id), key: `string-${record.id}`, controller_id: Number(record.controller_id),
          name: record.name, display_order: Number(record.display_order), pixel_count: Number(record.pixel_count),
          direction: item.Direction || "forward", pattern: item.Pattern || "linear",
          rows: Number(item.Rows) || 1, columns: Number(item.Columns) || 1,
          custom_order: Array.isArray(item.CustomOrder) ? item.CustomOrder.map(Number) : [],
          layout_complete: Number(record.layout_complete) === 1,
          sections: (item.Sections || []).map(section => {
            let points = [];
            try { points = JSON.parse(section.points_json || '[]'); } catch { points = []; }
            return { id: Number(section.id), key: `section-${section.id}`, display_order: Number(section.display_order), pixel_count: String(section.pixel_count), points: Array.isArray(points) ? points : [] };
          }),
        };
      }));
      this.persistedWiringJSON = JSON.stringify(this.wiringStrings);
      this.electricalRevision = cabinet.electrical_revision;
      const query = new URLSearchParams(window.location.search);
      this.controllerSaveError = query.get('controller_error') || '';
      this.controllerSaveNotice = query.get('controller_saved') === '1' ? 'Controller assignment saved.' : '';
      this.capacitySaveError = query.get('capacity_error') || '';
      this.capacitySaveNotice = query.get('capacity_saved') === '1' ? 'Controller-wide pixel capacity saved.' : '';
      this.ledSaveError = query.get('led_error') || '';
      this.ledSaveNotice = query.get('led_saved') === '1' ? 'Cabinet-wide drawer mapping saved.' : '';
      this.wiringSaveError = query.get('wiring_error') || '';
      this.wiringSaveNotice = query.get('wiring_saved') === '1' ? 'Controller strings and wiring paths saved.' : '';
      const oldLedDraftKey = `wledger-led-allocation-draft-cab-${cabinet.id}`;
      try {
        if (localStorage.getItem(oldLedDraftKey) !== null) this.legacyLedDraftNotice = 'A legacy browser-local LED draft exists but was not imported. Database allocations are authoritative; the old draft has been left untouched.';
      } catch { this.legacyLedDraftNotice = 'Browser storage is unavailable; saved database allocations remain authoritative.'; }
      let decorations = [];
      try { decorations = JSON.parse(cabinet.layout_decorations_json || '[]'); } catch { decorations = []; }
      this.state = {
        id: cabinet.id,
        revision: cabinet.layout_revision,
        name: cabinet.name,
        coordinate_system: cabinet.coordinate_system,
        display_order: cabinet.display_order,
        width: cabinet.layout_width,
        height: cabinet.layout_height,
        decorations: Array.isArray(decorations) ? decorations : [],
        uiSelectionKind: '',
        uiSelectionKey: '',
        drawers: (data.Drawers || []).map(item => ({
          id: item.Drawer.id,
          key: `drawer-${item.Drawer.id}`,
          label: item.Drawer.label,
          archived: Number(item.Drawer.archived) !== 0,
          display_order: item.Drawer.display_order,
          x: item.Drawer.x,
          y: item.Drawer.y,
          width: item.Drawer.width,
          height: item.Drawer.height,
          bins: (item.Bins || []).map(bin => ({
            id: bin.bin_id,
            key: `bin-${bin.bin_id}`,
            name: bin.name,
            display_order: bin.display_order,
            x: bin.x,
            y: bin.y,
            width: bin.width,
            height: bin.height,
          })),
        })),
      };
      this.ledRanges = {};
      for (const item of data.Drawers || []) {
        const key = `drawer-${item.Drawer.id}`;
        this.ledRanges[key] = (item.LedAllocations || []).map(range => ({ id: range.id, start_pixel: String(range.start_pixel), pixel_count: String(range.pixel_count) }));
        if (!this.ledRanges[key].length) this.ledRanges[key].push({ id: 0, start_pixel: '', pixel_count: '' });
      }
      this.persistedLedRanges = this.clone(this.ledRanges);
      this.bins = (data.UnattachedBins || []).map(bin => ({ id: bin.id, name: bin.name }));
      this.draftKey = `wledger-layout-draft-${cabinet.id}`;
      if (this.error) this.restoreDraft();
      if (this.state.uiSelectionKind === 'drawer') {
        const selectedDrawer = this.state.drawers.find(drawer => drawer.key === this.state.uiSelectionKey);
        if (selectedDrawer) this.activeDrawerKey = selectedDrawer.key;
      } else if (this.state.uiSelectionKind === 'bin') {
        const selectedBinDrawer = this.state.drawers.find(drawer => drawer.bins.some(bin => bin.key === this.state.uiSelectionKey));
        if (selectedBinDrawer) {
          this.activeDrawerKey = selectedBinDrawer.key;
          this.mode = 'bins';
        }
      }
      const requestedDrawerID = Number(query.get('drawer_id')) || 0;
      const requestedBinID = Number(query.get('bin_id')) || 0;
      const requestedDrawer = this.state.drawers.find(drawer => Number(drawer.id) === requestedDrawerID);
      if (requestedDrawer) {
        this.activeDrawerKey = requestedDrawer.key;
        this.mode = 'bins';
        if (requestedBinID) {
          const requestedBin = requestedDrawer.bins.find(bin => Number(bin.id) === requestedBinID);
          if (requestedBin) {
            this.state.uiSelectionKind = 'bin';
            this.state.uiSelectionKey = requestedBin.key;
            this.moveBinX = Number(requestedBin.x) || 0;
            this.moveBinY = Number(requestedBin.y) || 0;
          }
        }
      }
      this.selection = { kind: this.state.uiSelectionKind || '', key: this.state.uiSelectionKey || '' };
      this.lastStateJSON = this.layoutFingerprint();
      this.pushHistoryBase();
      const initiallySelectedDrawer = this.selectedDrawer();
      if (initiallySelectedDrawer) this.loadCanonicalDrawerMapping(initiallySelectedDrawer);
      this.$watch('state', () => {
        const current = this.layoutFingerprint();
        if (!this.saving && current !== this.lastStateJSON) {
          this.dirty = true;
          this.persistDraft();
        }
        this.lastStateJSON = current;
        this.renderCanvas();
      }, { deep: true });
      this.$watch('wiringStrings', () => { this.wiringMappingImpactConfirmed = false; this.renderCanvas(); }, { deep: true });
      this.$watch('configuredControllerIDs', () => { this.wiringMappingImpactConfirmed = false; }, { deep: true });
      this.$watch('controllerDraft', () => { this.controllerMappingImpactConfirmed = false; this.renderCanvas(); }, { deep: true });
      window.addEventListener('beforeunload', event => {
        if ((this.dirty || this.hasUnsavedElectricalChanges()) && !this.saving && !this.allowElectricalNavigation) { event.preventDefault(); event.returnValue = ''; }
      });
      this.$nextTick(() => { this.renderCanvas(); this.$refs.canvas?.focus(); });
    },

    clone(value) { return JSON.parse(JSON.stringify(value)); },
        layoutFingerprint() { const value=this.clone(this.state); delete value.uiSelectionKind; delete value.uiSelectionKey; return JSON.stringify(value); },
        setSelection(kind,key) { this.selection={kind,key}; this.state.uiSelectionKind=kind; this.state.uiSelectionKey=key; this.renderCanvas(); },
    pushHistoryBase() { this.undoStack = []; this.redoStack = []; },
    checkpoint() {
      this.undoStack.push(this.clone(this.state));
      if (this.undoStack.length > 80) this.undoStack.shift();
      this.redoStack = [];
    },
    undo() {
      if (!this.undoStack.length) return;
      this.redoStack.push(this.clone(this.state));
      this.state = this.undoStack.pop();
      this.setSelection(this.state.uiSelectionKind || '', this.state.uiSelectionKey || '');
      this.markDirty();
    },
    redo() {
      if (!this.redoStack.length) return;
      this.undoStack.push(this.clone(this.state));
      this.state = this.redoStack.pop();
      this.setSelection(this.state.uiSelectionKind || '', this.state.uiSelectionKey || '');
      this.markDirty();
    },
    beginPropertyEdit() { this.propertySnapshot = this.clone(this.state); },
    finishPropertyEdit() {
      if (this.propertySnapshot && JSON.stringify(this.propertySnapshot) !== JSON.stringify(this.state)) {
        this.undoStack.push(this.propertySnapshot);
        this.redoStack = [];
        this.markDirty();
      }
      this.propertySnapshot = null;
    },
    markDirty() { this.dirty = true; this.persistDraft(); },
    persistDraft() {
      if (!this.draftKey || !this.state.id) return;
      try { localStorage.setItem(this.draftKey, JSON.stringify(this.state)); } catch { /* storage may be disabled */ }
    },
    restoreDraft() {
      try {
        const draft = JSON.parse(localStorage.getItem(this.draftKey) || 'null');
        if (draft && draft.id === this.state.id && draft.revision === this.state.revision) {
          this.state = draft;
          this.dirty = true;
          this.error = new URLSearchParams(window.location.search).get('layout_error') || this.error;
        } else if (draft) localStorage.removeItem(this.draftKey);
      } catch { localStorage.removeItem(this.draftKey); }
    },
    cleanPayload() {
      const payload = this.clone(this.state);
      delete payload.uiSelectionKind; delete payload.uiSelectionKey;
      payload.drawers.forEach(drawer => {
        delete drawer.key;
        drawer.bins.forEach(bin => { delete bin.key; });
      });
      return payload;
    },
    prepare(event) {
      this.error = '';
      if (this.hasUnsavedElectricalChanges()) {
        event.preventDefault();
        this.error = 'Save or cancel controller and LED allocation changes before saving the physical layout.';
        return;
      }
      const payload = this.cleanPayload();
      if (!payload.name.trim() || !Number.isFinite(payload.width) || !Number.isFinite(payload.height) || payload.width <= 0 || payload.height <= 0) {
        event.preventDefault();
        this.error = 'Enter a cabinet name and positive canvas dimensions.';
        return;
      }
      this.persistDraft();
      this.saving = true;
      this.$refs.layout.value = JSON.stringify(payload);
      this.dirty = false;
    },
    setTool(tool) { this.tool = tool; this.pointerOp = null; },
    canvasWidth() { return this.mode === 'bins' ? (this.activeDrawer()?.width || 1) : this.state.width; },
    canvasHeight() { return this.mode === 'bins' ? (this.activeDrawer()?.height || 1) : this.state.height; },
    canvasViewBox() {
      const width = Math.max(1, this.canvasWidth() / this.zoom);
      const height = Math.max(1, this.canvasHeight() / this.zoom);
      const x = (this.canvasWidth() - width) / 2 + this.pan.x;
      const y = (this.canvasHeight() - height) / 2 + this.pan.y;
      return `${x} ${y} ${width} ${height}`;
    },
    canvasPoint(event) {
      const svg = this.$refs.canvas;
      const point = svg.createSVGPoint();
      point.x = event.clientX;
      point.y = event.clientY;
      const local = point.matrixTransform(svg.getScreenCTM().inverse());
      return { x: local.x, y: local.y };
    },
    snapValue(value) { return this.snap ? Math.round(value / this.gridSize) * this.gridSize : value; },
    pointForEdit(event) { const point = this.canvasPoint(event); return { x: this.snapValue(point.x), y: this.snapValue(point.y) }; },
    canvasPointerDown(event) {

      if (event.button !== 0) return;
      if (this.tool === 'select' && event.target.closest('[data-layout-entity]')) return;
      this.$refs.canvas.setPointerCapture(event.pointerId);
      const point = this.pointForEdit(event);
      if (this.mode === 'wiring' && this.tool === 'path-point') { this.addPathPoint(event); return; }
      if (this.tool === 'pan') {
        this.pointerOp = { kind: 'pan', screenX: event.clientX, screenY: event.clientY, panX: this.pan.x, panY: this.pan.y };
        return;
      }
      if (this.tool === 'select') { this.setSelection('', ''); return; }
      if (this.tool === 'label') {
        if (this.mode !== 'cabinet') return;
        this.checkpoint();
        const label = { key: crypto.randomUUID(), kind: 'label', x: point.x, y: point.y, width: 0, height: 0, text: 'Label' };
        this.state.decorations.push(label);
        this.setSelection('decoration', label.key);
        this.markDirty();
        return;
      }
      if (this.tool === 'bin' && (!this.selectedAvailableBin || !this.availableBins().some(bin => String(bin.id) === String(this.selectedAvailableBin)))) {
        this.error = 'Choose an existing unattached inventory bin before drawing a placement.';
        return;
      }
      this.error = '';
      this.pointerOp = { kind: 'draw', tool: this.tool, start: point, current: point };
            this.renderCanvas();
    },
    canvasPointerMove(event) {
      if (!this.pointerOp) return;
      if (this.pointerOp.kind === 'pan') {
        const rect = this.$refs.canvas.getBoundingClientRect();
        const box = this.canvasViewBox().split(' ').map(Number);
        this.pan.x = this.pointerOp.panX - (event.clientX - this.pointerOp.screenX) * box[2] / rect.width;
        this.pan.y = this.pointerOp.panY - (event.clientY - this.pointerOp.screenY) * box[3] / rect.height;
        this.updateViewBox();
        return;
      }
      const point = this.pointForEdit(event);
      if (this.pointerOp.kind === 'draw') { this.pointerOp.current = point; this.renderCanvas(); return; }
      const op = this.pointerOp;
      if (op.kind === 'move') {
        const object = this.entity(op.entityKind, op.key);
        if (!object) return;
        const rect = this.boundsFor(op.entityKind);
        object.x = Math.max(0, Math.min(rect.width - object.width, op.originX + point.x - op.startX));
        object.y = Math.max(0, Math.min(rect.height - object.height, op.originY + point.y - op.startY));
      } else if (op.kind === 'resize') {
        const object = this.entity(op.entityKind, op.key);
        if (!object) return;
        this.resizeObject(object, op, point, this.boundsFor(op.entityKind));
      }
      this.markDirty();
    },
    canvasPointerUp(event) {

      if (!this.pointerOp) return;
      const op = this.pointerOp;
      if (op.kind === 'draw') this.completeDrawing(op);
      this.pointerOp = null;
      this.renderCanvas();
      try { this.$refs.canvas.releasePointerCapture(event.pointerId); } catch { /* pointer already released */ }
    },
    cancelPointer() { this.pointerOp = null; this.renderCanvas(); },
    canvasClick() { if (this.tool === 'select' && !this.pointerOp) this.setSelection('', ''); },
        svgElement(tag, attributes = {}) {
          const element = document.createElementNS('http://www.w3.org/2000/svg', tag);
          for (const [name, value] of Object.entries(attributes)) if (value !== null && value !== undefined) element.setAttribute(name, String(value));
          return element;
        },
        renderCanvas() {
          if (!this.$refs.decorLayer || !this.$refs.drawerLayer || !this.$refs.binLayer || !this.$refs.previewLayer) return;
          this.updateViewBox();
          const decorLayer = this.$refs.decorLayer;
          const drawerLayer = this.$refs.drawerLayer;
          const binLayer = this.$refs.binLayer;
          const previewLayer = this.$refs.previewLayer;
          decorLayer.replaceChildren(); drawerLayer.replaceChildren(); binLayer.replaceChildren(); previewLayer.replaceChildren();
          const addHandles = (group, item, kind, size) => {
            if (this.selection.kind !== kind || this.selection.key !== item.key) return;
            const outline = this.svgElement('rect', { x:item.x, y:item.y, width:item.width, height:item.height, fill:'none', stroke:'var(--color-primary)', 'stroke-width':0.35 }); group.appendChild(outline);
            for (const handle of this.handles) {
              const node = this.svgElement('rect', { x:this.handleX(item,handle)-size/2, y:this.handleY(item,handle)-size/2, width:size, height:size, fill:'var(--color-base-100)', stroke:'var(--color-primary)', 'stroke-width':0.3, tabindex:0, role:'button', 'aria-label':`Resize ${kind} ${handle}` });
              node.addEventListener('pointerdown', event => { event.stopPropagation(); this.beginResize(event, kind, item, handle); });
              node.addEventListener('keydown', event => { if (event.key === 'Enter') { event.preventDefault(); this.keyboardResize(item,handle,1); } });
              group.appendChild(node);
            }
          };
          if (this.mode === 'cabinet' || this.mode === 'wiring') {
            this.renderWiringPaths(decorLayer);
            for (const item of this.state.decorations) {
              const group = this.svgElement('g', { 'data-layout-entity':'true', role:'button', tabindex:0, 'aria-label':`${item.kind} drawing` });
              group.addEventListener('pointerdown', event => this.beginMove(event,'decoration',item));
              group.addEventListener('click', event => { event.stopPropagation(); this.selectDecoration(item); });
              if (item.kind === 'line') group.appendChild(this.svgElement('line',{x1:item.x,y1:item.y,x2:item.x+item.width,y2:item.y+item.height,stroke:'var(--color-secondary)','stroke-width':0.6}));
              else if (item.kind === 'label') { const text=this.svgElement('text',{x:item.x,y:item.y+1.5,'font-size':2,fill:'var(--color-base-content)'});text.textContent=item.text||'Label';group.appendChild(text); }
              else group.appendChild(this.svgElement('rect',{x:item.x,y:item.y,width:item.width,height:item.height,fill:item.kind==='outline'?'none':'var(--color-accent)','fill-opacity':item.kind==='outline'?0:0.12,stroke:'var(--color-secondary)','stroke-width':0.35,'stroke-dasharray':item.kind==='outline'?'1 0.5':null}));
              addHandles(group,item,'decoration',0.9); decorLayer.appendChild(group);
            }
            for (const drawer of this.state.drawers) {
              const group = this.svgElement('g',{ 'data-layout-entity':'true',role:'button',tabindex:0,'aria-label':`Select drawer ${drawer.label}` });
              group.addEventListener('pointerdown',event=>this.beginMove(event,'drawer',drawer));
              group.addEventListener('click',event=>{event.stopPropagation();this.selectDrawer(drawer);});
              group.addEventListener('dblclick',event=>{event.stopPropagation();if(this.mode==='cabinet')this.openDrawer(drawer);});
              group.addEventListener('keydown',event=>{if(event.key==='Enter'||event.key===' '){event.preventDefault();this.selectDrawer(drawer);}});
              const selected=this.selection.kind==='drawer'&&this.selection.key===drawer.key;
              group.appendChild(this.svgElement('rect',{x:drawer.x,y:drawer.y,width:drawer.width,height:drawer.height,rx:0.5,fill:selected?'var(--color-primary)':'var(--color-info)','fill-opacity':0.22,stroke:selected?'var(--color-primary)':'var(--color-info)','stroke-width':0.45}));
              const text=this.svgElement('text',{x:drawer.x+0.8,y:drawer.y+2.5,'font-size':1.8,fill:'var(--color-base-content)'});text.textContent=drawer.label;group.appendChild(text);
              addHandles(group,drawer,'drawer',1.3);drawerLayer.appendChild(group);
            }
          } else {
            const drawer=this.activeDrawer();
            if(drawer) for(const bin of drawer.bins){
              const group=this.svgElement('g',{'data-layout-entity':'true',role:'button',tabindex:0,'aria-label':`Select bin ${bin.name||bin.id}`});
              group.addEventListener('pointerdown',event=>this.beginMove(event,'bin',bin));
              group.addEventListener('click',event=>{event.stopPropagation();this.selectBin(bin);});
              group.addEventListener('keydown',event=>{if(event.key==='Enter'||event.key===' '){event.preventDefault();this.selectBin(bin);}});
              const selected=this.selection.kind==='bin'&&this.selection.key===bin.key;
              group.appendChild(this.svgElement('rect',{x:bin.x,y:bin.y,width:bin.width,height:bin.height,rx:0.3,fill:selected?'var(--color-primary)':'var(--color-success)','fill-opacity':0.25,stroke:'var(--color-success)','stroke-width':0.35}));
              const text=this.svgElement('text',{x:bin.x+0.5,y:bin.y+1.8,'font-size':1.4,fill:'var(--color-base-content)'});text.textContent=bin.name||`Bin #${bin.id}`;group.appendChild(text);
              addHandles(group,bin,'bin',1.1);binLayer.appendChild(group);
            }
          }
          if(this.pointerOp?.kind==='draw'){
            const r=this.previewRect();previewLayer.appendChild(this.svgElement('rect',{x:r.x,y:r.y,width:r.width,height:r.height,fill:'var(--color-primary)','fill-opacity':0.12,stroke:'var(--color-primary)','stroke-width':0.35,'stroke-dasharray':'1 0.5'}));
          }
        },
    selectWiringString(string) { if (!string) return; this.selectedStringKey = string.key; this.setSelection('', ''); this.renderCanvas(); },
    renderWiringPaths(layer) {
      const spans = this.controllerSpans();
      const colors = ['var(--color-primary)', 'var(--color-secondary)', 'var(--color-accent)', 'var(--color-info)', 'var(--color-success)'];
      const selectedDrawer = this.selection.kind === 'drawer' ? this.selectedDrawer() : null;
      for (let controllerIndex = 0; controllerIndex < spans.length; controllerIndex++) {
        const span = spans[controllerIndex];
        const strings = this.stringRows(span.controller_id);
        let stringOffset = 0;
        for (let stringIndex = 0; stringIndex < strings.length; stringIndex++) {
          const string = strings[stringIndex];
          let sectionOffset = 0;
          for (const section of [...string.sections].sort((a, b) => Number(a.display_order) - Number(b.display_order))) {
            const count = Number(section.pixel_count) || 0;
            const physicalPixelStart = sectionOffset + 1;
            const physicalPixelEnd = physicalPixelStart + count - 1;
            const points = section.points || [];
            if (points.length >= 2) {
              const group = this.svgElement('g', { 'data-wiring-string': string.key, role: 'button', tabindex: 0, 'aria-label': `${string.name}, section ${Number(section.display_order) + 1}, physical output pixels ${physicalPixelStart}–${physicalPixelEnd}` });
              group.addEventListener('click', event => { event.stopPropagation(); this.selectWiringString(string); });
              const polyline = values => values.map(point => `${point.x},${point.y}`).join(' ');
              const selected = this.selectedStringKey === string.key;
              group.appendChild(this.svgElement('polyline', { points: polyline(points), fill: 'none', stroke: colors[controllerIndex % colors.length], 'stroke-width': selected ? 0.9 : 0.55, 'stroke-dasharray': string.layout_complete ? null : '1 0.6', 'stroke-linejoin': 'round', 'stroke-linecap': 'round' }));
              const first = points[0], last = points[points.length - 1];
              const label = this.svgElement('text', { x: first.x + 0.4, y: first.y - 0.5, 'font-size': 1.25, fill: colors[controllerIndex % colors.length] });
              label.textContent = `${controllerIndex + 1}.${stringIndex + 1} output ${physicalPixelStart}–${physicalPixelEnd}`;
              group.appendChild(label);
              const arrow = this.svgElement('text', { x: last.x + 0.3, y: last.y + 1.3, 'font-size': 2, fill: colors[controllerIndex % colors.length] });
              arrow.textContent = '›'; group.appendChild(arrow);
              if (selectedDrawer) for (const range of this.ledRangesFor(selectedDrawer)) {
                const pieces = this.splitAllocationRange(range, selectedDrawer).filter(piece => piece.controller_id === span.controller_id && String(piece.string_id) === String(string.id || string.key));
                for (const piece of pieces) {
                  const left = Math.max(physicalPixelStart, piece.string_start), right = Math.min(physicalPixelEnd, piece.string_start + piece.count - 1);
                  if (left > right) continue;
                  const sliced = this.sliceWiringPolyline(points, (left - physicalPixelStart) / count, (right - physicalPixelStart + 1) / count);
                  if (sliced.length >= 2) group.appendChild(this.svgElement('polyline', { points: polyline(sliced), fill: 'none', stroke: 'var(--color-primary)', 'stroke-width': 1.35, 'stroke-linecap': 'round', 'stroke-linejoin': 'round', 'data-drawer-highlight': selectedDrawer.key }));
                }
              }
              layer.appendChild(group);
            }
            sectionOffset += count;
          }
          stringOffset += Number(string.pixel_count) || 0;
        }
      }
      const draftSection = this.currentPathSection();
      if (this.mode === 'wiring' && this.tool === 'path-point' && draftSection?.points?.length) {
        const pts = draftSection.points.map(point => `${point.x},${point.y}`).join(' ');
        layer.appendChild(this.svgElement('polyline', { points: pts, fill: 'none', stroke: 'var(--color-warning)', 'stroke-width': 1, 'stroke-dasharray': '1 0.5', 'stroke-linecap': 'round' }));
        for (const point of draftSection.points) layer.appendChild(this.svgElement('circle', { cx: point.x, cy: point.y, r: 0.7, fill: 'var(--color-warning)' }));
      }
    },
    sliceWiringPolyline(points, from, to) {
      const lengths = [];
      let total = 0;
      for (let i = 1; i < points.length; i++) {
        const dx = points[i].x - points[i - 1].x, dy = points[i].y - points[i - 1].y;
        const length = Math.hypot(dx, dy); lengths.push(length); total += length;
      }
      if (!total) return [];
      const at = fraction => {
        const target = Math.max(0, Math.min(1, fraction)) * total;
        let walked = 0;
        for (let i = 0; i < lengths.length; i++) {
          const length = lengths[i];
          if (target <= walked + length || i === lengths.length - 1) {
            const part = length ? (target - walked) / length : 0;
            return { x: points[i].x + (points[i + 1].x - points[i].x) * part, y: points[i].y + (points[i + 1].y - points[i].y) * part };
          }
          walked += length;
        }
        return points.at(-1);
      };
      const out = [at(from)]; let walked = 0;
      for (let i = 0; i < lengths.length; i++) {
        walked += lengths[i]; const fraction = walked / total;
        if (fraction > from && fraction < to) out.push(points[i + 1]);
      }
      out.push(at(to)); return out;
    },
    completeDrawing(op) {
      const end = op.current || op.start;
      const rect = this.rectFromPoints(op.start, end);

      const min = this.snap ? Math.max(0.1, this.gridSize) : 0.5;
      if (op.tool === 'line' ? Math.max(rect.width, rect.height) < min : rect.width < min || rect.height < min) { this.error = 'Draw a larger shape on the canvas.'; return; }
      const maxW = this.canvasWidth(); const maxH = this.canvasHeight();
      rect.width = Math.min(rect.width, maxW - rect.x); rect.height = Math.min(rect.height, maxH - rect.y);
      if (op.tool === 'line' ? rect.width <= 0 && rect.height <= 0 : rect.width <= 0 || rect.height <= 0) { this.error = 'The drawing must be inside the canvas.'; return; }
      this.checkpoint();
      if (op.tool === 'drawer') {
        const drawer = { id: 0, key: crypto.randomUUID(), label: `Drawer ${this.state.drawers.length + 1}`, display_order: this.state.drawers.length, ...rect, bins: [] };
        this.state.drawers.push(drawer);
        this.setSelection('drawer', drawer.key);
      } else if (op.tool === 'bin') {
        const bin = this.availableBins().find(item => String(item.id) === String(this.selectedAvailableBin));
        if (!bin || !this.activeDrawer()) { this.undoStack.pop(); return; }
        const placement = { id: Number(bin.id), key: `bin-${bin.id}`, name: bin.name, display_order: this.activeDrawer().bins.length, ...rect };
        this.activeDrawer().bins.push(placement);
        this.setSelection('bin', placement.key);
        this.selectedAvailableBin = '';
      } else {
        const decoration = { key: crypto.randomUUID(), kind: op.tool, ...rect, text: '' };
        this.state.decorations.push(decoration);
        this.setSelection('decoration', decoration.key);
      }
      this.markDirty();
    },
    rectFromPoints(a, b) { return { x: Math.min(a.x, b.x), y: Math.min(a.y, b.y), width: Math.abs(b.x-a.x), height: Math.abs(b.y-a.y) }; },
    previewRect() { return this.rectFromPoints(this.pointerOp.start, this.pointerOp.current || this.pointerOp.start); },
    beginMove(event, entityKind, object) {
      if (this.tool !== 'select') return;
      this.$refs.canvas.setPointerCapture(event.pointerId);
      const point = this.pointForEdit(event);
      this.selectEntity(entityKind, object);
      this.checkpoint();
      this.pointerOp = { kind: 'move', entityKind, key: object.key, startX: point.x, startY: point.y, originX: object.x, originY: object.y };
    },
    beginResize(event, entityKind, object, handle) {
      this.$refs.canvas.setPointerCapture(event.pointerId);
      const point = this.pointForEdit(event);
      this.selectEntity(entityKind, object);
      this.checkpoint();
      this.pointerOp = { kind: 'resize', entityKind, key: object.key, handle, startX: point.x, startY: point.y, originX: object.x, originY: object.y, originW: object.width, originH: object.height };
    },
    resizeObject(object, op, point, bounds) {
      const dx = point.x - op.startX; const dy = point.y - op.startY;
      const west = op.handle.includes('w'); const north = op.handle.includes('n');
      const right = op.originX + op.originW; const bottom = op.originY + op.originH;
      if (west) { object.x = Math.max(0, Math.min(right - 0.1, op.originX + dx)); object.width = right - object.x; }
      else object.width = Math.max(0.1, Math.min(bounds.width - op.originX, op.originW + dx));
      if (north) { object.y = Math.max(0, Math.min(bottom - 0.1, op.originY + dy)); object.height = bottom - object.y; }
      else object.height = Math.max(0.1, Math.min(bounds.height - op.originY, op.originH + dy));
    },
    handleX(object, handle) { return handle.includes('w') ? object.x : object.x + object.width; },
    handleY(object, handle) { return handle.includes('n') ? object.y : object.y + object.height; },
    boundsFor(kind) { return kind === 'bin' ? this.activeDrawer() : { width: this.state.width, height: this.state.height }; },
    entity(kind, key) { if (kind === 'drawer') return this.state.drawers.find(item => item.key === key); if (kind === 'bin') return this.activeDrawer()?.bins.find(item => item.key === key); return this.state.decorations.find(item => item.key === key); },
    selectEntity(kind, object) { this.setSelection(kind, object.key); },
    selectDrawer(drawer) { this.selectEntity('drawer', drawer); this.loadCanonicalDrawerMapping(drawer); },
    selectBin(bin) {
      this.moveTargetDrawerID = '';
      this.moveBinX = Number(bin.x) || 0;
      this.moveBinY = Number(bin.y) || 0;
      this.selectEntity('bin', bin);
    },
    selectDecoration(item) { this.selectEntity('decoration', item); },
    selectedDrawer() { return this.entity('drawer', this.selection.key) || (this.emptyDrawer ||= { id:0,key:'',label:'',display_order:0,x:0,y:0,width:1,height:1,bins:[] }); },
    selectedBin() { return this.entity('bin', this.selection.key) || (this.emptyBin ||= { id:0,key:'',name:'',display_order:0,x:0,y:0,width:1,height:1 }); },
    selectedDecoration() { return this.entity('decoration', this.selection.key) || (this.emptyDecoration ||= { key:'',kind:'label',text:'',x:0,y:0,width:0,height:0 }); },
    ledRangesFor(drawer) {
      if (!drawer) return [];
      if (!this.ledRanges[drawer.key]) this.ledRanges[drawer.key] = [];
      return this.ledRanges[drawer.key];
    },
    ledRangesChanged(drawerKey) {
      return JSON.stringify(this.ledRanges[drawerKey] || []) !== JSON.stringify(this.persistedLedRanges[drawerKey] || []);
    },
    hasUnsavedLedRangesExcept(drawerKey = '') {
      return this.state.drawers.some(drawer => drawer.key !== drawerKey && this.ledRangesChanged(drawer.key));
    },
    controllerAssignmentChanged() {
      const current = this.assignedControllers.map(item => Number(item.Controller.id));
      return JSON.stringify(current) !== JSON.stringify(this.controllerDraft.map(Number));
    },
    wiringChanged() {
      return JSON.stringify(this.wiringStrings) !== this.persistedWiringJSON || JSON.stringify([...this.configuredControllerIDs].sort()) !== JSON.stringify(this.assignedControllers.filter(item => Number(item.StringsConfigured) === 1).map(item => Number(item.Controller.id)).sort());
    },
    controllerSelected(controllerID) { return this.controllerDraft.includes(Number(controllerID)); },
    toggleController(controllerID, checked) {
      const id = Number(controllerID);
      if (checked && !this.controllerDraft.includes(id)) {
        if (this.controllerDraft.length >= 5) { this.controllerSaveError = 'A cabinet supports at most five WLED controllers.'; return; }
        this.controllerDraft = [...this.controllerDraft, id];
      } else if (!checked) {
        if (this.stringRows(id).length) { this.controllerSaveError = 'Remove and save this controller’s physical strings before unassigning it from the cabinet.'; return; }
        this.controllerDraft = this.controllerDraft.filter(value => value !== id);
      }
      this.controllerMappingImpactConfirmed = false;
      this.controllerSaveError = ''; this.controllerSaveNotice = '';
    },
    moveController(controllerID, delta) {
      const index = this.controllerDraft.indexOf(Number(controllerID)); const target = index + delta;
      if (index < 0 || target < 0 || target >= this.controllerDraft.length) return;
      const next = [...this.controllerDraft]; [next[index], next[target]] = [next[target], next[index]];
      this.controllerDraft = next; this.controllerMappingImpactConfirmed = false;
    },
    controllerCapacityChanged(controllerID = 0) {
      return this.controllers.some(controller => (!controllerID || controller.id === Number(controllerID)) && Number(this.capacityDrafts[controller.id]) !== Number(controller.led_count));
    },
    hasUnsavedElectricalChanges() {
      return this.controllerAssignmentChanged() || this.controllerCapacityChanged() || this.wiringChanged() || this.hasUnsavedLedRangesExcept();
    },
    stringRows(controllerID, source = this.wiringStrings) {
      return source.filter(item => Number(item.controller_id) === Number(controllerID)).sort((a, b) => Number(a.display_order) - Number(b.display_order));
    },
    controllerCapacity(controllerID, strings = this.wiringStrings, configuredIDs = this.configuredControllerIDs) {
      const records = this.stringRows(controllerID, strings);
      if (records.length || configuredIDs.map(Number).includes(Number(controllerID))) return records.reduce((sum, item) => sum + (Number(item.pixel_count) || 0), 0);
      const controller = this.controllers.find(item => Number(item.id) === Number(controllerID));
      return Number(controller?.led_count) || 0;
    },
    controllerSpans(order = this.controllerDraft, strings = this.wiringStrings, configuredIDs = this.configuredControllerIDs) {
      let next = 1; const spans = [];
      for (const controllerID of order.map(Number)) {
        const pixels = this.controllerCapacity(controllerID, strings, configuredIDs);
        spans.push({ controller_id: controllerID, start: next, pixels, end: next + pixels - 1 });
        next += pixels;
      }
      return spans;
    },
    totalCabinetPixels() { return this.controllerSpans().reduce((sum, item) => sum + item.pixels, 0); },
    splitRange(startValue, countValue, order = this.controllerDraft, strings = this.wiringStrings, configuredIDs = this.configuredControllerIDs) {
      const start = Number(startValue), count = Number(countValue), end = start + count - 1;
      if (!Number.isSafeInteger(start) || start < 1 || !Number.isSafeInteger(count) || count < 1 || !Number.isSafeInteger(end)) return [];
      const spans = this.controllerSpans(order, strings, configuredIDs); const pieces = [];
      const mapping = window.WLEDgerPixelMapping;
      if (!mapping) return [];
      for (const span of spans) {
        const left = Math.max(start, span.start), right = Math.min(end, span.end);
        if (left > right) continue;
        const stringRows = this.stringRows(span.controller_id, strings);
        if (!stringRows.length) {
          pieces.push({ controller_id: span.controller_id, start: left - span.start + 1, count: right - left + 1, string_id: 0, string_start: left - span.start + 1 });
          continue;
        }
        let stringOffset = 0;
        for (const string of stringRows) {
          const pixelCount = Number(string.pixel_count) || 0;
          const localStart = stringOffset + 1, localEnd = stringOffset + pixelCount;
          const stringLeft = Math.max(left - span.start + 1, localStart), stringRight = Math.min(right - span.start + 1, localEnd);
          if (stringLeft <= stringRight) {
            const outputMapping = { pixel_count: pixelCount, direction: string.direction || "forward", pattern: string.pattern || "linear", rows: string.rows || 1, columns: string.columns || 1, custom_order: string.custom_order || [] };
            let physicalPixels;
            try { physicalPixels = mapping.physicalIndices(outputMapping, stringLeft - localStart, stringRight - stringLeft + 1); } catch { return []; }
            for (const physical of physicalPixels) {
              pieces.push({ controller_id: span.controller_id, start: stringOffset + physical + 1, count: 1, string_id: string.id || string.key, string_start: physical + 1 });
            }
          }
          stringOffset += pixelCount;
        }
      }
      const controllerOrder = new Map(spans.map((span, index) => [span.controller_id, index]));
      pieces.sort((a, b) => (controllerOrder.get(a.controller_id) ?? 0) - (controllerOrder.get(b.controller_id) ?? 0) || a.start - b.start);
      const merged = [];
      for (const piece of pieces) {
        const previous = merged[merged.length - 1];
        if (previous && previous.controller_id === piece.controller_id && previous.string_id === piece.string_id && previous.start + previous.count === piece.start && previous.string_start + previous.count === piece.string_start) previous.count += piece.count;
        else merged.push({ ...piece });
      }
      return merged.reduce((total, piece) => total + piece.count, 0) === count ? merged : [];
    },
    async loadCanonicalDrawerMapping(drawer) {
      if (!drawer?.id) return;
      const drawerID = Number(drawer.id);
      const request = (this.canonicalMappingRequests[drawerID] || 0) + 1;
      this.canonicalMappingRequests[drawerID] = request;
      try {
        const response = await fetch(`/storage/cabinets/${this.state.id}/drawers/${drawerID}/pixel-map`, { headers: { Accept: 'application/json' } });
        const data = await response.json();
        if (!response.ok || this.canonicalMappingRequests[drawerID] !== request) return;
        if (Number(data.electrical_revision) !== Number(this.electricalRevision)) {
          delete this.canonicalAllocationMappings[drawerID];
          this.error = 'Controller wiring changed while this editor was open. Reload the cabinet before reviewing pixel previews.';
          return;
        }
        this.canonicalAllocationMappings[drawerID] = Array.isArray(data.allocations) ? data.allocations : [];
        this.renderCanvas();
      } catch {
        if (this.canonicalMappingRequests[drawerID] === request) delete this.canonicalAllocationMappings[drawerID];
      }
    },
    savedCanonicalPieces(range, drawer) {
      if (!drawer?.id || this.controllerAssignmentChanged() || this.controllerCapacityChanged() || this.wiringChanged()) return null;
      const allocation = (this.canonicalAllocationMappings[Number(drawer.id)] || []).find(item => Number(item.id) === Number(range?.id) && Number(item.start_pixel) === Number(range?.start_pixel) && Number(item.pixel_count) === Number(range?.pixel_count));
      if (!allocation) return null;
      const rank = new Map(this.controllerSpans().map((span, index) => [Number(span.controller_id), index]));
      const pieces = (allocation.pieces || []).map(piece => ({
        controller_id: Number(piece.controller_id), string_id: Number(piece.string_id),
        start: Number(piece.local_start), count: Number(piece.count), string_start: Number(piece.string_start),
      })).sort((a, b) => (rank.get(a.controller_id) ?? 0) - (rank.get(b.controller_id) ?? 0) || a.start - b.start);
      const merged = [];
      for (const piece of pieces) {
        const previous = merged[merged.length - 1];
        if (previous && previous.controller_id === piece.controller_id && previous.string_id === piece.string_id && previous.start + previous.count === piece.start && previous.string_start + previous.count === piece.string_start) previous.count += piece.count;
        else merged.push({ ...piece });
      }
      return merged;
    },
    splitAllocationRange(range, drawer, order = this.controllerDraft, strings = this.wiringStrings, configuredIDs = this.configuredControllerIDs) {
      if (order === this.controllerDraft && strings === this.wiringStrings && configuredIDs === this.configuredControllerIDs) {
        const canonical = this.savedCanonicalPieces(range, drawer);
        if (canonical) return canonical;
      }
      return this.splitRange(range?.start_pixel, range?.pixel_count, order, strings, configuredIDs);
    },
    allocationDestinationPreview(range, order = this.controllerDraft, strings = this.wiringStrings, configuredIDs = this.configuredControllerIDs, drawer = this.selectedDrawer()) {
      const start = Number(range?.start_pixel), count = Number(range?.pixel_count), end = start + count - 1;
      const pieces = this.splitAllocationRange(range, drawer, order, strings, configuredIDs);
      const capacity = this.controllerSpans(order, strings, configuredIDs).reduce((sum, span) => sum + span.pixels, 0);
      const valid = Number.isSafeInteger(start) && start >= 1 && Number.isSafeInteger(count) && count >= 1 && Number.isSafeInteger(end) && end <= capacity && pieces.reduce((sum, piece) => sum + piece.count, 0) === count;
      const destinations = valid ? pieces.map(piece => {
        const controller = this.controllers.find(item => Number(item.id) === piece.controller_id);
        const string = this.stringRows(piece.controller_id, strings).find(item => String(item.id || item.key) === String(piece.string_id));
        const pieceEnd = piece.start + piece.count - 1;
        return `${controller?.name || `Controller #${piece.controller_id}`} local ${piece.start}–${pieceEnd}${string ? ` · ${string.name} pixels ${piece.string_start}–${piece.string_start + piece.count - 1}` : ''}`;
      }) : ['Invalid or incomplete destination'];
      return { valid, destinations };
    },
    allocationDestinations(range, order = this.controllerDraft, strings = this.wiringStrings, configuredIDs = this.configuredControllerIDs, drawer = this.selectedDrawer()) {
      return this.allocationDestinationPreview(range, order, strings, configuredIDs, drawer).destinations;
    },
    changedMappingPreview(oldOrder, oldStrings, oldConfigured, newOrder, newStrings, newConfigured) {
      const changes = [];
      for (const drawer of this.state.drawers) for (const range of this.ledRangesFor(drawer)) {
        if (!String(range.start_pixel ?? '').trim() && !String(range.pixel_count ?? '').trim()) continue;
        const beforePreview = this.allocationDestinationPreview(range, oldOrder, oldStrings, oldConfigured, drawer);
        const afterPreview = this.allocationDestinationPreview(range, newOrder, newStrings, newConfigured, drawer);
        const before = beforePreview.destinations, after = afterPreview.destinations;
        if (!beforePreview.valid || !afterPreview.valid || JSON.stringify(before) !== JSON.stringify(after)) {
          changes.push({ drawer: drawer.label, start: range.start_pixel, count: range.pixel_count, before, after, valid: beforePreview.valid && afterPreview.valid });
        }
      }
      return changes;
    },
    controllerMappingPreview() {
      const oldOrder = this.assignedControllers.map(item => Number(item.Controller.id));
      const oldConfigured = this.assignedControllers.filter(item => Number(item.StringsConfigured) === 1).map(item => Number(item.Controller.id));
      return this.changedMappingPreview(oldOrder, this.wiringStrings, oldConfigured, this.controllerDraft, this.wiringStrings, this.configuredControllerIDs);
    },

    ledEnd(range) {
      const start = Number(range?.start_pixel); const count = Number(range?.pixel_count);
      return Number.isSafeInteger(start) && start >= 1 && Number.isSafeInteger(count) && count >= 1 && Number.isSafeInteger(start + count - 1) ? start + count - 1 : '—';
    },
    ledStatus(drawer) {
      if (this.controllerError) return { error: `${this.controllerError} Cabinet pixel capacity cannot be verified.` };
      const capacity = this.totalCabinetPixels(); const ranges = [];
      for (const candidate of this.state.drawers) for (const range of this.ledRangesFor(candidate)) {
        const startText = String(range.start_pixel ?? '').trim(), countText = String(range.pixel_count ?? '').trim();
        if (!startText && !countText) continue;
        if (!startText || !countText) return { error: `Enter both Start and Count for every range on ${candidate.label}.` };
        const start = Number(startText), count = Number(countText), end = start + count - 1;
        if (!Number.isSafeInteger(start) || start < 1) return { error: 'Cabinet pixel Start must be a whole number of at least 1.' };
        if (!Number.isSafeInteger(count) || count < 1) return { error: 'Pixel Count must be a positive whole number.' };
        if (!Number.isSafeInteger(end)) return { error: 'Pixel End overflows the supported integer range.' };
        if (end > capacity) return { error: `Range ${start}–${end} exceeds cabinet capacity (${capacity} pixels).` };
        ranges.push({ drawer: candidate, start, end });
      }
      for (let i = 0; i < ranges.length; i++) for (let j = i + 1; j < ranges.length; j++) {
        if (ranges[i].start <= ranges[j].end && ranges[j].start <= ranges[i].end) return { error: `Cabinet pixel ranges overlap: ${ranges[i].drawer.label} (${ranges[i].start}–${ranges[i].end}) and ${ranges[j].drawer.label} (${ranges[j].start}–${ranges[j].end}).` };
      }
      return { error: '', message: `${capacity} cabinet pixels · controller and string offsets are calculated automatically.` };
    },
    controllerSummary() {
      if (this.controllerError) return this.controllerError;
      const spans = this.controllerSpans();
      if (!spans.length) return 'No WLED controllers are assigned. Assign a controller before mapping drawer LEDs.';
      return spans.map((span, index) => `${index + 1}. ${this.controllers.find(item => item.id === span.controller_id)?.name || `Controller #${span.controller_id}`} · ${span.pixels} pixels · cabinet ${span.pixels ? `${span.start}–${span.end}` : 'not sized'}`).join('; ');
    },
    controllerCabinetSpan(controllerID) {
      const span = this.controllerSpans().find(item => item.controller_id === Number(controllerID));
      return span ? `${span.pixels} pixels · cabinet ${span.pixels ? `${span.start}–${span.end}` : 'not sized'}` : 'not assigned';
    },
    stringCabinetRange(string) {
      const span = this.controllerSpans().find(item => item.controller_id === Number(string.controller_id));
      if (!span) return 'not assigned';
      let offset = 0;
      for (const row of this.stringRows(string.controller_id)) {
        if (row.key === string.key) break;
        offset += Number(row.pixel_count) || 0;
      }
      const count = Number(string.pixel_count) || 0;
      return count ? `${span.start + offset}–${span.start + offset + count - 1}` : 'not sized';
    },
    configureControllerStrings(controllerID) {
      const id = Number(controllerID);
      this.configuredControllerIDs = [...new Set([...this.configuredControllerIDs.map(Number), id])];
      this.wiringSaveError = ''; this.wiringMappingImpactConfirmed = false;
    },
    wiringStructureChanged() {
      let previous = [];
      try { previous = JSON.parse(this.persistedWiringJSON || '[]'); } catch { previous = []; }
      const normalize = values => values.map(string => ({
        id: Number(string.id) || string.key, controller_id: Number(string.controller_id), display_order: Number(string.display_order),
        pixel_count: Number(string.pixel_count), layout_complete: Boolean(string.layout_complete),
        sections: (string.sections || []).map(section => ({ display_order: Number(section.display_order), pixel_count: Number(section.pixel_count), points: section.points || [] })),
      })).sort((a, b) => a.controller_id - b.controller_id || a.display_order - b.display_order);
      return JSON.stringify(normalize(previous)) !== JSON.stringify(normalize(this.wiringStrings));
    },
    wiringMappingPreview() {
      const order = this.assignedControllers.map(item => Number(item.Controller.id));
      const configured = this.assignedControllers.filter(item => Number(item.StringsConfigured) === 1).map(item => Number(item.Controller.id));
      let previousStrings = [];
      try { previousStrings = JSON.parse(this.persistedWiringJSON || '[]'); } catch { previousStrings = []; }
      const changes = this.changedMappingPreview(order, previousStrings, configured, order, this.wiringStrings, this.configuredControllerIDs);
      if (changes.length || !this.wiringStructureChanged()) return changes;
      const structural = this.state.drawers.flatMap(drawer => this.ledRangesFor(drawer).filter(range => String(range.start_pixel ?? '').trim() || String(range.pixel_count ?? '').trim()).map(range => {
        const before = this.allocationDestinationPreview(range, order, previousStrings, configured, drawer);
        const after = this.allocationDestinationPreview(range, order, this.wiringStrings, this.configuredControllerIDs, drawer);
        return {
          drawer: drawer.label, start: range.start_pixel, count: range.pixel_count,
          before: [...before.destinations, 'existing physical path'],
          after: [...after.destinations, 'draft physical path'], valid: before.valid && after.valid,
        };
      }));
      return structural;
    },
    async discoverPhysicalOutputs(controllerID) {
      this.outputDiscoveryError = ''; this.physicalOutputLayout = null;
      controllerID = Number(controllerID || this.diagnosticControllerID) || 0;
      if (!controllerID) { this.outputDiscoveryError = 'Assign a controller to this cabinet before discovering outputs.'; return; }
      try {
        const response = await fetch(`/storage/cabinets/${this.state.id}/controllers/${controllerID}/outputs`, { headers: { Accept: 'application/json' } });
        const data = await response.json();
        if (!response.ok) throw new Error(data.error || `Output discovery failed (${response.status}).`);
        this.diagnosticControllerID = controllerID; this.physicalOutputLayout = data;
      } catch (error) { this.outputDiscoveryError = error?.message || 'Read-only WLED output discovery failed.'; }
    },
    outputAllocationMarkers(output, drawer) {
      if (!output || !drawer || Number(this.diagnosticControllerID) !== Number(this.physicalOutputLayout?.controller_id)) return [];
      const first = Number(output.first_index), last = Number(output.last_index), length = Number(output.length);
      if (!Number.isSafeInteger(first) || !Number.isSafeInteger(last) || !Number.isSafeInteger(length) || length < 1) return [];
      return this.ledRangesFor(drawer).flatMap((range, index) => {
        const pieces = this.splitAllocationRange(range, drawer).filter(piece => piece.controller_id === Number(this.physicalOutputLayout.controller_id));
        return pieces.flatMap((piece, pieceIndex) => {
          const pieceStart = piece.start, pieceEnd = piece.start + piece.count - 1;
          const clippedStart = Math.max(pieceStart, first), clippedEnd = Math.min(pieceEnd, last);
          if (clippedStart > clippedEnd) return [];
          return [{ key: `${range.id || index}-${pieceIndex}-${output.order}`, start: clippedStart, end: clippedEnd, left: (clippedStart - first) / length * 100, width: (clippedEnd - clippedStart + 1) / length * 100 }];
        });
      });
    },
    addLedRange(drawer) {
      if (!drawer?.id) { this.ledSaveError = 'Save this physical drawer first to obtain its stable ID.'; return; }
      this.ledRangesFor(drawer).push({ id: 0, start_pixel: '', pixel_count: '' });
      this.ledSaveError = ''; this.ledSaveNotice = '';
    },
    removeLedRange(drawer, index) {
      this.ledRangesFor(drawer).splice(index, 1);
      this.ledSaveError = ''; this.ledSaveNotice = '';
    },
    addWiringString(controllerID) {
      const id = Number(controllerID); const rows = this.stringRows(id);
      this.configuredControllerIDs = [...new Set([...this.configuredControllerIDs.map(Number), id])];
      this.wiringStrings.push({ id: 0, key: `string-${crypto.randomUUID()}`, controller_id: id, name: `String ${rows.length + 1}`, display_order: rows.length, pixel_count: '100', layout_complete: false, sections: [] });
      this.wiringSaveError = ''; this.wiringMappingImpactConfirmed = false;
    },
    removeWiringString(string) {
      if (!string) return;
      this.wiringStrings = this.wiringStrings.filter(item => item.key !== string.key);
      this.stringRows(string.controller_id).forEach((item, index) => { item.display_order = index; });
      this.wiringMappingImpactConfirmed = false;
    },
    addPathSection(string) {
      if (!string) return;
      string.sections.push({ id: 0, key: `section-${crypto.randomUUID()}`, display_order: string.sections.length, pixel_count: '1', points: [] });
      this.selectedStringKey = string.key; this.activePathSectionKey = string.sections.at(-1).key;
      this.mode = 'wiring'; this.tool = 'path-point'; this.wiringSaveError = '';
    },
    drawPathSection(string, section) {
      if (!string || !section) return;
      this.selectedStringKey = string.key; this.activePathSectionKey = section.key;
      this.mode = 'wiring'; this.tool = 'path-point'; this.renderCanvas();
    },
    addPathPoint(event) {
      const section = this.currentPathSection(); if (!section) return;
      const point = this.pointForEdit(event);
      section.points.push({ x: Number(point.x.toFixed(3)), y: Number(point.y.toFixed(3)) });
      this.renderCanvas();
    },
    currentPathSection() {
      const string = this.wiringStrings.find(item => item.key === this.selectedStringKey);
      return string?.sections.find(item => item.key === this.activePathSectionKey) || null;
    },
    finishPathSection() {
      const section = this.currentPathSection();
      if (!section || section.points.length < 2) { this.wiringSaveError = 'Add at least two ordered points to define a section direction.'; return; }
      this.mode = 'cabinet'; this.tool = 'select'; this.activePathSectionKey = ''; this.renderCanvas();
    },
    wiringPathPixelTotal(string) { return (string?.sections || []).reduce((sum, section) => sum + (Number(section.pixel_count) || 0), 0); },
    moveWiringString(string, delta) {
      const rows = this.stringRows(string.controller_id); const index = rows.findIndex(item => item.key === string.key); const target = index + delta;
      if (index < 0 || target < 0 || target >= rows.length) return;
      [rows[index].display_order, rows[target].display_order] = [rows[target].display_order, rows[index].display_order];
      this.wiringMappingImpactConfirmed = false;
    },
    movePathSection(string, section, delta) {
      const rows = string.sections; const index = rows.indexOf(section); const target = index + delta;
      if (index < 0 || target < 0 || target >= rows.length) return;
      [rows[index].display_order, rows[target].display_order] = [rows[target].display_order, rows[index].display_order];
      rows.sort((a, b) => a.display_order - b.display_order); this.wiringMappingImpactConfirmed = false;
    },
    removePathSection(string, section) {
      if (!string || !section) return;
      string.sections = string.sections.filter(item => item.key !== section.key);
      string.sections.forEach((item, index) => { item.display_order = index; });
      if (this.activePathSectionKey === section.key) { this.activePathSectionKey = ''; this.mode = 'cabinet'; this.tool = 'select'; }
      this.wiringMappingImpactConfirmed = false;
    },
    saveCabinetController() {
      this.controllerSaveError = ''; this.controllerSaveNotice = '';
      if (this.dirty) { this.controllerSaveError = 'Save or cancel physical layout changes before changing controller assignments.'; return; }
      if (this.controllerCapacityChanged() || this.wiringChanged() || this.hasUnsavedLedRangesExcept()) { this.controllerSaveError = 'Save or cancel string, capacity and LED-range edits before changing controller assignments.'; return; }
      if (this.controllerDraft.length > 5 || new Set(this.controllerDraft).size !== this.controllerDraft.length) { this.controllerSaveError = 'Select no more than five distinct controllers.'; return; }
      const preview = this.controllerMappingPreview();
      if (preview.some(change => !change.valid)) { this.controllerSaveError = 'Correct invalid or incomplete drawer destinations before changing controller order.'; return; }
      if (preview.length && !this.controllerMappingImpactConfirmed) { this.controllerSaveError = 'Review the before/after mapping preview and check the explicit confirmation before saving controller order.'; return; }
      const form = document.createElement('form'); form.method = 'post'; form.action = `/storage/cabinets/${this.state.id}/controller`;
      const fields = [['csrf_token', this.$refs.csrf.value], ['electrical_revision', this.electricalRevision]];
      if (preview.length) fields.push(['confirm_mapping_impact', 'confirm']);
      for (const [name, value] of fields) { const input = document.createElement('input'); input.type = 'hidden'; input.name = name; input.value = value; form.appendChild(input); }
      for (const id of this.controllerDraft) { const input = document.createElement('input'); input.type = 'hidden'; input.name = 'controller_ids'; input.value = id; form.appendChild(input); }
      this.allowElectricalNavigation = true; document.body.appendChild(form); form.submit();
    },
    saveCabinetWiring() {
      this.wiringSaveError = ''; this.wiringSaveNotice = '';
      if (this.dirty) { this.wiringSaveError = 'Save or cancel physical drawer geometry before saving wiring.'; return; }
      if (this.controllerAssignmentChanged() || this.controllerCapacityChanged() || this.hasUnsavedLedRangesExcept()) { this.wiringSaveError = 'Save or cancel controller order, legacy capacity and drawer range edits first.'; return; }
      for (const string of this.wiringStrings) {
        const pixels = Number(string.pixel_count);
        if (!Number.isSafeInteger(pixels) || pixels < 1) { this.wiringSaveError = `${string.name} needs a positive whole-pixel length.`; return; }
        if (string.layout_complete && this.wiringPathPixelTotal(string) !== pixels) { this.wiringSaveError = `${string.name} path sections account for ${this.wiringPathPixelTotal(string)} of ${pixels} pixels.`; return; }
      }
      const preview = this.wiringMappingPreview();
      if (preview.some(change => !change.valid)) { this.wiringSaveError = 'Correct invalid or incomplete drawer destinations before saving wiring changes.'; return; }
      if (preview.length && !this.wiringMappingImpactConfirmed) { this.wiringSaveError = 'Review the before/after drawer pixel destinations and confirm this wiring change.'; return; }
      const wiring = { configured_controller_ids: this.configuredControllerIDs.map(Number), strings: this.wiringStrings.map(string => ({
        id: Number(string.id) || 0, controller_id: Number(string.controller_id), name: string.name, display_order: Number(string.display_order),
        pixel_count: Number(string.pixel_count), layout_complete: Boolean(string.layout_complete),
        sections: string.sections.map(section => ({ id: Number(section.id) || 0, display_order: Number(section.display_order), pixel_count: Number(section.pixel_count), points: section.points || [] })),
      })) };
      const form = document.createElement('form'); form.method = 'post'; form.action = `/storage/cabinets/${this.state.id}/wiring`;
      const fields = [['csrf_token', this.$refs.csrf.value], ['electrical_revision', this.electricalRevision], ['wiring', JSON.stringify(wiring)]];
      if (preview.length) fields.push(['confirm_mapping_impact', 'confirm']);
      for (const [name, value] of fields) { const input = document.createElement('input'); input.type = 'hidden'; input.name = name; input.value = value; form.appendChild(input); }
      this.allowElectricalNavigation = true; document.body.appendChild(form); form.submit();
    },
    saveControllerCapacity(controllerID) {
      this.capacitySaveError = ''; this.capacitySaveNotice = '';
      const controller = this.controllers.find(item => item.id === Number(controllerID));
      if (!controller || this.configuredControllerIDs.map(Number).includes(Number(controllerID))) { this.capacitySaveError = 'Configured physical strings determine this controller capacity; do not set it manually.'; return; }
      if (this.dirty || this.controllerAssignmentChanged() || this.wiringChanged() || this.hasUnsavedLedRangesExcept()) { this.capacitySaveError = 'Save or cancel other cabinet configuration edits first.'; return; }
      const capacity = Number(this.capacityDrafts[controller.id]);
      if (!Number.isSafeInteger(capacity) || capacity < 1) { this.capacitySaveError = 'Legacy capacity must be a positive whole number.'; return; }
      const form = document.createElement('form'); form.method = 'post'; form.action = `/hardware/${controller.id}/capacity`;
      const fields = [['csrf_token', this.$refs.csrf.value], ['led_count', capacity], ['expected_led_count', controller.led_count], ['electrical_revision', this.electricalRevision]];
      for (const [name, value] of fields) { const input = document.createElement('input'); input.type = 'hidden'; input.name = name; input.value = value; form.appendChild(input); }
      this.allowElectricalNavigation = true; document.body.appendChild(form); form.submit();
    },
    saveLedAllocations(drawer) {
      this.ledSaveError = ''; this.ledSaveNotice = '';
      if (this.dirty || this.controllerAssignmentChanged() || this.controllerCapacityChanged() || this.wiringChanged()) { this.ledSaveError = 'Save or cancel physical layout, controller order and wiring edits first.'; return; }
      if (this.hasUnsavedLedRangesExcept(drawer.key)) { this.ledSaveError = 'Save or cancel LED-range edits on other drawers before saving this drawer.'; return; }
      if (!drawer?.id) { this.ledSaveError = 'Save this physical drawer first; no permanent mapping is created for an unsaved drawer.'; return; }
      const status = this.ledStatus(drawer);
      if (status.error) { this.ledSaveError = status.error; return; }
      const allocations = this.ledRangesFor(drawer).filter(range => String(range.start_pixel ?? '').trim() || String(range.pixel_count ?? '').trim()).map(range => ({ id: Number(range.id) || 0, start_pixel: Number(range.start_pixel), pixel_count: Number(range.pixel_count) }));
      const form = document.createElement('form'); form.method = 'post'; form.action = `/storage/cabinets/${this.state.id}/drawers/${drawer.id}/led`;
      const fields = [['csrf_token', this.$refs.csrf.value], ['revision', this.electricalRevision], ['coordinate_space', 'cabinet'], ['allocations', JSON.stringify(allocations)]];
      for (const [name, value] of fields) { const input = document.createElement('input'); input.type = 'hidden'; input.name = name; input.value = value; form.appendChild(input); }
      this.allowElectricalNavigation = true; document.body.appendChild(form); form.submit();
    },
    activeDrawer() { return this.state.drawers.find(item => item.key === this.activeDrawerKey) || (this.selection.kind === 'drawer' ? this.selectedDrawer() : null); },
    openDrawer(drawer) { if (!drawer) return; this.activeDrawerKey = drawer.key; this.mode = 'bins'; this.tool = 'select'; this.setSelection('', ''); this.resetView(); this.renderCanvas(); },
    closeDrawer() { this.mode = 'cabinet'; this.tool = 'select'; this.setSelection('drawer', this.activeDrawerKey); this.resetView(); this.renderCanvas(); },
    availableBins() { const placed = new Set(this.state.drawers.flatMap(drawer => drawer.bins.map(bin => Number(bin.id)))); return this.bins.filter(bin => !placed.has(Number(bin.id))); },
    createPhysicalBin() {
      const drawer = this.activeDrawer();
      if (!drawer?.id || drawer.archived) { this.error = 'Save the drawer and choose an active drawer before adding a bin.'; return; }
      if (this.dirty) { this.error = 'Save or cancel physical layout changes before creating a bin.'; return; }
      const draft = this.newBinDraft;
      if (!String(draft.name || '').trim() || String(draft.name).trim().length > 200 ||
          ![draft.x, draft.y].every(value => Number.isFinite(Number(value))) ||
          ![draft.width, draft.height].every(value => Number.isFinite(Number(value)) && Number(value) > 0)) {
        this.error = 'Enter a bin name and finite positive dimensions and position.'; return;
      }
      const form = document.createElement('form');
      form.method = 'post';
      form.action = `/storage/cabinets/${this.state.id}/drawers/${drawer.id}/bins`;
      const fields = [
        ['csrf_token', this.$refs.csrf.value], ['revision', this.state.revision],
        ['name', String(draft.name).trim()], ['x', draft.x], ['y', draft.y],
        ['width', draft.width], ['height', draft.height],
      ];
      for (const [name, value] of fields) {
        const input = document.createElement('input'); input.type = 'hidden'; input.name = name; input.value = value; form.appendChild(input);
      }
      document.body.appendChild(form); form.submit();
    },
    moveSelectedBin() {
      const bin = this.selectedBin();
      const targetDrawerID = Number(this.moveTargetDrawerID);
      if (!bin?.id || !targetDrawerID) { this.error = 'Choose an active destination drawer.'; return; }
      if (this.dirty || this.hasUnsavedElectricalChanges()) { this.error = 'Save or cancel other layout and electrical edits before moving this bin.'; return; }
      const form = document.createElement('form');
      form.method = 'post';
      form.action = `/storage/cabinets/${this.state.id}/bins/${bin.id}/move`;
      const fields = [
        ['csrf_token', this.$refs.csrf.value],
        ['revision', this.state.revision],
        ['target_drawer_id', targetDrawerID],
        ['x', this.moveBinX],
        ['y', this.moveBinY],
      ];
      for (const [name, value] of fields) {
        const input = document.createElement('input'); input.type = 'hidden'; input.name = name; input.value = value; form.appendChild(input);
      }
      document.body.appendChild(form); form.submit();
    },
    addDrawerFromButton() { this.setTool('drawer'); this.error = 'Drag on the canvas to draw the new drawer.'; },
    zoomBy(factor) { this.zoom = Math.min(4, Math.max(0.25, this.zoom * factor)); this.updateViewBox(); },
    resetView() { this.zoom = 1; this.pan = { x: 0, y: 0 }; this.updateViewBox(); },
    updateViewBox() { if (this.$refs.canvas) this.$refs.canvas.setAttribute('viewBox', this.canvasViewBox()); },
    wheelZoom(event) { this.zoomBy(event.deltaY < 0 ? 1.1 : 0.9); },
    beginHistoryEdit() { this.beginPropertyEdit(); },
    beginPropertyEdit() { this.propertySnapshot = this.clone(this.state); },
    finishPropertyEdit() {
      if (this.propertySnapshot && JSON.stringify(this.propertySnapshot) !== JSON.stringify(this.state)) { this.undoStack.push(this.propertySnapshot); this.redoStack = []; this.markDirty(); }
      this.propertySnapshot = null;
    },
    archiveCabinet() { this.lifecycle('/archive', 'Archive this cabinet? Drawers and placements are preserved.'); },
    deleteCabinet() { this.lifecycle('/delete', 'Permanently delete this empty cabinet? This cannot be undone.'); },
    archiveDrawer() { const drawer = this.selectedDrawer(); if (drawer?.id) this.lifecycle(`/drawers/${drawer.id}/archive`, 'Archive this drawer? Its bins and stock are preserved.'); },
    deleteSelectedDrawer() {
      const drawer = this.selectedDrawer(); if (!drawer) return;
      if (!drawer.id) { if (window.confirm('Discard this new drawer?')) { this.checkpoint(); this.state.drawers = this.state.drawers.filter(item => item.key !== drawer.key); this.setSelection('', ''); this.markDirty(); } return; }
      if (drawer.bins.length) { this.error = 'This drawer has bin placements. Remove or relocate each placement before deleting it.'; return; }
      this.lifecycle(`/drawers/${drawer.id}/delete`, `Permanently delete empty drawer “${drawer.label}”?`);
    },
    removeSelectedPlacement() { const bin = this.selectedBin(); if (bin?.id) this.lifecycle(`/bins/${bin.id}/detach`, 'Remove this physical placement only? The inventory bin, stock and electrical mapping remain.'); },
    deleteDecoration() { const item = this.selectedDecoration(); if (!item) return; this.checkpoint(); this.state.decorations = this.state.decorations.filter(value => value.key !== item.key); this.setSelection('', ''); this.markDirty(); },
    lifecycle(path, message) {
      if (this.dirty) { this.error = 'Save or cancel your canvas edits before using archive, delete or placement removal.'; return; }
      if (!window.confirm(message)) return;
      const form = document.createElement('form'); form.method = 'post'; form.action = `/storage/cabinets/${this.state.id}${path}`;
      for (const [name, value] of [['csrf_token', this.$refs.csrf.value], ['revision', this.state.revision]]) { const input = document.createElement('input'); input.type = 'hidden'; input.name = name; input.value = value; form.appendChild(input); }
      document.body.appendChild(form); form.submit();
    },
    onKeydown(event) {
      if (event.target.matches('input,textarea,select,[contenteditable="true"]')) return;
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'z') { event.preventDefault(); event.shiftKey ? this.redo() : this.undo(); return; }
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'y') { event.preventDefault(); this.redo(); return; }
      if (event.key === 'Escape') { this.pointerOp = null; this.setSelection('', ''); return; }
      if (event.key === 'Delete' || event.key === 'Backspace') {
        if (!this.selection.kind) return;
        event.preventDefault();
        if (this.selection.kind === 'drawer') this.deleteSelectedDrawer();
        else if (this.selection.kind === 'bin') this.removeSelectedPlacement();
        else if (window.confirm('Delete this decorative drawing?')) this.deleteDecoration();
        return;
      }
      if (['ArrowLeft','ArrowRight','ArrowUp','ArrowDown'].includes(event.key) && this.selection.kind) {
        event.preventDefault(); const entity = this.entity(this.selection.kind, this.selection.key); if (!entity) return;
        this.checkpoint(); const step = event.shiftKey ? this.gridSize * 5 : (this.snap ? this.gridSize : 1);
        entity.x = Math.max(0, entity.x + (event.key === 'ArrowLeft' ? -step : event.key === 'ArrowRight' ? step : 0));
        entity.y = Math.max(0, entity.y + (event.key === 'ArrowUp' ? -step : event.key === 'ArrowDown' ? step : 0)); this.markDirty();
      }
    },
    keyboardResize(object, handle, direction) { this.checkpoint(); const step = this.snap ? this.gridSize : 1; if (handle.includes('e')) object.width = Math.max(0.1, object.width + step * direction); if (handle.includes('s')) object.height = Math.max(0.1, object.height + step * direction); this.markDirty(); },
  };
}
