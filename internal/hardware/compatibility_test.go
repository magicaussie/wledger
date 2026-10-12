package hardware

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/ledspace"
)

// compatFixture builds compatibility-baseline scenarios in a real, isolated
// SQLite database.
type compatFixture struct {
	t      *testing.T
	store  db.Store
	dbConn *sql.DB
	ctx    context.Context
}

// compatIDs are the row ids of one compatibility scenario.
type compatIDs struct {
	controller int64
	container  int64
	bin        int64
	part       int64
}

type compatControllerSpec struct {
	Name     string
	IP       string
	Port     sql.NullInt64
	LedCount int64
}

type compatContainerSpec struct {
	Name       string
	Controller int64
	Segment    int64
	LedStart   int64
	LedCount   int64
	Config     sql.NullString
}

type compatBinSpec struct {
	Name      string
	Container int64
	LedIndex  sql.NullInt64
	Width     sql.NullInt64
}

// setupCompatFixture opens a temporary file-backed SQLite database in WAL mode
// with the existing schema applied, and closes it when the test ends.
func setupCompatFixture(t *testing.T, name string) compatFixture {
	t.Helper()
	dbConn, err := db.Open("file:" + filepath.Join(t.TempDir(), name+".db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { dbConn.Close() })
	if err := db.Migrate(dbConn); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	return compatFixture{t: t, store: db.NewStore(dbConn), dbConn: dbConn, ctx: context.Background()}
}

func (f compatFixture) controller(spec compatControllerSpec) int64 {
	f.t.Helper()
	row, err := f.store.CreateController(f.ctx, db.CreateControllerParams{Name: spec.Name, IpAddress: spec.IP, Port: spec.Port})
	if err != nil {
		f.t.Fatalf("create controller %q: %v", spec.Name, err)
	}
	if spec.LedCount != 0 {
		// The generated controller insert never sets led_count, so a known
		// controller capacity is written directly against the existing schema.
		f.exec("UPDATE controllers SET led_count = ? WHERE id = ?", spec.LedCount, row.ID)
	}
	return row.ID
}

func (f compatFixture) container(spec compatContainerSpec) int64 {
	f.t.Helper()
	id, err := f.store.CreateContainer(f.ctx, db.CreateContainerParams{
		Name:         spec.Name,
		ControllerID: spec.Controller,
		SegmentID:    spec.Segment,
		ConfigJson:   spec.Config,
		LedStart:     spec.LedStart,
		LedCount:     spec.LedCount,
	})
	if err != nil {
		f.t.Fatalf("create container %q: %v", spec.Name, err)
	}
	return id
}

func (f compatFixture) bin(spec compatBinSpec) int64 {
	f.t.Helper()
	id, err := f.store.CreateBin(f.ctx, db.CreateBinParams{
		Name:        spec.Name,
		ContainerID: sql.NullInt64{Int64: spec.Container, Valid: true},
		LedIndex:    spec.LedIndex,
		Width:       spec.Width,
	})
	if err != nil {
		f.t.Fatalf("create bin %q: %v", spec.Name, err)
	}
	return id
}

func (f compatFixture) part(name string) int64 {
	f.t.Helper()
	id, err := f.store.CreatePart(f.ctx, db.CreatePartParams{Name: name})
	if err != nil {
		f.t.Fatalf("create part %q: %v", name, err)
	}
	return id
}

func (f compatFixture) assignment(partID int64, binID sql.NullInt64, quantity int64) {
	f.t.Helper()
	if err := f.store.CreatePartAssignment(f.ctx, db.CreatePartAssignmentParams{PartID: partID, BinID: binID, Quantity: quantity}); err != nil {
		f.t.Fatalf("create assignment for part %d: %v", partID, err)
	}
}

func (f compatFixture) setFlag(key, value string) {
	f.t.Helper()
	if err := f.store.SetFlag(f.ctx, db.SetFlagParams{Key: key, Value: value}); err != nil {
		f.t.Fatalf("set flag %q: %v", key, err)
	}
}

func (f compatFixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.dbConn.ExecContext(f.ctx, query, args...); err != nil {
		f.t.Fatalf("exec %q: %v", query, err)
	}
}

// execWithForeignKeysOff runs one statement on a dedicated connection with
// foreign keys disabled, so a fixture can store the orphan relationships a real
// corrupted database carries.
func (f compatFixture) execWithForeignKeysOff(query string, args ...any) {
	f.t.Helper()
	conn, err := f.dbConn.Conn(f.ctx)
	if err != nil {
		f.t.Fatalf("checkout connection: %v", err)
	}
	defer conn.Close()
	defer func() {
		if _, err := conn.ExecContext(f.ctx, "PRAGMA foreign_keys = ON"); err != nil {
			f.t.Errorf("restore foreign keys: %v", err)
		}
	}()
	if _, err := conn.ExecContext(f.ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		f.t.Fatalf("disable foreign keys: %v", err)
	}
	if _, err := conn.ExecContext(f.ctx, query, args...); err != nil {
		f.t.Fatalf("exec with foreign keys off %q: %v", query, err)
	}
}

func (f compatFixture) capture() CompatibilityBaseline {
	f.t.Helper()
	return f.captureWith(f.store)
}

func (f compatFixture) captureWith(store db.Store) CompatibilityBaseline {
	f.t.Helper()
	return f.captureIn(f.ctx, store)
}

func (f compatFixture) captureIn(ctx context.Context, store db.Store) CompatibilityBaseline {
	f.t.Helper()
	baseline, err := CaptureCompatibilityBaseline(ctx, store)
	if err != nil {
		f.t.Fatalf("capture compatibility baseline: %v", err)
	}
	return baseline
}

func validInt64(value int64) sql.NullInt64 {
	return sql.NullInt64{Int64: value, Valid: true}
}

// linearConfig is a container layout configuration with the given LED total.
func linearConfig(total int) sql.NullString {
	return sql.NullString{String: fmt.Sprintf(`{"type":"linear","total":%d}`, total), Valid: true}
}

func compatTarget(t *testing.T, baseline CompatibilityBaseline, binID int64) CompatibilityBinTarget {
	t.Helper()
	for _, target := range baseline.Targets {
		if target.BinID == binID {
			return target
		}
	}
	t.Fatalf("bin %d has no target record in %+v", binID, baseline.Targets)
	return CompatibilityBinTarget{}
}

func compatDiagnosticsFor(baseline CompatibilityBaseline, code CompatibilityCode) []CompatibilityDiagnostic {
	var findings []CompatibilityDiagnostic
	for _, diagnostic := range baseline.Diagnostics {
		if diagnostic.Code == code {
			findings = append(findings, diagnostic)
		}
	}
	return findings
}

func compatHasCode(baseline CompatibilityBaseline, code CompatibilityCode) bool {
	return len(compatDiagnosticsFor(baseline, code)) > 0
}

func compatHasDiagnosticFor(baseline CompatibilityBaseline, code CompatibilityCode, binID int64) bool {
	for _, diagnostic := range compatDiagnosticsFor(baseline, code) {
		if diagnostic.BinID == binID {
			return true
		}
	}
	return false
}

func compatHasContainerDiagnostic(baseline CompatibilityBaseline, code CompatibilityCode, containerID int64) bool {
	for _, diagnostic := range compatDiagnosticsFor(baseline, code) {
		if diagnostic.ContainerID == containerID {
			return true
		}
	}
	return false
}

func compatRawBin(t *testing.T, raw CompatibilityRaw, id int64) db.Bin {
	t.Helper()
	for _, bin := range raw.Bins {
		if bin.ID == id {
			return bin
		}
	}
	t.Fatalf("bin %d is missing from the captured rows", id)
	return db.Bin{}
}

func compatRawBinByName(t *testing.T, raw CompatibilityRaw, name string) db.Bin {
	t.Helper()
	for _, bin := range raw.Bins {
		if bin.Name == name {
			return bin
		}
	}
	t.Fatalf("bin %q is missing from the captured rows", name)
	return db.Bin{}
}

// compatAssertZeroBaseline asserts that a failed capture returned no data at all,
// so partial data can never be mistaken for compatible data.
func compatAssertZeroBaseline(t *testing.T, baseline CompatibilityBaseline) {
	t.Helper()
	if !reflect.DeepEqual(baseline, CompatibilityBaseline{}) {
		t.Errorf("a failed capture must return the zero baseline, got %+v", baseline)
	}
	for _, fingerprint := range []CompatibilityFingerprint{baseline.InventoryFingerprint, baseline.MappingFingerprint, baseline.TargetsFingerprint} {
		if fingerprint.String() != "" {
			t.Errorf("a failed capture must not certify a fingerprint, got %q", fingerprint.String())
		}
	}
}

// compatDomainDelta is one domain's fingerprint before and after a mutation.
type compatDomainDelta struct {
	Domain  string
	Before  CompatibilityFingerprint
	After   CompatibilityFingerprint
	Changed bool
}

func compatAssertDelta(t *testing.T, deltas ...compatDomainDelta) {
	t.Helper()
	for _, delta := range deltas {
		if changed := delta.Before.Digest != delta.After.Digest; changed != delta.Changed {
			t.Errorf("%s fingerprint changed=%v, want %v (%s -> %s)", delta.Domain, changed, delta.Changed, delta.Before.String(), delta.After.String())
		}
	}
}

func TestCompatibilityBaselineSegmentSnapshot(t *testing.T) {
	f := setupCompatFixture(t, "compat_segment")
	controller := f.controller(compatControllerSpec{Name: "Ctrl", IP: "10.0.0.7", Port: validInt64(8080)})
	container := f.container(compatContainerSpec{Name: "A", Controller: controller, Segment: 0, Config: linearConfig(10)})
	mapped := f.bin(compatBinSpec{Name: "a1", Container: container, LedIndex: validInt64(3)})
	unmapped := f.bin(compatBinSpec{Name: "a2", Container: container})
	part := f.part("Resistor")
	f.assignment(part, validInt64(mapped), 7)
	f.assignment(part, sql.NullInt64{}, 0)

	baseline := f.capture()

	if baseline.Version != CompatibilityFingerprintVersion {
		t.Errorf("baseline version = %q, want %q", baseline.Version, CompatibilityFingerprintVersion)
	}
	if baseline.Space != ledspace.Segment || !baseline.SpaceKnown {
		t.Errorf("space = %q known=%v, want segment and known", baseline.Space, baseline.SpaceKnown)
	}
	if baseline.LocateBlocked {
		t.Error("an absent led_coordinate_space flag means segment and must not block locate")
	}
	for _, flag := range []CompatibilityFlag{baseline.Flags.LedCoordinateSpace, baseline.Flags.Migration005Applied, baseline.Flags.DrawerAllocationBackfilled} {
		if flag.Present || flag.Value != "" {
			t.Errorf("flag %q must be captured as absent, got present=%v value=%q", flag.Key, flag.Present, flag.Value)
		}
	}

	if len(baseline.Raw.Controllers) != 1 {
		t.Fatalf("captured %d controllers, want 1", len(baseline.Raw.Controllers))
	}
	rawController := baseline.Raw.Controllers[0]
	if !rawController.Port.Valid || rawController.Port.Int64 != 8080 {
		t.Errorf("raw port = %+v, want the stored 8080", rawController.Port)
	}
	if rawController.IpAddress != "10.0.0.7" || rawController.ID != controller {
		t.Errorf("raw controller = %+v, want id %d at 10.0.0.7", rawController, controller)
	}

	rawMapped := compatRawBin(t, baseline.Raw, mapped)
	if !rawMapped.LedIndex.Valid || rawMapped.LedIndex.Int64 != 3 {
		t.Errorf("raw led_index = %+v, want 3", rawMapped.LedIndex)
	}
	if rawMapped.Width.Valid {
		t.Errorf("raw width = %+v, want SQL NULL preserved", rawMapped.Width)
	}
	rawUnmapped := compatRawBin(t, baseline.Raw, unmapped)
	if rawUnmapped.LedIndex.Valid {
		t.Errorf("raw led_index = %+v for an unmapped bin, want SQL NULL preserved", rawUnmapped.LedIndex)
	}
	if !rawUnmapped.ContainerID.Valid || rawUnmapped.ContainerID.Int64 != container {
		t.Errorf("raw container_id = %+v, want %d", rawUnmapped.ContainerID, container)
	}

	if len(baseline.Raw.Parts) != 1 || baseline.Raw.Parts[0].ID != part || baseline.Raw.Parts[0].Name != "Resistor" {
		t.Fatalf("captured parts = %+v, want part %d Resistor", baseline.Raw.Parts, part)
	}
	if len(baseline.Raw.PartAssignments) != 2 {
		t.Fatalf("captured %d assignments, want 2", len(baseline.Raw.PartAssignments))
	}
	stocked, orphan := baseline.Raw.PartAssignments[0], baseline.Raw.PartAssignments[1]
	if stocked.PartID != part || !stocked.BinID.Valid || stocked.BinID.Int64 != mapped || stocked.Quantity != 7 {
		t.Errorf("stocked assignment = %+v, want part %d in bin %d quantity 7", stocked, part, mapped)
	}
	if orphan.BinID.Valid || orphan.Quantity != 0 {
		t.Errorf("orphan assignment = %+v, want a NULL bin_id and quantity 0", orphan)
	}

	target := compatTarget(t, baseline, mapped)
	if !target.Resolved || target.Refusal != "" {
		t.Fatalf("mapped bin target = %+v, want a resolved target", target)
	}
	if target.SegmentID != 0 || target.Start != 3 || target.Count != 1 {
		t.Errorf("mapped bin target = %+v, want segment 0 start 3 count 1", target)
	}
	if target.Address != "10.0.0.7:8080" || target.ControllerID != controller {
		t.Errorf("mapped bin target = %+v, want controller %d at 10.0.0.7:8080", target, controller)
	}
	if target.RawWidth.Valid {
		t.Errorf("target raw width = %+v, want SQL NULL preserved", target.RawWidth)
	}
	missing := compatTarget(t, baseline, unmapped)
	if missing.Resolved || missing.Refusal != CodeUnmapped {
		t.Errorf("unmapped bin target = %+v, want refusal %q", missing, CodeUnmapped)
	}
	if missing.EffectiveWidth != 1 {
		t.Errorf("unmapped bin effective width = %d, want 1", missing.EffectiveWidth)
	}

	if !compatHasDiagnosticFor(baseline, CodeUnmapped, unmapped) {
		t.Errorf("diagnostics = %+v, want unmapped for bin %d", baseline.Diagnostics, unmapped)
	}
	if compatHasCode(baseline, CodeRangeBeyondCapacity) {
		t.Error("a controller LED count of 0 is unknown, not a capacity of 0")
	}
	if findings := compatDiagnosticsFor(baseline, CodeInvalidAllocation); len(findings) != 0 {
		t.Errorf("diagnostics = %+v, want no invalid allocation for an unallocated drawer", findings)
	}

	for _, fingerprint := range []CompatibilityFingerprint{baseline.InventoryFingerprint, baseline.MappingFingerprint, baseline.TargetsFingerprint} {
		if fingerprint.Version != CompatibilityFingerprintVersion {
			t.Errorf("fingerprint version = %q, want %q", fingerprint.Version, CompatibilityFingerprintVersion)
		}
		if _, err := hex.DecodeString(fingerprint.Digest); err != nil || len(fingerprint.Digest) != 64 {
			t.Errorf("fingerprint digest = %q, want 32 hex-encoded bytes", fingerprint.Digest)
		}
	}
	if baseline.InventoryFingerprint.Digest == baseline.MappingFingerprint.Digest ||
		baseline.MappingFingerprint.Digest == baseline.TargetsFingerprint.Digest ||
		baseline.InventoryFingerprint.Digest == baseline.TargetsFingerprint.Digest {
		t.Error("each domain must have its own fingerprint")
	}
}

func TestCompatibilityBaselineTargetAddressUsesControllerIP(t *testing.T) {
	f := setupCompatFixture(t, "compat_address")
	controller := f.controller(compatControllerSpec{Name: "Ctrl", IP: "192.168.1.50", Port: validInt64(80)})
	container := f.container(compatContainerSpec{Name: "A", Controller: controller, Segment: 0, LedStart: 0, LedCount: 4, Config: linearConfig(4)})
	bin := f.bin(compatBinSpec{Name: "a1", Container: container, LedIndex: validInt64(1), Width: validInt64(1)})

	before := f.capture()
	if target := compatTarget(t, before, bin); target.Address != "192.168.1.50" {
		t.Errorf("target address = %q, want the controller IpAddress", target.Address)
	}
	if port := before.Raw.Controllers[0].Port; !port.Valid || port.Int64 != 80 {
		t.Errorf("raw port = %+v, want the stored 80", port)
	}

	// The configured non-default port is part of the wire endpoint and therefore
	// changes the target fingerprint as well as the controller mapping fingerprint.
	f.exec("UPDATE controllers SET port = ? WHERE id = ?", 8080, controller)
	reported := f.capture()
	compatAssertDelta(t,
		compatDomainDelta{Domain: "inventory", Before: before.InventoryFingerprint, After: reported.InventoryFingerprint},
		compatDomainDelta{Domain: "mapping", Before: before.MappingFingerprint, After: reported.MappingFingerprint, Changed: true},
		compatDomainDelta{Domain: "targets", Before: before.TargetsFingerprint, After: reported.TargetsFingerprint, Changed: true},
	)
	if port := reported.Raw.Controllers[0].Port; !port.Valid || port.Int64 != 8080 {
		t.Errorf("raw port after the change = %+v, want 8080", port)
	}
	if target := compatTarget(t, reported, bin); target.Address != "192.168.1.50:8080" {
		t.Errorf("target address after the port change = %q, want configured endpoint", target.Address)
	}

	// The address is also part of the wire-targeting endpoint.
	f.exec("UPDATE controllers SET ip_address = ? WHERE id = ?", "192.168.1.51", controller)
	moved := f.capture()
	compatAssertDelta(t,
		compatDomainDelta{Domain: "targets", Before: before.TargetsFingerprint, After: moved.TargetsFingerprint, Changed: true},
	)
	if target := compatTarget(t, moved, bin); target.Address != "192.168.1.51:8080" {
		t.Errorf("target address = %q, want the updated address with configured port", target.Address)
	}
}

func TestCompatibilityBaselineDrawerSpaceTargets(t *testing.T) {
	f := setupCompatFixture(t, "compat_drawer")
	f.setFlag(ledspace.FlagKey, ledspace.Drawer)
	controller := f.controller(compatControllerSpec{Name: "Ctrl", IP: "10.0.0.7"})
	allocated := f.container(compatContainerSpec{Name: "Allocated", Controller: controller, Segment: 0, LedStart: 10, LedCount: 5, Config: linearConfig(5)})
	unallocated := f.container(compatContainerSpec{Name: "Unallocated", Controller: controller, Segment: 0, Config: linearConfig(5)})
	inside := f.bin(compatBinSpec{Name: "inside", Container: allocated, LedIndex: validInt64(2), Width: validInt64(1)})
	overrun := f.bin(compatBinSpec{Name: "overrun", Container: allocated, LedIndex: validInt64(3), Width: validInt64(9)})
	outside := f.bin(compatBinSpec{Name: "outside", Container: allocated, LedIndex: validInt64(5), Width: validInt64(1)})
	noAllocation := f.bin(compatBinSpec{Name: "no-allocation", Container: unallocated, LedIndex: validInt64(0), Width: validInt64(1)})

	baseline := f.capture()

	if baseline.Space != ledspace.Drawer || !baseline.SpaceKnown || baseline.LocateBlocked {
		t.Errorf("space = %q known=%v blocked=%v, want a resolved drawer space", baseline.Space, baseline.SpaceKnown, baseline.LocateBlocked)
	}
	if !baseline.Flags.LedCoordinateSpace.Present || baseline.Flags.LedCoordinateSpace.Value != ledspace.Drawer {
		t.Errorf("led_coordinate_space flag = %+v, want drawer", baseline.Flags.LedCoordinateSpace)
	}
	if target := compatTarget(t, baseline, inside); !target.Resolved || target.Start != 12 || target.Count != 1 || target.SegmentID != 0 {
		t.Errorf("inside target = %+v, want the drawer-relative index added to the allocation start", target)
	}
	wide := compatTarget(t, baseline, overrun)
	if !wide.Resolved || wide.Start != 13 || wide.Count != 9 {
		t.Errorf("overrun target = %+v, want the mapper's unresolved-width range recorded unchanged", wide)
	}
	if !compatHasDiagnosticFor(baseline, CodeRangeOutsideAllocation, overrun) {
		t.Errorf("diagnostics = %+v, want a range outside the allocation for bin %d", baseline.Diagnostics, overrun)
	}
	if diagnostics := compatDiagnosticsFor(baseline, CodeRangeOutsideAllocation); len(diagnostics) != 1 || diagnostics[0].Capacity != 5 || diagnostics[0].Width != 9 {
		t.Errorf("diagnostics = %+v, want one finding with capacity 5 and width 9", diagnostics)
	}
	if target := compatTarget(t, baseline, outside); target.Resolved || target.Refusal != CodeIndexOutsideAllocation {
		t.Errorf("outside target = %+v, want refusal %q", target, CodeIndexOutsideAllocation)
	}
	if target := compatTarget(t, baseline, noAllocation); target.Resolved || target.Refusal != CodeNoDrawerAllocation {
		t.Errorf("unallocated drawer target = %+v, want refusal %q", target, CodeNoDrawerAllocation)
	}
	if !compatHasDiagnosticFor(baseline, CodeNoDrawerAllocation, noAllocation) {
		t.Errorf("diagnostics = %+v, want the drawer allocation refusal for bin %d", baseline.Diagnostics, noAllocation)
	}
	if compatHasCode(baseline, CodeRangeBeyondCapacity) {
		t.Error("a controller LED count of 0 is unknown, not a capacity of 0")
	}
}

func TestCompatibilityBaselineFlagPresenceIsDistinct(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		f := setupCompatFixture(t, "compat_flag_absent")
		baseline := f.capture()
		if baseline.Space != ledspace.Segment || baseline.Flags.LedCoordinateSpace.Present {
			t.Errorf("space = %q flag = %+v, want an absent flag resolved to segment", baseline.Space, baseline.Flags.LedCoordinateSpace)
		}
	})

	t.Run("segment value differs from absent", func(t *testing.T) {
		f := setupCompatFixture(t, "compat_flag_segment")
		before := f.capture()
		f.setFlag(ledspace.FlagKey, ledspace.Segment)
		after := f.capture()
		if after.Space != ledspace.Segment || !after.SpaceKnown {
			t.Errorf("space = %q known=%v, want a resolved segment space", after.Space, after.SpaceKnown)
		}
		flag := after.Flags.LedCoordinateSpace
		if !flag.Present || flag.Value != ledspace.Segment {
			t.Errorf("flag = %+v, want a present segment value", flag)
		}
		compatAssertDelta(t,
			compatDomainDelta{Domain: "mapping", Before: before.MappingFingerprint, After: after.MappingFingerprint, Changed: true},
		)
	})

	t.Run("false value differs from absent", func(t *testing.T) {
		f := setupCompatFixture(t, "compat_flag_false")
		before := f.capture()
		f.setFlag(Migration005FlagKey, "false")
		after := f.capture()
		flag := after.Flags.Migration005Applied
		if !flag.Present || flag.Value != "false" {
			t.Errorf("flag = %+v, want a present false value", flag)
		}
		if after.Space != ledspace.Segment {
			t.Errorf("space = %q, want segment", after.Space)
		}
		compatAssertDelta(t,
			compatDomainDelta{Domain: "mapping", Before: before.MappingFingerprint, After: after.MappingFingerprint, Changed: true},
		)
	})

	t.Run("backfilled flag value", func(t *testing.T) {
		f := setupCompatFixture(t, "compat_flag_backfilled")
		f.setFlag(DrawerAllocationBackfilledFlagKey, "true")
		f.setFlag(Migration005FlagKey, "true")
		baseline := f.capture()
		if flag := baseline.Flags.DrawerAllocationBackfilled; !flag.Present || flag.Value != "true" || flag.Key != DrawerAllocationBackfilledFlagKey {
			t.Errorf("flag = %+v, want the stored true value", flag)
		}
		if flag := baseline.Flags.Migration005Applied; !flag.Present || flag.Value != "true" {
			t.Errorf("flag = %+v, want the stored true value", flag)
		}
	})
}

