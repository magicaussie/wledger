package backup

import (
	"fmt"
	"path"
	"strings"

	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/db"
)

// validateRestoredCategorySemantics enforces the category and specification
// rules that the category service applies on write but that foreign-key and
// SQLite integrity checks cannot express: acyclic parent graphs, attributes
// scoped to the product's category, typed values that match the attribute
// definition, option and numeric-range membership, and required attributes
// present for every product in a category.
//
// Restore inserts category rows directly, so without this a crafted archive
// could establish state that normal application operations reject (and which
// would later make an export fail). It runs before any target data is touched
// because restoreData is invoked inside the disposable candidate database first.
func validateRestoredCategorySemantics(m Manifest) error {
	categoryByID := make(map[int64]db.Category, len(m.Categories))
	for _, category := range m.Categories {
		if category.ID <= 0 {
			return fmt.Errorf("category restore: invalid category id %d", category.ID)
		}
		if _, duplicate := categoryByID[category.ID]; duplicate {
			return fmt.Errorf("category restore: duplicate category id %d", category.ID)
		}
		if strings.TrimSpace(category.Name) == "" || len(category.Name) > 120 || len(category.Description) > 4000 {
			return fmt.Errorf("category restore: category %d has an invalid name or description", category.ID)
		}
		if category.ImagePath != "" {
			if !strings.HasPrefix(category.ImagePath, config.UrlPrefixImages) ||
				path.Base(category.ImagePath) != strings.TrimPrefix(category.ImagePath, config.UrlPrefixImages) {
				return fmt.Errorf("category restore: category %d has an invalid image path", category.ID)
			}
		}
		categoryByID[category.ID] = category
	}
	for _, category := range m.Categories {
		if !category.ParentID.Valid {
			continue
		}
		if category.ParentID.Int64 == category.ID {
			return fmt.Errorf("category restore: category %d is its own parent", category.ID)
		}
		if _, ok := categoryByID[category.ParentID.Int64]; !ok {
			return fmt.Errorf("category restore: category %d has a missing parent %d", category.ID, category.ParentID.Int64)
		}
	}
	if err := validateCategoryAcyclic(categoryByID); err != nil {
		return err
	}

	attributeByID := make(map[int64]db.CategoryAttribute, len(m.CategoryAttributes))
	for _, attribute := range m.CategoryAttributes {
		if attribute.ID <= 0 {
			return fmt.Errorf("category restore: invalid specification id %d", attribute.ID)
		}
		if _, ok := categoryByID[attribute.CategoryID]; !ok {
			return fmt.Errorf("category restore: specification %d refers to missing category %d", attribute.ID, attribute.CategoryID)
		}
		if _, duplicate := attributeByID[attribute.ID]; duplicate {
			return fmt.Errorf("category restore: duplicate specification id %d", attribute.ID)
		}
		if strings.TrimSpace(attribute.Name) == "" || len(attribute.Name) > 120 || len(attribute.Unit) > 32 || attribute.DisplayOrder < 0 {
			return fmt.Errorf("category restore: specification %d has an invalid name, unit or order", attribute.ID)
		}
		switch attribute.DataType {
		case "text", "number", "boolean":
		default:
			return fmt.Errorf("category restore: specification %d has unsupported type %q", attribute.ID, attribute.DataType)
		}
		if attribute.MinValue.Valid && attribute.MaxValue.Valid && attribute.MinValue.Float64 > attribute.MaxValue.Float64 {
			return fmt.Errorf("category restore: specification %d has an inverted range", attribute.ID)
		}
		attributeByID[attribute.ID] = attribute
	}

	// Product category assignments: the referenced category must exist and each
	// product may hold at most one category.
	partCategory := make(map[int64]int64, len(m.PartCategories))
	for _, assignment := range m.PartCategories {
		if _, ok := categoryByID[assignment.CategoryID]; !ok {
			return fmt.Errorf("category restore: product %d refers to missing category %d", assignment.PartID, assignment.CategoryID)
		}
		if existing, duplicate := partCategory[assignment.PartID]; duplicate {
			return fmt.Errorf("category restore: product %d is assigned to multiple categories (%d and %d)", assignment.PartID, existing, assignment.CategoryID)
		}
		partCategory[assignment.PartID] = assignment.CategoryID
	}

	// Specification values: the attribute must belong to the product's category
	// and the typed value must match the attribute definition.
	valueByPartAttribute := make(map[[2]int64]struct{}, len(m.PartAttributeValues))
	for _, value := range m.PartAttributeValues {
		categoryID, ok := partCategory[value.PartID]
		if !ok {
			return fmt.Errorf("category restore: specification for uncategorized product %d", value.PartID)
		}
		attribute, ok := attributeByID[value.AttributeID]
		if !ok {
			return fmt.Errorf("category restore: product %d refers to missing specification %d", value.PartID, value.AttributeID)
		}
		if attribute.CategoryID != categoryID {
			return fmt.Errorf("category restore: product %d has a specification outside its category", value.PartID)
		}
		key := [2]int64{value.PartID, value.AttributeID}
		if _, duplicate := valueByPartAttribute[key]; duplicate {
			return fmt.Errorf("category restore: product %d has a duplicate value for specification %d", value.PartID, value.AttributeID)
		}
		if err := validateRestoredSpecificationValue(value, attribute); err != nil {
			return err
		}
		valueByPartAttribute[key] = struct{}{}
	}

	// Required attributes must be present for every product in the category.
	for _, assignment := range m.PartCategories {
		for _, attribute := range m.CategoryAttributes {
			if attribute.CategoryID != assignment.CategoryID || !attribute.IsRequired {
				continue
			}
			if _, ok := valueByPartAttribute[[2]int64{assignment.PartID, attribute.ID}]; !ok {
				return fmt.Errorf("category restore: product %d is missing required specification %q", assignment.PartID, attribute.Name)
			}
		}
	}
	return nil
}

