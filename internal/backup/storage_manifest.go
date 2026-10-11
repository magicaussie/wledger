package backup

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/ledspace"
)

// Current-envelope member names. The envelope is complete by definition: every
// member below is required, and the physical storage and flag sections must
// declare their own contents explicitly rather than relying on an absence.
const (
	storageManifestLegacyField      = "legacy"
	storageManifestPhysicalField    = "physical_storage"
	storageManifestSystemFlagsField = "system_flags"
	binIndexSpaceField              = "bin_index_space"
)

// StorageManifest is the complete current (version 2.0) backup envelope. It is
// the only manifest the current export writes, so one archive entry carries the
// whole installation: the historical manifest plus the physical storage graph
// and every system flag, all read in one transaction.
type StorageManifest struct {
	Version    string    `json:"version"`
	ExportedAt time.Time `json:"exported_at"`
	// Legacy is the complete historical manifest, including its own version 1.0
	// marker, so an archive remains readable by the code paths and tooling that
	// understand the legacy shape.
	Legacy          Manifest                `json:"legacy"`
	PhysicalStorage PhysicalStorageSnapshot `json:"physical_storage"`
	// SystemFlags is the complete flag set, including the absence of a key, so a
	// restore can reproduce the exact flags instead of normalizing them.
	SystemFlags []db.SystemFlag `json:"system_flags"`
}

// PhysicalStorageSnapshot is the complete physical storage graph: every cabinet
// and drawer, including archived rows, and every bin attachment. An empty graph
// is captured as empty lists so the current format always declares its physical
// storage explicitly rather than leaving it unknown.
type PhysicalStorageSnapshot struct {
	Cabinets               []db.Cabinet             `json:"cabinets"`
	Drawers                []db.Drawer              `json:"drawers"`
	BinAttachments         []db.BinDrawerAttachment `json:"bin_attachments"`
	LEDAllocations         []db.DrawerLedAllocation `json:"drawer_led_allocations"`
	LEDAllocationsDeclared bool                     `json:"-"`
}

// storageManifestTopLevelFields returns the set of top-level JSON member names
// the current envelope defines, derived from its struct tags.
func storageManifestTopLevelFields() map[string]struct{} {
	return topLevelJSONFields(reflect.TypeOf(StorageManifest{}))
}

// storageManifestRequiredFields returns the member names a current envelope
// requires: all of them, so a truncated archive is rejected rather than read as
// an installation with nothing to restore.
func storageManifestRequiredFields() map[string]struct{} {
	return requiredTopLevelJSONFields(reflect.TypeOf(StorageManifest{}))
}

// physicalStorageTopLevelFields returns the accepted member names of the
// physical storage section.
func physicalStorageTopLevelFields() map[string]struct{} {
	return topLevelJSONFields(reflect.TypeOf(PhysicalStorageSnapshot{}))
}

// physicalStorageRequiredFields returns the member names the physical storage
// section requires: all three lists must be present, so an omitted list can never
// be read as an empty one.
func physicalStorageRequiredFields() map[string]struct{} {
	fields := requiredTopLevelJSONFields(reflect.TypeOf(PhysicalStorageSnapshot{}))
	// The LED list was added additively to v2.0. New exports always include it,
	// but older current-format archives remain readable when the field is absent.
	delete(fields, "drawer_led_allocations")
	return fields
}

// requireObjectMember returns the boundary-checked members of a required nested
// object member. An absent member or a value that is not a JSON object is
// rejected, and the object itself is scanned with the same scanner the top-level
// envelope uses, so a duplicate or unknown member nested inside it is rejected
// too instead of being silently ignored.
func requireObjectMember(parent map[string]json.RawMessage, name string, allowed map[string]struct{}, where string) (map[string]json.RawMessage, error) {
	raw, ok := parent[name]
	if !ok {
		return nil, &BackupFormatError{
			Code:   FormatMissingManifestField,
			Reason: where + " is missing required member " + name,
		}
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, &BackupFormatError{
			Code:   FormatInvalidManifestField,
			Reason: where + "." + name + " must be a JSON object",
		}
	}
	return scanJSONObject(trimmed, allowed)
}

// requireJSONArray rejects a required member that is JSON null or is not an
// array, so a declared list cannot be silently absent from a complete archive.
func requireJSONArray(raw json.RawMessage, where string) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return &BackupFormatError{
			Code:   FormatInvalidManifestField,
			Reason: where + " must be a JSON array",
		}
	}
	return nil
}

