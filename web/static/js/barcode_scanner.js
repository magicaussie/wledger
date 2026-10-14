(() => {
    const stateKey = '__wledgerBarcodeScanner';
    if (window[stateKey]) {
        window.__wledgerCleanupScannerDuplicates?.();
        return;
    }

    const state = {
        scanner: null,
        activeTargetInputId: null,
        generation: 0,
        running: false,
        clearPromise: null,
        boundModal: null,
        controllerScript: document.currentScript,
    };
    window[stateKey] = state;

    function cleanupScannerDuplicates() {
        const main = document.getElementById('main-content');
        const modals = Array.from(document.querySelectorAll('#global_scanner_modal'));
        const canonicalModal = modals.find(modal => !main?.contains(modal)) || modals[0];
        for (const modal of modals) if (modal !== canonicalModal) modal.remove();
        for (const script of document.querySelectorAll('script[src="/static/js/barcode_scanner.js"]')) {
            if (script !== state.controllerScript) script.remove();
        }
    }
    window.__wledgerCleanupScannerDuplicates = cleanupScannerDuplicates;
    document.addEventListener('htmx:afterSwap', cleanupScannerDuplicates);

    window.startGlobalScanner = async function (targetInputId) {
        if (!window.isSecureContext) {
            alert(
                "Camera access is blocked because this page is not served over HTTPS.\n\n" +
                "To fix this:\n" +
                "1. Use a Reverse Proxy with HTTPS (Caddy/Nginx)\n" +
                "2. Or enable the 'Insecure origins' flag in your browser settings for this IP."
            );
            return;
        }

        const modal = document.getElementById('global_scanner_modal');
        if (!modal || !document.getElementById(targetInputId)) return;
        state.activeTargetInputId = targetInputId;
        const generation = ++state.generation;
        if (!modal.open) modal.showModal();
        bindModalClose(modal);

        try {
            if (state.clearPromise) await state.clearPromise;
            if (generation !== state.generation || !state.activeTargetInputId) return;
            if (!state.scanner) {
                state.scanner = new Html5QrcodeScanner(
                    'reader',
                    {
                        fps: 10,
                        qrbox: { width: 250, height: 250 },
                        aspectRatio: 1.0,
                        showTorchButtonIfSupported: true,
                        formatsToSupport: [
                            Html5QrcodeSupportedFormats.QR_CODE,
                            Html5QrcodeSupportedFormats.CODE_128,
                        ],
                    },
                    false
                );
            }
            if (state.running) return;
            state.scanner.render(onGlobalScanSuccess, () => {
                // Standard scanning errors are transient and intentionally ignored.
            });
            state.running = true;
        } catch (error) {
            state.running = false;
            console.warn('Scanner start warning:', error);
        }
    };

    window.stopGlobalScanner = function (closeModal = true) {
        state.generation++;
        state.activeTargetInputId = null;
        const modal = document.getElementById('global_scanner_modal');
        if (modal?.open && closeModal) modal.close();
        if (!state.scanner || !state.running) return;

        state.running = false;
        const scanner = state.scanner;
        state.clearPromise = (async () => {
            try {
                await scanner.clear();
            } catch (error) {
                console.warn('Scanner clear warning:', error);
            }
        })().finally(() => {
            state.clearPromise = null;
        });
    };

    function bindModalClose(modal) {
        if (state.boundModal === modal) return;
        state.boundModal = modal;
        modal.addEventListener('close', () => window.stopGlobalScanner(false));
    }

    function onGlobalScanSuccess(decodedText) {
        const targetInputId = state.activeTargetInputId;
        if (!targetInputId) return;
        if (!window.__wledgerScanContext) {
            const input = document.getElementById(targetInputId);
            if (input) {
                input.value = decodedText;
                input.dispatchEvent(new Event('input', { bubbles: true }));
                input.dispatchEvent(new Event('change', { bubbles: true }));
            }
        }
        window.stopGlobalScanner();
        document.dispatchEvent(new CustomEvent('fastscan', {
            detail: { code: decodedText, targetInputId }
        }));
    }
})();
