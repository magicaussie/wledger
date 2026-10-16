package hardware

import (
	"context"
	"database/sql"
	"errors"

	"testing"

	"github.com/tuxedocurly/wledger/internal/controllerconfig"
	"github.com/tuxedocurly/wledger/internal/db"
)

func TestWiringDesignerApplyRequiresCurrentReviewAndPreservesOutputIDs(t *testing.T) {
	svc, _, conn := setupTest(t)
	defer conn.Close()
	ctx := context.Background()
	created, err := svc.CreateControllerConfigured(ctx, db.CreateControllerParams{Name: "Designer", IpAddress: "192.0.2.84", Port: sql.NullInt64{Int64: 80, Valid: true}}, controllerconfig.Settings{Outputs: []controllerconfig.Output{{DisplayOrder: 0, Name: "A", PixelCount: 8, Direction: "forward", Pattern: "linear"}}})
	if err != nil {
		t.Fatal(err)
	}
	old, err := svc.GetControllerSettings(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	proposed := old
	proposed.Outputs[0].Direction = "reverse"
	proposed.Outputs[0].PixelCount = 6
	review, err := svc.ReviewWiringDesigner(ctx, created.ID, proposed)
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Changes) == 0 || review.ExpectedSignature == "" {
		t.Fatalf("incomplete review: %+v", review)
	}
	if err = svc.ApplyWiringDesigner(ctx, created.ID, proposed, "stale"); !errors.Is(err, ErrElectricalDesignerStale) {
		t.Fatalf("stale apply error=%v", err)
	}
	if err = svc.ApplyWiringDesigner(ctx, created.ID, proposed, review.ExpectedSignature); err != nil {
		t.Fatal(err)
	}
	actual, err := svc.GetControllerSettings(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if actual.Outputs[0].ID != old.Outputs[0].ID || actual.Outputs[0].Direction != "reverse" || actual.Outputs[0].PixelCount != 6 {
		t.Fatalf("output identity/config changed unexpectedly: old=%+v actual=%+v", old.Outputs, actual.Outputs)
	}

}

func TestWiringDesignerReviewRejectsLegacyControllerDependencies(t *testing.T) {
	svc, store, conn := setupTest(t)
	defer conn.Close()
	ctx := context.Background()
	created, err := svc.CreateControllerConfigured(ctx, db.CreateControllerParams{Name: "Mapped", IpAddress: "192.0.2.85", Port: sql.NullInt64{Int64: 80, Valid: true}}, controllerconfig.Settings{Outputs: []controllerconfig.Output{{DisplayOrder: 0, Name: "A", PixelCount: 8, Direction: "forward", Pattern: "linear"}}})
	if err != nil {
		t.Fatal(err)
	}
	containerID, err := store.CreateContainer(ctx, db.CreateContainerParams{Name: "legacy", ControllerID: created.ID, SegmentID: 0, PositionIndex: 0, LedStart: 0, LedCount: 8})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateBin(ctx, db.CreateBinParams{Name: "mapped", ContainerID: sql.NullInt64{Int64: containerID, Valid: true}, LedIndex: sql.NullInt64{Int64: 0, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	settings, err := svc.GetControllerSettings(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	settings.Outputs[0].Direction = "reverse"
	if _, err = svc.ReviewWiringDesigner(ctx, created.ID, settings); !errors.Is(err, ErrElectricalDesignerDependencies) {
		t.Fatalf("review error=%v", err)
	}
}