// decodeCurrentStorageManifest decodes and validates the single current
// (version 2.0) manifest. It rejects malformed JSON, trailing content, duplicate
// or unknown members at every level, a member the format requires being absent or
// of the wrong shape, an unsupported envelope or embedded legacy version, a
// coordinate marker that contradicts the captured coordinate flag, and a physical
// storage graph that is not internally consistent.
//
// Every check runs here, before the restore touches the filesystem or the
// database, so an unusable or incomplete current backup is rejected with the
// installation untouched.
func decodeCurrentStorageManifest(f *zip.File) (StorageManifest, error) {
	data, err := readManifestEntry(f)
	if err != nil {
		return StorageManifest{}, err
	}

	members, err := scanJSONObject(data, storageManifestTopLevelFields())
	if err != nil {
		return StorageManifest{}, err
	}
	// A current envelope is complete by definition, so an archive that lost a
	// member is rejected instead of being read as an installation with no such
	// data. This is what stops an incomplete or hand-edited archive from clearing
	// a populated installation.
	if err := requireMembers(members, storageManifestRequiredFields(), currentStorageManifestName); err != nil {
		return StorageManifest{}, err
	}

	// The embedded legacy manifest is itself a complete manifest object, so it
	// gets the same duplicate/unknown-member boundary check and its own required
	// members. Its omitempty fields stay optional.
	legacyMembers, err := requireObjectMember(members, storageManifestLegacyField, manifestTopLevelFields(), currentStorageManifestName)
	if err != nil {
		return StorageManifest{}, err
	}
	if err := requireMembers(legacyMembers, manifestRequiredFields(), storageManifestLegacyField); err != nil {
		return StorageManifest{}, err
	}

	// The physical storage section must declare every list explicitly, so a
	// missing or null section is never read as an empty graph that would replace a
	// populated one.
	physicalMembers, err := requireObjectMember(members, storageManifestPhysicalField, physicalStorageTopLevelFields(), currentStorageManifestName)
	if err != nil {
		return StorageManifest{}, err
	}
	if err := requireMembers(physicalMembers, physicalStorageRequiredFields(), storageManifestPhysicalField); err != nil {
		return StorageManifest{}, err
	}
	for _, name := range sortedFieldNames(physicalStorageRequiredFields()) {
		if err := requireJSONArray(physicalMembers[name], storageManifestPhysicalField+"."+name); err != nil {
			return StorageManifest{}, err
		}
	}
	if raw, ok := physicalMembers["drawer_led_allocations"]; ok {
		if err := requireJSONArray(raw, storageManifestPhysicalField+".drawer_led_allocations"); err != nil {
			return StorageManifest{}, err
		}
	}
	if err := requireJSONArray(members[storageManifestSystemFlagsField], storageManifestSystemFlagsField); err != nil {
		return StorageManifest{}, err
	}

	var env StorageManifest
	if err := json.Unmarshal(data, &env); err != nil {
		return StorageManifest{}, malformedManifest("failed to parse backup JSON: " + err.Error())
	}
	_, env.PhysicalStorage.LEDAllocationsDeclared = physicalMembers["drawer_led_allocations"]

	if env.Version != supportedCurrentManifestVersion {
		return StorageManifest{}, &BackupFormatError{
			Code:   FormatUnsupportedManifestVersion,
			Reason: fmt.Sprintf("unsupported backup manifest version %q (supported: %q)", env.Version, supportedCurrentManifestVersion),
		}
	}
	if env.Legacy.Version != supportedLegacyManifestVersion {
		return StorageManifest{}, &BackupFormatError{
			Code:   FormatUnsupportedManifestVersion,
			Reason: fmt.Sprintf("unsupported embedded legacy manifest version %q (supported: %q)", env.Legacy.Version, supportedLegacyManifestVersion),
		}
	}
	if err := validateStorageGraph(env.Legacy, env.PhysicalStorage, env.SystemFlags); err != nil {
		return StorageManifest{}, err
	}
	if err := validateCoordinateSpaceConsistency(env.Legacy, env.SystemFlags); err != nil {
		return StorageManifest{}, err
	}
	return env, nil
}

// validateCoordinateSpaceConsistency requires the embedded legacy manifest's
// coordinate marker and the captured LED coordinate flag to describe the same
// space: an absent value on either side means segment, two explicit known values
// must match, and a value this build cannot interpret is rejected.
//
// The flags are only compared, never repaired: a current backup is a byte-exact
// capture of a running installation, so a disagreement is an archive defect the
// administrator must resolve, not something a restore may normalize away.
func validateCoordinateSpaceConsistency(legacy Manifest, flags []db.SystemFlag) error {
	markerSpace, err := coordinateSpaceValue(legacy.BinIndexSpace, binIndexSpaceField)
	if err != nil {
		return err
	}
	declaredFlag := ""
	for _, f := range flags {
		if f.Key == ledspace.FlagKey {
			declaredFlag = f.Value
			break
		}
	}
	flagSpace, err := coordinateSpaceValue(declaredFlag, ledspace.FlagKey)
	if err != nil {
		return err
	}
	if markerSpace != flagSpace {
		return &BackupFormatError{
			Code: FormatInconsistentCoordinateSpace,
			Reason: fmt.Sprintf("embedded legacy %s %q and system flag %s %q describe different coordinate spaces (an absent value means %q)",
				binIndexSpaceField, legacy.BinIndexSpace, ledspace.FlagKey, declaredFlag, ledspace.Segment),
		}
	}
	return nil
}

