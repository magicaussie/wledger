package backup

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

// This file tests multi-controller backup support: the physical storage section
// carries the cabinet controller membership list, a new export always declares
// it (empty when there is none), an older current-format archive that predates
// the membership table is read with legacy single-controller semantics, and such
// an archive is refused over an installation that holds secondary memberships so
// it cannot silently erase them. It reuses the raw-SQL fixtures and proofs of the
// physical round-trip suite so assertions compare state, never prose.

// ccSeedMultiController seeds a multi-controller physical graph with cabinet-wide
// ranges. Drawer 2 occupies cabinet pixels 301–304, which are local pixels 1–4 on
// cabinet 1's slot-1 controller after the first controller's 300-pixel span.
func ccSeedMultiController(t *testing.T, conn *sql.DB) {
	t.Helper()
	physSeedLegacy(t, conn)
	exec := func(query string, args ...any) { physExec(t, conn, query, args...) }

	exec(`INSERT INTO controllers (id, name, ip_address, port, led_count) VALUES (3, 'Legacy C', '192.168.1.12', 80, 300)`)
	exec(`INSERT INTO controller_outputs (controller_id, display_order, name, pixel_count) VALUES (3, 0, 'Output 1', 300)`)

	exec(`INSERT INTO cabinets (id, name, controller_id, coordinate_system, layout_revision, electrical_revision, display_order, archived, created_at, updated_at)
		VALUES (1, 'Cab Multi', 1, 'grid', 3, 5, 0, 0, '2024-01-02 03:04:05', '2024-02-03 04:05:06')`)
	exec(`INSERT INTO cabinets (id, name, controller_id, coordinate_system, layout_revision, electrical_revision, display_order, archived, created_at, updated_at)
		VALUES (2, 'Cab Solo', 3, 'grid', 1, 1, 1, 0, '2024-03-04 05:06:07', '2024-03-04 05:06:07')`)

	exec(`INSERT INTO cabinet_controllers (cabinet_id, controller_id, slot, created_at, updated_at)
		VALUES (1, 1, 0, '2024-01-02 03:04:05', '2024-02-03 04:05:06')`)
	exec(`INSERT INTO cabinet_controllers (cabinet_id, controller_id, slot, created_at, updated_at)
		VALUES (1, 2, 1, '2024-01-03 03:04:05', '2024-02-04 04:05:06')`)
	exec(`INSERT INTO cabinet_controllers (cabinet_id, controller_id, slot, created_at, updated_at)
		VALUES (2, 3, 0, '2024-03-04 05:06:07', '2024-03-04 05:06:07')`)

	exec(`INSERT INTO drawers (id, cabinet_id, label, display_order, x, y, width, height, archived, created_at, updated_at)
		VALUES (1, 1, 'D1', 0, 0, 0, 10, 5, 0, '2024-01-05 06:07:08', '2024-01-05 06:07:08')`)
	exec(`INSERT INTO drawers (id, cabinet_id, label, display_order, x, y, width, height, archived, created_at, updated_at)
		VALUES (2, 1, 'D2', 1, 10, 0, 10, 5, 0, '2024-01-06 07:08:09', '2024-01-06 07:08:09')`)
	exec(`INSERT INTO drawers (id, cabinet_id, label, display_order, x, y, width, height, archived, created_at, updated_at)
		VALUES (3, 2, 'D3', 0, 0, 0, 10, 5, 0, '2024-01-07 08:09:10', '2024-01-07 08:09:10')`)

	exec(`INSERT INTO drawer_led_allocations (id, cabinet_id, drawer_id, start_pixel, pixel_count, display_order, created_at, updated_at)
		VALUES (1, 1, 1, 1, 8, 0, '2024-01-11 12:13:14', '2024-01-11 12:13:14')`)
	exec(`INSERT INTO drawer_led_allocations (id, cabinet_id, drawer_id, start_pixel, pixel_count, display_order, created_at, updated_at)
		VALUES (2, 1, 2, 301, 4, 0, '2024-01-12 13:14:15', '2024-01-12 13:14:15')`)
	exec(`INSERT INTO drawer_led_allocations (id, cabinet_id, drawer_id, start_pixel, pixel_count, display_order, created_at, updated_at)
		VALUES (3, 2, 3, 1, 4, 0, '2024-01-13 14:15:16', '2024-01-13 14:15:16')`)
}