func TestCompatibilityBaselineSpaceGuardBlocksEveryBin(t *testing.T) {
	t.Run("unknown space", func(t *testing.T) {
		f := setupCompatFixture(t, "compat_unknown_space")
		controller := f.controller(compatControllerSpec{Name: "Ctrl", IP: "10.0.0.7"})
		container := f.container(compatContainerSpec{Name: "A", Controller: controller, Segment: 0, Config: linearConfig(10)})
		mapped := f.bin(compatBinSpec{Name: "a1", Container: container, LedIndex: validInt64(3), Width: validInt64(1)})
		unmapped := f.bin(compatBinSpec{Name: "a2", Container: container})
		f.setFlag(ledspace.FlagKey, "banana")

		baseline := f.capture()

		if baseline.Space != "banana" || baseline.SpaceKnown {
			t.Errorf("space = %q known=%v, want the unrecognised value preserved as unknown", baseline.Space, baseline.SpaceKnown)
		}
		if !baseline.LocateBlocked {
			t.Error("an unknown coordinate space must block locate and flash")
		}
		if !compatHasCode(baseline, CodeUnknownSpace) {
			t.Errorf("diagnostics = %+v, want %q", baseline.Diagnostics, CodeUnknownSpace)
		}
		for _, binID := range []int64{mapped, unmapped} {
			target := compatTarget(t, baseline, binID)
			if target.Resolved || target.Refusal != CodeUnknownSpace {
				t.Errorf("bin %d target = %+v, want refusal %q", binID, target, CodeUnknownSpace)
			}
			if target.Start != 0 || target.Count != 0 {
				t.Errorf("bin %d target = %+v, want no invented range", binID, target)
			}
		}
		if compatHasCode(baseline, CodeUnmapped) {
			t.Error("the coordinate guard precedes per-bin mapping, so the unmapped bin is refused by the space too")
		}
	})

	t.Run("unresolved space", func(t *testing.T) {
		f := setupCompatFixture(t, "compat_unresolved_space")
		controller := f.controller(compatControllerSpec{Name: "Ctrl", IP: "10.0.0.7"})
		container := f.container(compatContainerSpec{Name: "A", Controller: controller, Segment: 0, Config: linearConfig(10)})
		mapped := f.bin(compatBinSpec{Name: "a1", Container: container, LedIndex: validInt64(3), Width: validInt64(1)})
		unmapped := f.bin(compatBinSpec{Name: "a2", Container: container})
		f.setFlag(ledspace.FlagKey, ledspace.Unresolved)

		baseline := f.capture()

		if baseline.Space != ledspace.Unresolved || !baseline.SpaceKnown {
			t.Errorf("space = %q known=%v, want an unresolved but recognised value", baseline.Space, baseline.SpaceKnown)
		}
		if !baseline.LocateBlocked {
			t.Error("an unresolved coordinate space must block locate and flash")
		}
		if !compatHasCode(baseline, CodeUnresolvedSpace) {
			t.Errorf("diagnostics = %+v, want %q", baseline.Diagnostics, CodeUnresolvedSpace)
		}
		for _, binID := range []int64{mapped, unmapped} {
			if target := compatTarget(t, baseline, binID); target.Resolved || target.Refusal != CodeUnresolvedSpace {
				t.Errorf("bin %d target = %+v, want refusal %q", binID, target, CodeUnresolvedSpace)
			}
		}
	})
}

