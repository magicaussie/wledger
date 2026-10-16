package wled

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"testing"

	"github.com/tuxedocurly/wledger/internal/cabinetpixels"
	"github.com/tuxedocurly/wledger/internal/db"
)

func TestBinPixelOverrideSecondAllocationAndWholeDrawerFallback(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	before, err := f.store.GetBin(ctx, f.bin1)
	if err != nil {
		t.Fatal(err)
	}
	fallback, err := f.service.resolve(ctx, f.drawer1, f.bin1)
	if err != nil {
		t.Fatal(err)
	}
	if len(fallback.ranges) != 2 || fallback.ranges[0].Count != 8 {
		t.Fatalf("fallback=%+v", fallback.ranges)
	}
	p, relative, err := AssignBinPixel(ctx, f.store, f.bin1, f.drawer1, f.controller, 150)
	if err != nil {
		t.Fatal(err)
	}
	if relative != 9 || p.Offset != 1 {
		t.Fatalf("saved=%+v relative=%d", p, relative)
	}
	resolved, err := f.service.resolve(ctx, f.drawer1, f.bin1)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.ranges) != 1 || resolved.ranges[0].Start != 151 || resolved.ranges[0].Count != 1 || len(resolved.targets) != 1 || resolved.targets[0].ranges[0].Start != 151 {
		t.Fatalf("resolved=%+v targets=%+v", resolved.ranges, resolved.targets)
	}
	after, err := f.store.GetBin(ctx, f.bin1)
	if err != nil {
		t.Fatal(err)
	}
	if before.LedIndex != after.LedIndex || before.ContainerID != after.ContainerID {
		t.Fatal("legacy electrical bin association changed")
	}
	// Logical reordering changes the displayed relative index, not the LED.
	if _, err = f.database.Exec(`UPDATE drawer_led_allocations SET display_order=10 WHERE drawer_id=? AND start_pixel=97`, f.drawer1); err != nil {
		t.Fatal(err)
	}
	topology, err := cabinetpixels.Load(ctx, f.store, 1)
	if err != nil {
		t.Fatal(err)
	}
	allocations, err := f.store.ListDrawerLEDAllocationsByDrawer(ctx, f.drawer1)
	if err != nil {
		t.Fatal(err)
	}
	_, cabinetPixel, relative, err := ResolveBinPixelOverride(ctx, f.store, f.bin1, f.drawer1, topology, allocations)
	if err != nil || cabinetPixel != 151 || relative != 1 {
		t.Fatalf("reordered mapping pixel=%d relative=%d err=%v", cabinetPixel, relative, err)
	}
	if err := ClearBinPixel(ctx, f.store, f.bin1, f.drawer1); err != nil {
		t.Fatal(err)
	}
	cleared, err := f.service.resolve(ctx, f.drawer1, f.bin1)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleared.ranges) != 2 {
		t.Fatalf("clearing override did not restore whole-drawer fallback: %+v", cleared.ranges)
	}
	total := int64(0)
	for _, r := range cleared.ranges {
		total += r.Count
	}
	if total != 10 {
		t.Fatalf("clear changed drawer allocations: %+v", cleared.ranges)
	}
}

func TestBinPixelOverrideRejectsDuplicateWrongDrawerAndOutsideRanges(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	for _, in := range [][3]int64{{f.bin1, f.drawer2, 10}, {f.bin1, f.drawer1, 105}, {f.bin1, f.drawer1, -1}} {
		if _, _, err := AssignBinPixel(ctx, f.store, in[0], in[1], f.controller, in[2]); err == nil {
			t.Fatalf("accepted %+v", in)
		}
	}
	if _, _, err := AssignBinPixel(ctx, f.store, f.bin1, f.drawer1, f.controller, 96); err != nil {
		t.Fatal(err)
	}
	b, err := f.store.CreateBin(ctx, db.CreateBinParams{Name: "duplicate pixel bin"})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.CreateBinDrawerAttachment(ctx, db.CreateBinDrawerAttachmentParams{BinID: b, DrawerID: f.drawer1, X: 2, Width: 1, Height: 1}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = AssignBinPixel(ctx, f.store, b, f.drawer1, f.controller, 96); err == nil {
		t.Fatal("accepted duplicate physical pixel")
	}
	if _, err = f.store.GetPhysicalBinPixel(ctx, b); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("failed assignment persisted: %v", err)
	}
}

