package backup

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

// restoreArchiveShouldFail builds a legacy restore archive from the given
// category records and asserts Restore rejects it with a category-validation
// error, leaving the target without any restored category data.
func restoreArchiveShouldFail(t *testing.T, manifest Manifest) {
	t.Helper()
	_, s, svc, _, cleanup := newBackupService(t)
	defer cleanup()
	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatal(err)
	}
	manifest.Version = "1.0"
	manifest.Settings = minimalSettings()
	zipBytes := buildRestoreZip(t, manifest)
	err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err == nil {
		t.Fatal("restore accepted an archive with semantically invalid category data")
	}
	if !strings.Contains(err.Error(), "category restore") {
		t.Fatalf("restore error = %v, want a category-validation rejection", err)
	}
	categories, err := s.GetAllCategories(ctx)
	if err != nil {
		t.Fatalf("read categories: %v", err)
	}
	if len(categories) != 0 {
		t.Fatalf("rejected restore left %d categories in the target", len(categories))
	}
}

// TestRestoreRejectsCategoryCycle verifies that an archive whose category parent
// graph loops is rejected. The database accepts such rows, and only the service
// (or this validator) prevents them.
func TestRestoreRejectsCategoryCycle(t *testing.T) {
	restoreArchiveShouldFail(t, Manifest{
		Categories: []db.Category{
			{ID: 1, Name: "A", ParentID: sql.NullInt64{Int64: 2, Valid: true}},
			{ID: 2, Name: "B", ParentID: sql.NullInt64{Int64: 1, Valid: true}},
		},
	})
}

// TestRestoreRejectsSpecificationOutsideCategory verifies that a product
// specification referencing an attribute owned by a different category is
// rejected.
func TestRestoreRejectsSpecificationOutsideCategory(t *testing.T) {
	restoreArchiveShouldFail(t, Manifest{
		Categories: []db.Category{{ID: 1, Name: "A"}, {ID: 2, Name: "B"}},
		CategoryAttributes: []db.CategoryAttribute{
			{ID: 10, CategoryID: 1, Name: "Length", DataType: "number"},
		},
		PartCategories: []db.PartCategory{{PartID: 1, CategoryID: 2}},
		PartAttributeValues: []db.PartAttributeValue{
			{PartID: 1, AttributeID: 10, ValueNumber: sql.NullFloat64{Float64: 35, Valid: true}},
		},
	})
}

// TestRestoreRejectsWrongTypedValue verifies that a numeric value supplied for a
// text specification is rejected.
func TestRestoreRejectsWrongTypedValue(t *testing.T) {
	restoreArchiveShouldFail(t, Manifest{
		Categories: []db.Category{{ID: 1, Name: "A"}},
		CategoryAttributes: []db.CategoryAttribute{
			{ID: 10, CategoryID: 1, Name: "Colour", DataType: "text"},
		},
		PartCategories: []db.PartCategory{{PartID: 1, CategoryID: 1}},
		PartAttributeValues: []db.PartAttributeValue{
			{PartID: 1, AttributeID: 10, ValueNumber: sql.NullFloat64{Float64: 1, Valid: true}},
		},
	})
}

// TestRestoreRejectsMissingRequiredSpecification verifies that a product in a
// category with a required specification and no value is rejected.
func TestRestoreRejectsMissingRequiredSpecification(t *testing.T) {
	restoreArchiveShouldFail(t, Manifest{
		Categories: []db.Category{{ID: 1, Name: "A"}},
		CategoryAttributes: []db.CategoryAttribute{
			{ID: 10, CategoryID: 1, Name: "Length", DataType: "number", IsRequired: true},
		},
		PartCategories: []db.PartCategory{{PartID: 1, CategoryID: 1}},
	})
}

// TestRestoreRejectsNumericValueOutsideRange verifies that a numeric value
// outside the attribute's declared range is rejected.
func TestRestoreRejectsNumericValueOutsideRange(t *testing.T) {
	restoreArchiveShouldFail(t, Manifest{
		Categories: []db.Category{{ID: 1, Name: "A"}},
		CategoryAttributes: []db.CategoryAttribute{
			{ID: 10, CategoryID: 1, Name: "Length", DataType: "number", MinValue: sql.NullFloat64{Float64: 1, Valid: true}, MaxValue: sql.NullFloat64{Float64: 100, Valid: true}},
		},
		PartCategories: []db.PartCategory{{PartID: 1, CategoryID: 1}},
		PartAttributeValues: []db.PartAttributeValue{
			{PartID: 1, AttributeID: 10, ValueNumber: sql.NullFloat64{Float64: 500, Valid: true}},
		},
	})
}

// TestRestoreRejectsMissingCategoryImage verifies that a category image
// reference with no corresponding uploaded file is rejected before activation.
func TestRestoreRejectsMissingCategoryImage(t *testing.T) {
	_, s, svc, _, cleanup := newBackupService(t)
	defer cleanup()
	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{
		Version:  "1.0",
		Settings: minimalSettings(),
		Categories: []db.Category{
			{ID: 1, Name: "A", ImagePath: "/uploads/images/missing.jpg"},
		},
	}
	zipBytes := buildRestoreZip(t, manifest)
	err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err == nil {
		t.Fatal("restore accepted a category image reference without a file")
	}
	if !strings.Contains(err.Error(), "image") {
		t.Fatalf("restore error = %v, want a category-image rejection", err)
	}
}
