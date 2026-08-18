/**
 * fast_scan.js — keyboard-wedge barcode/QR scanner support.
 *
 * USB wireless barcode scanners present as keyboards: they type the decoded
 * value very quickly and then send Enter. This script buffers keystrokes and,
 * when a burst is followed by Enter, fires a `fastscan` CustomEvent carrying
 * the decoded value (trimmed).
 *
 * Pages/scripts listen with:
 *   document.addEventListener('fastscan', (e) => console.log(e.detail.code));
 *
 * A scan is considered "fast" when characters arrive in quick succession (a
 * hardware scanner emits keys with very small, regular gaps). Manual typing
 * produces larger, irregular gaps and is ignored so normal text entry is not
 * hijacked.
 */

(function () {
    const MAX_INTERVAL = 50;      // ms between keys to still count as a burst
    const MIN_LENGTH = 3;          // ignore tiny runs (manual typing)
    let buffer = '';
    let lastKeyTime = 0;

    document.addEventListener('keydown', function (e) {
        if (e.key === 'Enter') {
            if (buffer.length >= MIN_LENGTH) {
                const code = buffer.trim();
                buffer = '';
                lastKeyTime = 0;
                if (code) {
                    document.dispatchEvent(new CustomEvent('fastscan', {
                        detail: { code: code },
                        bubbles: true
                    }));
                }
            } else {
                buffer = '';
                lastKeyTime = 0;
            }
            return;
        }

        // Only printable single characters (scanners send one key event per
        // character; Shift may accompany for uppercase).
        if (e.key.length !== 1 || e.ctrlKey || e.metaKey || e.altKey) {
            if (e.key.length !== 1) {
                buffer = '';
                lastKeyTime = 0;
            }
            return;
        }

        const now = Date.now();
        // Slow arrival relative to the last buffered char => not a scanner run.
        if (lastKeyTime !== 0 && now - lastKeyTime > MAX_INTERVAL) {
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