func TestCompatibilityBaselineNullAndNegativeIndices(t *testing.T) {
	f := setupCompatFixture(t, "compat_null_indices")
	controller := f.controller(compatControllerSpec{Name: "Ctrl", IP: "10.0.0.7"})
	container := f.container(compatContainerSpec{Name: "A", Controller: controller, Segment: 0, Config: linearConfig(10)})
	nullIndex := f.bin(compatBinSpec{Name: "null-index", Container: container})
	zeroWidth := f.bin(compatBinSpec{Name: "zero-width", Container: container, LedIndex: validInt64(5), Width: validInt64(0)})
	negativeWidth := f.bin(compatBinSpec{Name: "negative-width", Container: container, LedIndex: validInt64(6), Width: validInt64(-3)})
	negativeIndex := f.bin(compatBinSpec{Name: "negative-index", Container: container, LedIndex: validInt64(-1), Width: validInt64(2)})
	nullWidth := f.bin(compatBinSpec{Name: "null-width", Container: container, LedIndex: validInt64(7)})

	baseline := f.capture()

	if bin := compatRawBin(t, baseline.Raw, nullIndex); bin.LedIndex.Valid || bin.Width.Valid {
		t.Errorf("raw bin = %+v, want SQL NULL index and width preserved", bin)
	}
	if bin := compatRawBin(t, baseline.Raw, zeroWidth); !bin.Width.Valid || bin.Width.Int64 != 0 {
		t.Errorf("raw bin = %+v, want the stored zero width preserved", bin)
	}
	if bin := compatRawBin(t, baseline.Raw, negativeWidth); !bin.Width.Valid || bin.Width.Int64 != -3 {
		t.Errorf("raw bin = %+v, want the stored negative width preserved", bin)
	}
	if bin := compatRawBin(t, baseline.Raw, negativeIndex); !bin.LedIndex.Valid || bin.LedIndex.Int64 != -1 {
		t.Errorf("raw bin = %+v, want the stored negative index preserved", bin)
	}
	if bin := compatRawBin(t, baseline.Raw, nullWidth); bin.Width.Valid {
		t.Errorf("raw bin = %+v, want SQL NULL width preserved", bin)
	}

	if target := compatTarget(t, baseline, nullIndex); target.Resolved || target.Refusal != CodeUnmapped || target.EffectiveWidth != 1 {
		t.Errorf("null index target = %+v, want refusal %q with effective width 1", target, CodeUnmapped)
	}
	for _, binID := range []int64{zeroWidth, negativeWidth, nullWidth} {
		target := compatTarget(t, baseline, binID)
		if !target.Resolved || target.Count != 1 || target.EffectiveWidth != 1 {
			t.Errorf("bin %d target = %+v, want a resolved single-LED span", binID, target)
		}
	}
	negative := compatTarget(t, baseline, negativeIndex)
	if negative.Resolved || negative.Refusal != CodeNegativeIndex || negative.EffectiveWidth != 2 {
		t.Errorf("negative index target = %+v, want refusal %q preserving width 2", negative, CodeNegativeIndex)
	}

	// The capture reports invalid data; it never repairs it in the database.
	stored, err := f.store.GetBinsByContainer(f.ctx, sql.NullInt64{Int64: container, Valid: true})
	if err != nil {
		t.Fatalf("read stored bins: %v", err)
	}
	byName := map[string]db.Bin{}
	for _, bin := range stored {
		byName[bin.Name] = bin
	}
	if bin := byName["negative-width"]; !bin.Width.Valid || bin.Width.Int64 != -3 {
		t.Errorf("stored bin = %+v, want the negative width left in place", bin)
	}
	if bin := byName["negative-index"]; !bin.LedIndex.Valid || bin.LedIndex.Int64 != -1 {
		t.Errorf("stored bin = %+v, want the negative index left in place", bin)
	}
	if bin := byName["null-width"]; bin.Width.Valid {
		t.Errorf("stored bin = %+v, want the NULL width left in place", bin)
	}
}