// ccSingleControllerSource seeds a single-controller physical graph and returns
// its fixture, used to build an older current-format archive by stripping the
// membership member after export.
func ccSingleControllerSource(t *testing.T, name string) *physFixture {
	t.Helper()
	source := physNewFixture(t, name)
	source.initSettings()
	physSeedLegacy(t, source.conn)
	physSeedPhysical(t, source.conn)
	return source
}

// ccDecodeEnvelope decodes an archive through the real current-envelope decoder,
// so its declared flags and graph validation run exactly as a restore would.
func ccDecodeEnvelope(t *testing.T, archive []byte) StorageManifest {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatalf("read zip: %v", err)
	}
	for _, f := range zr.File {
		if f.Name == currentStorageManifestName {
			env, err := decodeCurrentStorageManifest(f)
			if err != nil {
				t.Fatalf("decode %s: %v", currentStorageManifestName, err)
			}
			return env
		}
	}
	t.Fatalf("archive is missing %s", currentStorageManifestName)
	return StorageManifest{}
}

// ccStripPhysicalMember removes one member from the physical storage section of a
// marshaled current archive, producing an older current-format archive that
// predates that member.
func ccStripPhysicalMember(t *testing.T, archive []byte, member string) []byte {
	t.Helper()
	entries := physZipEntries(t, archive)
	raw, ok := entries[currentStorageManifestName]
	if !ok {
		t.Fatalf("archive is missing %s", currentStorageManifestName)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	var physical map[string]json.RawMessage
	if err := json.Unmarshal(top[storageManifestPhysicalField], &physical); err != nil {
		t.Fatalf("decode physical storage: %v", err)
	}
	if _, ok := physical[member]; !ok {
		t.Fatalf("physical storage has no member %q to strip", member)
	}
	delete(physical, member)
	physicalJSON, err := json.Marshal(physical)
	if err != nil {
		t.Fatalf("marshal physical storage: %v", err)
	}
	top[storageManifestPhysicalField] = physicalJSON
	out, err := json.Marshal(top)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return physBuildZip(t, [][2]string{{currentStorageManifestName, string(out)}})
}

// ccMultiControllerManifest exports a multi-controller source and returns the
// decoded envelope, so defect cases mutate one aspect of a genuinely valid
// multi-controller archive.
func ccMultiControllerManifest(t *testing.T) StorageManifest {
	t.Helper()
	source := physNewFixture(t, "cc_mut_source")
	source.initSettings()
	ccSeedMultiController(t, source.conn)
	return ccDecodeEnvelope(t, source.export())
}

func ccMutatedArchive(t *testing.T, mutate func(*StorageManifest)) []byte {
	t.Helper()
	manifest := ccMultiControllerManifest(t)
	mutate(&manifest)
	return physCurrentArchive(t, manifest)
}

func ccMembershipPairs(rows []db.CabinetController) map[[2]int64]int64 {
	out := make(map[[2]int64]int64, len(rows))
	for _, m := range rows {
		out[[2]int64{m.CabinetID, m.ControllerID}] = m.Slot
	}
	return out
}

// TestCabinetControllersBackup_MultiControllerRoundTrip exports a cabinet that
// owns two controllers and restores it into a separate installation: every
// membership (including the secondary slot-1 assignment and a controller owned by
// a different cabinet) and every allocation must be reproduced exactly, and the
// shared inventory must be untouched.
func TestCabinetControllersBackup_MultiControllerRoundTrip(t *testing.T) {
	source := physNewFixture(t, "cc_roundtrip_source")
	source.initSettings()
	ccSeedMultiController(t, source.conn)

	before := physCapture(t, source.store)
	archive := source.export()

	env := ccDecodeEnvelope(t, archive)
	if !env.PhysicalStorage.CabinetControllersDeclared {
		t.Errorf("a new export did not declare its cabinet controller memberships")
	}
	wantMemberships := map[[2]int64]int64{{1, 1}: 0, {1, 2}: 1, {2, 3}: 0}
	if got := ccMembershipPairs(env.PhysicalStorage.CabinetControllers); !reflect.DeepEqual(got, wantMemberships) {
		t.Errorf("captured memberships = %v, want %v", got, wantMemberships)
	}
	if n := len(env.PhysicalStorage.LEDAllocations); n != 3 {
		t.Errorf("captured allocations = %d, want 3", n)
	}

	target := physNewFixture(t, "cc_roundtrip_target")
	target.initSettings()
	if err := target.restore(archive); err != nil {
		t.Fatalf("restore multi-controller archive: %v", err)
	}

	wantRows := physReadPhysicalRows(t, source.conn)
	gotRows := physReadPhysicalRows(t, target.conn)
	if !reflect.DeepEqual(wantRows, gotRows) {
		t.Errorf("physical graph changed across the multi-controller roundtrip:\nwant=%+v\ngot=%+v", wantRows, gotRows)
	}
	if got := ccMembershipPairs(gotRows.CabinetControllers); !reflect.DeepEqual(got, wantMemberships) {
		t.Errorf("restored memberships = %v, want %v", got, wantMemberships)
	}
	physAssertFingerprintsEqual(t, "multi-controller roundtrip", before, physCapture(t, target.store))
	if want, got := physReadFlags(t, source.conn), physReadFlags(t, target.conn); !reflect.DeepEqual(want, got) {
		t.Errorf("system flags changed across the multi-controller roundtrip: want=%v got=%v", want, got)
	}
	physAssertLegacyIdentities(t, target.conn)
}

// TestCabinetControllersBackup_NewExportAlwaysDeclaresMemberships verifies that a
// new export includes the membership member even when a cabinet has no membership
// row, so an empty list is an explicit declaration rather than an absence.
func TestCabinetControllersBackup_NewExportAlwaysDeclaresMemberships(t *testing.T) {
	source := physNewFixture(t, "cc_empty_memberships")
	source.initSettings()
	physSeedLegacy(t, source.conn)
	physExec(t, source.conn, `INSERT INTO cabinets (id, name, controller_id, coordinate_system, layout_revision, electrical_revision, display_order, archived)
		VALUES (1, 'Cab', 1, 'grid', 0, 0, 0, 0)`)

	archive := source.export()
	entries := physZipEntries(t, archive)
	var top map[string]json.RawMessage
	if err := json.Unmarshal(entries[currentStorageManifestName], &top); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	var physical map[string]json.RawMessage
	if err := json.Unmarshal(top[storageManifestPhysicalField], &physical); err != nil {
		t.Fatalf("decode physical storage: %v", err)
	}
	raw, ok := physical["cabinet_controllers"]
	if !ok {
		t.Fatalf("a new export omitted cabinet_controllers")
	}
	if got := strings.TrimSpace(string(raw)); got != "[]" {
		t.Errorf("cabinet_controllers = %s, want []", got)
	}

	env := ccDecodeEnvelope(t, archive)
	if !env.PhysicalStorage.CabinetControllersDeclared {
		t.Errorf("an explicit empty membership list was not recorded as declared")
	}
	if len(env.PhysicalStorage.CabinetControllers) != 0 {
		t.Errorf("captured memberships = %+v, want none", env.PhysicalStorage.CabinetControllers)
	}
}

// TestCabinetControllersBackup_OldArchiveDerivesPrimaryMembership verifies that an
// older current-format archive that omits cabinet_controllers is still readable
// and is interpreted with legacy single-controller semantics: each cabinet's own
// controller is restored as slot 0.
func TestCabinetControllersBackup_OldArchiveDerivesPrimaryMembership(t *testing.T) {
	source := ccSingleControllerSource(t, "cc_old_source")
	archive := source.export()
	old := ccStripPhysicalMember(t, archive, "cabinet_controllers")

	// The stripped archive no longer declares the membership member.
	env := ccDecodeEnvelope(t, old)
	if env.PhysicalStorage.CabinetControllersDeclared {
		t.Errorf("stripped archive is still marked as declaring memberships")
	}

	target := physNewFixture(t, "cc_old_target")
	target.initSettings()
	if err := target.restore(old); err != nil {
		t.Fatalf("restore older archive: %v", err)
	}

	got := physReadPhysicalRows(t, target.conn)
	wantMemberships := map[[2]int64]int64{{1, 1}: 0}
	if pairs := ccMembershipPairs(got.CabinetControllers); !reflect.DeepEqual(pairs, wantMemberships) {
		t.Errorf("derived memberships = %v, want %v", pairs, wantMemberships)
	}
	// Only each cabinet's own controller is derived; nothing else is inferred.
	if len(got.CabinetControllers) != 1 {
		t.Errorf("derived %d memberships, want exactly 1", len(got.CabinetControllers))
	}
}

// TestCabinetControllersBackup_OldArchiveRefusesSecondaryMemberships verifies that
// an older current-format archive that omits cabinet_controllers is refused over
// an installation holding a secondary controller assignment, before any database,
// uploads or staging change, so a restore cannot silently erase it.
func TestCabinetControllersBackup_OldArchiveRefusesSecondaryMemberships(t *testing.T) {
	source := ccSingleControllerSource(t, "cc_conflict_source")
	old := ccStripPhysicalMember(t, source.export(), "cabinet_controllers")

	target := physNewFixture(t, "cc_conflict_target")
	target.initSettings()
	ccSeedMultiController(t, target.conn)
	physSeedUploadSentinels(t, target.uploadsDir)

	before := physTakeSnapshot(t, target)
	uploads := physUploadsSnapshot(t, target.uploadsDir)

	err := target.restore(old)
	var conflict *CabinetControllerConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("restoring an older archive over secondary memberships = %v; want CabinetControllerConflictError", err)
	}
	physAssertUnchanged(t, "older-archive-secondary-membership-conflict", target, before)
	if got := physUploadsSnapshot(t, target.uploadsDir); !reflect.DeepEqual(got, uploads) {
		t.Errorf("uploads changed by a refused older archive: want %v got %v", uploads, got)
	}
}

