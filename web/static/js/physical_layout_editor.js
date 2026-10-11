function physicalLayoutEditor() {
  return {
    state: {},
    lastStateJSON: '',
    bins: [],
    controller: null,
    controllers: [],
    controllerError: '',
    controllerDraft: '0',
    controllerSaveNotice: '',
    controllerSaveError: '',
    ledRanges: {},
    persistedLedRanges: {},
    electricalRevision: 0,
    allowElectricalNavigation: false,
    capacityDraft: '',
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
      this.controllerError = data.ControllerError || '';
      this.controllerDraft = this.controller ? String(this.controller.id) : '0';
      this.electricalRevision = cabinet.electrical_revision;
      const query = new URLSearchParams(window.location.search);
      this.controllerSaveError = query.get('controller_error') || '';
      this.controllerSaveNotice = query.get('controller_saved') === '1' ? 'Controller assignment saved.' : '';
      this.capacityDraft = this.controller ? String(this.controller.led_count) : '';
      this.capacitySaveError = query.get('capacity_error') || '';
      this.capacitySaveNotice = query.get('capacity_saved') === '1' ? 'Controller-wide pixel capacity saved.' : '';
      this.ledSaveError = query.get('led_error') || '';
      this.ledSaveNotice = new URLSearchParams(window.location.search).get('led_saved') === '1' ? 'Electrical mapping saved to the database.' : '';
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
          display_order: item.Drawer.display_order,
          x: item.Drawer.x,
          y: item.Drawer.y,
          width: item.Drawer.width,
          height: item.Drawer.height,
          bins: (item.Bins || []).map(bin => ({
            id: bin.bin_id,
            key: `bin-${bin.bin_id}`,
            name: `Bin #${bin.bin_id}`,
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
        this.ledRanges[`drawer-${item.Drawer.id}`] = (item.LedAllocations || []).map(range => ({ id: range.id, start_pixel: String(range.start_pixel), pixel_count: String(range.pixel_count) }));
      }
      this.persistedLedRanges = this.clone(this.ledRanges);
      this.bins = (data.UnattachedBins || []).map(bin => ({ id: bin.id, name: bin.name }));
      this.draftKey = `wledger-layout-draft-${cabinet.id}`;
      if (this.error) this.restoreDraft();
      this.selection = { kind: this.state.uiSelectionKind || '', key: this.state.uiSelectionKey || '' };
      this.lastStateJSON = this.layoutFingerprint();
      this.pushHistoryBase();
      this.$watch('state', () => {
        const current = this.layoutFingerprint();
        if (!this.saving && current !== this.lastStateJSON) {
          this.dirty = true;
          this.persistDraft();
        }
        this.lastStateJSON = current;
        this.renderCanvas();
      }, { deep: true });
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
    canvasWidth() { return this.mode === 'cabinet' ? this.state.width : (this.activeDrawer()?.width || 1); },
    canvasHeight() { return this.mode === 'cabinet' ? this.state.height : (this.activeDrawer()?.height || 1); },
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
          if (this.mode === 'cabinet') {
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
              group.addEventListener('dblclick',event=>{event.stopPropagation();this.openDrawer(drawer);});
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
    selectDrawer(drawer) { this.selectEntity('drawer', drawer); },
    selectBin(bin) { this.selectEntity('bin', bin); },
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
      return String(Number(this.controllerDraft) || 0) !== String(this.controller?.id || 0);
    },
    controllerCapacityChanged() {
      return !!this.controller && Number(this.capacityDraft) !== Number(this.controller.led_count);
    },
    hasUnsavedElectricalChanges() {
      return this.controllerAssignmentChanged() || this.controllerCapacityChanged() || this.hasUnsavedLedRangesExcept();
    },
    ledEnd(range) {
      const start = Number(range?.start_pixel); const count = Number(range?.pixel_count);
      return Number.isSafeInteger(start) && start >= 1 && Number.isSafeInteger(count) && count >= 1 && Number.isSafeInteger(start + count - 1) ? start + count - 1 : '—';
    },
    ledStatus(drawer) {
      if (this.controllerError) return { error: `${this.controllerError} Electrical allocations cannot be validated.` };
      if (!this.controller) return { error: 'Assign a controller to this cabinet before saving LED allocations.' };
      const capacity = Number(this.controller.led_count);
      if (!Number.isSafeInteger(capacity) || capacity < 1) return { error: 'The assigned controller has no valid positive LED capacity.' };
      const ranges = [];
      for (const candidate of this.state.drawers) {
        for (const range of this.ledRangesFor(candidate)) {
          const startText = String(range.start_pixel ?? '').trim(); const countText = String(range.pixel_count ?? '').trim();
          if (!startText && !countText) continue;
          if (!startText || !countText) return { error: `Enter both LED Start and LED Count for every range on ${candidate.label}.` };
          const start = Number(startText); const count = Number(countText);
          if (!Number.isSafeInteger(start) || start < 1) return { error: 'LED Start must be a whole number of at least 1.' };
          if (!Number.isSafeInteger(count) || count < 1) return { error: 'LED Count must be a positive whole number.' };
          const end = start + count - 1;
          if (!Number.isSafeInteger(end)) return { error: 'LED End overflows the supported integer range.' };
          if (end > capacity) return { error: `LED range ends at ${end}, beyond controller #${this.controller.id} capacity (${capacity}).` };
          ranges.push({ drawer: candidate, range, start, end });
        }
      }
      for (let i = 0; i < ranges.length; i++) for (let j = i + 1; j < ranges.length; j++) {
        if (ranges[i].start <= ranges[j].end && ranges[j].start <= ranges[i].end) return { error: `LED ranges overlap: ${ranges[i].drawer.label} (${ranges[i].start}–${ranges[i].end}) and ${ranges[j].drawer.label} (${ranges[j].start}–${ranges[j].end}).` };
      }
      return { error: '', message: `Validated in controller-wide 1-based pixel space (capacity ${capacity}). Physical string boundaries do not split a range.` };
    },
    controllerSummary() {
      if (this.controllerError) return this.controllerError;
      if (!this.controller) return 'LED Controller: none assigned to this cabinet.';
      return `Assigned controller: ${this.controller.name} (#${this.controller.id}), capacity ${this.controller.led_count} controller-wide pixels.`;
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
    saveCabinetController() {
      this.controllerSaveError = ''; this.controllerSaveNotice = '';
      if (this.dirty) { this.controllerSaveError = 'Save or cancel physical layout changes before changing controller assignment.'; return; }
      if (this.controllerCapacityChanged() || this.hasUnsavedLedRangesExcept()) { this.controllerSaveError = 'Save or cancel controller-capacity and LED-range edits before changing controller assignment.'; return; }
      const controllerID = Number(this.controllerDraft);
      if (!Number.isSafeInteger(controllerID) || controllerID < 0) { this.controllerSaveError = 'Choose a valid controller or Unassigned.'; return; }
      const form = document.createElement('form'); form.method = 'post'; form.action = `/storage/cabinets/${this.state.id}/controller`;
      const fields = [['csrf_token', this.$refs.csrf.value], ['electrical_revision', this.electricalRevision], ['controller_id', controllerID]];
      for (const [name, value] of fields) { const input = document.createElement('input'); input.type = 'hidden'; input.name = name; input.value = value; form.appendChild(input); }
      this.allowElectricalNavigation = true;
      document.body.appendChild(form); form.submit();
    },
    saveControllerCapacity() {
      this.capacitySaveError = ''; this.capacitySaveNotice = '';
      if (this.dirty) { this.capacitySaveError = 'Save or cancel physical layout changes before saving controller capacity.'; return; }
      if (this.controllerAssignmentChanged() || this.hasUnsavedLedRangesExcept()) { this.capacitySaveError = 'Save or cancel controller-assignment and LED-range edits before saving capacity.'; return; }
      if (!this.controller) { this.capacitySaveError = 'Assign a controller to this cabinet before configuring its capacity.'; return; }
      const capacity = Number(this.capacityDraft);
      if (!Number.isSafeInteger(capacity) || capacity < 1) { this.capacitySaveError = 'Controller-wide LED capacity must be a positive whole number.'; return; }
      const form = document.createElement('form'); form.method = 'post'; form.action = `/hardware/${this.controller.id}/capacity`;
      const fields = [['csrf_token', this.$refs.csrf.value], ['led_count', capacity], ['expected_led_count', this.controller.led_count], ['electrical_revision', this.electricalRevision]];
      for (const [name, value] of fields) { const input = document.createElement('input'); input.type = 'hidden'; input.name = name; input.value = value; form.appendChild(input); }
      this.allowElectricalNavigation = true;
      document.body.appendChild(form); form.submit();
    },
    saveLedAllocations(drawer) {
      this.ledSaveError = ''; this.ledSaveNotice = '';
      if (this.dirty) { this.ledSaveError = 'Save or cancel physical layout changes before saving LED allocations.'; return; }
      if (this.controllerAssignmentChanged() || this.controllerCapacityChanged()) { this.ledSaveError = 'Save or cancel controller-assignment and capacity edits before saving LED allocations.'; return; }
      if (this.hasUnsavedLedRangesExcept(drawer.key)) { this.ledSaveError = 'Save or cancel LED-range edits on other drawers before saving this drawer.'; return; }
      if (!drawer?.id) { this.ledSaveError = 'Save this physical drawer first; no permanent mapping is created for an unsaved drawer.'; return; }
      const status = this.ledStatus(drawer);
      if (status.error) { this.ledSaveError = status.error; return; }
      const allocations = this.ledRangesFor(drawer)
        .filter(range => String(range.start_pixel ?? '').trim() || String(range.pixel_count ?? '').trim())
        .map(range => ({ id: Number(range.id) || 0, start_pixel: Number(range.start_pixel), pixel_count: Number(range.pixel_count) }));
      const form = document.createElement('form'); form.method = 'post'; form.action = `/storage/cabinets/${this.state.id}/drawers/${drawer.id}/led`;
      const fields = [['csrf_token', this.$refs.csrf.value], ['revision', this.electricalRevision], ['allocations', JSON.stringify(allocations)]];
      for (const [name, value] of fields) { const input = document.createElement('input'); input.type = 'hidden'; input.name = name; input.value = value; form.appendChild(input); }
      this.allowElectricalNavigation = true;
      document.body.appendChild(form); form.submit();
    },
    activeDrawer() { return this.state.drawers.find(item => item.key === this.activeDrawerKey) || (this.selection.kind === 'drawer' ? this.selectedDrawer() : null); },
    openDrawer(drawer) { if (!drawer) return; this.activeDrawerKey = drawer.key; this.mode = 'bins'; this.tool = 'select'; this.setSelection('', ''); this.resetView(); this.renderCanvas(); },
    closeDrawer() { this.mode = 'cabinet'; this.tool = 'select'; this.setSelection('drawer', this.activeDrawerKey); this.resetView(); this.renderCanvas(); },
    availableBins() { const placed = new Set(this.state.drawers.flatMap(drawer => drawer.bins.map(bin => Number(bin.id)))); return this.bins.filter(bin => !placed.has(Number(bin.id))); },
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
