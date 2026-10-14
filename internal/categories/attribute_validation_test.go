package categories

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestCategoryAttributeOptionsAndNumericBounds(t *testing.T) {
	_, service, partID := categoryFixture(t)
	ctx := context.Background()
	categoryID, err := service.Save(ctx, Category{Name: "Coatings"})
	if err != nil {
		t.Fatal(err)
	}
	textAttribute, err := service.SaveAttribute(ctx, AttributeInput{CategoryID: categoryID, Name: "Finish", DataType: TextType, Options: []string{"Zinc", "Black oxide"}})
	if err != nil {
		t.Fatal(err)
	}
	minimum, maximum := 1.0, 100.0
	numericAttribute, err := service.SaveAttribute(ctx, AttributeInput{CategoryID: categoryID, Name: "Thickness", DataType: NumberType, Unit: "um", Minimum: sql.NullFloat64{Float64: minimum, Valid: true}, Maximum: sql.NullFloat64{Float64: maximum, Valid: true}, Required: true})
	if err != nil {
		t.Fatal(err)
	}

	finish := "Chrome"
	thickness := 5.0
	if err := service.SetProductCategory(ctx, partID, &categoryID, []Specification{{AttributeID: textAttribute, Text: &finish}, {AttributeID: numericAttribute, Number: &thickness}}); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("unsupported text option accepted: %v", err)
	}
	finish = "Zinc"
	thickness = 0
	if err := service.SetProductCategory(ctx, partID, &categoryID, []Specification{{AttributeID: textAttribute, Text: &finish}, {AttributeID: numericAttribute, Number: &thickness}}); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("number below category range accepted: %v", err)
	}
	thickness = 5
	if err := service.SetProductCategory(ctx, partID, &categoryID, []Specification{{AttributeID: textAttribute, Text: &finish}, {AttributeID: numericAttribute, Number: &thickness}}); err != nil {
		t.Fatalf("valid constrained specifications rejected: %v", err)
	}
	values, err := service.Products(ctx, categoryID, "Zinc")
	if err != nil || len(values) != 1 || values[0].ID != partID {
		t.Fatalf("search by configured option value = %#v err=%v", values, err)
	}

	badRange := AttributeInput{CategoryID: categoryID, Name: "Impossible", DataType: NumberType, Minimum: sql.NullFloat64{Float64: 10, Valid: true}, Maximum: sql.NullFloat64{Float64: 2, Valid: true}}
	if _, err := service.SaveAttribute(ctx, badRange); !errors.Is(err, ErrInvalid) {
		t.Fatalf("inverted range error=%v, want ErrInvalid", err)
	}

}
