/**
 * fast_scan.js — keyboard-wedge barcode/QR scanner support.
 *
 * USB and Bluetooth (dongle) scanners present as keyboards: they type the
 * decoded value very quickly and then send a terminator (usually Enter). This
 * script buffers keystrokes and, when a burst is followed by a terminator,
 * fires a `fastscan` CustomEvent carrying the decoded value (trimmed).
 *
 * Pages/scripts listen with:
 *   document.addEventListener('fastscan', (e) => console.log(e.detail.code));
 *
 * A scan is considered "fast" when characters arrive in quick succession (a
 * hardware scanner emits keys with small, regular gaps). Manual typing produces
 * larger, irregular gaps and is ignored so normal text entry is not hijacked.
 *
 * Configuration (optional) — set `window.WLEDGER_SCAN` before this script loads:
 *   window.WLEDGER_SCAN = {
 *     maxInterval: 80,          // ms between keys to still count as a burst
 *     minLength: 3,             // ignore tiny runs (manual typing)
 *     prefix: '',               // optional scanner prefix to strip
 *     terminators: ['Enter', 'Tab']
 *   };
 * The defaults are tuned to work with both USB and slower Bluetooth HID
 * scanners.
 */

(function () {
    const cfg = Object.assign({
        maxInterval: 80,
        minLength: 3,
        prefix: '',
        terminators: ['Enter', 'Tab']
    }, window.WLEDGER_SCAN || {});

    let buffer = '';
    let lastKeyTime = 0;

    function reset() {
        buffer = '';
        lastKeyTime = 0;
    }

    function emit(rawCode) {
        let code = rawCode.trim();
        if (cfg.prefix && code.startsWith(cfg.prefix)) {
            code = code.slice(cfg.prefix.length);
        }
        if (!code) return;
        document.dispatchEvent(new CustomEvent('fastscan', {
            detail: { code: code },
            bubbles: true
        }));
    }

    document.addEventListener('keydown', function (e) {
        // Terminator ends a scan (Enter/CR, or Tab for scanners configured that way).
        if (cfg.terminators.includes(e.key) || e.key === '\n' || e.key === '\r') {
            if (buffer.length >= cfg.minLength) {
                const code = buffer;
                reset();
                emit(code);
            } else {
                reset();
            }
            return;
        }

        // Only printable single characters (scanners send one key event per
        // character; Shift may accompany for uppercase).
        if (e.key.length !== 1 || e.ctrlKey || e.metaKey || e.altKey) {
            if (e.key.length !== 1) {
                reset();
            }
            return;
        }

        const now = Date.now();
        // Slow arrival relative to the last buffered char => not a scanner run.
        if (lastKeyTime !== 0 && now - lastKeyTime > cfg.maxInterval) {
            buffer = '';
        }
        lastKeyTime = now;
        buffer += e.key;

        // Safety cap: hardware scanners won't produce unbounded runs.
        if (buffer.length > 64) {
            buffer = '';
        }
    });
})();