func TestBinPixelOverrideBlocksAllocationReplacementAndDetectsMovedBinAndTopology(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	p, _, err := AssignBinPixel(ctx, f.store, f.bin1, f.drawer1, f.controller, 149)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.database.Exec(`DELETE FROM drawer_led_allocations WHERE id=?`, p.AllocationID); err == nil {
		t.Fatal("referenced allocation deleted")
	}
	if _, err = f.database.Exec(`UPDATE drawer_led_allocations SET start_pixel=160 WHERE id=?`, p.AllocationID); err == nil {
		t.Fatal("referenced allocation retargeted")
	}
	if _, err = f.database.Exec(`UPDATE bin_drawer_attachments SET drawer_id=? WHERE bin_id=?`, f.drawer2, f.bin1); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.resolve(ctx, f.drawer2, f.bin1); !errors.Is(err, ErrLocateTopology) {
		t.Fatalf("moved override did not fail closed: %v", err)
	}
	if _, err = f.database.Exec(`UPDATE bin_drawer_attachments SET drawer_id=? WHERE bin_id=?`, f.drawer1, f.bin1); err != nil {
		t.Fatal(err)
	}
	if _, err = f.database.Exec(`UPDATE controllers SET ip_address='192.0.2.123' WHERE id=?`, f.controller); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.resolve(ctx, f.drawer1, f.bin1); !errors.Is(err, ErrLocateTopology) {
		t.Fatalf("changed topology did not fail closed: %v", err)
	}
}

func TestBinPixelOverrideReverseAndCustomMapping(t *testing.T) {
	for _, pattern := range []string{"linear", "custom"} {
		t.Run(pattern, func(t *testing.T) {
			f := newPhysicalFixture(t)
			ctx := context.Background()
			order := "[]"
			if pattern == "custom" {
				order = "["
				for i := 0; i < 200; i++ {
					if i > 0 {
						order += ","
					}
					order += strconv.Itoa((i + 1) % 200)
				}
				order += "]"
			}
			var outputID int64
			if err := f.database.QueryRow(`INSERT INTO controller_outputs(controller_id,display_order,name,pixel_count,direction,pattern,rows,columns,custom_order_json) VALUES (?,0,'mapped',200,'reverse',?,1,1,?) RETURNING id`, f.controller, pattern, order).Scan(&outputID); err != nil {
				t.Fatal(err)
			}
			if _, err := f.database.Exec(`INSERT INTO cabinet_led_strings(cabinet_id,controller_id,name,display_order,pixel_count,layout_complete,controller_output_id) VALUES (1,?,'mapped',0,200,1,?)`, f.controller, outputID); err != nil {
				t.Fatal(err)
			}
			if _, err := f.database.Exec(`UPDATE cabinet_controllers SET strings_configured=1`); err != nil {
				t.Fatal(err)
			}
			topology, err := cabinetpixels.Load(ctx, f.store, 1)
			if err != nil {
				t.Fatal(err)
			}
			mapped, err := cabinetpixels.SplitTopologyRange(topology, 151, 1)
			if err != nil {
				t.Fatal(err)
			}
			p, relative, err := AssignBinPixel(ctx, f.store, f.bin1, f.drawer1, f.controller, mapped[0].LocalStart-1)
			if err != nil {
				t.Fatal(err)
			}
			if relative != 9 || p.Offset != 1 {
				t.Fatalf("relative=%d override=%+v", relative, p)
			}
			resolved, err := f.service.resolve(ctx, f.drawer1, f.bin1)
			if err != nil {
				t.Fatal(err)
			}
			if len(resolved.ranges) != 1 || resolved.ranges[0].Start != 151 {
				t.Fatalf("ranges=%+v", resolved.ranges)
			}
		})
	}
}
