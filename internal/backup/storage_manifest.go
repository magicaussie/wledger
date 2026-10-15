package backup

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/tuxedocurly/wledger/internal/controllerconfig"
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

// StorageManifest is the complete current (version 2.3) backup envelope.
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

func (m StorageManifest) MarshalJSON() ([]byte, error) {
	type storageManifestAlias StorageManifest
	if m.Version != supportedPreviousManifestVersion {
		if m.Legacy.Categories == nil {
			m.Legacy.Categories = []db.Category{}
		}
		if m.Legacy.PartCategories == nil {
			m.Legacy.PartCategories = []db.PartCategory{}
		}
		if m.Legacy.CategoryAttributes == nil {
			m.Legacy.CategoryAttributes = []db.CategoryAttribute{}
		}
		if m.Legacy.PartAttributeValues == nil {
			m.Legacy.PartAttributeValues = []db.PartAttributeValue{}
		}
	}
	if (m.Version == supportedProductDetailsManifestVersion || m.Version == supportedCurrentManifestVersion) && m.Legacy.PartDetails == nil {
		m.Legacy.PartDetails = []db.PartDetail{}
	}
	if m.Version == supportedCurrentManifestVersion && m.Legacy.ControllerOutputs == nil {
		m.Legacy.ControllerOutputs = []db.ControllerOutput{}
	}
	return json.Marshal(storageManifestAlias(m))
}

