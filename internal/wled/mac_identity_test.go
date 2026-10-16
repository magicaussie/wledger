package wled

import (
	"context"
	"database/sql"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

func TestNormalizeControllerMACFormats(t *testing.T) {
	for _, raw := range []string{"aabbccddeeff", "AABBCCDDEEFF", "AA:BB:CC:DD:EE:FF", "aa-bb-cc-dd-ee-ff", "aabb.ccdd.eeff", " AA:BB:CC:DD:EE:FF "} {
		mac, err := NormalizeControllerMAC(raw)
		if err != nil || mac != "aa:bb:cc:dd:ee:ff" {
			t.Fatalf("%q -> %q, %v", raw, mac, err)
		}
	}
	for _, raw := range []string{"", "000000000000", "ff:ff:ff:ff:ff:ff", "01:00:00:00:00:01", "aabbccddee", "aabbccddeeff0011", "not-a-device"} {
		if _, err := NormalizeControllerMAC(raw); err == nil {
			t.Fatalf("accepted invalid physical identity %q", raw)
		}
	}
	a := db.Controller{ID: 1, MacAddress: sql.NullString{String: "AABBCCDDEEFF", Valid: true}}
	b := db.Controller{ID: 2, MacAddress: sql.NullString{String: "aa:bb:cc:dd:ee:ff", Valid: true}}
	if !ConfiguredControllersAlias(a, b) {
		t.Fatal("MAC representation created separate configured identities")
	}
}

func TestPhysicalOwnershipAcceptsWLEDCompactMAC(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	f.mock.setInfo(`{"ver":"0.15.0","mac":"aabbccddeeff","leds":{"count":200,"rgbw":false}}`)
	if _, err := f.database.Exec(`UPDATE controllers SET mac_address='AA:BB:CC:DD:EE:FF' WHERE id=?`, f.controller); err != nil {
		t.Fatal(err)
	}
	if err := f.service.SaveControllerMappingVerification(ctx, f.controller, true); err != nil {
		t.Fatal(err)
	}
	operation, err := f.service.StartTest(ctx, centreRequest(f), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer f.service.StopOperation(ctx, operation.ID, 1)
	claims, err := f.store.ListPhysicalOwnershipRanges(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || claims[0].MAC != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("noncanonical physical ownership=%+v", claims)
	}
}
