package storage

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/tuxedocurly/wledger/internal/cabinetpixels"
	"github.com/tuxedocurly/wledger/internal/db"
)

// TestTopologyWithProposedStringsPreservesOutputTransforms verifies that an
// unchanged wiring save produces a proposed topology identical to the current
// one, including reverse/serpentine transforms. Previously the proposed strings
// were rebuilt with empty direction/pattern, so every reverse or serpentine
// output compared as forward/linear and the mapping-impact check was spurious.
func TestTopologyWithProposedStringsPreservesOutputTransforms(t *testing.T) {
	current := cabinetpixels.Topology{
		CabinetID: 1,
		Total:     6,
		Controllers: []cabinetpixels.Controller{{
			Record: db.Controller{ID: 1},
			Span:   cabinetpixels.Span{ControllerID: 1, Order: 0, Start: 1, Pixels: 6},
			Strings: []cabinetpixels.String{{
				Record:          db.CabinetLedString{ID: 7, CabinetID: 1, ControllerID: 1, DisplayOrder: 0, PixelCount: 6},
				Direction:       "reverse",
				Pattern:         "serpentine_rows",
				Rows:            2,
				Columns:         3,
				ControllerStart: 1,
				CabinetStart:    1,
			}},
		}},
	}

	input := CabinetWiringInput{
		Strings: []WiringStringInput{{ID: 7, ControllerID: 1, Name: "String 1", DisplayOrder: 0, PixelCount: 6}},
	}
	spans := []cabinetpixels.Span{{ControllerID: 1, Order: 0, Start: 1, Pixels: 6}}

	out := topologyWithProposedStrings(current, input, map[int64]bool{1: true}, spans, 6)
	if len(out.Controllers) != 1 || len(out.Controllers[0].Strings) != 1 {
		t.Fatalf("unexpected proposed topology shape: %+v", out)
	}
	got := out.Controllers[0].Strings[0]
	if got.Direction != "reverse" || got.Pattern != "serpentine_rows" || got.Rows != 2 || got.Columns != 3 {
		t.Fatalf("proposed string lost output transforms: direction=%q pattern=%q rows=%d cols=%d",
			got.Direction, got.Pattern, got.Rows, got.Columns)
	}
}

// TestSaveCabinetWiringRejectsLinkedStringResize verifies that changing the
// length of a string that is projected from a controller output is rejected
// rather than committed. Previously such a save left the string/output mismatched
// so the next topology Load failed, and the output editor could not repair it.
func TestSaveCabinetWiringRejectsLinkedStringResize(t *testing.T) {
	service, store, cabinetID, _ := ledAllocationFixture(t)
	ctx := context.Background()

	cabinet, err := store.GetCabinet(ctx, cabinetID)
	if err != nil {
		t.Fatal(err)
	}
	controllerID := cabinet.ControllerID.Int64

	var stringID int64
	err = store.ExecImmediateTx(ctx, func(q db.Querier) error {
		outputID, err := q.CreateControllerOutput(ctx, db.CreateControllerOutputParams{
			ControllerID: controllerID, DisplayOrder: 0, Name: "Output 1", PixelCount: 100,
			Direction: "forward", Pattern: "linear", Rows: 1, Columns: 1, CustomOrderJson: "[]",
		})
		if err != nil {
			return err
		}
		stringID, err = q.CreateCabinetLEDString(ctx, db.CreateCabinetLEDStringParams{
			CabinetID: cabinetID, ControllerID: controllerID, Name: "String 1", DisplayOrder: 0,
			PixelCount: 100, LayoutComplete: 0, ControllerOutputID: sql.NullInt64{Int64: outputID, Valid: true},
		})
		if err != nil {
			return err
		}
		if _, err := q.UpdateCabinetControllerStringsConfigured(ctx, db.UpdateCabinetControllerStringsConfiguredParams{
			StringsConfigured: 1, CabinetID: cabinetID, ControllerID: controllerID,
		}); err != nil {
			return err
		}
		_, err = q.SetControllerLEDCapacityFromStrings(ctx, db.SetControllerLEDCapacityFromStringsParams{LedCount: 100, ID: controllerID})
		return err
	})
	if err != nil {
		t.Fatalf("seed linked string: %v", err)
	}

	// Shrinking a linked string's length must be rejected, not persisted.
	err = service.SaveCabinetWiring(ctx, cabinetID, CabinetWiringInput{
		Revision: 0,
		Strings:  []WiringStringInput{{ID: stringID, ControllerID: controllerID, Name: "String 1", DisplayOrder: 0, PixelCount: 90}},
	}, true)
	if !errors.Is(err, ErrUnsafeLayout) {
		t.Fatalf("resizing a linked string = %v, want ErrUnsafeLayout", err)
	}

	// The unchanged wiring still loads, proving no inconsistent state was written.
	if _, err := cabinetpixels.Load(ctx, store, cabinetID); err != nil {
		t.Fatalf("topology after rejected save: %v", err)
	}
}