func TestCompatibilityBaselineUnsafeSegmentSpans(t *testing.T) {
	f := setupCompatFixture(t, "compat_unsafe_segment")
	controller := f.controller(compatControllerSpec{Name: "Ctrl", IP: "10.0.0.7", LedCount: 4})
	allocated := f.container(compatContainerSpec{Name: "Allocated", Controller: controller, Segment: 0, LedStart: 0, LedCount: 4, Config: linearConfig(4)})
	overrun := f.bin(compatBinSpec{Name: "overrun", Container: allocated, LedIndex: validInt64(3), Width: validInt64(5)})
	unallocated := f.container(compatContainerSpec{Name: "Unallocated", Controller: controller, Segment: 0, Config: linearConfig(4)})
	overflow := f.bin(compatBinSpec{Name: "overflow", Container: unallocated, LedIndex: validInt64(math.MaxInt64), Width: validInt64(5)})
	invalid := f.container(compatContainerSpec{Name: "Invalid", Controller: controller, Segment: 1, LedStart: -1, LedCount: 5, Config: linearConfig(5)})
	f.bin(compatBinSpec{Name: "valid", Container: invalid, LedIndex: validInt64(0), Width: validInt64(1)})

	baseline := f.capture()

	wide := compatTarget(t, baseline, overrun)
	if !wide.Resolved || wide.Start != 3 || wide.Count != 5 {
		t.Errorf("overrun target = %+v, want the mapper's would-be range recorded unchanged", wide)
	}
	if !compatHasDiagnosticFor(baseline, CodeRangeOutsideAllocation, overrun) {
		t.Errorf("diagnostics = %+v, want a range outside the allocation for bin %d", baseline.Diagnostics, overrun)
	}
	if !compatHasDiagnosticFor(baseline, CodeRangeBeyondCapacity, overrun) {
		t.Errorf("diagnostics = %+v, want a range beyond the controller capacity for bin %d", baseline.Diagnostics, overrun)
	}
	capacity := compatDiagnosticsFor(baseline, CodeRangeBeyondCapacity)
	if len(capacity) != 1 || capacity[0].Capacity != 4 || capacity[0].Index != 3 || capacity[0].Width != 5 {
		t.Errorf("diagnostics = %+v, want one finding with capacity 4, index 3 and width 5", capacity)
	}

	overflowing := compatTarget(t, baseline, overflow)
	if !overflowing.Resolved || overflowing.Start != math.MaxInt64 || overflowing.Count != 5 {
		t.Errorf("overflow target = %+v, want the overflowed span recorded unchanged", overflowing)
	}
	if !compatHasDiagnosticFor(baseline, CodeRangeOverflow, overflow) {
		t.Errorf("diagnostics = %+v, want a range overflow for bin %d", baseline.Diagnostics, overflow)
	}
	if compatHasDiagnosticFor(baseline, CodeRangeBeyondCapacity, overflow) || compatHasDiagnosticFor(baseline, CodeRangeOutsideAllocation, overflow) {
		t.Errorf("diagnostics = %+v, want the overflowing span reported once", compatDiagnosticsFor(baseline, CodeRangeOverflow))
	}

	if !compatHasContainerDiagnostic(baseline, CodeInvalidAllocation, invalid) {
		t.Errorf("diagnostics = %+v, want an invalid allocation for container %d", baseline.Diagnostics, invalid)
	}
}

func TestCompatibilityBaselineDrawerRefusals(t *testing.T) {
	f := setupCompatFixture(t, "compat_drawer_refusals")
	f.setFlag(ledspace.FlagKey, ledspace.Drawer)
	controller := f.controller(compatControllerSpec{Name: "Ctrl", IP: "10.0.0.7"})
	overflowing := f.container(compatContainerSpec{Name: "Overflow", Controller: controller, Segment: 0, LedStart: math.MaxInt64, LedCount: 2, Config: linearConfig(2)})
	overflow := f.bin(compatBinSpec{Name: "overflow", Container: overflowing, LedIndex: validInt64(1), Width: validInt64(1)})
	unallocated := f.container(compatContainerSpec{Name: "Unallocated", Controller: controller, Segment: 0, Config: linearConfig(5)})
	noAllocation := f.bin(compatBinSpec{Name: "no-allocation", Container: unallocated, LedIndex: validInt64(0), Width: validInt64(1)})
	short := f.container(compatContainerSpec{Name: "Short", Controller: controller, Segment: 0, LedStart: 0, LedCount: 3, Config: linearConfig(3)})
	outside := f.bin(compatBinSpec{Name: "outside", Container: short, LedIndex: validInt64(4), Width: validInt64(1)})
	negative := f.bin(compatBinSpec{Name: "negative", Container: short, LedIndex: validInt64(-2), Width: validInt64(1)})

	baseline := f.capture()

	if target := compatTarget(t, baseline, overflow); target.Resolved || target.Refusal != CodeIndexOverflow {
		t.Errorf("overflow target = %+v, want refusal %q", target, CodeIndexOverflow)
	}
	if target := compatTarget(t, baseline, noAllocation); target.Resolved || target.Refusal != CodeNoDrawerAllocation {
		t.Errorf("unallocated target = %+v, want refusal %q", target, CodeNoDrawerAllocation)
	}
	if target := compatTarget(t, baseline, outside); target.Resolved || target.Refusal != CodeIndexOutsideAllocation {
		t.Errorf("outside target = %+v, want refusal %q", target, CodeIndexOutsideAllocation)
	}
	if target := compatTarget(t, baseline, negative); target.Resolved || target.Refusal != CodeNegativeIndex {
		t.Errorf("negative target = %+v, want refusal %q", target, CodeNegativeIndex)
	}
	if !compatHasContainerDiagnostic(baseline, CodeInvalidAllocation, overflowing) {
		t.Errorf("diagnostics = %+v, want an invalid allocation for container %d", baseline.Diagnostics, overflowing)
	}
	if compatHasCode(baseline, CodeUnclassifiedRefusal) {
		t.Errorf("diagnostics = %+v, want every mapper refusal classified", baseline.Diagnostics)
	}
}

func TestCompatibilityBaselineInventoryStockPreservation(t *testing.T) {
	f := setupCompatFixture(t, "compat_stock")
	controller := f.controller(compatControllerSpec{Name: "Ctrl", IP: "10.0.0.7"})
	container := f.container(compatContainerSpec{Name: "A", Controller: controller, Segment: 0, Config: linearConfig(10)})
	firstBin := f.bin(compatBinSpec{Name: "a1", Container: container, LedIndex: validInt64(0), Width: validInt64(1)})
	secondBin := f.bin(compatBinSpec{Name: "a2", Container: container, LedIndex: validInt64(1), Width: validInt64(1)})
	multi := f.part("Resistor")
	orphaned := f.part("Orphaned")
	empty := f.part("Empty")
	f.assignment(multi, validInt64(firstBin), 5)
	f.assignment(multi, validInt64(secondBin), 2)
	f.assignment(orphaned, sql.NullInt64{}, 0)
	f.assignment(empty, validInt64(firstBin), 0)

	baseline := f.capture()

	if len(baseline.Raw.Parts) != 3 {
		t.Fatalf("captured %d parts, want 3", len(baseline.Raw.Parts))
	}
	for i, want := range []struct {
		id   int64
		name string
	}{{multi, "Resistor"}, {orphaned, "Orphaned"}, {empty, "Empty"}} {
		if part := baseline.Raw.Parts[i]; part.ID != want.id || part.Name != want.name {
			t.Errorf("part %d = %+v, want id %d named %q", i, part, want.id, want.name)
		}
	}

	want := []struct {
		partID   int64
		binID    sql.NullInt64
		quantity int64
	}{
		{multi, validInt64(firstBin), 5},
		{multi, validInt64(secondBin), 2},
		{orphaned, sql.NullInt64{}, 0},
		{empty, validInt64(firstBin), 0},
	}
	if len(baseline.Raw.PartAssignments) != len(want) {
		t.Fatalf("captured %d assignments, want %d", len(baseline.Raw.PartAssignments), len(want))
	}
	total := int64(0)
	for i, expected := range want {
		assignment := baseline.Raw.PartAssignments[i]
		if assignment.PartID != expected.partID || assignment.Quantity != expected.quantity || assignment.BinID != expected.binID {
			t.Errorf("assignment %d = %+v, want part %d bin %+v quantity %d", i, assignment, expected.partID, expected.binID, expected.quantity)
		}
		if assignment.ID <= 0 {
			t.Errorf("assignment %d = %+v, want the stored id preserved", i, assignment)
		}
		total += assignment.Quantity
	}
	if total != 7 {
		t.Errorf("captured total quantity = %d, want 7", total)
	}
	if len(baseline.Diagnostics) != 0 {
		t.Errorf("diagnostics = %+v, want none for a fully mapped drawer", baseline.Diagnostics)
	}
}

func TestCompatibilityBaselineFingerprintSensitivity(t *testing.T) {
	cases := []struct {
		name          string
		wantInventory bool
		wantMapping   bool
		wantTargets   bool
		setup         func(f compatFixture) compatIDs
		mutate        func(f compatFixture, ids compatIDs, before CompatibilityBaseline)
	}{
		{
			name:          "part quantity",
			wantInventory: true,
			setup:         compatBaseScenario,
			mutate: func(f compatFixture, ids compatIDs, before CompatibilityBaseline) {
				f.exec("UPDATE part_assignments SET quantity = 9 WHERE id = ?", before.Raw.PartAssignments[0].ID)
			},
		},
		{
			name:          "part name",
			wantInventory: true,
			setup:         compatBaseScenario,
			mutate: func(f compatFixture, ids compatIDs, before CompatibilityBaseline) {
				f.exec("UPDATE parts SET name = 'Capacitor' WHERE id = ?", ids.part)
			},
		},
		{
			name:        "bin index",
			wantMapping: true,
			wantTargets: true,
			setup:       compatBaseScenario,
			mutate: func(f compatFixture, ids compatIDs, before CompatibilityBaseline) {
				f.exec("UPDATE bins SET led_index = 2 WHERE id = ?", ids.bin)
			},
		},
		{
			name:          "new bin identity",
			wantInventory: true,
			wantMapping:   true,
			wantTargets:   true,
			setup:         compatBaseScenario,
			mutate: func(f compatFixture, ids compatIDs, before CompatibilityBaseline) {
				f.exec("INSERT INTO bins (name, container_id, led_index, width) VALUES ('d2', ?, 3, 1)", ids.container)
			},
		},
		{
			name:        "sql null validity",
			wantMapping: true,
			setup:       compatBaseScenario,
			mutate: func(f compatFixture, ids compatIDs, before CompatibilityBaseline) {
				f.exec("UPDATE bins SET width = NULL WHERE id = ?", ids.bin)
			},
		},
		{
			name:        "drawer allocation",
			wantMapping: true,
			wantTargets: true,
			setup: func(f compatFixture) compatIDs {
				ids := compatBaseScenario(f)
				f.setFlag(ledspace.FlagKey, ledspace.Drawer)
				return ids
			},
			mutate: func(f compatFixture, ids compatIDs, before CompatibilityBaseline) {
				f.exec("UPDATE containers SET led_start = 4 WHERE id = ?", ids.container)
			},
		},
		{
			name:        "controller address",
			wantMapping: true,
			wantTargets: true,
			setup:       compatBaseScenario,
			mutate: func(f compatFixture, ids compatIDs, before CompatibilityBaseline) {
				f.exec("UPDATE controllers SET ip_address = '10.0.0.8' WHERE id = ?", ids.controller)
			},
		},
		{
			name:        "coordinate space flag",
			wantMapping: true,
			setup:       compatBaseScenario,
			mutate: func(f compatFixture, ids compatIDs, before CompatibilityBaseline) {
				f.setFlag(Migration005FlagKey, "true")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := setupCompatFixture(t, "compat_sensitivity")
			ids := tc.setup(f)
			before := f.capture()
			tc.mutate(f, ids, before)
			after := f.capture()
			compatAssertDelta(t,
				compatDomainDelta{Domain: "inventory", Before: before.InventoryFingerprint, After: after.InventoryFingerprint, Changed: tc.wantInventory},
				compatDomainDelta{Domain: "mapping", Before: before.MappingFingerprint, After: after.MappingFingerprint, Changed: tc.wantMapping},
				compatDomainDelta{Domain: "targets", Before: before.TargetsFingerprint, After: after.TargetsFingerprint, Changed: tc.wantTargets},
			)
		})
	}
}

