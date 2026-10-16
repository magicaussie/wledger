package backup

import (
	"context"
	"encoding/hex"
	"fmt"
	"github.com/tuxedocurly/wledger/internal/db"
)

type PhysicalBinPixelConflictError struct{}

func (*PhysicalBinPixelConflictError) Error() string {
	return "restore refused: older backup omits physical bin pixel overrides that exist here; export a new complete backup first"
}

func preserveBinPixels(ctx context.Context, q db.Querier, current *StorageManifest) error {
	if current != nil && current.PhysicalStorage.BinPixelsDeclared {
		return nil
	}
	led, err := db.RequireLEDQuerier(q)
	if err != nil {
		return err
	}
	rows, err := led.ListPhysicalBinPixels(ctx)
	if err != nil {
		return err
	}
	if len(rows) > 0 {
		return &PhysicalBinPixelConflictError{}
	}
	return nil
}

func validateBinPixelGraph(legacy Manifest, snap PhysicalStorageSnapshot) error {
	invalid := func(message string) error {
		return &BackupFormatError{Code: FormatInvalidPhysicalGraph, Reason: "physical bin pixels: " + message}
	}
	bins := map[int64]bool{}
	for _, b := range legacy.Bins {
		bins[b.ID] = true
	}
	controllers := map[int64]bool{}
	for _, c := range legacy.Controllers {
		controllers[c.ID] = true
	}
	allocations := map[int64]db.DrawerLedAllocation{}
	for _, a := range snap.LEDAllocations {
		allocations[a.ID] = a
	}
	seen := map[int64]bool{}
	logical := map[[2]int64]bool{}
	physical := map[[2]int64]bool{}
	for _, p := range snap.BinPixels {
		a, ok := allocations[p.AllocationID]
		digest, err := hex.DecodeString(p.TopologyFingerprint)
		if !bins[p.BinID] || !controllers[p.ControllerID] || !ok || a.DrawerID != p.DrawerID || p.Offset < 0 || p.Offset >= a.PixelCount || p.Pixel < 0 || err != nil || len(digest) != 32 {
			return invalid(fmt.Sprintf("invalid reference, offset, pixel or fingerprint for bin %d", p.BinID))
		}
		l := [2]int64{p.AllocationID, p.Offset}
		physicalKey := [2]int64{p.ControllerID, p.Pixel}
		if seen[p.BinID] || logical[l] || physical[physicalKey] {
			return invalid("duplicate bin or pixel assignment")
		}
		seen[p.BinID] = true
		logical[l] = true
		physical[physicalKey] = true
	}
	return nil
}
