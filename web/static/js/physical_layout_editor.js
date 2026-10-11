function physicalLayoutEditor() {
  return {
    state: {},
    lastStateJSON: '',
    bins: [],
    controller: null,
    controllerError: '',
    ledDrafts: {},
    ledDraftKey: '',
    ledDraftSaved: false,
    ledDraftError: '',
    ledDraftLoadNotice: '',
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
      this.controllerError = data.ControllerError || '';
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
      this.bins = (data.UnattachedBins || []).map(bin => ({ id: bin.id, name: bin.name }));
      this.ledDraftKey = `wledger-led-allocation-draft-cab-${cabinet.id}`;
      for (const drawer of this.state.drawers) this.ledDrafts[drawer.key] = { start: '', count: '' };
      this.loadLedDrafts();
      this.draftKey = `wledger-layout-draft-${cabinet.id}`;
      if (this.error) this.restoreDraft();
      this.selection = { kind: this.state.uiSelectionKind || '', key: this.state.uiSelectionKey || '' };
      this.lastStateJSON = this.layoutFingerprint();
      this.pushHistoryBase();
      this.$watch('ledDrafts', () => { this.ledDraftSaved = false; this.ledDraftError = ''; }, { deep: true });
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
        if (this.dirty) { event.preventDefault(); event.returnValue = ''; }
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
    loadLedDrafts() {
      try {
        const saved = JSON.parse(localStorage.getItem(this.ledDraftKey) || 'null');
        const controllerID = this.controller ? Number(this.controller.id) : null;
        if (!saved) return;
        if (saved.controllerId !== controllerID) { this.ledDraftLoadNotice = 'A saved draft belongs to a different controller configuration and was not loaded.'; return; }
        for (const drawer of this.state.drawers) {
          if (!drawer.id) continue;
          const entry = saved.allocations?.[String(drawer.id)];
          if (entry) this.ledDrafts[drawer.key] = { start: entry.start ?? '', count: entry.count ?? '' };
        }
      } catch { this.ledDraftLoadNotice = 'The saved LED draft could not be read.'; }
    },
    ledDraftFor(drawer) {
      if (!drawer) return this.emptyLedDraft ||= { start: '', count: '' };
      if (!this.ledDrafts[drawer.key]) this.ledDrafts[drawer.key] = { start: '', count: '' };
      return this.ledDrafts[drawer.key];
    },
    ledDraftStatus(drawer) {
      const draft = this.ledDraftFor(drawer);
      const startText = String(draft.start ?? '').trim();
      const countText = String(draft.count ?? '').trim();
      if (!startText && !countText) return { error: '', message: 'Unconfigured. No LED draft values are stored.' };
      if (!startText || !countText) return { error: 'Enter both LED Start and LED Count, or leave both blank.' };
      const start = Number(startText); const count = Number(countText);
      if (!Number.isSafeInteger(start) || start < 0) return { error: 'LED Start must be a non-negative whole number.' };
      if (!Number.isSafeInteger(count) || count <= 0) return { error: 'LED Count must be a positive whole number.' };
      const end = start + count;
      if (!Number.isSafeInteger(end)) return { error: 'LED range exceeds the supported numeric range.' };
      for (const other of this.state.drawers) {
        if (other.key === drawer?.key || !other.id) continue;
        const otherDraft = this.ledDraftFor(other);
        const otherStart = Number(otherDraft.start); const otherCount = Number(otherDraft.count);
        if (!String(otherDraft.start ?? '').trim() || !String(otherDraft.count ?? '').trim() || !Number.isSafeInteger(otherStart) || !Number.isSafeInteger(otherCount) || otherCount <= 0) continue;
        if (start < otherStart + otherCount && otherStart < end) return { error: `This draft overlaps ${other.label}. Segment assignment is unknown, so overlap is checked conservatively across this cabinet.` };
      }
      if (this.controllerError) return { error: this.controllerError + ' Drafts cannot be validated against a controller.' };
            if (!this.controller) return { error: '', message: 'Valid local draft only. No cabinet controller is assigned; the range cannot be checked against a controller.' };
      const capacity = Number(this.controller.led_count);
      if (!Number.isSafeInteger(capacity) || capacity <= 0) return { error: 'The assigned controller has no valid LED capacity.' };
      if (end > capacity) return { error: `LED range ends at ${end}, beyond controller #${this.controller.id} capacity (${capacity}).` };
      return { error: '', message: `Draft range [${start}, ${end}) fits controller #${this.controller.id} (${capacity} LEDs). No electrical segment is assigned; this remains a local draft.` };
    },
    controllerSummary() {
      if (this.controllerError) return this.controllerError;
      if (!this.controller) return 'LED Controller: none assigned to this cabinet.';
      return `LED Controller: ${this.controller.name} (#${this.controller.id}), ${this.controller.led_count} LEDs (read-only).`;
    },
    saveLedDraft(drawer) {
      this.ledDraftError = '';
      if (!drawer?.id) { this.ledDraftError = 'Save this physical drawer first so the draft can use its stable ID.'; return; }
      const status = this.ledDraftStatus(drawer);
      if (status.error) { this.ledDraftError = status.error; return; }
      const allocations = {};
      for (const item of this.state.drawers) {
        if (!item.id) continue;
        const value = this.ledDraftFor(item);
        if (String(value.start ?? '').trim() || String(value.count ?? '').trim()) allocations[String(item.id)] = { start: String(value.start ?? ''), count: String(value.count ?? '') };
      }
      const controllerId = this.controller ? Number(this.controller.id) : null;
      try { localStorage.setItem(this.ledDraftKey, JSON.stringify({ controllerId, allocations })); this.ledDraftSaved = true; }
      catch { this.ledDraftError = 'This browser cannot store the local LED draft.'; }
    },
    clearLedDraft(drawer) {
      if (!drawer?.id) return;
      this.ledDraftError = '';
      this.ledDraftFor(drawer).start = '';
      this.ledDraftFor(drawer).count = '';
      const saved = {};
      for (const item of this.state.drawers) {
        if (!item.id || item.id === drawer.id) continue;
        const value = this.ledDraftFor(item);
        if (String(value.start ?? '').trim() || String(value.count ?? '').trim()) saved[String(item.id)] = { start:String(value.start ?? ''), count:String(value.count ?? '') };
      }
      try { localStorage.setItem(this.ledDraftKey, JSON.stringify({ controllerId:this.controller ? Number(this.controller.id) : null, allocations:saved })); this.ledDraftSaved = true; }
      catch { this.ledDraftError = 'This browser cannot update the local LED draft.'; }
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
