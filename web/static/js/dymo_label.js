/**
 * dymo_label.js — Alpine component for printing DYMO LabelWriter labels.
 *
 * Renders a preview and a print-only copy of a label at the true physical
 * size for the selected DYMO roll, and injects an @page rule so the browser
 * print dialog prints to the exact label dimensions.
 */
document.addEventListener('alpine:init', () => {
    Alpine.data('dymoLabel', (prefix, qrURL) => ({
        prefix: prefix,
        sizes: [
            { id: '30252', name: '1" x 1" Square', w: 25, h: 25 },
            { id: '30335', name: '2" x 1/2"', w: 52, h: 13 },
            { id: '30334', name: '2-1/16" x 1"', w: 52, h: 25 },
            { id: '30347', name: '2" x 1-1/4" Address', w: 52, h: 31 },
            { id: '30336', name: '2" x 1-1/2"', w: 52, h: 38 },
            { id: '30346', name: '2" x 3"', w: 52, h: 76 },
            { id: '30345', name: '2" x 4"', w: 52, h: 101 },
            { id: '30329', name: '3-1/8" x 2-1/8" Shipping', w: 79, h: 54 },
            { id: '30328', name: '3-1/8" x 5" Shipping', w: 79, h: 127 }
        ],
        selected: 0,
        title: '',
        subtext: '',
        qrSrc: '',

        init() {
            // Seed fields from element (set by templ caller if desired).
            const el = this.$root;
            this.title = el.dataset.title || '';
            this.subtext = el.dataset.subtext || '';
            this.qrSrc = qrURL || el.dataset.qr || '';
            // Default to a practical size for product/bins: address label.
            // Leave index 0 (1" x 1") as default unless a better one is passed.
        },

        onSizeChange() {},

        printStyle() {
            const s = this.sizes[this.selected];
            return `
                @page {
                    size: ${s.w}mm ${s.h}mm;
                    margin: 0;
                }
                @media print {
                    body * { visibility: hidden; }
                    #dymo-print-${this.prefix}, #dymo-print-${this.prefix} * { visibility: visible; }
                    #dymo-print-${this.prefix} { position: absolute; left: 0; top: 0; }
                    .dymo-label { page-break-inside: avoid; }
                }
            `;
        },

        doPrint() {
            window.print();
        }
    }));
});