// PhysicalStorageSnapshot is the complete physical storage graph: every cabinet
// and drawer, including archived rows, every bin attachment, every cabinet
// controller membership and every drawer LED allocation. An empty graph is
// captured as empty lists so the current format always declares its physical
// storage explicitly rather than leaving it unknown.
type PhysicalStorageSnapshot struct {
	Cabinets                  []db.Cabinet               `json:"cabinets"`
	Drawers                   []db.Drawer                `json:"drawers"`
	BinAttachments            []db.BinDrawerAttachment   `json:"bin_attachments"`
	CabinetControllers        []db.CabinetController     `json:"cabinet_controllers"`
	LEDAllocations            []db.DrawerLedAllocation   `json:"drawer_led_allocations"`
	CabinetLEDStrings         []db.CabinetLedString      `json:"cabinet_led_strings"`
	CabinetLEDPathSections    []db.CabinetLedPathSection `json:"cabinet_led_path_sections"`
	AllocationCoordinateSpace string                     `json:"allocation_coordinate_space,omitempty"`
	LEDAllocationsDeclared    bool                       `json:"-"`
	CabinetWiringDeclared     bool                       `json:"-"`
	// CabinetControllersDeclared distinguishes an explicit (possibly empty)
	// membership list from the field being absent. An older current-format archive
	// predates the membership table, so its absence is read with legacy
	// single-controller semantics rather than as an empty membership set.
	CabinetControllersDeclared bool `json:"-"`
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
// section requires: the core lists must be present, so an omitted list can never
// be read as an empty one.
func physicalStorageRequiredFields() map[string]struct{} {
	fields := requiredTopLevelJSONFields(reflect.TypeOf(PhysicalStorageSnapshot{}))
	// The LED list and the cabinet controller membership list were added additively
	// to v2.0. New exports always include them, but older current-format archives
	// remain readable when the fields are absent.
	delete(fields, "drawer_led_allocations")
	delete(fields, "cabinet_controllers")
	delete(fields, "cabinet_led_strings")
	delete(fields, "cabinet_led_path_sections")
	delete(fields, "allocation_coordinate_space")
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

// decodeCurrentStorageManifest decodes and validates the current
// (version 2.3) manifest and compatible 2.0/2.1/2.2 archives. It rejects malformed JSON, trailing content, duplicate
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

	var envelopeVersion string
	if err := json.Unmarshal(members["version"], &envelopeVersion); err != nil {
		return StorageManifest{}, malformedManifest("current backup version must be a string")
	}
	if envelopeVersion != supportedCurrentManifestVersion && envelopeVersion != supportedProductDetailsManifestVersion && envelopeVersion != supportedCategoryManifestVersion && envelopeVersion != supportedPreviousManifestVersion {
		return StorageManifest{}, &BackupFormatError{Code: FormatUnsupportedManifestVersion, Reason: fmt.Sprintf("unsupported backup manifest version %q", envelopeVersion)}
	}

	// The embedded legacy manifest gets strict boundary validation. Category lists
	// became required in 2.1, product details in 2.2, and controller outputs in 2.3.
	legacyMembers, err := requireObjectMember(members, storageManifestLegacyField, manifestTopLevelFields(), currentStorageManifestName)
	if err != nil {
		return StorageManifest{}, err
	}
	legacyRequired := manifestRequiredFields()
	categoryFields := []string{"categories", "part_categories", "category_attributes", "part_attribute_values"}
	if envelopeVersion != supportedPreviousManifestVersion {
		for _, name := range categoryFields {
			legacyRequired[name] = struct{}{}
		}
	}
	if envelopeVersion == supportedProductDetailsManifestVersion || envelopeVersion == supportedCurrentManifestVersion {
		legacyRequired["part_details"] = struct{}{}
	}
	if envelopeVersion == supportedCurrentManifestVersion {
		legacyRequired["controller_outputs"] = struct{}{}
	}
	if err := requireMembers(legacyMembers, legacyRequired, storageManifestLegacyField); err != nil {
		return StorageManifest{}, err
	}
	for _, name := range []string{"categories", "part_categories", "category_attributes", "part_attribute_values", "part_details", "controller_outputs"} {
		if raw, ok := legacyMembers[name]; ok {
			if err := requireJSONArray(raw, storageManifestLegacyField+"."+name); err != nil {
				return StorageManifest{}, err
			}
		}
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
	if raw, ok := physicalMembers["cabinet_controllers"]; ok {
		if err := requireJSONArray(raw, storageManifestPhysicalField+".cabinet_controllers"); err != nil {
			return StorageManifest{}, err
		}
	}
	for _, name := range []string{"cabinet_led_strings", "cabinet_led_path_sections"} {
		if raw, ok := physicalMembers[name]; ok {
			if err := requireJSONArray(raw, storageManifestPhysicalField+"."+name); err != nil {
				return StorageManifest{}, err
			}
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
	_, env.PhysicalStorage.CabinetControllersDeclared = physicalMembers["cabinet_controllers"]
	_, stringsDeclared := physicalMembers["cabinet_led_strings"]
	_, sectionsDeclared := physicalMembers["cabinet_led_path_sections"]
	if stringsDeclared != sectionsDeclared {
		return StorageManifest{}, malformedManifest("physical_storage must declare both cabinet_led_strings and cabinet_led_path_sections, or omit both for an older archive")
	}
	env.PhysicalStorage.CabinetWiringDeclared = stringsDeclared && sectionsDeclared

	if env.Version != supportedCurrentManifestVersion && env.Version != supportedProductDetailsManifestVersion && env.Version != supportedCategoryManifestVersion && env.Version != supportedPreviousManifestVersion {
		return StorageManifest{}, &BackupFormatError{
			Code:   FormatUnsupportedManifestVersion,
			Reason: fmt.Sprintf("unsupported backup manifest version %q (supported: %q, %q, %q and %q)", env.Version, supportedPreviousManifestVersion, supportedCategoryManifestVersion, supportedProductDetailsManifestVersion, supportedCurrentManifestVersion),
		}
	}
	if env.Legacy.Version != supportedLegacyManifestVersion {
		return StorageManifest{}, &BackupFormatError{
			Code:   FormatUnsupportedManifestVersion,
			Reason: fmt.Sprintf("unsupported embedded legacy manifest version %q (supported: %q)", env.Legacy.Version, supportedLegacyManifestVersion),
		}
	}
	if err := convertLegacyAllocationCoordinates(&env, physicalMembers); err != nil {
		return StorageManifest{}, err
	}
	if err := validateStorageGraph(env.Legacy, env.PhysicalStorage, env.SystemFlags); err != nil {
		return StorageManifest{}, err
	}
	if env.Version == supportedCurrentManifestVersion || len(env.Legacy.ControllerOutputs) > 0 {
		if err := validateControllerOutputGraph(env.Legacy, env.PhysicalStorage); err != nil {
			return StorageManifest{}, err
		}
	}
	if err := validateCoordinateSpaceConsistency(env.Legacy, env.SystemFlags); err != nil {
		return StorageManifest{}, err
	}
	return env, nil
}

// convertLegacyAllocationCoordinates upgrades pre-cabinet-wide v2 snapshots in
// memory before validation/restore. The archived controller membership order and
// led_count provide the exact historical local-to-cabinet offset; missing or
// inconsistent facts are rejected before any database mutation.
func convertLegacyAllocationCoordinates(env *StorageManifest, physical map[string]json.RawMessage) error {
	invalid := func(reason string, args ...any) error {
		return &BackupFormatError{Code: FormatInvalidPhysicalGraph, Reason: fmt.Sprintf(reason, args...)}
	}
	space := env.PhysicalStorage.AllocationCoordinateSpace
	if env.PhysicalStorage.CabinetWiringDeclared && space != "cabinet" {
		return invalid("declared cabinet wiring requires cabinet-wide allocation coordinates")
	}
	if space == "cabinet" {
		if !env.PhysicalStorage.CabinetWiringDeclared {
			return invalid("cabinet-wide allocations require declared string and path-section lists")
		}
		return nil
	}
	if space != "" {
		return invalid("unsupported drawer allocation coordinate space %q", space)
	}
	raw, declared := physical["drawer_led_allocations"]
	if !declared || len(env.PhysicalStorage.LEDAllocations) == 0 {
		env.PhysicalStorage.AllocationCoordinateSpace = "cabinet"
		return nil
	}
	var local []struct {
		ID           int64 `json:"id"`
		CabinetID    int64 `json:"cabinet_id"`
		DrawerID     int64 `json:"drawer_id"`
		ControllerID int64 `json:"controller_id"`
		StartPixel   int64 `json:"start_pixel"`
		PixelCount   int64 `json:"pixel_count"`
		DisplayOrder int64 `json:"display_order"`
	}
	if err := json.Unmarshal(raw, &local); err != nil {
		return invalid("cannot decode legacy controller-local drawer allocations: %v", err)
	}
	if len(local) != len(env.PhysicalStorage.LEDAllocations) {
		return invalid("legacy drawer allocation list changed while translating coordinates")
	}
	controllers := make(map[int64]db.Controller, len(env.Legacy.Controllers))
	for _, controller := range env.Legacy.Controllers {
		controllers[controller.ID] = controller
	}
	membersByCabinet := make(map[int64][]db.CabinetController)
	for _, member := range resolveCabinetControllers(env.PhysicalStorage) {
		membersByCabinet[member.CabinetID] = append(membersByCabinet[member.CabinetID], member)
	}
	for i, allocation := range local {
		current := env.PhysicalStorage.LEDAllocations[i]
		if allocation.ID != current.ID || allocation.CabinetID != current.CabinetID || allocation.DrawerID != current.DrawerID || allocation.StartPixel != current.StartPixel || allocation.PixelCount != current.PixelCount || allocation.DisplayOrder != current.DisplayOrder {
			return invalid("legacy drawer allocation %d has inconsistent archive fields", allocation.ID)
		}
		controller, ok := controllers[allocation.ControllerID]
		if !ok || controller.LedCount < 1 || allocation.StartPixel < 1 || allocation.PixelCount < 1 || allocation.PixelCount > math.MaxInt64-allocation.StartPixel+1 || allocation.StartPixel+allocation.PixelCount-1 > controller.LedCount {
			return invalid("legacy drawer allocation %d cannot be converted within controller #%d capacity", allocation.ID, allocation.ControllerID)
		}
		var offset int64
		var targetSlot int64 = -1
		for _, member := range membersByCabinet[allocation.CabinetID] {
			memberController, exists := controllers[member.ControllerID]
			if !exists || memberController.LedCount < 0 || member.Slot < 0 || member.Slot > 4 {
				return invalid("legacy cabinet %d has a controller with invalid order or unknown capacity", allocation.CabinetID)
			}
			if member.ControllerID == allocation.ControllerID {
				targetSlot = member.Slot
			}
		}
		if targetSlot < 0 {
			return invalid("legacy allocation %d controller is not a member of cabinet %d", allocation.ID, allocation.CabinetID)
		}
		for _, member := range membersByCabinet[allocation.CabinetID] {
			if member.Slot >= targetSlot {
				continue
			}
			memberController := controllers[member.ControllerID]
			if memberController.LedCount > math.MaxInt64-offset {
				return invalid("legacy cabinet %d controller offsets overflow", allocation.CabinetID)
			}
			offset += memberController.LedCount
		}
		if allocation.StartPixel > math.MaxInt64-offset {
			return invalid("legacy allocation %d cabinet offset overflows", allocation.ID)
		}
		env.PhysicalStorage.LEDAllocations[i].StartPixel = allocation.StartPixel + offset
	}
	env.PhysicalStorage.AllocationCoordinateSpace = "cabinet"
	return nil
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

// validateControllerOutputGraph validates version 2.3 hardware configuration and
// any controller outputs carried by compatible older envelopes before restore.
func validateControllerOutputGraph(legacy Manifest, snap PhysicalStorageSnapshot) error {
	invalid := func(reason string, args ...any) error {
		return &BackupFormatError{Code: FormatInvalidPhysicalGraph, Reason: fmt.Sprintf(reason, args...)}
	}
	controllers := make(map[int64]db.Controller, len(legacy.Controllers))
	for _, controller := range legacy.Controllers {
		if controller.ID <= 0 {
			return invalid("controller has a non-positive id %d", controller.ID)
		}
		if _, duplicate := controllers[controller.ID]; duplicate {
			return invalid("duplicate controller id %d", controller.ID)
		}
		controllers[controller.ID] = controller
	}

	outputsByID := make(map[int64]db.ControllerOutput, len(legacy.ControllerOutputs))
	outputsByController := make(map[int64][]db.ControllerOutput, len(controllers))
	for _, output := range legacy.ControllerOutputs {
		if output.ID <= 0 || output.ControllerID <= 0 || output.DisplayOrder < 0 {
			return invalid("controller output has a non-positive id/owner or negative order")
		}
		if _, exists := controllers[output.ControllerID]; !exists {
			return invalid("controller output %d references unknown controller %d", output.ID, output.ControllerID)
		}
		if _, duplicate := outputsByID[output.ID]; duplicate {
			return invalid("duplicate controller output id %d", output.ID)
		}
		outputsByID[output.ID] = output
		outputsByController[output.ControllerID] = append(outputsByController[output.ControllerID], output)
	}

	for controllerID, controller := range controllers {
		outputs := outputsByController[controllerID]
		sort.Slice(outputs, func(i, j int) bool { return outputs[i].DisplayOrder < outputs[j].DisplayOrder })
		settings := controllerconfig.Settings{ControllerType: controller.ControllerType, ConnectionTimeoutMS: controller.ConnectionTimeoutMs}
		for index, output := range outputs {
			if output.DisplayOrder != int64(index) {
				return invalid("controller %d output order is not contiguous at %d", controllerID, index)
			}
			var customOrder []int64
			if err := json.Unmarshal([]byte(output.CustomOrderJson), &customOrder); err != nil {
				return invalid("controller output %d has invalid custom pixel order: %v", output.ID, err)
			}
			settings.Outputs = append(settings.Outputs, controllerconfig.Output{
				ID: output.ID, DisplayOrder: output.DisplayOrder, Name: output.Name, PixelCount: output.PixelCount,
				Direction: output.Direction, Pattern: output.Pattern, Rows: output.Rows, Columns: output.Columns,
				CustomOrder: customOrder,
			})
		}
		total, err := settings.NormalizeAndValidate()
		if err != nil {
			return invalid("controller %d has invalid hardware configuration: %v", controllerID, err)
		}
		if total != controller.LedCount {
			return invalid("controller %d capacity %d does not match output total %d", controllerID, controller.LedCount, total)
		}
	}

	usedProjections := make(map[int64]int64, len(snap.CabinetLEDStrings))
	for _, stringRecord := range snap.CabinetLEDStrings {
		if !stringRecord.ControllerOutputID.Valid {
			continue
		}
		outputID := stringRecord.ControllerOutputID.Int64
		output, exists := outputsByID[outputID]
		if !exists || output.ControllerID != stringRecord.ControllerID || output.DisplayOrder != stringRecord.DisplayOrder || output.PixelCount != stringRecord.PixelCount {
			return invalid("cabinet string %d does not match its controller output %d", stringRecord.ID, outputID)
		}
		if priorStringID, duplicate := usedProjections[outputID]; duplicate {
			return invalid("controller output %d is projected by cabinet strings %d and %d", outputID, priorStringID, stringRecord.ID)
		}
		usedProjections[outputID] = stringRecord.ID
	}
	return nil
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
	cabinetRecords := make(map[int64]db.Cabinet, len(snap.Cabinets))
	ownedControllers := make(map[int64]int64, len(snap.Cabinets))
	for _, c := range snap.Cabinets {
		if c.ID <= 0 {
			return invalid("cabinet has a non-positive id %d", c.ID)
		}
		if _, dup := cabinets[c.ID]; dup {
			return invalid("duplicate cabinet id %d", c.ID)
		}
		cabinets[c.ID] = struct{}{}
		cabinetRecords[c.ID] = c
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

	// Cabinet controller memberships. A current snapshot either declares its
	// membership rows explicitly or is an older current archive that predates the
	// membership table; the latter is interpreted with legacy single-controller
	// semantics. Identity is (cabinet_id, controller_id): a controller is owned by
	// at most one cabinet, and a cabinet holds at most five memberships in slots
	// 0..4. No membership is invented beyond what the archive declares or, for an
	// older archive, each cabinet's own configured controller as slot 0.
	memberships := resolveCabinetControllers(snap)
	membershipByPair := make(map[[2]int64]db.CabinetController, len(memberships))
	ownedMembers := make(map[int64]int64, len(memberships))
	memberSlots := make(map[int64]map[int64]struct{}, len(memberships))
	memberCount := make(map[int64]int, len(memberships))
	for _, m := range memberships {
		if _, ok := cabinets[m.CabinetID]; !ok {
			return invalid("cabinet controller membership references cabinet %d, which the backup does not contain", m.CabinetID)
		}
		if _, ok := controllers[m.ControllerID]; !ok {
			return invalid("cabinet controller membership for cabinet %d references controller %d, which the backup does not contain", m.CabinetID, m.ControllerID)
		}
		if m.Slot < 0 || m.Slot > 4 {
			return invalid("cabinet %d controller %d has an invalid slot %d (slots are 0..4)", m.CabinetID, m.ControllerID, m.Slot)
		}
		if m.StringsConfigured != 0 && m.StringsConfigured != 1 {
			return invalid("cabinet %d controller %d has invalid strings_configured value %d", m.CabinetID, m.ControllerID, m.StringsConfigured)
		}
		if owner, taken := ownedMembers[m.ControllerID]; taken {
			return invalid("controller %d is a member of cabinets %d and %d", m.ControllerID, owner, m.CabinetID)
		}
		ownedMembers[m.ControllerID] = m.CabinetID
		slots := memberSlots[m.CabinetID]
		if slots == nil {
			slots = make(map[int64]struct{})
			memberSlots[m.CabinetID] = slots
		}
		if _, dup := slots[m.Slot]; dup {
			return invalid("cabinet %d has more than one controller in slot %d", m.CabinetID, m.Slot)
		}
		slots[m.Slot] = struct{}{}
		memberCount[m.CabinetID]++
		if memberCount[m.CabinetID] > 5 {
			return invalid("cabinet %d has more than five controllers", m.CabinetID)
		}
		membershipByPair[[2]int64{m.CabinetID, m.ControllerID}] = m
	}

	stringsByID := make(map[int64]db.CabinetLedString, len(snap.CabinetLEDStrings))
	stringOrders := make(map[[3]int64]struct{}, len(snap.CabinetLEDStrings))
	stringPixelsByController := make(map[int64]int64, len(snap.CabinetLEDStrings))
	for _, stringRecord := range snap.CabinetLEDStrings {
		if stringRecord.ID <= 0 || stringRecord.CabinetID <= 0 || stringRecord.ControllerID <= 0 || stringRecord.DisplayOrder < 0 || stringRecord.PixelCount < 1 || strings.TrimSpace(stringRecord.Name) == "" {
			return invalid("cabinet wiring string has an invalid id, owner, name, order or pixel count")
		}
		if _, duplicate := stringsByID[stringRecord.ID]; duplicate {
			return invalid("duplicate cabinet wiring string id %d", stringRecord.ID)
		}
		if _, member := membershipByPair[[2]int64{stringRecord.CabinetID, stringRecord.ControllerID}]; !member {
			return invalid("string %d references controller %d that is not assigned to cabinet %d", stringRecord.ID, stringRecord.ControllerID, stringRecord.CabinetID)
		}
		orderKey := [3]int64{stringRecord.CabinetID, stringRecord.ControllerID, stringRecord.DisplayOrder}
		if _, duplicate := stringOrders[orderKey]; duplicate {
			return invalid("cabinet %d controller %d has duplicate string order %d", stringRecord.CabinetID, stringRecord.ControllerID, stringRecord.DisplayOrder)
		}
		stringOrders[orderKey] = struct{}{}
		if stringRecord.LayoutComplete != 0 && stringRecord.LayoutComplete != 1 {
			return invalid("string %d has invalid layout_complete value %d", stringRecord.ID, stringRecord.LayoutComplete)
		}
		if stringRecord.PixelCount > math.MaxInt64-stringPixelsByController[stringRecord.ControllerID] {
			return invalid("controller %d string capacity overflows", stringRecord.ControllerID)
		}
		stringPixelsByController[stringRecord.ControllerID] += stringRecord.PixelCount
		stringsByID[stringRecord.ID] = stringRecord
	}
	sectionIDs := make(map[int64]struct{}, len(snap.CabinetLEDPathSections))
	sectionOrders := make(map[[2]int64]struct{}, len(snap.CabinetLEDPathSections))
	sectionPixelsByString := make(map[int64]int64, len(snap.CabinetLEDPathSections))
	sectionCountByString := make(map[int64]int, len(snap.CabinetLEDPathSections))
	for _, section := range snap.CabinetLEDPathSections {
		stringRecord, exists := stringsByID[section.StringID]
		if section.ID <= 0 || !exists || section.DisplayOrder < 0 || section.PixelCount < 1 {
			return invalid("wiring path section %d has invalid identity, string, order or pixel count", section.ID)
		}
		if _, duplicate := sectionIDs[section.ID]; duplicate {
			return invalid("duplicate wiring path section id %d", section.ID)
		}
		sectionIDs[section.ID] = struct{}{}
		orderKey := [2]int64{section.StringID, section.DisplayOrder}
		if _, duplicate := sectionOrders[orderKey]; duplicate {
			return invalid("string %d has duplicate path section order %d", section.StringID, section.DisplayOrder)
		}
		sectionOrders[orderKey] = struct{}{}
		if section.PixelCount > math.MaxInt64-sectionPixelsByString[section.StringID] {
			return invalid("string %d path pixel count overflows", section.StringID)
		}
		sectionPixelsByString[section.StringID] += section.PixelCount
		sectionCountByString[section.StringID]++
		var points []struct {
			X float64 `json:"x"`
			Y float64 `json:"y"`
		}
		if err := json.Unmarshal([]byte(section.PointsJson), &points); err != nil || points == nil {
			return invalid("path section %d has invalid point geometry", section.ID)
		}
		if len(points) != 0 && len(points) < 2 {
			return invalid("path section %d needs at least two ordered points", section.ID)
		}
		if stringRecord.LayoutComplete == 1 && len(points) < 2 {
			return invalid("complete string %d contains a path section without direction geometry", stringRecord.ID)
		}
		cabinet := cabinetRecords[stringRecord.CabinetID]
		width, height := cabinet.LayoutWidth, cabinet.LayoutHeight
		if width <= 0 || height <= 0 {
			width, height = 100, 100
		}
		for _, point := range points {
			if math.IsNaN(point.X) || math.IsNaN(point.Y) || math.IsInf(point.X, 0) || math.IsInf(point.Y, 0) || point.X < 0 || point.Y < 0 || point.X > width || point.Y > height {
				return invalid("path section %d has a point outside cabinet %d bounds", section.ID, stringRecord.CabinetID)
			}
		}
	}
	for _, stringRecord := range snap.CabinetLEDStrings {
		sectionPixels := sectionPixelsByString[stringRecord.ID]
		if sectionPixels > stringRecord.PixelCount || (stringRecord.LayoutComplete == 1 && (sectionCountByString[stringRecord.ID] == 0 || sectionPixels != stringRecord.PixelCount)) {
			return invalid("string %d path sections account for %d of %d pixels but layout_complete=%d", stringRecord.ID, sectionPixels, stringRecord.PixelCount, stringRecord.LayoutComplete)
		}
	}
	for _, member := range memberships {
		if member.StringsConfigured == 1 {
			controller := controllers[member.ControllerID]
			if controller.LedCount != stringPixelsByController[member.ControllerID] {
				return invalid("controller %d capacity %d does not equal configured string total %d", member.ControllerID, controller.LedCount, stringPixelsByController[member.ControllerID])
			}
		} else if stringPixelsByController[member.ControllerID] != 0 {
			return invalid("controller %d has strings but strings_configured is false", member.ControllerID)
		}
	}
	if snap.CabinetWiringDeclared && (len(snap.CabinetLEDStrings) == 0 && len(snap.CabinetLEDPathSections) != 0) {
		return invalid("wiring path sections exist without any strings")
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

	cabinetCapacity := make(map[int64]int64, len(memberships))
	for _, member := range memberships {
		controller := controllers[member.ControllerID]
		if controller.LedCount < 0 || controller.LedCount > math.MaxInt64-cabinetCapacity[member.CabinetID] {
			return invalid("cabinet %d has invalid total controller capacity", member.CabinetID)
		}
		cabinetCapacity[member.CabinetID] += controller.LedCount
	}
	allocationIDs := make(map[int64]struct{}, len(snap.LEDAllocations))
	allocationRanges := make([]struct{ cabinetID, start, end, allocationID int64 }, 0, len(snap.LEDAllocations))
	cabinetByDrawer := make(map[int64]int64, len(snap.Drawers))
	for _, drawer := range snap.Drawers {
		cabinetByDrawer[drawer.ID] = drawer.CabinetID
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
		if allocation.DisplayOrder < 0 || allocation.StartPixel < 1 || allocation.PixelCount < 1 || allocation.PixelCount > math.MaxInt64-allocation.StartPixel+1 {
			return invalid("drawer LED allocation %d has an invalid start, count, order or overflowing end", allocation.ID)
		}
		end := allocation.StartPixel + allocation.PixelCount - 1
		if cabinetCapacity[allocation.CabinetID] < 1 || end > cabinetCapacity[allocation.CabinetID] {
			return invalid("drawer LED allocation %d exceeds cabinet #%d capacity", allocation.ID, allocation.CabinetID)
		}
		for _, other := range allocationRanges {
			if other.cabinetID == allocation.CabinetID && allocation.StartPixel <= other.end && other.start <= end {
				return invalid("drawer LED allocations %d and %d overlap in cabinet #%d pixel space", allocation.ID, other.allocationID, allocation.CabinetID)
			}
		}
		allocationRanges = append(allocationRanges, struct{ cabinetID, start, end, allocationID int64 }{allocation.CabinetID, allocation.StartPixel, end, allocation.ID})
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

// resolveCabinetControllers returns the cabinet controller memberships a snapshot
// carries. A snapshot that declares its membership list uses it verbatim. An older
// current-format archive that predates the membership table declares none, so it
// is read with legacy single-controller semantics: each cabinet's own configured
// controller becomes slot 0, and nothing else is inferred. A cabinet with a NULL
// controller stays unassigned.
func resolveCabinetControllers(snap PhysicalStorageSnapshot) []db.CabinetController {
	if snap.CabinetControllersDeclared {
		return snap.CabinetControllers
	}
	derived := make([]db.CabinetController, 0, len(snap.Cabinets))
	for _, c := range snap.Cabinets {
		if !c.ControllerID.Valid {
			continue
		}
		derived = append(derived, db.CabinetController{
			CabinetID:    c.ID,
			ControllerID: c.ControllerID.Int64,
			Slot:         0,
			CreatedAt:    c.CreatedAt.Time,
			UpdatedAt:    c.UpdatedAt.Time,
		})
	}
	return derived
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