// compatBaseScenario builds one controller, one allocated drawer, one mapped bin
// and one stocked part assignment, and returns the row ids.
func compatBaseScenario(f compatFixture) compatIDs {
	controller := f.controller(compatControllerSpec{Name: "Ctrl", IP: "10.0.0.7", Port: validInt64(80)})
	container := f.container(compatContainerSpec{Name: "Drawer", Controller: controller, Segment: 0, LedStart: 0, LedCount: 5, Config: linearConfig(5)})
	bin := f.bin(compatBinSpec{Name: "d1", Container: container, LedIndex: validInt64(1), Width: validInt64(1)})
	part := f.part("Resistor")
	f.assignment(part, validInt64(bin), 3)
	return compatIDs{controller: controller, container: container, bin: bin, part: part}
}

func TestCompatibilityBaselineFingerprintsIgnoreRowOrder(t *testing.T) {
	f := setupCompatFixture(t, "compat_order")
	controller := f.controller(compatControllerSpec{Name: "Ctrl", IP: "10.0.0.7"})
	var binIDs []int64
	for index, name := range []string{"A", "B", "C"} {
		container := f.container(compatContainerSpec{Name: name, Controller: controller, Segment: int64(index), Config: linearConfig(5)})
		binIDs = append(binIDs, f.bin(compatBinSpec{Name: name + "1", Container: container, LedIndex: validInt64(0), Width: validInt64(1)}))
		binIDs = append(binIDs, f.bin(compatBinSpec{Name: name + "2", Container: container}))
	}
	first := f.part("Resistor")
	second := f.part("Capacitor")
	f.assignment(first, validInt64(binIDs[0]), 2)
	f.assignment(second, sql.NullInt64{}, 0)
	f.assignment(second, validInt64(binIDs[1]), 3)

	direct := f.capture()
	reversed := f.captureWith(&compatStore{Store: f.store, hooks: compatHooks{reverse: true}})

	if direct.InventoryFingerprint != reversed.InventoryFingerprint ||
		direct.MappingFingerprint != reversed.MappingFingerprint ||
		direct.TargetsFingerprint != reversed.TargetsFingerprint {
		t.Errorf("fingerprints depend on row order: %+v vs %+v", direct.InventoryFingerprint, reversed.InventoryFingerprint)
	}
	if len(reversed.Raw.Bins) != len(direct.Raw.Bins) {
		t.Fatalf("captured %d bins in reverse order and %d in query order", len(reversed.Raw.Bins), len(direct.Raw.Bins))
	}
	if !slices.IsSortedFunc(reversed.Raw.Bins, func(a, b db.Bin) int { return int(a.ID - b.ID) }) {
		t.Errorf("captured bins are not canonicalised by id: %+v", reversed.Raw.Bins)
	}
	if !slices.IsSortedFunc(reversed.Targets, func(a, b CompatibilityBinTarget) int { return int(a.BinID - b.BinID) }) {
		t.Errorf("captured targets are not canonicalised by bin id: %+v", reversed.Targets)
	}
	directJSON, err := json.Marshal(direct)
	if err != nil {
		t.Fatalf("marshal direct baseline: %v", err)
	}
	reversedJSON, err := json.Marshal(reversed)
	if err != nil {
		t.Fatalf("marshal reversed baseline: %v", err)
	}
	if string(directJSON) != string(reversedJSON) {
		t.Errorf("baselines differ by row order:\n%s\n%s", directJSON, reversedJSON)
	}
}

func TestCompatibilityBaselineWithoutPartialResults(t *testing.T) {
	injected := errors.New("injected compatibility failure")

	t.Run("transaction failure", func(t *testing.T) {
		f := setupCompatFixture(t, "compat_tx_failure")
		baseline, err := CaptureCompatibilityBaseline(f.ctx, &compatStore{Store: f.store, fail: injected})
		if !errors.Is(err, injected) {
			t.Fatalf("error = %v, want it to wrap the injected failure", err)
		}
		compatAssertZeroBaseline(t, baseline)
	})

	for _, method := range []string{
		"GetControllers", "GetAllContainers", "GetAllBins",
		"GetAllParts", "GetAllPartAssignments", "GetFlag",
	} {
		t.Run(method+" failure", func(t *testing.T) {
			f := setupCompatFixture(t, "compat_read_failure")
			compatBaseScenario(f)
			store := &compatStore{Store: f.store, hooks: compatHooks{failMethod: method, failErr: injected}}
			baseline, err := CaptureCompatibilityBaseline(f.ctx, store)
			if !errors.Is(err, injected) {
				t.Fatalf("error = %v, want it to wrap the injected failure", err)
			}
			compatAssertZeroBaseline(t, baseline)
		})
	}

	t.Run("serialization failure", func(t *testing.T) {
		f := setupCompatFixture(t, "compat_serialization_failure")
		ids := compatBaseScenario(f)
		f.exec("UPDATE parts SET unit_cost = ? WHERE id = ?", math.Inf(1), ids.part)
		captured, captureErr := CaptureCompatibilityBaseline(f.ctx, f.store)
		var captureUnsupported *json.UnsupportedValueError
		if !errors.As(captureErr, &captureUnsupported) {
			t.Fatalf("capture error = %v, want a serialization failure", captureErr)
		}
		compatAssertZeroBaseline(t, captured)

		raw := CompatibilityRaw{Parts: []db.Part{{ID: 1, Name: "Resistor", UnitCost: sql.NullFloat64{Float64: math.NaN(), Valid: true}}}}
		fingerprint, err := FingerprintInventory(raw)
		if err == nil {
			t.Fatalf("fingerprint = %q, want an error for a value that cannot be serialized", fingerprint.String())
		}
		var unsupported *json.UnsupportedValueError
		if !errors.As(err, &unsupported) {
			t.Errorf("error = %v, want a JSON serialization failure", err)
		}
		if fingerprint.String() != "" {
			t.Errorf("fingerprint = %q, want no digest for a failed serialization", fingerprint.String())
		}
		baseline, err := buildCompatibilityBaseline(raw, CompatibilityFlags{})
		if err == nil {
			t.Fatal("buildCompatibilityBaseline accepted a value that cannot be serialized")
		}
		compatAssertZeroBaseline(t, baseline)
	})

	t.Run("cancelled context", func(t *testing.T) {
		f := setupCompatFixture(t, "compat_cancelled")
		compatBaseScenario(f)
		ctx, cancel := context.WithCancel(f.ctx)
		cancel()
		baseline, err := CaptureCompatibilityBaseline(ctx, f.store)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want a cancelled capture", err)
		}
		compatAssertZeroBaseline(t, baseline)
	})
}

// compatMutationEvidence is the observable database state a read-only capture
// must not change: the captured rows, the captured flags and the audit log.
type compatMutationEvidence struct {
	rowsJSON  string
	flagsJSON string
	auditRows int
}

func compatMutationEvidenceOf(t *testing.T, f compatFixture) compatMutationEvidence {
	t.Helper()
	raw, flags, err := readCompatibilitySnapshot(f.ctx, f.store)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	rows, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal rows: %v", err)
	}
	flagPayload, err := json.Marshal(flags)
	if err != nil {
		t.Fatalf("marshal flags: %v", err)
	}
	audits, err := f.store.GetAllAuditLogs(f.ctx)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	return compatMutationEvidence{rowsJSON: string(rows), flagsJSON: string(flagPayload), auditRows: len(audits)}
}

func TestCompatibilityBaselinePerformsNoWrites(t *testing.T) {
	f := setupCompatFixture(t, "compat_readonly")
	ids := compatBaseScenario(f)
	f.setFlag(Migration005FlagKey, "true")
	f.setFlag(DrawerAllocationBackfilledFlagKey, "true")

	before := compatMutationEvidenceOf(t, f)
	f.dbConn.SetMaxOpenConns(1)
	f.exec("PRAGMA query_only = ON")
	first := f.capture()
	second := f.capture()
	after := compatMutationEvidenceOf(t, f)

	if first.InventoryFingerprint != second.InventoryFingerprint ||
		first.MappingFingerprint != second.MappingFingerprint ||
		first.TargetsFingerprint != second.TargetsFingerprint {
		t.Error("two captures of the same state must produce the same fingerprints")
	}
	if before != after {
		t.Errorf("the capture changed the database: before %+v after %+v", before, after)
	}
	if after.auditRows != 0 {
		t.Errorf("the capture wrote %d audit rows, want none", after.auditRows)
	}
	if target := compatTarget(t, first, ids.bin); !target.Resolved {
		t.Errorf("target = %+v, want the fixture bin resolved", target)
	}
}

func TestCompatibilityBaselineConcurrentSnapshotIsConsistent(t *testing.T) {
	f := setupCompatFixture(t, "compat_concurrent")
	controller := f.controller(compatControllerSpec{Name: "Ctrl", IP: "10.0.0.7"})
	container := f.container(compatContainerSpec{Name: "A", Controller: controller, Segment: 0, LedStart: 0, LedCount: 4, Config: linearConfig(4)})
	f.bin(compatBinSpec{Name: "a1", Container: container, LedIndex: validInt64(1), Width: validInt64(1)})
	before := f.capture()

	writerCtx, stopWriter := context.WithTimeout(f.ctx, 30*time.Second)
	defer stopWriter()
	captureCtx, stopCapture := context.WithTimeout(f.ctx, 30*time.Second)
	defer stopCapture()

	snapshotEstablished := make(chan struct{})
	writerDone := make(chan struct{})
	hooked := &compatStore{Store: f.store, hooks: compatHooks{
		afterMethod: "GetControllers",
		after: func() {
			// The capture's first read has established its snapshot: let the writer
			// commit now and hold the capture until that commit is durable.
			close(snapshotEstablished)
			<-writerDone
		},
	}}

	writerErr := make(chan error, 1)
	go func() {
		defer close(writerDone)
		select {
		case <-snapshotEstablished:
		case <-writerCtx.Done():
			writerErr <- writerCtx.Err()
			return
		}
		if _, err := f.store.CreateBin(writerCtx, db.CreateBinParams{
			Name:        "late",
			ContainerID: sql.NullInt64{Int64: container, Valid: true},
			LedIndex:    validInt64(3),
			Width:       validInt64(1),
		}); err != nil {
			writerErr <- err
			return
		}
		writerErr <- f.store.SetFlag(writerCtx, db.SetFlagParams{Key: DrawerAllocationBackfilledFlagKey, Value: "true"})
	}()

	during := f.captureIn(captureCtx, hooked)
	if err := <-writerErr; err != nil {
		t.Fatalf("concurrent writer: %v", err)
	}

	if len(during.Raw.Bins) != 1 {
		t.Errorf("captured %d bins during the concurrent write, want the one committed before the snapshot", len(during.Raw.Bins))
	}
	if during.MappingFingerprint != before.MappingFingerprint || during.InventoryFingerprint != before.InventoryFingerprint ||
		during.TargetsFingerprint != before.TargetsFingerprint {
		t.Error("the capture must observe the snapshot it started from, not the concurrent writer's state")
	}
	if during.Flags.DrawerAllocationBackfilled.Present {
		t.Errorf("captured flag = %+v, want the pre-write absent flag", during.Flags.DrawerAllocationBackfilled)
	}

	// The writer really committed, so the test cannot pass by blocking it.
	after := f.capture()
	if len(after.Raw.Bins) != 2 {
		t.Fatalf("captured %d bins after the writer finished, want 2", len(after.Raw.Bins))
	}
	if !after.Flags.DrawerAllocationBackfilled.Present {
		t.Errorf("captured flag = %+v, want the committed flag", after.Flags.DrawerAllocationBackfilled)
	}
	compatAssertDelta(t,
		compatDomainDelta{Domain: "mapping", Before: before.MappingFingerprint, After: after.MappingFingerprint, Changed: true},
	)
}