// TestCabinetControllersBackup_OldArchiveDoesNotInferSecondaryMemberships verifies
// that an older archive whose allocations reference a controller that would only
// be a secondary membership is rejected: legacy semantics derive only each
// cabinet's primary controller, so such an archive cannot be reproduced and is
// never guessed.
func TestCabinetControllersBackup_OldArchiveDoesNotInferSecondaryMemberships(t *testing.T) {
	source := physNewFixture(t, "cc_no_infer_source")
	source.initSettings()
	ccSeedMultiController(t, source.conn)
	old := ccStripPhysicalMember(t, source.export(), "cabinet_controllers")

	target := physNewFixture(t, "cc_no_infer_target")
	target.initSettings()
	physSeedUploadSentinels(t, target.uploadsDir)
	before := physTakeSnapshot(t, target)
	uploads := physUploadsSnapshot(t, target.uploadsDir)

	err := target.restore(old)
	if err == nil {
		t.Fatalf("an archive whose allocation needs an inferred secondary membership was accepted")
	}
	var formatErr *BackupFormatError
	if !errors.As(err, &formatErr) {
		t.Fatalf("expected *BackupFormatError, got %T: %v", err, err)
	}
	if formatErr.Code != FormatInvalidPhysicalGraph {
		t.Errorf("format code = %q, want %q (%v)", formatErr.Code, FormatInvalidPhysicalGraph, err)
	}
	physAssertUnchanged(t, "older-archive-no-inference", target, before)
	if got := physUploadsSnapshot(t, target.uploadsDir); !reflect.DeepEqual(got, uploads) {
		t.Errorf("uploads changed by a rejected older archive: want %v got %v", uploads, got)
	}
}

