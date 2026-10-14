package categories

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

func categoryFixture(t *testing.T) (*sql.DB, *Service, int64) {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "categories.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	store := db.NewStore(conn)
	partID, err := store.CreatePart(context.Background(), db.CreatePartParams{Name: "Reusable part"})
	if err != nil {
		t.Fatal(err)
	}
	return conn, NewService(conn), partID
}

func TestCategoryCRUDHierarchyAndSafeDelete(t *testing.T) {
	conn, service, partID := categoryFixture(t)
	ctx := context.Background()
	parentID, err := service.Save(ctx, Category{Name: "Hardware", Description: "Fasteners and tools"})
	if err != nil {
		t.Fatal(err)
	}
	childID, err := service.Save(ctx, Category{Name: "Fasteners", ParentID: sql.NullInt64{Int64: parentID, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Assign(ctx, partID, &childID); err != nil {
		t.Fatal(err)
	}

	child, err := service.Get(ctx, childID)
	if err != nil || child.ProductCount != 1 {
		t.Fatalf("child count = %d, err=%v; want 1", child.ProductCount, err)
	}
	if _, err := service.Save(ctx, Category{ID: parentID, Name: "Hardware", ParentID: sql.NullInt64{Int64: childID, Valid: true}}); !errors.Is(err, ErrCategoryCycle) {
		t.Fatalf("cycle update error = %v, want ErrCategoryCycle", err)
	}
	if err := service.Delete(ctx, childID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ForPart(ctx, partID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted category still assigned: %v", err)
	}
	var partCount int
	if err := conn.QueryRow(`SELECT count(*) FROM parts WHERE id = ?`, partID).Scan(&partCount); err != nil || partCount != 1 {
		t.Fatalf("product was not preserved by category deletion: count=%d err=%v", partCount, err)
	}
	if err := conn.QueryRow(`SELECT count(*) FROM part_assignments WHERE part_id = ?`, partID).Scan(&partCount); err != nil || partCount != 0 {
		t.Fatalf("unexpected stock rows for fixture product: count=%d err=%v", partCount, err)
	}
}

func TestTypedCategorySpecificationsAndRequiredValidation(t *testing.T) {
	_, service, partID := categoryFixture(t)
	ctx := context.Background()
	categoryID, err := service.Save(ctx, Category{Name: "Bolts"})
	if err != nil {
		t.Fatal(err)
	}
	attributeID, err := service.SaveAttribute(ctx, AttributeInput{CategoryID: categoryID, Name: "Length", DataType: NumberType, Unit: "mm", Required: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SetProductCategory(ctx, partID, &categoryID, nil); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("missing required value error = %v, want ErrInvalidValue", err)
	}
	if _, err := service.ForPart(ctx, partID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed category assignment persisted: %v", err)
	}
	length := 42.5
	if err := service.SetProductCategory(ctx, partID, &categoryID, []Specification{{AttributeID: attributeID, Number: &length}}); err != nil {
		t.Fatal(err)
	}
	values, err := service.Specifications(ctx, partID)
	if err != nil || len(values) != 1 || !values[0].Number.Valid || values[0].Number.Float64 != length || values[0].Attribute.Unit != "mm" {
		t.Fatalf("specifications = %#v, err=%v", values, err)
	}
	text := "M8"
	if err := service.SaveSpecifications(ctx, partID, []Specification{{AttributeID: attributeID, Text: &text}}); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("wrong typed value error = %v, want ErrInvalidValue", err)
	}
}

func TestRequiredAttributeCannotInvalidateExistingProducts(t *testing.T) {
	_, service, partID := categoryFixture(t)
	ctx := context.Background()
	categoryID, err := service.Save(ctx, Category{Name: "Tools"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Assign(ctx, partID, &categoryID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveAttribute(ctx, AttributeInput{CategoryID: categoryID, Name: "Serial number", DataType: TextType, Required: true}); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("required attribute creation error = %v, want ErrInvalidValue for existing categorized product", err)
	}
	attributes, err := service.ListAttributes(ctx, categoryID)
	if err != nil || len(attributes) != 0 {
		t.Fatalf("rejected attribute changed category definitions: %#v err=%v", attributes, err)
	}
}

func TestCategoryDuplicateNameAndAttributeIsolation(t *testing.T) {
	_, service, _ := categoryFixture(t)
	ctx := context.Background()
	firstID, err := service.Save(ctx, Category{Name: "Electronics"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Save(ctx, Category{Name: "electronics"}); err == nil {
		t.Fatal("case-insensitive duplicate category name was accepted")
	}
	if _, err := service.SaveAttribute(ctx, AttributeInput{CategoryID: firstID, Name: "Rating", DataType: TextType}); err != nil {
		t.Fatal(err)
	}
	attributes, err := service.ListAttributes(ctx, firstID)
	if err != nil || len(attributes) != 1 || attributes[0].Name != "Rating" {
		t.Fatalf("attributes = %#v, err=%v", attributes, err)
	}
}
