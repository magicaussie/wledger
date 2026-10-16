package backup

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/wled"
)

func seedBackupBinPixel(t *testing.T, f *physFixture) db.PhysicalBinPixel {
	t.Helper()
	f.initSettings()
	physSeedLegacy(t, f.conn)
	physSeedPhysical(t, f.conn)
	p, relative, err := wled.AssignBinPixel(f.ctx, f.store, 1, 1, 1, 151)
	if err != nil {
		t.Fatal(err)
	}
	if relative != 10 {
		t.Fatalf("relative=%d", relative)
	}
	return p
}
func TestPhysicalBinPixelBackupRoundTripPreservesIdentityAndLegacyIndex(t *testing.T) {
	source := physNewFixture(t, "bin_override_source")
	want := seedBackupBinPixel(t, source)
	archive := source.export()
	manifest := physDecodeStorageManifest(t, archive)
	if len(manifest.PhysicalStorage.BinPixels) != 1 || !reflect.DeepEqual(manifest.PhysicalStorage.BinPixels[0], want) {
		t.Fatalf("snapshot=%+v", manifest.PhysicalStorage.BinPixels)
	}
	target := physNewFixture(t, "bin_override_target")
	target.initSettings()
	if err := target.restore(archive); err != nil {
		t.Fatal(err)
	}
	got, err := target.store.GetPhysicalBinPixel(target.ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got=%+v want=%+v", got, want)
	}
	legacy, err := target.store.GetBin(target.ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !legacy.LedIndex.Valid || legacy.LedIndex.Int64 != 0 {
		t.Fatal("legacy led_index changed")
	}
	// A second restore must clear override rows before allocation FK restrictions.
	if err := target.restore(archive); err != nil {
		t.Fatalf("repeat restore: %v", err)
	}
}
func TestPhysicalBinPixelBackupOmissionCannotDiscardOverrides(t *testing.T) {
	target := physNewFixture(t, "bin_override_preserve")
	want := seedBackupBinPixel(t, target)
	entries := physZipEntries(t, target.export())
	var root map[string]json.RawMessage
	json.Unmarshal(entries[currentStorageManifestName], &root)
	var physical map[string]json.RawMessage
	json.Unmarshal(root["physical_storage"], &physical)
	delete(physical, "physical_bin_pixels")
	root["physical_storage"], _ = json.Marshal(physical)
	raw, _ := json.Marshal(root)
	archive := physBuildZip(t, [][2]string{{currentStorageManifestName, string(raw)}})
	var conflict *PhysicalBinPixelConflictError
	if err := target.restore(archive); !errors.As(err, &conflict) {
		t.Fatalf("got %v, want preservation conflict", err)
	}
	got, err := target.store.GetPhysicalBinPixel(target.ctx, 1)
	if err != nil || got != want {
		t.Fatalf("override changed: %+v %v", got, err)
	}
	// Older archives remain valid for an installation without overrides.
	fresh := physNewFixture(t, "bin_override_old_compatible")
	fresh.initSettings()
	if err := fresh.restore(archive); err != nil {
		t.Fatalf("compatible older archive: %v", err)
	}
}
func TestPhysicalBinPixelBackupRejectsInvalidRowsBeforeMutation(t *testing.T) {
	source := physNewFixture(t, "bin_override_validation")
	seedBackupBinPixel(t, source)
	base := physDecodeStorageManifest(t, source.export())
	for _, mutate := range []func(*PhysicalStorageSnapshot){
		func(s *PhysicalStorageSnapshot) { s.BinPixels[0].AllocationID = 999 },
		func(s *PhysicalStorageSnapshot) { s.BinPixels[0].Offset = 99 },
		func(s *PhysicalStorageSnapshot) { s.BinPixels[0].TopologyFingerprint = "invalid" },
		func(s *PhysicalStorageSnapshot) { s.BinPixels = append(s.BinPixels, s.BinPixels[0]) },
	} {
		manifest := base
		manifest.PhysicalStorage.BinPixels = append([]db.PhysicalBinPixel(nil), base.PhysicalStorage.BinPixels...)
		mutate(&manifest.PhysicalStorage)
		target := physNewFixture(t, "invalid_bin_override")
		target.initSettings()
		before := physTakeSnapshot(t, target)
		if err := target.restore(physCurrentArchive(t, manifest)); err == nil {
			t.Fatal("accepted invalid mapping")
		}
		after := physTakeSnapshot(t, target)
		if !reflect.DeepEqual(before, after) {
			t.Fatal("rejected backup mutated target")
		}
	}
}