// TestCabinetControllersBackup_RejectsInvalidMembershipsBeforeMutation verifies
// that a current archive with an invalid membership list or an allocation that is
// not on a cabinet member is rejected before any state change.
func TestCabinetControllersBackup_RejectsInvalidMembershipsBeforeMutation(t *testing.T) {
	cases := []struct {
		name    string
		archive func(t *testing.T) []byte
	}{
		{
			name: "membership references unknown cabinet",
			archive: func(t *testing.T) []byte {
				return ccMutatedArchive(t, func(m *StorageManifest) { m.PhysicalStorage.CabinetControllers[1].CabinetID = 999 })
			},
		},
		{
			name: "membership references unknown controller",
			archive: func(t *testing.T) []byte {
				return ccMutatedArchive(t, func(m *StorageManifest) { m.PhysicalStorage.CabinetControllers[1].ControllerID = 999 })
			},
		},
		{
			name: "controller owned by two cabinets",
			archive: func(t *testing.T) []byte {
				return ccMutatedArchive(t, func(m *StorageManifest) {
					m.PhysicalStorage.CabinetControllers = append(m.PhysicalStorage.CabinetControllers, db.CabinetController{
						CabinetID: 2, ControllerID: 2, Slot: 1, CreatedAt: physTime().Time, UpdatedAt: physTime().Time,
					})
				})
			},
		},
		{
			name: "slot above the maximum",
			archive: func(t *testing.T) []byte {
				return ccMutatedArchive(t, func(m *StorageManifest) { m.PhysicalStorage.CabinetControllers[1].Slot = 5 })
			},
		},
		{
			name: "negative slot",
			archive: func(t *testing.T) []byte {
				return ccMutatedArchive(t, func(m *StorageManifest) { m.PhysicalStorage.CabinetControllers[1].Slot = -1 })
			},
		},
		{
			name: "duplicate slot within a cabinet",
			archive: func(t *testing.T) []byte {
				return ccMutatedArchive(t, func(m *StorageManifest) { m.PhysicalStorage.CabinetControllers[1].Slot = 0 })
			},
		},
		{
			name: "allocation drawer does not belong to cabinet",
			archive: func(t *testing.T) []byte {
				return ccMutatedArchive(t, func(m *StorageManifest) { m.PhysicalStorage.LEDAllocations[0].CabinetID = 2 })
			},
		},
		{
			name: "overlapping cabinet-wide ranges",
			archive: func(t *testing.T) []byte {
				return ccMutatedArchive(t, func(m *StorageManifest) {
					m.PhysicalStorage.LEDAllocations = append(m.PhysicalStorage.LEDAllocations, db.DrawerLedAllocation{
						ID: 99, CabinetID: 1, DrawerID: 1, StartPixel: 2, PixelCount: 1,
						DisplayOrder: 1, CreatedAt: physTime().Time, UpdatedAt: physTime().Time,
					})
				})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := physNewFixture(t, "cc_reject")
			target.initSettings()
			ccSeedMultiController(t, target.conn)
			physSeedUploadSentinels(t, target.uploadsDir)

			before := physTakeSnapshot(t, target)
			uploads := physUploadsSnapshot(t, target.uploadsDir)

			err := target.restore(tc.archive(t))
			if err == nil {
				t.Fatalf("invalid membership archive was accepted")
			}
			var formatErr *BackupFormatError
			if !errors.As(err, &formatErr) {
				t.Fatalf("expected *BackupFormatError, got %T: %v", err, err)
			}
			if formatErr.Code != FormatInvalidPhysicalGraph {
				t.Errorf("format code = %q, want %q (%v)", formatErr.Code, FormatInvalidPhysicalGraph, err)
			}
			physAssertUnchanged(t, "invalid-membership-"+tc.name, target, before)
			if got := physUploadsSnapshot(t, target.uploadsDir); !reflect.DeepEqual(got, uploads) {
				t.Errorf("uploads changed by a rejected archive: want %v got %v", uploads, got)
			}
		})
	}
}