func validateRestoredSpecificationValue(value db.PartAttributeValue, attribute db.CategoryAttribute) error {
	switch attribute.DataType {
	case "text":
		if !value.ValueText.Valid {
			return fmt.Errorf("category restore: specification %q expects a text value", attribute.Name)
		}
		options := decodeRestoredOptions(attribute.Options)
		if len(options) > 0 && !containsString(options, value.ValueText.String) {
			return fmt.Errorf("category restore: specification %q value is not an allowed option", attribute.Name)
		}
	case "number":
		if !value.ValueNumber.Valid {
			return fmt.Errorf("category restore: specification %q expects a numeric value", attribute.Name)
		}
		if attribute.MinValue.Valid && value.ValueNumber.Float64 < attribute.MinValue.Float64 {
			return fmt.Errorf("category restore: specification %q value is below its minimum", attribute.Name)
		}
		if attribute.MaxValue.Valid && value.ValueNumber.Float64 > attribute.MaxValue.Float64 {
			return fmt.Errorf("category restore: specification %q value is above its maximum", attribute.Name)
		}
	case "boolean":
		if !value.ValueBoolean.Valid {
			return fmt.Errorf("category restore: specification %q expects a boolean value", attribute.Name)
		}
	}
	return nil
}

func decodeRestoredOptions(raw string) []string {
	if raw == "" {
		return nil
	}
	var options []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			options = append(options, line)
		}
	}
	return options
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// validateCategoryAcyclic rejects any parent chain that loops back on itself.
func validateCategoryAcyclic(categoryByID map[int64]db.Category) error {
	const (
		visiting = 1
		done     = 2
	)
	state := make(map[int64]int, len(categoryByID))
	var visit func(int64) error
	visit = func(id int64) error {
		switch state[id] {
		case visiting:
			return fmt.Errorf("category restore: category parent graph contains a cycle at %d", id)
		case done:
			return nil
		}
		state[id] = visiting
		if category, ok := categoryByID[id]; ok && category.ParentID.Valid {
			if err := visit(category.ParentID.Int64); err != nil {
				return err
			}
		}
		state[id] = done
		return nil
	}
	for id := range categoryByID {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}
