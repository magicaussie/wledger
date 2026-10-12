package backup

import (
	"context"
	"fmt"

	"github.com/tuxedocurly/wledger/internal/db"
)

// PhysicalStorageConflictError reports that a restore was refused because it
// would discard physical storage the archive cannot reproduce. A legacy (1.0)
// archive carries no physical storage rows, so restoring it over a populated
// physical graph would destroy cabinets, drawers and bin attachments with no way
// to recover them. It is returned before the uploads staging cleanup and before
// any database mutation, so a refused restore changes nothing.
type PhysicalStorageConflictError struct {
	Reason string
}

func (e *PhysicalStorageConflictError) Error() string {
	return "restore refused: " + e.Reason
}

// DrawerLEDAllocationConflictError prevents an older v2.0 archive that predates
// persistent drawer LED allocations from silently clearing mappings it cannot
// reproduce.
type DrawerLEDAllocationConflictError struct {
	Reason string
}

func (e *DrawerLEDAllocationConflictError) Error() string {
	return "restore refused: " + e.Reason
}

// CabinetControllerConflictError prevents an older v2.0 archive that predates the
// cabinet controller membership table from silently erasing secondary controller
// assignments it cannot reproduce. It is the preservation analogue of
// DrawerLEDAllocationConflictError for the electrical membership graph.
type CabinetControllerConflictError struct {
	Reason string
}

func (e *CabinetControllerConflictError) Error() string {
	return "restore refused: " + e.Reason
}

type CabinetWiringConflictError struct {
	Reason string
}

func (e *CabinetWiringConflictError) Error() string {
	return "restore refused: " + e.Reason
}

type ActivePhysicalLocateConflictError struct {
	Reason string
}

func (e *ActivePhysicalLocateConflictError) Error() string {
	return "restore refused: " + e.Reason
}

// physicalGraphPopulated reports whether the installation holds any cabinet,
// drawer or bin attachment. Every table is read so a partially populated graph is
// still detected rather than assumed impossible.
func physicalGraphPopulated(ctx context.Context, q db.Querier) (bool, error) {
	cabinets, err := q.ListCabinetsIncludingArchived(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to read cabinets: %w", err)
	}
	if len(cabinets) > 0 {
		return true, nil
	}
	drawers, err := q.ListAllDrawers(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to read drawers: %w", err)
	}
	if len(drawers) > 0 {
		return true, nil
	}
	attachments, err := q.ListBinDrawerAttachments(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to read bin attachments: %w", err)
	}
	return len(attachments) > 0, nil
}

// secondaryCabinetMembershipsExist reports whether the installation holds any
// cabinet controller membership that is not the legacy primary pointer
// (cabinet_id, cabinets.controller_id). Such a membership is a secondary
// controller assignment: an older archive that carries no membership list can
// only reproduce each cabinet's own configured controller as slot 0, so restoring
// it would discard every other membership.
func secondaryCabinetMembershipsExist(ctx context.Context, q db.Querier) (bool, error) {
	memberships, err := q.ListAllCabinetControllers(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to read cabinet controllers: %w", err)
	}
	if len(memberships) == 0 {
		return false, nil
	}
	cabinets, err := q.ListCabinetsIncludingArchived(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to read cabinets: %w", err)
	}
	primary := make(map[[2]int64]struct{}, len(cabinets))
	for _, c := range cabinets {
		if c.ControllerID.Valid {
			primary[[2]int64{c.ID, c.ControllerID.Int64}] = struct{}{}
		}
	}
	for _, m := range memberships {
		if _, ok := primary[[2]int64{m.CabinetID, m.ControllerID}]; !ok {
			return true, nil
		}
	}
	return false, nil
}

func activePhysicalLocateSessionExists(ctx context.Context, q db.Querier) (bool, error) {
	owned, err := db.HasPhysicalLocateOwnership(ctx, q)
	if err != nil {
		return false, fmt.Errorf("failed to read durable physical Locate ownership: %w", err)
	}
	return owned, nil
}

func cabinetWiringExists(ctx context.Context, q db.Querier) (bool, error) {
	strings, err := q.ListAllCabinetLEDStrings(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to read cabinet LED strings: %w", err)
	}
	sections, err := q.ListAllCabinetLEDPathSections(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to read cabinet LED path sections: %w", err)
	}
	memberships, err := q.ListAllCabinetControllers(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to read cabinet wiring configuration state: %w", err)
	}
	if len(strings) != 0 || len(sections) != 0 {
		return true, nil
	}
	for _, member := range memberships {
		if member.StringsConfigured != 0 {
			return true, nil
		}
	}
	return false, nil
}

