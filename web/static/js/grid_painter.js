document.addEventListener('alpine:init', () => {
    Alpine.data('gridPainter', (ctrlId, binsDataId, containersDataId, binIndexSpace, canEdit) => ({
        canEdit: canEdit,
        // Active coordinate space of the stored bin LED indices. In segment mode
        // a cell's led_index is segment-absolute; in drawer mode it is relative to
        // its owning drawer's allocation (physical LED = drawer.led_start + index).
        binIndexSpace: binIndexSpace,
        containers: [],
        selectedContainerIndex: 0,
        cells: {},
        confirmMessage: '',
        pendingAction: null,
        selectedCellRef: null,

        init() {
            // Helper to decode HTML entities if the proxy escapes JSON content
            const decodeHtml = (html) => {
                const txt = document.createElement("textarea");
                txt.innerHTML = html;
                return txt.value;
            };

            // Retrieve and Parse Data safely
            const rawContainers = document.getElementById(containersDataId).textContent;
            const rawBins = document.getElementById(binsDataId).textContent;

            const initialContainers = JSON.parse(decodeHtml(rawContainers));
            const existingBins = JSON.parse(decodeHtml(rawBins)) || [];

            if (initialContainers && initialContainers.length > 0) {
                this.containers = initialContainers.map(c => ({
                    id: c.id,
                    name: c.name,
                    segment_id: c.segment_id,
                    led_start: Number(c.led_start) || 0,
                    led_count: Number(c.led_count) || 0,
                    config: this.parseConfig(c.config_json?.String || "{}")
                }));
            } else {
                this.addContainer();
            }

            // Load Existing Bins
            existingBins.forEach(b => {
                // Preserve the NULL / index-0 distinction: a bin with no LED
                // assignment (led_index NULL) must stay unmapped rather than
                // silently becoming index 0.
                const mapped = !!(b.led_index && b.led_index.Valid);
                const led = mapped ? Number(b.led_index.Int64) : null;
                const cID = b.container_id;

                // Find container index by ID
                const cIdx = this.containers.findIndex(c => c.id === cID);
                if (cIdx !== -1) {
                    const localIdx = this.getLocalIndexFromGridPos(cIdx, b.grid_x?.Int64 || 0, b.grid_y?.Int64 || 0);
                    if (localIdx !== -1) {
                        const key = `${cIdx},${localIdx}`;
                        const width = (b.width?.Int64 >= 1) ? Number(b.width.Int64) : 1;
                        this.cells[key] = {
                            led_index: led,
                            width: width,
                            name: b.name
                        };
                    }
                }
            });
        },

        parseConfig(jsonStr) {
            const defaultConfig = {
                type: 'grid',
                rows: 8,
                cols: 8,
                total: 64,
                start_corner: 'tl',
                sections: [{ rows: 4, cols: 4 }]
            };

            if (!jsonStr || typeof jsonStr !== 'string') {
                return defaultConfig;
            }

            try {
                const cfg = JSON.parse(jsonStr);

                if (!cfg || typeof cfg !== 'object') {
                    return defaultConfig;
                }

                return {
                    type: cfg.type || 'grid',
                    rows: Number(cfg.rows) || 8,
                    cols: Number(cfg.cols) || 8,
                    total: Number(cfg.total) || 64,
                    start_corner: cfg.start_corner || 'tl',
                    sections: Array.isArray(cfg.sections) ? cfg.sections : [{ rows: 4, cols: 4 }]
                };
            } catch (e) {
                console.warn('Error parsing container config:', e);
                return defaultConfig;
            }
        },

        addContainer() {
            // Suggest a non-overlapping start after existing drawers in segment 0.
            // Existing drawers' allocations are never shifted.
            const seg = 0;
            let start = 0;
            this.containers.forEach(c => {
                if (c.segment_id === seg) {
                    const end = (Number(c.led_start) || 0) + (Number(c.led_count) || 0);
                    if (end > start) start = end;
                }
            });
            this.containers.push({
                id: null,
                name: "New Container " + (this.containers.length + 1),
                segment_id: seg,
                led_start: start,
                led_count: 64,
                config: { type: 'grid', rows: 8, cols: 8, start_corner: 'tl', sections: [{ rows: 4, cols: 4 }] }
            });
            this.selectedContainerIndex = this.containers.length - 1;
        },

        removeContainer(idx) {
            this.containers.splice(idx, 1);
            // Clear cells for this container and shift others
            const newCells = {};
            Object.keys(this.cells).forEach(key => {
                const [cIdx, lIdx] = key.split(',').map(Number);
                if (cIdx < idx) {
                    newCells[key] = this.cells[key];
                } else if (cIdx > idx) {
                    newCells[`${cIdx - 1},${lIdx}`] = this.cells[key];
                }
            });
            this.cells = newCells;
            if (this.selectedContainerIndex >= this.containers.length) {
                this.selectedContainerIndex = Math.max(0, this.containers.length - 1);
            }
        },

        get currentContainer() {
            return this.containers[this.selectedContainerIndex];
        },

        getRenderSections(cIdx) {
            const cfg = this.containers[cIdx].config;
            if (cfg.type === 'linear') return [{ rows: 1, cols: cfg.total }];
            if (cfg.type === 'grid') return [{ rows: cfg.rows, cols: cfg.cols }];
            return cfg.sections;
        },

        getSectionBaseIndex(cIdx, secIdx) {
            const cfg = this.containers[cIdx].config;
            if (cfg.type !== 'compound') return 0;
            let count = 0;
            for (let i = 0; i < secIdx; i++) {
                const s = cfg.sections[i];
                count += (s.rows * s.cols);
            }
            return count;
        },

        getXY(cIdx, cellIdx) {
            const cfg = this.containers[cIdx].config;
            if (cfg.type === 'linear') return { x: cellIdx, y: 0 };
            if (cfg.type === 'grid') return { x: cellIdx % cfg.cols, y: Math.floor(cellIdx / cfg.cols) };

            // Compound
            let count = 0;
            for (let sIdx = 0; sIdx < cfg.sections.length; sIdx++) {
                const s = cfg.sections[sIdx];
                const sSize = s.rows * s.cols;
                if (cellIdx < count + sSize) {
                    const localIdx = cellIdx - count;
                    return { x: localIdx % s.cols, y: Math.floor(localIdx / s.cols), sectionIndex: sIdx };
                }
                count += sSize;
            }
            return { x: 0, y: 0 };
        },

        getLocalIndexFromGridPos(cIdx, gx, gy) {
            const cfg = this.containers[cIdx].config;
            if (cfg.type === 'linear') return gx;
            if (cfg.type === 'grid') return (gy * cfg.cols) + gx;

            // Compound
            let currentYBase = 0;
            let currentCountBase = 0;
            for (let i = 0; i < cfg.sections.length; i++) {
                const s = cfg.sections[i];
                if (gy >= currentYBase && gy < currentYBase + s.rows) {
                    const localY = gy - currentYBase;
                    if (gx < s.cols) {
                        return currentCountBase + (localY * s.cols) + gx;
                    }
                }
                currentYBase += (s.rows + 1);
                currentCountBase += (s.rows * s.cols);
            }
            return -1;
        },

        getCellClass(cIdx, cellIdx) {
            const key = `${cIdx},${cellIdx}`;
            const cell = this.cells[key];
            if (!cell) return 'bg-base-100 text-base-content/20';
            if (cell.led_index === null || cell.led_index === undefined) return ''; // unmapped: styled by the template
            return 'bg-primary text-primary-content border-primary';
        },

        /** True when a cell exists and carries an explicit LED assignment (0 included). */
        isMappedCell(cIdx, cellIdx) {
            const cell = this.cells[`${cIdx},${cellIdx}`];
            return !!cell && cell.led_index !== null && cell.led_index !== undefined;
        },

        /** True when a cell exists but has no LED assignment (NULL). */
        isUnmappedCell(cIdx, cellIdx) {
            const cell = this.cells[`${cIdx},${cellIdx}`];
            return !!cell && (cell.led_index === null || cell.led_index === undefined);
        },

        getGlobalLedIndex(cIdx, cellIdx) {
            const key = `${cIdx},${cellIdx}`;
            if (!this.cells[key]) return '';
            return this.cells[key].led_index;
        },

        /** 1-based start LED for a mapped bin (physical LED numbering). */
        getStartLed(cIdx, cellIdx) {
            const key = `${cIdx},${cellIdx}`;
            const cell = this.cells[key];
            if (!cell || cell.led_index === null || cell.led_index === undefined) return '';
            return cell.led_index + 1;
        },

        /** number of LEDs a mapped bin spans (defaults to 1). */
        getWidth(cIdx, cellIdx) {
            const key = `${cIdx},${cellIdx}`;
            if (!this.cells[key]) return 0;
            return Math.max(1, this.cells[key].width || 1);
        },

        /** 1-based end LED for a mapped bin. */
        getEndLed(cIdx, cellIdx) {
            const start = this.getStartLed(cIdx, cellIdx);
            if (start === '') return '';
            return start + this.getWidth(cIdx, cellIdx) - 1;
        },

        toggleCell(cIdx, cellIdx) {
            if (!this.canEdit) return;
            const key = `${cIdx},${cellIdx}`;
            if (this.cells[key]) {
                delete this.cells[key];
                if (this.selectedCellRef && this.selectedCellRef.key === key) {
                    this.selectedCellRef = null;
                }
            } else {
                const nextIndex = this.getNextAvailableLedIndex(cIdx);
                const name = this.generateName(cIdx, cellIdx);
                this.cells[key] = { led_index: nextIndex, width: 1, name: name };
                this.selectedCellRef = { key: key, cIdx, cellIdx };
            }
        },

        /** Cell click: select a mapped cell for LED range editing, or create a new one. */
        onCellClick(cIdx, cellIdx) {
            if (!this.canEdit) return;
            const key = `${cIdx},${cellIdx}`;
            if (this.cells[key]) {
                this.selectedCellRef = { key, cIdx, cellIdx };
            } else {
                this.toggleCell(cIdx, cellIdx);
            }
        },

        isSelectedCell(cIdx, cellIdx) {
            return this.selectedCellRef && this.selectedCellRef.cIdx === cIdx && this.selectedCellRef.cellIdx === cellIdx;
        },

        /** Editor state - snapshots of the selected bin's LED range (1-based). */
        get selectedCell() {
            if (!this.selectedCellRef) return null;
            const { cIdx, cellIdx } = this.selectedCellRef;
            const key = `${cIdx},${cellIdx}`;
            return this.cells[key] || null;
        },

        get selectedCellName() {
            const cell = this.selectedCell;
            return cell ? cell.name : '';
        },

        get selectedStartLed() {
            const cell = this.selectedCell;
            const idx = (cell && cell.led_index !== null && cell.led_index !== undefined) ? cell.led_index : 0;
            return idx + 1;
        },
        set selectedStartLed(v) {
            const cell = this.cells[this.selectedCellRef.key];
            cell.led_index = this.clampStoredIndex(this.selectedCellRef.cIdx, Math.max(1, Number(v)) - 1);
        },

        get selectedEndLed() {
            const cell = this.selectedCell;
            const mapped = cell && cell.led_index !== null && cell.led_index !== undefined;
            const idx = mapped ? cell.led_index : 0;
            const width = mapped ? Math.max(1, cell.width || 1) : 1;
            return cell ? idx + width : 1;
        },
        set selectedEndLed(v) {
            const cell = this.cells[this.selectedCellRef.key];
            const start = Math.max(1, Number(this.selectedStartLed));
            let end = Math.max(start, Number(v));
            if (this.isDrawerSpace) {
                const { count } = this.getContainerAllocation(this.selectedCellRef.cIdx);
                end = Math.min(end, Math.max(start, count));
            }
            cell.led_index = start - 1;
            cell.width = end - start + 1;
        },

        /** Apply the edited LED range to the selected bin, clamped to the active space. */
        applyCellRange() {
            if (!this.selectedCellRef) return;
            const key = this.selectedCellRef.key;
            const cell = this.cells[key];
            if (!cell) return;
            const start = Math.max(1, Number(this.selectedStartLed) || 1);
            let end = Math.max(start, Number(this.selectedEndLed) || start);
            if (this.isDrawerSpace) {
                const { count } = this.getContainerAllocation(this.selectedCellRef.cIdx);
                end = Math.min(end, Math.max(start, count));
            }
            cell.led_index = start - 1;
            cell.width = end - start + 1;
        },

        /** Remove the selected bin's mapping. */
        unmapCell() {
            if (!this.selectedCellRef) return;
            const key = this.selectedCellRef.key;
            delete this.cells[key];
            this.selectedCellRef = null;
        },

        closeCellEditor() {
            this.selectedCellRef = null;
        },

        /** Display label for a cell: shows the LED range (1-based) in the active space. */
        getCellLedLabel(cIdx, cellIdx) {
            const key = `${cIdx},${cellIdx}`;
            const cell = this.cells[key];
            if (!cell || cell.led_index === null || cell.led_index === undefined) return '';
            const start = cell.led_index + 1;
            const width = Math.max(1, cell.width || 1);
            return (width > 1) ? `${start}-${start + width - 1}` : `${start}`;
        },

        /** Segment-relative (physical) 1-based LED range for a mapped cell. */
        getCellSegmentLabel(cIdx, cellIdx) {
            const key = `${cIdx},${cellIdx}`;
            const cell = this.cells[key];
            if (!cell || cell.led_index === null || cell.led_index === undefined) return '';
            const base = this.isDrawerSpace ? this.getContainerAllocation(cIdx).start : 0;
            const start = cell.led_index + base + 1;
            const width = Math.max(1, cell.width || 1);
            return (width > 1) ? `${start}-${start + width - 1}` : `${start}`;
        },

        /** True when stored bin indices are relative to their owning drawer. */
        get isDrawerSpace() {
            return this.binIndexSpace === 'drawer';
        },

        /** Label describing which index space the LED range editor edits. */
        get spaceLabel() {
            return this.isDrawerSpace ? 'Drawer-relative (D)' : 'Segment-relative (S)';
        },

        /** Summary of the selected bin's range in the active space and, when
         *  drawer-relative, its physical (segment-relative) range. */
        get rangeSummary() {
            const cell = this.selectedCell;
            if (!cell || !this.selectedCellRef) return '';
            if (cell.led_index === null || cell.led_index === undefined) return 'Unassigned';
            const width = Math.max(1, cell.width || 1);
            const aStart = cell.led_index + 1;
            const aEnd = aStart + width - 1;
            if (this.isDrawerSpace) {
                const base = this.getContainerAllocation(this.selectedCellRef.cIdx).start;
                return `D ${aStart}\u2013${aEnd} \u00b7 S ${aStart + base}\u2013${aEnd + base}`;
            }
            return `S ${aStart}\u2013${aEnd}`;
        },

        /** Drawer allocation (segment-relative start and count) for a drawer. */
        getContainerAllocation(cIdx) {
            const c = this.containers[cIdx] || {};
            return {
                start: Number(c.led_start) || 0,
                count: Number(c.led_count) || 0,
            };
        },

        /** Clamp a stored LED index to the active space's allowed range. */
        clampStoredIndex(cIdx, idx) {
            let v = Math.max(0, Number(idx) || 0);
            if (this.isDrawerSpace) {
                const { count } = this.getContainerAllocation(cIdx);
                const max = Math.max(0, count - 1);
                if (v > max) v = max;
            }
            return v;
        },

        /**
         * Next free LED index for a new bin in the active coordinate space.
         * Segment space dedupes across the whole WLED segment; drawer space
         * dedupes only within the drawer and never leaves its allocation.
         */
        getNextAvailableLedIndex(cIdx) {
            const isDrawer = this.isDrawerSpace;
            const target = this.containers[cIdx];
            const used = new Set();

            Object.keys(this.cells).forEach(key => {
                const [ci] = key.split(',').map(Number);
                const c = this.containers[ci];
                if (!c) return;
                if (isDrawer) {
                    if (ci !== cIdx) return; // drawer-local scope
                } else if (c.segment_id !== target.segment_id) {
                    return; // segment scope
                }
                const cell = this.cells[key];
                if (cell.led_index === null || cell.led_index === undefined) return; // unmapped: not an occupied index
                const start = cell.led_index;
                const count = Math.max(1, cell.width || 1);
                for (let j = 0; j < count; j++) {
                    used.add(start + j);
                }
            });

            const { count } = this.getContainerAllocation(cIdx);
            const limit = isDrawer ? Math.max(0, count) : Infinity;
            let i = 0;
            while (i < limit && used.has(i)) i++;
            return i;
        },

        getContainerTotalLeds(cIdx) {
            const cfg = this.containers[cIdx].config;
            if (cfg.type === 'linear') return cfg.total;
            if (cfg.type === 'grid') return cfg.rows * cfg.cols;
            return cfg.sections.reduce((sum, s) => sum + (s.rows * s.cols), 0);
        },

        getBinName(cIdx, cellIdx) {
            const key = `${cIdx},${cellIdx}`;
            return this.cells[key] ? this.cells[key].name : '';
        },

        generateName(cIdx, cellIdx) {
            const { x, y, sectionIndex } = this.getXY(cIdx, cellIdx);
            const charCode = 65 + (x % 26);
            const char = String.fromCharCode(charCode);
            const colLetter = char.repeat(Math.floor(x / 26) + 1);
            const baseName = `${colLetter}${y + 1}`;

            if (this.containers[cIdx].config.type === 'compound' && sectionIndex !== undefined) {
                return `S${sectionIndex + 1}-${baseName}`;
            }

            if (this.containers.length > 1) {
                return `C${cIdx + 1}-${baseName}`;
            }
            return baseName;
        },

        autoFill(mode) {
            if (!this.canEdit) return;
            const cIdx = this.selectedContainerIndex;
            const cfg = this.containers[cIdx].config;

            // Clear cells for current container
            Object.keys(this.cells).forEach(key => {
                if (key.startsWith(cIdx + ",")) delete this.cells[key];
            });
            if (this.selectedCellRef && this.selectedCellRef.cIdx === cIdx) {
                this.selectedCellRef = null;
            }

            let ledCounter = this.isDrawerSpace ? 0 : (Number(this.containers[cIdx].led_start) || 0);
            const startPos = cfg.start_corner || 'tl';
            const sections = this.getRenderSections(cIdx);
            let currentYOffset = 0;

            sections.forEach((sec, secIdx) => {
                for (let r = 0; r < sec.rows; r++) {
                    for (let c = 0; c < sec.cols; c++) {
                        let visualY = (startPos.includes('b')) ? (sec.rows - 1 - r) : r;
                        let visualX = (startPos.includes('r')) ? (sec.cols - 1 - c) : c;

                        if (mode === 'serpentine' && r % 2 !== 0) {
                            visualX = startPos.includes('r') ? c : (sec.cols - 1 - c);
                        }

                        const globalY = currentYOffset + visualY;
                        const localIdx = this.getLocalIndexFromGridPos(cIdx, visualX, globalY);
                        if (localIdx !== -1) {
                            this.cells[`${cIdx},${localIdx}`] = {
                                led_index: ledCounter,
                                width: 1,
                                name: this.generateName(cIdx, localIdx)
                            };
                        }
                        ledCounter++;
                    }
                }
                currentYOffset += (sec.rows + 1);
            });
        },

        askConfirm(action) {
            this.pendingAction = action;
            if (action === 'clear') {
                this.confirmMessage = "Are you sure you want to clear all mappings for this container? This cannot be undone.";
            } else if (action === 'save') {
                this.confirmMessage = "Are you sure you want to save these changes? This will overwrite the existing configuration.";
            }
            document.getElementById('grid_painter_confirm_modal').showModal();
        },

        confirmAction() {
            if (this.pendingAction === 'clear') {
                this.clearGrid();
            } else if (this.pendingAction === 'save') {
                this.submitGrid();
            }
            document.getElementById('grid_painter_confirm_modal').close();
            this.pendingAction = null;
        },

        clearGrid() {
            const cIdx = this.selectedContainerIndex;
            Object.keys(this.cells).forEach(key => {
                if (key.startsWith(cIdx + ",")) delete this.cells[key];
            });
            if (this.selectedCellRef && this.selectedCellRef.cIdx === cIdx) {
                this.selectedCellRef = null;
            }
        },

        submitGrid() {
            this.$refs.gridForm.submit();
        },

        exportBinData() {
            const result = [];
            this.containers.forEach((c, cIdx) => {
                const sections = this.getRenderSections(cIdx);
                let globalYOffset = 0;
                let countBase = 0;
                sections.forEach((sec, secIdx) => {
                    for (let i = 0; i < (sec.rows * sec.cols); i++) {
                        const localIdx = countBase + i;
                        const key = `${cIdx},${localIdx}`;
                        const cell = this.cells[key];
                        if (cell) {
                            const { x, y } = this.getXY(cIdx, localIdx);
                            result.push({
                                container_index: cIdx,
                                x: x,
                                y: y + globalYOffset,
                                led_index: cell.led_index,
                                width: Math.max(1, cell.width || 1),
                                name: cell.name
                            });
                        }
                    }
                    globalYOffset += (sec.rows + 1);
                    countBase += (sec.rows * sec.cols);
                });
            });
            return result;
        },

        exportContainerData() {
            return this.containers.map(c => ({
                id: c.id,
                name: c.name,
                segment_id: c.segment_id,
                led_start: Number(c.led_start) || 0,
                led_count: Number(c.led_count) || 0,
                config: c.config
            }));
        }
    }));
});