// coordinateSpaceValue resolves one declared coordinate-space value: an absent
// value means segment, a known space is returned unchanged, and any other value is
// rejected so an unrecognized space can never be mistaken for a resolved one.
func coordinateSpaceValue(value, where string) (string, error) {
	switch value {
	case "":
		return ledspace.Segment, nil
	case ledspace.Segment, ledspace.Drawer, ledspace.Unresolved:
		return value, nil
	default:
		return "", &BackupFormatError{
			Code:   FormatUnsupportedCoordinateSpace,
			Reason: fmt.Sprintf("unsupported coordinate space %q in %s", value, where),
		}
	}
}

// validateStorageGraph rejects a current snapshot whose physical storage rows are
// not internally consistent, before the restore touches the filesystem or the
// database. It checks exactly what the current format defines: identifiers and
// foreign references, exclusive controller ownership, cabinet, drawer and bin
// existence, geometry, revisions, archived flags, duplicate bin attachments and
// flag keys.
//
// It never derives topology or relationships: no bin is counted as a drawer and
// no attachment or controller assignment is inferred, so a valid snapshot is
// restored exactly as captured.
func validateStorageGraph(legacy Manifest, snap PhysicalStorageSnapshot, flags []db.SystemFlag) error {
	invalid := func(reason string, args ...any) error {
		return &BackupFormatError{
			Code:   FormatInvalidPhysicalGraph,
			Reason: fmt.Sprintf(reason, args...),
		}
	}

	controllers := make(map[int64]db.Controller, len(legacy.Controllers))
	for _, c := range legacy.Controllers {
		controllers[c.ID] = c
	}
	bins := make(map[int64]struct{}, len(legacy.Bins))
	for _, b := range legacy.Bins {
		bins[b.ID] = struct{}{}
	}

	cabinets := make(map[int64]struct{}, len(snap.Cabinets))
	ownedControllers := make(map[int64]int64, len(snap.Cabinets))
	for _, c := range snap.Cabinets {
		if c.ID <= 0 {
			return invalid("cabinet has a non-positive id %d", c.ID)
		}
		if _, dup := cabinets[c.ID]; dup {
			return invalid("duplicate cabinet id %d", c.ID)
		}
		cabinets[c.ID] = struct{}{}
		if strings.TrimSpace(c.Name) == "" {
			return invalid("cabinet %d has an empty name", c.ID)
		}
		if c.LayoutRevision < 0 || c.ElectricalRevision < 0 {
			return invalid("cabinet %d has a negative revision (layout %d, electrical %d)",
				c.ID, c.LayoutRevision, c.ElectricalRevision)
		}
		if c.Archived != 0 && c.Archived != 1 {
			return invalid("cabinet %d has a non-boolean archived flag %d", c.ID, c.Archived)
		}
		// A configured controller must exist in this backup and belong to exactly
		// one cabinet. A NULL controller is an unassigned draft and is allowed.
		if c.ControllerID.Valid {
			if _, ok := controllers[c.ControllerID.Int64]; !ok {
				return invalid("cabinet %d references controller %d, which the backup does not contain",
					c.ID, c.ControllerID.Int64)
			}
			if owner, taken := ownedControllers[c.ControllerID.Int64]; taken {
				return invalid("controller %d is claimed by cabinets %d and %d",
					c.ControllerID.Int64, owner, c.ID)
			}
			ownedControllers[c.ControllerID.Int64] = c.ID
		}
	}

	drawers := make(map[int64]struct{}, len(snap.Drawers))
	for _, d := range snap.Drawers {
		if d.ID <= 0 {
			return invalid("drawer has a non-positive id %d", d.ID)
		}
		if _, dup := drawers[d.ID]; dup {
			return invalid("duplicate drawer id %d", d.ID)
		}
		drawers[d.ID] = struct{}{}
		if strings.TrimSpace(d.Label) == "" {
			return invalid("drawer %d has an empty label", d.ID)
		}
		if _, ok := cabinets[d.CabinetID]; !ok {
			return invalid("drawer %d references cabinet %d, which the backup does not contain",
				d.ID, d.CabinetID)
		}
		if d.Archived != 0 && d.Archived != 1 {
			return invalid("drawer %d has a non-boolean archived flag %d", d.ID, d.Archived)
		}
		if reason := invalidGeometry(d.X, d.Y, d.Width, d.Height); reason != "" {
			return invalid("drawer %d has invalid geometry: %s", d.ID, reason)
		}
	}

	attached := make(map[int64]struct{}, len(snap.BinAttachments))
	for _, a := range snap.BinAttachments {
		if a.BinID <= 0 {
			return invalid("bin attachment has a non-positive bin id %d", a.BinID)
		}
		if _, dup := attached[a.BinID]; dup {
			return invalid("bin %d has more than one attachment", a.BinID)
		}
		attached[a.BinID] = struct{}{}
		if _, ok := drawers[a.DrawerID]; !ok {
			return invalid("bin attachment %d references drawer %d, which the backup does not contain",
				a.BinID, a.DrawerID)
		}
		if _, ok := bins[a.BinID]; !ok {
			return invalid("bin attachment %d references bin %d, which the backup does not contain",
				a.BinID, a.BinID)
		}
		if reason := invalidGeometry(a.X, a.Y, a.Width, a.Height); reason != "" {
			return invalid("bin attachment %d has invalid geometry: %s", a.BinID, reason)
		}
	}

	allocationIDs := make(map[int64]struct{}, len(snap.LEDAllocations))
	allocationRanges := make([]struct{ controllerID, start, end, allocationID int64 }, 0, len(snap.LEDAllocations))
	cabinetByDrawer := make(map[int64]int64, len(snap.Drawers))
	for _, drawer := range snap.Drawers {
		cabinetByDrawer[drawer.ID] = drawer.CabinetID
	}
	cabinetByID := make(map[int64]db.Cabinet, len(snap.Cabinets))
	for _, cabinet := range snap.Cabinets {
		cabinetByID[cabinet.ID] = cabinet
	}
	for _, allocation := range snap.LEDAllocations {
		if allocation.ID <= 0 {
			return invalid("drawer LED allocation has a non-positive id %d", allocation.ID)
		}
		if _, dup := allocationIDs[allocation.ID]; dup {
			return invalid("duplicate drawer LED allocation id %d", allocation.ID)
		}
		allocationIDs[allocation.ID] = struct{}{}
		cabinetID, drawerExists := cabinetByDrawer[allocation.DrawerID]
		if !drawerExists || cabinetID != allocation.CabinetID {
			return invalid("drawer LED allocation %d references drawer %d outside cabinet %d", allocation.ID, allocation.DrawerID, allocation.CabinetID)
		}
		cabinet, cabinetExists := cabinetByID[allocation.CabinetID]
		controller, controllerExists := controllers[allocation.ControllerID]
		if !cabinetExists || !cabinet.ControllerID.Valid || cabinet.ControllerID.Int64 != allocation.ControllerID || !controllerExists {
			return invalid("drawer LED allocation %d does not match its cabinet's configured controller", allocation.ID)
		}
		if allocation.DisplayOrder < 0 || allocation.StartPixel < 1 || allocation.PixelCount < 1 || allocation.PixelCount > math.MaxInt64-allocation.StartPixel+1 {
			return invalid("drawer LED allocation %d has an invalid start, count, order or overflowing end", allocation.ID)
		}
		end := allocation.StartPixel + allocation.PixelCount - 1
		if controller.LedCount < 1 || end > controller.LedCount {
			return invalid("drawer LED allocation %d exceeds controller #%d capacity", allocation.ID, allocation.ControllerID)
		}
		for _, other := range allocationRanges {
			if other.controllerID == allocation.ControllerID && allocation.StartPixel <= other.end && other.start <= end {
				return invalid("drawer LED allocations %d and %d overlap on controller #%d", allocation.ID, other.allocationID, allocation.ControllerID)
			}
		}
		allocationRanges = append(allocationRanges, struct{ controllerID, start, end, allocationID int64 }{allocation.ControllerID, allocation.StartPixel, end, allocation.ID})
	}

	flagKeys := make(map[string]struct{}, len(flags))
	for _, f := range flags {
		if strings.TrimSpace(f.Key) == "" {
			return invalid("system flag has an empty key")
		}
		if _, dup := flagKeys[f.Key]; dup {
			return invalid("duplicate system flag key %q", f.Key)
		}
		flagKeys[f.Key] = struct{}{}
	}
	return nil
}

// invalidGeometry reports why a rectangular placement is unusable, or "" when it
// is valid: every scalar must be finite, x and y must be non-negative, and width
// and height must be strictly positive.
func invalidGeometry(x, y, width, height float64) string {
	for _, v := range []struct {
		name  string
		value float64
	}{
		{"x", x},
		{"y", y},
		{"width", width},
		{"height", height},
	} {
		if math.IsNaN(v.value) || math.IsInf(v.value, 0) {
			return v.name + " is not a finite number"
		}
	}
	if x < 0 || y < 0 {
		return "x and y must be non-negative"
	}
	if width <= 0 || height <= 0 {
		return "width and height must be positive"
	}
	return ""
}