func TestCompatibilityBaselineDetectsMissingRelationships(t *testing.T) {
	f := setupCompatFixture(t, "compat_missing_relationships")
	controller := f.controller(compatControllerSpec{Name: "Ctrl", IP: "10.0.0.7"})
	f.execWithForeignKeysOff("INSERT INTO containers (name, controller_id, segment_id) VALUES ('Ghost', 999, 0)")
	f.execWithForeignKeysOff("INSERT INTO bins (name, container_id, led_index, width) VALUES ('orphan', 999, 0, 1)")

	baseline := f.capture()

	if baseline.LocateBlocked {
		t.Error("a segment coordinate space must not block locate")
	}
	orphan := compatRawBinByName(t, baseline.Raw, "orphan")
	target := compatTarget(t, baseline, orphan.ID)
	if target.Resolved || target.Refusal != CodeContainerNotFound {
		t.Errorf("orphan target = %+v, want refusal %q", target, CodeContainerNotFound)
	}
	if target.Address != "" || target.ControllerID != 0 {
		t.Errorf("orphan target = %+v, want no invented controller", target)
	}
	if !compatHasDiagnosticFor(baseline, CodeContainerNotFound, orphan.ID) {
		t.Errorf("diagnostics = %+v, want %q for bin %d", baseline.Diagnostics, CodeContainerNotFound, orphan.ID)
	}
	var ghostID int64
	for _, container := range baseline.Raw.Containers {
		if container.Name == "Ghost" {
			ghostID = container.ID
		}
	}
	if ghostID == 0 {
		t.Fatalf("the ghost container is missing from the captured rows: %+v", baseline.Raw.Containers)
	}
	if !compatHasContainerDiagnostic(baseline, CodeContainerMissingController, ghostID) {
		t.Errorf("diagnostics = %+v, want %q for container %d", baseline.Diagnostics, CodeContainerMissingController, ghostID)
	}
	if diagnostics := compatDiagnosticsFor(baseline, CodeContainerMissingController); len(diagnostics) != 1 || diagnostics[0].ControllerID != 999 {
		t.Errorf("diagnostics = %+v, want one finding for the missing controller 999", diagnostics)
	}
	if controller != 0 && baseline.Raw.Controllers[0].ID != controller {
		t.Errorf("captured controllers = %+v, want the stored controller %d", baseline.Raw.Controllers, controller)
	}
	ghostBin := f.bin(compatBinSpec{Name: "ghost-bin", Container: ghostID, LedIndex: validInt64(1), Width: validInt64(1)})
	ghostTarget := compatTarget(t, f.capture(), ghostBin)
	if ghostTarget.Resolved || ghostTarget.Refusal != CodeContainerMissingController {
		t.Errorf("ghost target = %+v, want missing-controller refusal", ghostTarget)
	}
}

func TestCompatibilityBaselineTreatsNullContainerAsUnassigned(t *testing.T) {
	f := setupCompatFixture(t, "compat_unassigned_container")
	ids := compatBaseScenario(f)
	f.exec(`INSERT INTO containers (id, name, controller_id, segment_id, led_start, led_count) VALUES (0, 'Zero', ?, 0, 0, 5)`, ids.controller)
	f.exec(`INSERT INTO bins (name, container_id, led_index, width) VALUES ('unassigned', NULL, 0, 1)`)

	baseline := f.capture()
	unassigned := compatRawBinByName(t, baseline.Raw, "unassigned")
	target := compatTarget(t, baseline, unassigned.ID)
	if target.Resolved || target.Refusal != CodeContainerUnassigned {
		t.Fatalf("unassigned target = %+v, want an unassigned-container refusal", target)
	}
	if target.Address != "" || target.ControllerID != 0 {
		t.Errorf("unassigned target = %+v, want no electrical controller", target)
	}
	if !compatHasDiagnosticFor(baseline, CodeContainerUnassigned, unassigned.ID) {
		t.Errorf("diagnostics = %+v, want %q for bin %d", baseline.Diagnostics, CodeContainerUnassigned, unassigned.ID)
	}
	for _, diagnostic := range compatDiagnosticsFor(baseline, CodeContainerUnassigned) {
		if diagnostic.BinID == unassigned.ID && diagnostic.Severity != CompatibilitySeverityInfo {
			t.Errorf("unassigned diagnostic severity = %q, want info", diagnostic.Severity)
		}
	}
}

func TestCompatibilityBaselineEquivalentTargets(t *testing.T) {
	f := setupCompatFixture(t, "compat_equivalent_targets")
	ids := compatBaseScenario(f)
	f.exec("UPDATE containers SET led_start = 3 WHERE id = ?", ids.container)
	f.exec("UPDATE bins SET led_index = 4 WHERE id = ?", ids.bin)
	before := f.capture()

	for _, width := range []any{nil, int64(0), int64(-3)} {
		f.exec("UPDATE bins SET width = ? WHERE id = ?", width, ids.bin)
		after := f.capture()
		compatAssertDelta(t,
			compatDomainDelta{Domain: "inventory", Before: before.InventoryFingerprint, After: after.InventoryFingerprint},
			compatDomainDelta{Domain: "mapping", Before: before.MappingFingerprint, After: after.MappingFingerprint, Changed: true},
			compatDomainDelta{Domain: "targets", Before: before.TargetsFingerprint, After: after.TargetsFingerprint},
		)
	}

	f.setFlag(ledspace.FlagKey, ledspace.Drawer)
	f.exec("UPDATE bins SET led_index = 1 WHERE id = ?", ids.bin)
	converted := f.capture()
	target := compatTarget(t, converted, ids.bin)
	if !target.Resolved || target.Start != 4 || target.Count != 1 {
		t.Fatalf("converted target = %+v, want unchanged start 4 count 1", target)
	}
	compatAssertDelta(t,
		compatDomainDelta{Domain: "inventory", Before: before.InventoryFingerprint, After: converted.InventoryFingerprint},
		compatDomainDelta{Domain: "mapping", Before: before.MappingFingerprint, After: converted.MappingFingerprint, Changed: true},
		compatDomainDelta{Domain: "targets", Before: before.TargetsFingerprint, After: converted.TargetsFingerprint},
	)
}

func TestCompatibilityBaselineEmptyRowSets(t *testing.T) {
	f := setupCompatFixture(t, "compat_empty_rows")
	baseline := f.capture()
	empty := CompatibilityRaw{
		Controllers: []db.Controller{}, Containers: []db.Container{},
		Bins: []db.Bin{}, Parts: []db.Part{}, PartAssignments: []db.PartAssignment{},
	}
	nilRows := CompatibilityRaw{}
	for _, raw := range []CompatibilityRaw{empty, nilRows} {
		inventory, err := FingerprintInventory(raw)
		if err != nil {
			t.Fatal(err)
		}
		mapping, err := FingerprintMapping(raw, baseline.Flags)
		if err != nil {
			t.Fatal(err)
		}
		if inventory != baseline.InventoryFingerprint || mapping != baseline.MappingFingerprint {
			t.Error("nil and empty row sets must have identical fingerprints")
		}
	}
	for _, targets := range [][]CompatibilityBinTarget{nil, {}} {
		fingerprint, err := FingerprintTargets(targets)
		if err != nil {
			t.Fatal(err)
		}
		if fingerprint != baseline.TargetsFingerprint {
			t.Error("nil and empty target sets must have identical fingerprints")
		}
	}
}

func TestCompatibilityBaselineInvalidTextFailsCapture(t *testing.T) {
	invalid := string([]byte{0xff})
	for _, column := range []string{"part", "controller", "flag"} {
		t.Run(column, func(t *testing.T) {
			f := setupCompatFixture(t, "compat_invalid_text")
			ids := compatBaseScenario(f)
			switch column {
			case "part":
				f.exec("UPDATE parts SET name = ? WHERE id = ?", invalid, ids.part)
			case "controller":
				f.exec("UPDATE controllers SET ip_address = ? WHERE id = ?", invalid, ids.controller)
			case "flag":
				f.setFlag(ledspace.FlagKey, invalid)
			}
			baseline, err := CaptureCompatibilityBaseline(f.ctx, f.store)
			if !errors.Is(err, errCompatibilityInvalidUTF8) {
				t.Fatalf("capture error = %v, want invalid text rejection", err)
			}
			compatAssertZeroBaseline(t, baseline)
		})
	}
	fingerprint, err := FingerprintTargets([]CompatibilityBinTarget{{BinID: 1, Resolved: true, Address: invalid}})
	if !errors.Is(err, errCompatibilityInvalidUTF8) || fingerprint != (CompatibilityFingerprint{}) {
		t.Fatalf("target fingerprint = %+v, error = %v, want rejection", fingerprint, err)
	}
}

func TestCompatibilityBaselineSpaceGuardPrecedesBrokenRelationships(t *testing.T) {
	for _, space := range []string{ledspace.Unresolved, "unknown"} {
		t.Run(space, func(t *testing.T) {
			f := setupCompatFixture(t, "compat_guard_priority")
			ids := compatBaseScenario(f)
			f.setFlag(ledspace.FlagKey, space)
			f.execWithForeignKeysOff("DELETE FROM containers WHERE id = ?", ids.container)
			baseline := f.capture()
			want := CodeUnknownSpace
			if space == ledspace.Unresolved {
				want = CodeUnresolvedSpace
			}
			target := compatTarget(t, baseline, ids.bin)
			if target.Resolved || target.Refusal != want {
				t.Errorf("target = %+v, want coordinate-guard refusal %s", target, want)
			}
			if !compatHasDiagnosticFor(baseline, CodeContainerNotFound, ids.bin) {
				t.Error("broken relationship must remain a diagnostic despite the space guard")
			}
		})
	}
}

