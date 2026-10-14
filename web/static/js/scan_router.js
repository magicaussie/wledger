/* Routes decoded keyboard-wedge and camera scans to navigation or the active form. */
(function () {
    const BIN_PREFIX = 'wledger:bin:';
    const PART_PREFIX = 'wledger:part:';
    const CABINET_PREFIX = 'wledger:cabinet:';
    const DRAWER_PREFIX = 'wledger:drawer:';
    const PHYSICAL_CABINET_PREFIX = 'wledger:physical-cabinet:';
    const PHYSICAL_DRAWER_PREFIX = 'wledger:physical-drawer:';
    const PART_ID_PREFIX = 'wledger:part-id:';
    const CATEGORY_PREFIX = 'wledger:category:';
    const pickerContexts = new WeakMap();
    let activePicker = null;

    function makePickerContext(root) {
        const context = {
            root,
            findBin(id) {
                return root.querySelector(`[data-bin-id="${id}"]`) ? Number(id) : null;
            },
            select(id) {
                const picker = root.querySelector('[data-storage-bin-picker]');
                if (picker) picker.dispatchEvent(new CustomEvent('bin-selected', {
                    detail: { id: String(id) },
                    bubbles: true
                }));
            },
            locationOfBin(id) {
                const button = root.querySelector(`[data-bin-id="${id}"]`);
                if (!button) return null;
                const drawer = button.closest('[data-bin-group="drawer"]');
                const cabinet = button.closest('[data-bin-group="cabinet"]');
                return {
                    drawerID: drawer ? Number(drawer.dataset.drawerId) : 0,
                    cabinetID: cabinet ? Number(cabinet.dataset.cabinetId) : 0
                };
            },
            filterCabinet(id) {
                const cabinet = root.querySelector(`[data-bin-group="cabinet"][data-cabinet-id="${id}"]`);
                if (!cabinet) return false;
                root.querySelectorAll('[data-bin-group="cabinet"]').forEach(group => { group.hidden = group !== cabinet; });
                root.querySelectorAll('[data-bin-group="inactive-physical"], [data-bin-group="unplaced"]').forEach(group => { group.hidden = true; });
                return true;
            },
            filterDrawer(id) {
                const drawer = root.querySelector(`[data-bin-group="drawer"][data-drawer-id="${id}"]`);
                if (!drawer) return null;
                const cabinet = drawer.closest('[data-bin-group="cabinet"]');
                root.querySelectorAll('[data-bin-group="cabinet"]').forEach(group => { group.hidden = group !== cabinet; });
                cabinet.querySelectorAll('[data-bin-group="drawer"]').forEach(group => { group.hidden = group !== drawer; });
                root.querySelectorAll('[data-bin-group="inactive-physical"], [data-bin-group="unplaced"]').forEach(group => { group.hidden = true; });
                return { cabinetID: Number(cabinet.dataset.cabinetId) };
            },
            clearFilter() {
                root.querySelectorAll('[data-bin-group]').forEach(group => { group.hidden = false; });
            }
        };
        return context;
    }

    window.pickerReady = function (root) {
        root = root || document.querySelector('#visual_bin_picker');
        if (!root) return;
        const context = makePickerContext(root);
        pickerContexts.set(root, context);
        if (!activePicker) window.__binPickerContext = context;
    };

    window.activateBinPicker = function (rootID) {
        const root = document.getElementById(rootID);
        if (!root) return false;
        let context = pickerContexts.get(root);
        if (!context) {
            context = makePickerContext(root);
            pickerContexts.set(root, context);
        }
        activePicker = context;
        window.__binPickerContext = context;
        return true;
    };

    window.deactivateBinPicker = function (rootID) {
        if (activePicker && activePicker.root.id === rootID) {
            activePicker.clearFilter();
            activePicker = null;
            window.__binPickerContext = null;
        }
    };

    window.clearBinPicker = function () {
        activePicker = null;
        window.__binPickerContext = null;
    };

    function selectBinFromCode(code) {
        const context = activePicker || window.__binPickerContext;
        if (!context) return false;
        const match = code.startsWith(BIN_PREFIX) ? code.slice(BIN_PREFIX.length) : code;
        if (!/^\d+$/.test(match)) return false;
        const bin = context.findBin(Number(match));
        if (!bin) return false;
        context.select(Number(match));
        return true;
    }

    function navigateForScan(code) {
        if (!code) return;
        if (code.startsWith(BIN_PREFIX) || code.startsWith(CABINET_PREFIX) || code.startsWith(DRAWER_PREFIX) ||
            code.startsWith(PHYSICAL_CABINET_PREFIX) || code.startsWith(PHYSICAL_DRAWER_PREFIX) || code.startsWith(PART_ID_PREFIX) || code.startsWith(CATEGORY_PREFIX)) {
            window.location.href = '/scan?q=' + encodeURIComponent(code);
            return;
        }
        const value = code.startsWith(PART_PREFIX) ? code.slice(PART_PREFIX.length) : code;
        window.location.href = '/scan?q=' + encodeURIComponent(value);
    }

    function fillInput(input, code) {
        input.value = code;
        input.dispatchEvent(new Event('input', { bubbles: true }));
        input.dispatchEvent(new Event('change', { bubbles: true }));
    }

    document.addEventListener('fastscan', function (event) {
        const code = event.detail && event.detail.code ? String(event.detail.code).trim() : '';
        if (!code) return;

        const operation = window.__wledgerScanContext;
        if (operation && typeof operation.handleScan === 'function') {
            operation.handleScan(code);
            return;
        }

        if (event.detail && event.detail.targetInputId) {
            const target = document.getElementById(event.detail.targetInputId);
            if (target) {
                if (target.value !== code) fillInput(target, code);
                return;
            }
        }

        const active = document.activeElement;
        if (active && (active.tagName === 'INPUT' || active.tagName === 'TEXTAREA') && !active.dataset.noScan) {
            fillInput(active, code);
            return;
        }

        if (selectBinFromCode(code)) return;
        navigateForScan(code);
    });
})();
