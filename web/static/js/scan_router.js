/**
 * scan_router.js — routes `fastscan` events (keyboard-wedge USB scanner and
 * webcam scans) to the right destination in the UI.
 *
 * Scanned values can be:
 *   - wledger:bin:<id>      -> open inventory filtered to that physical bin
 *   - wledger:part:<code>   -> go to the part page (or search) for that code
 *   - a plain barcode       -> go to the part page if it matches exactly,
 *                              otherwise search the inventory for it
 *
 * If the fast scan arrives while a bin picker is visible (the "visual bin
 * picker" used by the Add Stock modal), the matching bin is selected instead
 * of navigating away.
 */

(function () {
    const BIN_PREFIX = 'wledger:bin:';
    const PART_PREFIX = 'wledger:part:';

    // Focused bin-picker context: {findBin, select} registered by the bin
    // picker component when it is open.
    window.__binPickerContext = null;

    // Called by bin_picker_grid.templ (x-init) whenever the visual bin picker
    // renders, so a scanned bin QR can select that bin in the open form.
    window.pickerReady = function () {
        window.__binPickerContext = {
            findBin: function (id) {
                return document.querySelector('[data-bin-id="' + id + '"]') ? id : null;
            },
            select: function (id) {
                const picker = document.querySelector('#visual_bin_picker');
                if (picker) {
                    picker.dispatchEvent(new CustomEvent('bin-selected', {
                        detail: { id: String(id) },
                        bubbles: true
                    }));
                }
            }
        };
    };

    window.clearBinPicker = function () {
        window.__binPickerContext = null;
    };

    window.clearBinPicker = function () {
        window.__binPickerContext = null;
    };

    function selectBinFromCode(code) {
        if (!window.__binPickerContext) return false;
        const match = code.startsWith(BIN_PREFIX) ? code.slice(BIN_PREFIX.length) : code;
        if (!/^\d+$/.test(match)) return false;
        const ctx = window.__binPickerContext;
        const bin = ctx.findBin && ctx.findBin(Number(match));
        if (bin) {
            ctx.select(Number(match));
            return true;
        }
        return false;
    }

    function navigateForScan(code) {
        if (!code) return;
        if (code.startsWith(BIN_PREFIX)) {
            window.location.href = '/scan?q=' + encodeURIComponent(code);
            return;
        }
        // Plain barcode / part code — let the server resolve exact matches.
        const value = code.startsWith(PART_PREFIX) ? code.slice(PART_PREFIX.length) : code;
        window.location.href = '/scan?q=' + encodeURIComponent(value);
    }

    document.addEventListener('fastscan', function (e) {
        const code = e.detail && e.detail.code ? String(e.detail.code).trim() : '';
        if (!code) return;

        // If an input is focused, prefer filling it (e.g. barcode field).
        const active = document.activeElement;
        if (active && (active.tagName === 'INPUT' || active.tagName === 'TEXTAREA') && !active.dataset.noScan) {
            // Fill and notify listeners so forms/HTMX react.
            active.value = code;
            active.dispatchEvent(new Event('input', { bubbles: true }));
            active.dispatchEvent(new Event('change', { bubbles: true }));
            return;
        }

        if (selectBinFromCode(code)) {
            return;
        }
        navigateForScan(code);
    });
})();