func TestCompatibilityBaselineInvalidAllocationMetadata(t *testing.T) {
	f := setupCompatFixture(t, "compat_invalid_metadata")
	controller := f.controller(compatControllerSpec{Name: "Ctrl", IP: "10.0.0.7", LedCount: -1})
	negativeCount := f.container(compatContainerSpec{Name: "negative-count", Controller: controller, Segment: 0, LedCount: -1})
	negativeStart := f.container(compatContainerSpec{Name: "negative-start", Controller: controller, Segment: 1, LedStart: -1})
	negativeSegment := f.container(compatContainerSpec{Name: "negative-segment", Controller: controller, Segment: -1})
	bin := f.bin(compatBinSpec{Name: "mapped", Container: negativeSegment, LedIndex: validInt64(0), Width: validInt64(1)})
	baseline := f.capture()
	for _, container := range []int64{negativeCount, negativeStart} {
		if !compatHasContainerDiagnostic(baseline, CodeInvalidAllocation, container) {
			t.Errorf("missing invalid allocation diagnostic for container %d", container)
		}
	}
	if !compatHasContainerDiagnostic(baseline, CodeInvalidSegment, negativeSegment) ||
		!compatHasCode(baseline, CodeInvalidControllerCapacity) {
		t.Errorf("missing invalid hardware metadata diagnostics: %+v", baseline.Diagnostics)
	}
	target := compatTarget(t, baseline, bin)
	if !target.Resolved || target.SegmentID != -1 {
		t.Errorf("target = %+v, want the unchanged legacy mapper result", target)
	}
}

// compatOverlapPairPresent reports whether a diagnostic pairs the two bin ids in
// either order.
func compatOverlapPairPresent(diagnostics []CompatibilityDiagnostic, first, second int64) bool {
	for _, diagnostic := range diagnostics {
		if (diagnostic.BinID == first && diagnostic.OtherBinID == second) || (diagnostic.BinID == second && diagnostic.OtherBinID == first) {
			return true
		}
	}
	return false
}

// compatAllocationPairPresent reports whether a diagnostic pairs the two container
// ids in either order.
func compatAllocationPairPresent(diagnostics []CompatibilityDiagnostic, first, second int64) bool {
	for _, diagnostic := range diagnostics {
		if (diagnostic.ContainerID == first && diagnostic.OtherContainerID == second) || (diagnostic.ContainerID == second && diagnostic.OtherContainerID == first) {
			return true
		}
	}
	return false
}

func TestCompatibilityBaselineOverlappingBinSpans(t *testing.T) {
	f := setupCompatFixture(t, "compat_bin_overlap")
	controllerA := f.controller(compatControllerSpec{Name: "A", IP: "10.0.0.7"})
	controllerB := f.controller(compatControllerSpec{Name: "B", IP: "10.0.0.8"})
	a0 := f.container(compatContainerSpec{Name: "a0", Controller: controllerA, Segment: 0, Config: linearConfig(20)})
	a0b := f.container(compatContainerSpec{Name: "a0b", Controller: controllerA, Segment: 0, Config: linearConfig(20)})
	a1 := f.container(compatContainerSpec{Name: "a1", Controller: controllerA, Segment: 1, Config: linearConfig(20)})
	b0 := f.container(compatContainerSpec{Name: "b0", Controller: controllerB, Segment: 0, Config: linearConfig(20)})

	// wide [0,5) and partial [3,5) overlap partially; nested [2,3) lies inside wide
	// and only touches partial; adjacent [5,6) only touches wide.
	wide := f.bin(compatBinSpec{Name: "wide", Container: a0, LedIndex: validInt64(0), Width: validInt64(5)})
	partial := f.bin(compatBinSpec{Name: "partial", Container: a0b, LedIndex: validInt64(3), Width: validInt64(2)})
	nested := f.bin(compatBinSpec{Name: "nested", Container: a0, LedIndex: validInt64(2), Width: validInt64(1)})
	adjacent := f.bin(compatBinSpec{Name: "adjacent", Container: a0, LedIndex: validInt64(5), Width: validInt64(1)})
	otherSegment := f.bin(compatBinSpec{Name: "other-segment", Container: a1, LedIndex: validInt64(0), Width: validInt64(5)})
	otherController := f.bin(compatBinSpec{Name: "other-controller", Container: b0, LedIndex: validInt64(0), Width: validInt64(5)})

	baseline := f.capture()
	overlaps := compatDiagnosticsFor(baseline, CodeOverlappingBinSpan)
	if len(overlaps) != 2 {
		t.Fatalf("overlapping bin diagnostics = %+v, want the two pairs (wide,partial) and (wide,nested)", overlaps)
	}
	for _, diagnostic := range overlaps {
		if diagnostic.Severity != CompatibilitySeverityWarning {
			t.Errorf("diagnostic = %+v, want a warning", diagnostic)
		}
		if diagnostic.ControllerID != controllerA || diagnostic.SegmentID != 0 {
			t.Errorf("diagnostic = %+v, want controller %d segment 0", diagnostic, controllerA)
		}
	}
	if !compatOverlapPairPresent(overlaps, wide, partial) || !compatOverlapPairPresent(overlaps, wide, nested) {
		t.Errorf("diagnostics = %+v, want the pairs %d-%d and %d-%d", overlaps, wide, partial, wide, nested)
	}
	for _, independent := range []int64{adjacent, otherSegment, otherController} {
		for _, diagnostic := range overlaps {
			if diagnostic.BinID == independent || diagnostic.OtherBinID == independent {
				t.Errorf("diagnostic = %+v unexpectedly involves independent bin %d", diagnostic, independent)
			}
		}
	}

	// The diagnostic order must not depend on the order the rows were read in.
	reversed := f.captureWith(&compatStore{Store: f.store, hooks: compatHooks{reverse: true}})
	directDiagnostics, err := json.Marshal(baseline.Diagnostics)
	if err != nil {
		t.Fatalf("marshal diagnostics: %v", err)
	}
	reversedDiagnostics, err := json.Marshal(reversed.Diagnostics)
	if err != nil {
		t.Fatalf("marshal diagnostics: %v", err)
	}
	if string(directDiagnostics) != string(reversedDiagnostics) {
		t.Errorf("diagnostics depend on row order:\n%s\n%s", directDiagnostics, reversedDiagnostics)
	}
}

func TestCompatibilityBaselineOverlappingAllocations(t *testing.T) {
	f := setupCompatFixture(t, "compat_alloc_overlap")
	controllerA := f.controller(compatControllerSpec{Name: "A", IP: "10.0.0.7"})
	controllerB := f.controller(compatControllerSpec{Name: "B", IP: "10.0.0.8"})
	base := f.container(compatContainerSpec{Name: "base", Controller: controllerA, Segment: 0, LedStart: 0, LedCount: 10})
	partial := f.container(compatContainerSpec{Name: "partial", Controller: controllerA, Segment: 0, LedStart: 8, LedCount: 4})
	nested := f.container(compatContainerSpec{Name: "nested", Controller: controllerA, Segment: 0, LedStart: 2, LedCount: 2})
	// adjacent [12,16) only touches partial [8,12).
	adjacent := f.container(compatContainerSpec{Name: "adjacent", Controller: controllerA, Segment: 0, LedStart: 12, LedCount: 4})
	otherSegment := f.container(compatContainerSpec{Name: "other-segment", Controller: controllerA, Segment: 1, LedStart: 0, LedCount: 10})
	otherController := f.container(compatContainerSpec{Name: "other-controller", Controller: controllerB, Segment: 0, LedStart: 0, LedCount: 10})

	baseline := f.capture()
	overlaps := compatDiagnosticsFor(baseline, CodeOverlappingAllocation)
	if len(overlaps) != 2 {
		t.Fatalf("overlapping allocation diagnostics = %+v, want the two pairs (base,partial) and (base,nested)", overlaps)
	}
	for _, diagnostic := range overlaps {
		if diagnostic.Severity != CompatibilitySeverityWarning {
			t.Errorf("diagnostic = %+v, want a warning", diagnostic)
		}
		if diagnostic.ControllerID != controllerA || diagnostic.SegmentID != 0 {
			t.Errorf("diagnostic = %+v, want controller %d segment 0", diagnostic, controllerA)
		}
	}
	if !compatAllocationPairPresent(overlaps, base, partial) || !compatAllocationPairPresent(overlaps, base, nested) {
		t.Errorf("diagnostics = %+v, want pairs base-partial and base-nested", overlaps)
	}
	for _, diagnostic := range overlaps {
		if compatAllocationPairPresent([]CompatibilityDiagnostic{diagnostic}, base, partial) {
			if diagnostic.ContainerID != base || diagnostic.Index != 0 || diagnostic.Width != 10 {
				t.Errorf("diagnostic = %+v, want the earlier allocation [0,10) as the primary span", diagnostic)
			}
		}
	}
	for _, independent := range []int64{adjacent, otherSegment, otherController} {
		for _, diagnostic := range overlaps {
			if diagnostic.ContainerID == independent || diagnostic.OtherContainerID == independent {
				t.Errorf("diagnostic = %+v unexpectedly involves independent container %d", diagnostic, independent)
			}
		}
	}
}