// clearPhysicalStorage empties the physical storage tables. It must run before
// the legacy bin and controller clears: bin attachments reference bins and
// drawers, drawers reference cabinets and cabinets reference controllers, so the
// restrictive foreign keys would otherwise block the legacy clears.
func clearPhysicalStorage(ctx context.Context, q db.Querier) error {
	if err := q.ClearDrawerLEDAllocations(ctx); err != nil {
		return fmt.Errorf("failed to clear drawer LED allocations: %w", err)
	}
	if err := q.ClearCabinetLEDPathSections(ctx); err != nil {
		return fmt.Errorf("failed to clear cabinet LED path sections: %w", err)
	}
	if err := q.ClearCabinetLEDStrings(ctx); err != nil {
		return fmt.Errorf("failed to clear cabinet LED strings: %w", err)
	}
	if err := q.ClearBinDrawerAttachments(ctx); err != nil {
		return fmt.Errorf("failed to clear bin attachments: %w", err)
	}
	if err := q.ClearDrawers(ctx); err != nil {
		return fmt.Errorf("failed to clear drawers: %w", err)
	}
	// Memberships reference cabinets and reference every controller an allocation
	// may use, so they are cleared after the allocations that depend on them and
	// before the cabinets they belong to.
	if err := q.ClearCabinetControllers(ctx); err != nil {
		return fmt.Errorf("failed to clear cabinet controllers: %w", err)
	}
	if err := q.ClearCabinets(ctx); err != nil {
		return fmt.Errorf("failed to clear cabinets: %w", err)
	}
	return nil
}

// restorePhysicalStorage writes the captured physical graph after the legacy
// controllers and bins it references. Every id, revision, archived flag and
// timestamp is preserved, and nothing is derived: the rows restored are exactly
// the rows captured.
func restorePhysicalStorage(ctx context.Context, q db.Querier, snap PhysicalStorageSnapshot) error {
	for _, c := range snap.Cabinets {
		width, height := c.LayoutWidth, c.LayoutHeight
		if width <= 0 || height <= 0 { // backups created before M7 had no bounds
			width, height = 100, 100
		}
		decorations := c.LayoutDecorationsJson
		if decorations == "" { // backups written before the canvas used no Paint layer
			decorations = "[]"
		}
		if err := q.RestoreCabinet(ctx, db.RestoreCabinetParams{ID: c.ID, Name: c.Name, ControllerID: c.ControllerID, CoordinateSystem: c.CoordinateSystem, LayoutRevision: c.LayoutRevision, ElectricalRevision: c.ElectricalRevision, DisplayOrder: c.DisplayOrder, Archived: c.Archived, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt, LayoutWidth: width, LayoutHeight: height, LayoutDecorationsJson: decorations}); err != nil {
			return fmt.Errorf("cabinet restore: %w", err)
		}
	}
	// Cabinet controller memberships are written after their cabinets and the
	// legacy controllers they reference, and before the allocations keyed on them.
	// A declared snapshot is restored exactly; an older archive that predates the
	// membership table derives each cabinet's configured controller as slot 0.
	for _, m := range resolveCabinetControllers(snap) {
		if err := q.RestoreCabinetController(ctx, db.RestoreCabinetControllerParams(m)); err != nil {
			return fmt.Errorf("cabinet controller restore: %w", err)
		}
	}
	for _, stringRecord := range snap.CabinetLEDStrings {
		if err := q.RestoreCabinetLEDString(ctx, db.RestoreCabinetLEDStringParams(stringRecord)); err != nil {
			return fmt.Errorf("cabinet LED string restore: %w", err)
		}
	}
	for _, section := range snap.CabinetLEDPathSections {
		if err := q.RestoreCabinetLEDPathSection(ctx, db.RestoreCabinetLEDPathSectionParams(section)); err != nil {
			return fmt.Errorf("cabinet LED path section restore: %w", err)
		}
	}
	for _, d := range snap.Drawers {
		if err := q.RestoreDrawer(ctx, db.RestoreDrawerParams(d)); err != nil {
			return fmt.Errorf("drawer restore: %w", err)
		}
	}
	for _, a := range snap.BinAttachments {
		if err := q.RestoreBinDrawerAttachment(ctx, db.RestoreBinDrawerAttachmentParams(a)); err != nil {
			return fmt.Errorf("bin attachment restore: %w", err)
		}
	}
	for _, allocation := range snap.LEDAllocations {
		if err := q.RestoreDrawerLEDAllocation(ctx, db.RestoreDrawerLEDAllocationParams(allocation)); err != nil {
			return fmt.Errorf("drawer LED allocation restore: %w", err)
		}
	}
	return nil
}

// restoreSystemFlags replaces the installation's flag set with the exact set
// captured by a current backup. The current format carries every flag, including
// the absence of a key, so the legacy automatic flag normalization (setting the
// LED coordinate space and the migration markers) must not run alongside it.
func restoreSystemFlags(ctx context.Context, q db.Querier, flags []db.SystemFlag) error {
	if err := q.DeleteAllSystemFlags(ctx); err != nil {
		return fmt.Errorf("failed to clear system flags: %w", err)
	}
	for _, f := range flags {
		if err := q.RestoreSystemFlag(ctx, db.RestoreSystemFlagParams(f)); err != nil {
			return fmt.Errorf("system flag restore: %w", err)
		}
	}
	return nil
}
