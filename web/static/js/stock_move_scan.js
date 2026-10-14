(() => {
    let scanQueue = Promise.resolve();

    window.openStockMoveModal = function (modalID) {
        const dialog = document.getElementById(modalID);
        if (!dialog) return;
        if (!dialog.open) dialog.showModal();
        dialog.dispatchEvent(new CustomEvent('wledger:stock-move-open', { bubbles: true }));
    };

    window.stockMoveScan = function (modalID, partID, sourceBinID) {
        return {
            modalID,
            partID,
            sourceBinID,
            sourceConfirmed: sourceBinID <= 0,
            productConfirmed: false,
            selectedBinID: '',
            hasSelection: false,
            manualMode: false,
            selectedCabinetID: 0,
            selectedDrawerID: 0,
            error: '',
            pickerID: `${modalID}_visual_bin_picker`,
            dialog: null,
            openListener: null,
            closeListener: null,

            init() {
                this.dialog = document.getElementById(this.modalID);
                this.openListener = () => this.activate();
                this.closeListener = () => this.deactivate();
                this.dialog?.addEventListener('wledger:stock-move-open', this.openListener);
                this.dialog?.addEventListener('close', this.closeListener);
            },

            activate() {
                this.sourceConfirmed = this.sourceBinID <= 0;
                this.productConfirmed = false;
                this.selectedBinID = '';
                this.hasSelection = false;
                this.manualMode = false;
                this.selectedCabinetID = 0;
                this.selectedDrawerID = 0;
                this.error = '';
                scanQueue = Promise.resolve();
                window.activateBinPicker?.(this.pickerID);
                window.__wledgerScanContext = { handleScan: code => this.handleScan(code) };
            },

            deactivate() {
                if (window.__wledgerScanContext) window.__wledgerScanContext = null;
                window.deactivateBinPicker?.(this.pickerID);
            },

            useManualSelection() {
                this.manualMode = true;
                this.error = '';
            },

            useScanWorkflow() {
                this.manualMode = false;
                this.error = '';
            },

            instruction() {
                if (this.manualMode) return 'Manual mode: select a destination bin below, or scan a cabinet/drawer followed by its bin.';
                if (!this.sourceConfirmed) return `Scan the source bin #${this.sourceBinID} to confirm where the stock is coming from.`;
                if (!this.productConfirmed) return `Scan product #${this.partID} to confirm which product is being moved.`;
                if (this.selectedDrawerID) return 'Destination drawer selected. Scan one of its bins to finish the transfer.';
                if (this.selectedCabinetID) return 'Destination cabinet selected. Scan a drawer, then a bin, or scan a bin directly.';
                return 'Scan a destination cabinet or drawer, then its bin. A bin can also be scanned directly.';
            },

            get canSubmit() {
                return this.hasSelection && (this.manualMode || (this.sourceConfirmed && this.productConfirmed));
            },

            handleScan(code) {
                scanQueue = scanQueue
                    .then(() => this.processScan(code))
                    .catch(() => { this.error = 'Unable to process this scan. Try again.'; });
            },

            async processScan(code) {
                this.error = '';
                let response;
                let resolution;
                try {
                    response = await fetch(`/scan/resolve?q=${encodeURIComponent(code)}`, {
                        headers: { Accept: 'application/json' },
                        cache: 'no-store'
                    });
                    if (!response.ok) {
                        const message = (await response.text()).trim();
                        this.error = message || 'This scan could not be resolved.';
                        return;
                    }
                    resolution = await response.json();
                } catch (error) {
                    this.error = 'Unable to resolve this scan. Check the connection and try again.';
                    return;
                }

                if (resolution.type === 'legacy-controller' || resolution.type === 'legacy-container') {
                    this.error = 'This is a legacy electrical controller/container label, not a physical storage location.';
                    return;
                }

                if (resolution.type === 'part') {
                    if (resolution.id !== this.partID) {
                        this.error = `Scanned product #${resolution.id}; this transfer is for product #${this.partID}.`;
                        return;
                    }
                    this.productConfirmed = true;
                    this.error = 'Product confirmed.';
                    return;
                }

                if (resolution.type === 'bin' && !this.manualMode && !this.sourceConfirmed) {
                    if (resolution.id !== this.sourceBinID) {
                        this.error = `Scanned bin #${resolution.id}; the source is bin #${this.sourceBinID}.`;
                        return;
                    }
                    this.sourceConfirmed = true;
                    this.error = 'Source bin confirmed.';
                    return;
                }

                if (this.manualMode || (this.sourceConfirmed && this.productConfirmed)) {
                    this.handleDestinationScan(resolution);
                    return;
                }
                this.error = 'Confirm the source bin and product before selecting a destination.';
            },

            handleDestinationScan(resolution) {
                const picker = window.__binPickerContext;
                if (!picker || picker.root?.id !== this.pickerID) {
                    this.error = 'The destination picker is not ready. Wait for its bins to load and scan again.';
                    return;
                }

                if (resolution.type === 'physical-cabinet') {
                    if (!picker.filterCabinet(resolution.id)) {
                        this.error = `Physical cabinet #${resolution.id} has no selectable destination bins.`;
                        return;
                    }
                    this.selectedCabinetID = resolution.id;
                    this.selectedDrawerID = 0;
                    this.error = `Physical cabinet #${resolution.id} selected; scan a drawer or bin.`;
                    return;
                }

                if (resolution.type === 'physical-drawer') {
                    if (this.selectedCabinetID && resolution.cabinet_id !== this.selectedCabinetID) {
                        this.error = `Drawer #${resolution.id} is not in the selected cabinet.`;
                        return;
                    }
                    const location = picker.filterDrawer(resolution.id);
                    if (!location) {
                        this.error = `Physical drawer #${resolution.id} has no selectable destination bins.`;
                        return;
                    }
                    this.selectedCabinetID = location.cabinetID;
                    this.selectedDrawerID = resolution.id;
                    this.error = `Physical drawer #${resolution.id} selected; scan one of its bins.`;
                    return;
                }

                if (resolution.type === 'bin') {
                    const location = picker.locationOfBin(resolution.id);
                    if (!location) {
                        this.error = `Bin #${resolution.id} is not available in the destination picker.`;
                        return;
                    }
                    if (this.selectedCabinetID && location.cabinetID !== this.selectedCabinetID) {
                        this.error = `Bin #${resolution.id} is outside the selected cabinet.`;
                        return;
                    }
                    if (this.selectedDrawerID && location.drawerID !== this.selectedDrawerID) {
                        this.error = `Bin #${resolution.id} is outside the selected drawer.`;
                        return;
                    }
                    picker.select(resolution.id);
                    this.error = `Destination bin #${resolution.id} selected. Review and confirm the transfer.`;
                    return;
                }

                this.error = 'Scan a physical cabinet, physical drawer, or inventory bin for the destination.';
            }
        };
    };
})();