func TestCompatibilityBaselineControllerEndpointDiagnostics(t *testing.T) {
	for _, address := range []string{"10.0.0.7", "wled.local", "10.0.0.7:8080", "[2001:db8::1]", "2001:db8::1", "[::1]:8080", "[fe80::1%25eth0]:80"} {
		t.Run("valid "+address, func(t *testing.T) {
			f := setupCompatFixture(t, "compat_endpoint_valid")
			controller := f.controller(compatControllerSpec{Name: "Ctrl", IP: address, Port: validInt64(8080)})
			container := f.container(compatContainerSpec{Name: "A", Controller: controller, Segment: 0, Config: linearConfig(10)})
			bin := f.bin(compatBinSpec{Name: "a1", Container: container, LedIndex: validInt64(1), Width: validInt64(1)})

			baseline := f.capture()
			if compatHasCode(baseline, CodeControllerEndpointInvalid) || compatHasCode(baseline, CodeControllerEndpointMissing) {
				t.Errorf("diagnostics = %+v, want a usable endpoint for %q", baseline.Diagnostics, address)
			}
			target := compatTarget(t, baseline, bin)
			expectedAddress := controllerTargetAddress(db.Controller{IpAddress: address, Port: validInt64(8080)})
			if !target.Resolved || target.Address != expectedAddress {
				t.Errorf("target = %+v, want a resolved target at configured endpoint %q", target, expectedAddress)
			}
			if address == "2001:db8::1" && target.Address != "[2001:db8::1]:8080" {
				t.Errorf("bare IPv6 target address = %q, want bracketed configured endpoint", target.Address)
			}
			// The raw address and port remain separately preserved; the target above
			// uses the effective authority sent by the WLED client.
			if raw := baseline.Raw.Controllers[0]; raw.IpAddress != address || !raw.Port.Valid || raw.Port.Int64 != 8080 {
				t.Errorf("raw controller = %+v, want the stored address and port preserved", raw)
			}
		})
	}

	invalid := []struct {
		name    string
		address string
		code    CompatibilityCode
	}{
		{"empty", "", CodeControllerEndpointMissing},
		{"blank", "   ", CodeControllerEndpointMissing},
		{"credentials", "user:pass@10.0.0.7", CodeControllerEndpointInvalid},
		{"path", "10.0.0.7/extra", CodeControllerEndpointInvalid},
		{"query", "10.0.0.7?x=1", CodeControllerEndpointInvalid},
		{"fragment", "10.0.0.7#frag", CodeControllerEndpointInvalid},
		{"scheme", "http://10.0.0.7", CodeControllerEndpointInvalid},
		{"bad-port", "10.0.0.7:notaport", CodeControllerEndpointInvalid},
		{"out-of-range-port", "10.0.0.7:65536", CodeControllerEndpointInvalid},

		{"invalid-ipv6-literal", "[not-an-ip]", CodeControllerEndpointInvalid},
		{"unclosed-ipv6", "[::1", CodeControllerEndpointInvalid},
		{"missing-host", ":8080", CodeControllerEndpointInvalid},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			f := setupCompatFixture(t, "compat_endpoint_invalid")
			controller := f.controller(compatControllerSpec{Name: "Ctrl", IP: tc.address})
			container := f.container(compatContainerSpec{Name: "A", Controller: controller, Segment: 0, Config: linearConfig(10)})
			bin := f.bin(compatBinSpec{Name: "a1", Container: container, LedIndex: validInt64(1), Width: validInt64(1)})

			baseline := f.capture()
			findings := compatDiagnosticsFor(baseline, tc.code)
			if len(findings) != 1 || findings[0].ControllerID != controller {
				t.Fatalf("diagnostics = %+v, want one %q for controller %d", baseline.Diagnostics, tc.code, controller)
			}
			// A bad endpoint is reported without altering the legacy target intent.
			target := compatTarget(t, baseline, bin)
			if !target.Resolved || target.Address != tc.address {
				t.Errorf("target = %+v, want the unchanged legacy target for address %q", target, tc.address)
			}
			if raw := baseline.Raw.Controllers[0]; raw.IpAddress != tc.address {
				t.Errorf("raw controller = %+v, want the stored address preserved unchanged", raw)
			}
		})
	}
}

// assertAssignmentDiagnostic checks the one diagnostic carrying assignmentID.
func assertAssignmentDiagnostic(t *testing.T, findings []CompatibilityDiagnostic, assignmentID, partID, binID int64) {
	t.Helper()
	for _, diagnostic := range findings {
		if diagnostic.AssignmentID == assignmentID {
			if diagnostic.PartID != partID || diagnostic.BinID != binID || diagnostic.Severity != CompatibilitySeverityWarning {
				t.Errorf("diagnostic = %+v, want part %d bin %d warning", diagnostic, partID, binID)
			}
			return
		}
	}
	t.Errorf("findings = %+v, want a diagnostic for assignment %d", findings, assignmentID)
}

func TestCompatibilityBaselineDanglingAssignmentReferences(t *testing.T) {
	f := setupCompatFixture(t, "compat_dangling_assignments")
	controller := f.controller(compatControllerSpec{Name: "Ctrl", IP: "10.0.0.7"})
	container := f.container(compatContainerSpec{Name: "A", Controller: controller, Segment: 0, Config: linearConfig(10)})
	bin := f.bin(compatBinSpec{Name: "a1", Container: container, LedIndex: validInt64(0), Width: validInt64(1)})
	spare := f.bin(compatBinSpec{Name: "a2", Container: container, LedIndex: validInt64(1), Width: validInt64(1)})
	part := f.part("Resistor")
	f.assignment(part, validInt64(bin), 3)   // stocked
	f.assignment(part, sql.NullInt64{}, 5)   // legitimate unassigned stock
	f.assignment(part, validInt64(spare), 0) // zero quantity, valid
	// Corrupted references only a real database could carry, inserted with FKs off.
	f.execWithForeignKeysOff("INSERT INTO part_assignments (part_id, bin_id, quantity) VALUES (?, ?, ?)", part, 9999, 2)
	f.execWithForeignKeysOff("INSERT INTO part_assignments (part_id, bin_id, quantity) VALUES (?, ?, ?)", 8888, bin, 1)
	f.execWithForeignKeysOff("INSERT INTO part_assignments (part_id, bin_id, quantity) VALUES (?, ?, ?)", 8888, 9998, 4)

	// A problematic capture performs no writes: the database, flags and audit log
	// are identical before and after, read under PRAGMA query_only.
	before := compatMutationEvidenceOf(t, f)
	f.dbConn.SetMaxOpenConns(1)
	f.exec("PRAGMA query_only = ON")
	baseline := f.capture()
	after := compatMutationEvidenceOf(t, f)
	if before != after {
		t.Errorf("the capture changed the database: before %+v after %+v", before, after)
	}
	if after.auditRows != 0 {
		t.Errorf("the capture wrote %d audit rows, want none", after.auditRows)
	}

	if len(baseline.Raw.PartAssignments) != 6 {
		t.Fatalf("captured %d assignments, want all 6 rows preserved", len(baseline.Raw.PartAssignments))
	}
	assignmentID := func(partID int64, binID sql.NullInt64, quantity int64) int64 {
		for _, assignment := range baseline.Raw.PartAssignments {
			if assignment.PartID == partID && assignment.BinID == binID && assignment.Quantity == quantity {
				return assignment.ID
			}
		}
		t.Fatalf("assignment part %d bin %+v quantity %d is missing from the captured rows", partID, binID, quantity)
		return 0
	}
	for _, expected := range []struct {
		partID   int64
		binID    sql.NullInt64
		quantity int64
	}{
		{part, validInt64(bin), 3},
		{part, sql.NullInt64{}, 5},
		{part, validInt64(spare), 0},
		{part, validInt64(9999), 2},
		{8888, validInt64(bin), 1},
		{8888, validInt64(9998), 4},
	} {
		if id := assignmentID(expected.partID, expected.binID, expected.quantity); id <= 0 {
			t.Errorf("assignment part %d bin %+v quantity %d lost its stored id", expected.partID, expected.binID, expected.quantity)
		}
	}

	missingBin := assignmentID(part, validInt64(9999), 2)
	missingPart := assignmentID(8888, validInt64(bin), 1)
	both := assignmentID(8888, validInt64(9998), 4)
	nullStock := assignmentID(part, sql.NullInt64{}, 5)
	zeroQuantity := assignmentID(part, validInt64(spare), 0)

	binFindings := compatDiagnosticsFor(baseline, CodeAssignmentMissingBin)
	partFindings := compatDiagnosticsFor(baseline, CodeAssignmentMissingPart)
	if len(binFindings) != 2 || len(partFindings) != 2 {
		t.Fatalf("dangling diagnostics: missing-bin %+v missing-part %+v, want two of each", binFindings, partFindings)
	}
	assertAssignmentDiagnostic(t, binFindings, missingBin, part, 9999)
	assertAssignmentDiagnostic(t, binFindings, both, 8888, 9998)
	// The part-only dangling reference reports no offending bin.
	assertAssignmentDiagnostic(t, partFindings, missingPart, 8888, 0)
	assertAssignmentDiagnostic(t, partFindings, both, 8888, 0)

	for _, legitimate := range []int64{nullStock, zeroQuantity} {
		for _, diagnostic := range append(append([]CompatibilityDiagnostic{}, binFindings...), partFindings...) {
			if diagnostic.AssignmentID == legitimate {
				t.Errorf("diagnostic = %+v, want no dangling reference for legitimate assignment %d", diagnostic, legitimate)
			}
		}
	}
}

// compatHooks injects test-only row ordering, read failures and hook points into
// the real store, so the tests exercise the real SQLite path rather than a fake.
type compatHooks struct {
	failMethod  string
	failErr     error
	afterMethod string
	after       func()
	reverse     bool
}

func (h *compatHooks) fire(method string) {
	if h.afterMethod != method || h.after == nil {
		return
	}
	after := h.after
	h.after = nil
	after()
}

// compatStore routes a transaction through the hook querier. When fail is set the
// transaction itself fails.
type compatStore struct {
	db.Store
	hooks compatHooks
	fail  error
}

func (s *compatStore) ExecTx(ctx context.Context, fn func(db.Querier) error) error {
	if s.fail != nil {
		return s.fail
	}
	return s.Store.ExecTx(ctx, func(q db.Querier) error {
		return fn(&compatQuerier{Querier: q, hooks: &s.hooks})
	})
}

// compatQuerier applies the hooks to the reads a capture performs.
type compatQuerier struct {
	db.Querier
	hooks *compatHooks
}

// compatRead applies the failure and ordering hooks around one list read.
func compatRead[T any](hooks *compatHooks, method string, read func() ([]T, error)) ([]T, error) {
	if hooks.failMethod == method {
		return nil, hooks.failErr
	}
	rows, err := read()
	if err != nil {
		return nil, err
	}
	if hooks.reverse {
		slices.Reverse(rows)
	}
	hooks.fire(method)
	return rows, nil
}

func (q *compatQuerier) GetControllers(ctx context.Context) ([]db.Controller, error) {
	return compatRead(q.hooks, "GetControllers", func() ([]db.Controller, error) { return q.Querier.GetControllers(ctx) })
}

func (q *compatQuerier) GetControllersForCompatibility(ctx context.Context) ([]db.GetControllersForCompatibilityRow, error) {
	return compatRead(q.hooks, "GetControllers", func() ([]db.GetControllersForCompatibilityRow, error) {
		return q.Querier.GetControllersForCompatibility(ctx)
	})
}

func (q *compatQuerier) GetAllContainers(ctx context.Context) ([]db.Container, error) {
	return compatRead(q.hooks, "GetAllContainers", func() ([]db.Container, error) { return q.Querier.GetAllContainers(ctx) })
}

func (q *compatQuerier) GetAllBins(ctx context.Context) ([]db.Bin, error) {
	return compatRead(q.hooks, "GetAllBins", func() ([]db.Bin, error) { return q.Querier.GetAllBins(ctx) })
}

func (q *compatQuerier) GetAllParts(ctx context.Context) ([]db.Part, error) {
	return compatRead(q.hooks, "GetAllParts", func() ([]db.Part, error) { return q.Querier.GetAllParts(ctx) })
}

func (q *compatQuerier) GetAllPartAssignments(ctx context.Context) ([]db.PartAssignment, error) {
	return compatRead(q.hooks, "GetAllPartAssignments", func() ([]db.PartAssignment, error) { return q.Querier.GetAllPartAssignments(ctx) })
}

func (q *compatQuerier) GetFlag(ctx context.Context, key string) (string, error) {
	if q.hooks.failMethod == "GetFlag" {
		return "", q.hooks.failErr
	}
	return q.Querier.GetFlag(ctx, key)
}
