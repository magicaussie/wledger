package categories

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"path"
	"strconv"
	"strings"

	"github.com/tuxedocurly/wledger/internal/config"
)

var (
	ErrNotFound       = errors.New("category not found")
	ErrInvalid        = errors.New("invalid category data")
	ErrInvalidValue   = errors.New("invalid specification value")
	ErrCategoryCycle  = errors.New("category cannot be its own ancestor")
	ErrAttributeScope = errors.New("attribute does not belong to the product category")
)

type DataType string

const (
	TextType    DataType = "text"
	NumberType  DataType = "number"
	BooleanType DataType = "boolean"
)

type Category struct {
	ID           int64
	Name         string
	Description  string
	ImagePath    string
	ParentID     sql.NullInt64
	ProductCount int64
}

type Product struct {
	ID        int64
	Name      string
	ImagePath sql.NullString
}

type Attribute struct {
	ID           int64
	CategoryID   int64
	Name         string
	DataType     DataType
	Unit         string
	Options      []string
	Minimum      sql.NullFloat64
	Maximum      sql.NullFloat64
	Required     bool
	DisplayOrder int64
}

type AttributeInput struct {
	ID           int64
	CategoryID   int64
	Name         string
	DataType     DataType
	Unit         string
	Options      []string
	Minimum      sql.NullFloat64
	Maximum      sql.NullFloat64
	Required     bool
	DisplayOrder int64
}

func (a Attribute) OptionsText() string  { return strings.Join(a.Options, "\n") }
func (a Attribute) MinimumInput() string { return nullFloatInput(a.Minimum) }
func (a Attribute) MaximumInput() string { return nullFloatInput(a.Maximum) }

func nullFloatInput(value sql.NullFloat64) string {
	if !value.Valid {
		return ""
	}
	return strconv.FormatFloat(value.Float64, 'f', -1, 64)
}

type Specification struct {
	AttributeID int64
	Text        *string
	Number      *float64
	Boolean     *bool
}

type Service struct{ db *sql.DB }

func NewService(database *sql.DB) *Service { return &Service{db: database} }

func (s *Service) List(ctx context.Context, search string) ([]Category, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.name, c.description, c.image_path, c.parent_id,
		       (SELECT count(*) FROM part_categories pc WHERE pc.category_id = c.id)
		FROM categories c
		WHERE ? = '' OR c.name LIKE '%' || ? || '%' OR c.description LIKE '%' || ? || '%'
		ORDER BY c.name COLLATE NOCASE, c.id`, search, search, search)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Category
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &c.ImagePath, &c.ParentID, &c.ProductCount); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

func (s *Service) Products(ctx context.Context, categoryID int64, search string) ([]Product, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.id, p.name, p.image_path FROM part_categories pc JOIN parts p ON p.id = pc.part_id WHERE pc.category_id = ? AND (? = '' OR p.name LIKE '%' || ? || '%' OR coalesce(p.description, '') LIKE '%' || ? || '%' OR EXISTS (SELECT 1 FROM part_attribute_values v WHERE v.part_id = p.id AND (coalesce(v.value_text, '') LIKE '%' || ? || '%' OR cast(v.value_number AS TEXT) LIKE '%' || ? || '%'))) ORDER BY p.name COLLATE NOCASE, p.id`, categoryID, search, search, search, search, search)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var products []Product
	for rows.Next() {
		var product Product
		if err := rows.Scan(&product.ID, &product.Name, &product.ImagePath); err != nil {
			return nil, err
		}
		products = append(products, product)
	}
	return products, rows.Err()
}

func (s *Service) Get(ctx context.Context, id int64) (Category, error) {
	var c Category
	err := s.db.QueryRowContext(ctx, `SELECT c.id, c.name, c.description, c.image_path, c.parent_id,
		(SELECT count(*) FROM part_categories pc WHERE pc.category_id = c.id)
		FROM categories c WHERE c.id = ?`, id).Scan(&c.ID, &c.Name, &c.Description, &c.ImagePath, &c.ParentID, &c.ProductCount)
	if errors.Is(err, sql.ErrNoRows) {
		return Category{}, ErrNotFound
	}
	return c, err
}

func (s *Service) Save(ctx context.Context, category Category) (int64, error) {
	category.Name = strings.TrimSpace(category.Name)
	category.Description = strings.TrimSpace(category.Description)
	category.ImagePath = strings.TrimSpace(category.ImagePath)
	if category.Name == "" || len(category.Name) > 120 || len(category.Description) > 4000 {
		return 0, ErrInvalid
	}
	if category.ImagePath != "" && (!strings.HasPrefix(category.ImagePath, config.UrlPrefixImages) || path.Base(category.ImagePath) != strings.TrimPrefix(category.ImagePath, config.UrlPrefixImages)) {
		return 0, ErrInvalid
	}
	if category.ParentID.Valid && category.ParentID.Int64 <= 0 {
		return 0, ErrInvalid
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if category.ParentID.Valid {
		var parentID int64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM categories WHERE id = ?`, category.ParentID.Int64).Scan(&parentID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return 0, ErrNotFound
			}
			return 0, err
		}
		if category.ID == category.ParentID.Int64 {
			return 0, ErrCategoryCycle
		}
		var descendant int
		err := tx.QueryRowContext(ctx, `WITH RECURSIVE descendants(id) AS (
			SELECT id FROM categories WHERE parent_id = ?
			UNION ALL SELECT c.id FROM categories c JOIN descendants d ON c.parent_id = d.id
		) SELECT EXISTS(SELECT 1 FROM descendants WHERE id = ?)`, category.ID, category.ParentID.Int64).Scan(&descendant)
		if err != nil {
			return 0, err
		}
		if descendant != 0 {
			return 0, ErrCategoryCycle
		}
	}
	var id int64
	if category.ID == 0 {
		err = tx.QueryRowContext(ctx, `INSERT INTO categories(name, description, image_path, parent_id) VALUES(?, ?, ?, ?) RETURNING id`,
			category.Name, category.Description, category.ImagePath, nullableInt(category.ParentID)).Scan(&id)
	} else {
		result, updateErr := tx.ExecContext(ctx, `UPDATE categories SET name = ?, description = ?, image_path = ?, parent_id = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
			category.Name, category.Description, category.ImagePath, nullableInt(category.ParentID), category.ID)
		err = updateErr
		if err == nil {
			var affected int64
			affected, err = result.RowsAffected()
			if err == nil && affected == 0 {
				err = ErrNotFound
			}
			id = category.ID
		}
	}
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE categories SET parent_id = NULL WHERE parent_id = ?`, id); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM categories WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

func (s *Service) ListAttributes(ctx context.Context, categoryID int64) ([]Attribute, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, category_id, name, data_type, unit, options, min_value, max_value, is_required, display_order FROM category_attributes WHERE category_id = ? ORDER BY display_order, name COLLATE NOCASE, id`, categoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Attribute
	for rows.Next() {
		var a Attribute
		var typ, options string
		var required bool
		if err := rows.Scan(&a.ID, &a.CategoryID, &a.Name, &typ, &a.Unit, &options, &a.Minimum, &a.Maximum, &required, &a.DisplayOrder); err != nil {
			return nil, err
		}
		a.DataType, a.Required, a.Options = DataType(typ), required, decodeOptions(options)
		result = append(result, a)
	}
	return result, rows.Err()
}

func (s *Service) SaveAttribute(ctx context.Context, input AttributeInput) (int64, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Unit = strings.TrimSpace(input.Unit)
	options, err := normalizeOptions(input.Options)
	if err != nil {
		return 0, err
	}
	input.Options = options
	if input.CategoryID <= 0 || input.Name == "" || len(input.Name) > 120 || len(input.Unit) > 32 || input.DisplayOrder < 0 ||
		(input.DataType != TextType && input.DataType != NumberType && input.DataType != BooleanType) {
		return 0, ErrInvalid
	}
	if input.DataType == BooleanType && len(input.Options) > 0 {
		return 0, ErrInvalid
	}
	if input.DataType != NumberType && (input.Minimum.Valid || input.Maximum.Valid) {
		return 0, ErrInvalid
	}
	if input.Minimum.Valid && (!finite(input.Minimum.Float64) || input.Minimum.Float64 < 0) {
		return 0, ErrInvalid
	}
	if input.Maximum.Valid && (!finite(input.Maximum.Float64) || input.Maximum.Float64 < 0) {
		return 0, ErrInvalid
	}
	if input.Minimum.Valid && input.Maximum.Valid && input.Minimum.Float64 > input.Maximum.Float64 {
		return 0, ErrInvalid
	}
	if input.DataType == NumberType {
		for _, option := range input.Options {
			if _, err := strconv.ParseFloat(option, 64); err != nil {
				return 0, ErrInvalid
			}
		}
	}
	if _, err := s.Get(ctx, input.CategoryID); err != nil {
		return 0, err
	}
	if input.Required {
		var missing int
		err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM part_categories pc LEFT JOIN part_attribute_values v ON v.part_id = pc.part_id AND v.attribute_id = ? WHERE pc.category_id = ? AND (v.attribute_id IS NULL OR (v.value_text IS NOT NULL AND trim(v.value_text) = ''))`, input.ID, input.CategoryID).Scan(&missing)
		if err != nil {
			return 0, err
		}
		if missing > 0 {
			return 0, ErrInvalidValue
		}
	}
	if input.ID == 0 {
		var id int64
		err := s.db.QueryRowContext(ctx, `INSERT INTO category_attributes(category_id, name, data_type, unit, options, min_value, max_value, is_required, display_order) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
			input.CategoryID, input.Name, input.DataType, input.Unit, strings.Join(input.Options, "\n"), nullableFloat(input.Minimum), nullableFloat(input.Maximum), input.Required, input.DisplayOrder).Scan(&id)
		return id, err
	}
	var existingType string
	if err := s.db.QueryRowContext(ctx, `SELECT data_type FROM category_attributes WHERE id = ? AND category_id = ?`, input.ID, input.CategoryID).Scan(&existingType); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	if existingType != string(input.DataType) {
		var values int
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM part_attribute_values WHERE attribute_id = ?`, input.ID).Scan(&values); err != nil {
			return 0, err
		}
		if values > 0 {
			return 0, fmt.Errorf("%w: cannot change a specification type while product values exist", ErrInvalidValue)
		}
	}
	if err := s.validateStoredValues(ctx, input); err != nil {
		return 0, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE category_attributes SET name = ?, data_type = ?, unit = ?, options = ?, min_value = ?, max_value = ?, is_required = ?, display_order = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND category_id = ?`,
		input.Name, input.DataType, input.Unit, strings.Join(input.Options, "\n"), nullableFloat(input.Minimum), nullableFloat(input.Maximum), input.Required, input.DisplayOrder, input.ID, input.CategoryID)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, ErrNotFound
	}
	return input.ID, nil
}

func normalizeOptions(options []string) ([]string, error) {
	seen := make(map[string]bool, len(options))
	result := make([]string, 0, len(options))
	for _, option := range options {
		option = strings.TrimSpace(option)
		if option == "" {
			continue
		}
		if len(option) > 200 || strings.ContainsAny(option, "\r\n") || seen[option] {
			return nil, ErrInvalid
		}
		seen[option] = true
		result = append(result, option)
	}
	return result, nil
}

func decodeOptions(raw string) []string {
	if raw == "" {
		return nil
	}
	lines := strings.Split(raw, "\n")
	options, _ := normalizeOptions(lines)
	return options
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func nullableFloat(value sql.NullFloat64) any {
	if !value.Valid {
		return nil
	}
	return value.Float64
}

func (s *Service) validateStoredValues(ctx context.Context, input AttributeInput) error {
	rows, err := s.db.QueryContext(ctx, `SELECT value_text, value_number, value_boolean FROM part_attribute_values WHERE attribute_id = ?`, input.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var text sql.NullString
		var number sql.NullFloat64
		var boolean sql.NullBool
		if err := rows.Scan(&text, &number, &boolean); err != nil {
			return err
		}
		switch input.DataType {
		case TextType:
			if !text.Valid || (len(input.Options) > 0 && !containsOption(input.Options, text.String)) {
				return ErrInvalidValue
			}
		case NumberType:
			if !number.Valid || !withinRange(number.Float64, input.Minimum, input.Maximum) || (len(input.Options) > 0 && !containsNumberOption(input.Options, number.Float64)) {
				return ErrInvalidValue
			}
		case BooleanType:
			if !boolean.Valid {
				return ErrInvalidValue
			}
		}
	}
	return rows.Err()
}

func containsOption(options []string, value string) bool {
	for _, option := range options {
		if option == value {
			return true
		}
	}
	return false
}

func containsNumberOption(options []string, value float64) bool {
	for _, option := range options {
		number, err := strconv.ParseFloat(option, 64)
		if err == nil && number == value {
			return true
		}
	}
	return false
}

func withinRange(value float64, minimum, maximum sql.NullFloat64) bool {
	return finite(value) && (!minimum.Valid || value >= minimum.Float64) && (!maximum.Valid || value <= maximum.Float64)
}

func (s *Service) DeleteAttribute(ctx context.Context, categoryID, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM category_attributes WHERE id = ? AND category_id = ?`, id, categoryID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) Assign(ctx context.Context, partID int64, categoryID *int64) error {
	if partID <= 0 || (categoryID != nil && *categoryID <= 0) {
		return ErrInvalid
	}
	if categoryID == nil {
		return s.SetProductCategory(ctx, partID, nil, nil)
	}
	current, err := s.ForPart(ctx, partID)
	if err == nil && current.ID == *categoryID {
		return nil
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return s.SetProductCategory(ctx, partID, categoryID, nil)
}

func (s *Service) ForPart(ctx context.Context, partID int64) (Category, error) {
	var c Category
	err := s.db.QueryRowContext(ctx, `SELECT c.id, c.name, c.description, c.image_path, c.parent_id,
		(SELECT count(*) FROM part_categories pc WHERE pc.category_id = c.id)
		FROM part_categories pc JOIN categories c ON c.id = pc.category_id WHERE pc.part_id = ?`, partID).
		Scan(&c.ID, &c.Name, &c.Description, &c.ImagePath, &c.ParentID, &c.ProductCount)
	if errors.Is(err, sql.ErrNoRows) {
		return Category{}, ErrNotFound
	}
	return c, err
}

func (s *Service) ValidateSpecifications(ctx context.Context, categoryID int64, specs []Specification) error {
	if categoryID <= 0 {
		if len(specs) > 0 {
			return ErrAttributeScope
		}
		return nil
	}
	attributes, err := s.ListAttributes(ctx, categoryID)
	if err != nil {
		return err
	}
	byID := make(map[int64]Attribute, len(attributes))
	for _, attribute := range attributes {
		byID[attribute.ID] = attribute
	}
	provided := make(map[int64]bool, len(specs))
	for _, spec := range specs {
		attribute, ok := byID[spec.AttributeID]
		if !ok || provided[spec.AttributeID] {
			return ErrAttributeScope
		}
		provided[spec.AttributeID] = true
		switch attribute.DataType {
		case TextType:
			if spec.Text == nil || spec.Number != nil || spec.Boolean != nil || len(*spec.Text) > 4000 || (attribute.Required && strings.TrimSpace(*spec.Text) == "") || (len(attribute.Options) > 0 && !containsOption(attribute.Options, *spec.Text)) {
				return ErrInvalidValue
			}
		case NumberType:
			if spec.Number == nil || spec.Text != nil || spec.Boolean != nil || !withinRange(*spec.Number, attribute.Minimum, attribute.Maximum) || (len(attribute.Options) > 0 && !containsNumberOption(attribute.Options, *spec.Number)) {
				return ErrInvalidValue
			}
		case BooleanType:
			if spec.Boolean == nil || spec.Text != nil || spec.Number != nil {
				return ErrInvalidValue
			}
		default:
			return ErrInvalidValue
		}
	}
	for _, attribute := range attributes {
		if attribute.Required && !provided[attribute.ID] {
			return ErrInvalidValue
		}
	}
	return nil
}

func (s *Service) SetProductCategory(ctx context.Context, partID int64, categoryID *int64, specs []Specification) error {
	if partID <= 0 || (categoryID != nil && *categoryID <= 0) {
		return ErrInvalid
	}
	id := int64(0)
	if categoryID != nil {
		id = *categoryID
	}
	if err := s.ValidateSpecifications(ctx, id, specs); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if categoryID == nil {
		if len(specs) > 0 {
			return ErrAttributeScope
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM part_categories WHERE part_id = ?`, partID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM part_attribute_values WHERE part_id = ?`, partID); err != nil {
			return err
		}
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO part_categories(part_id, category_id) VALUES(?, ?) ON CONFLICT(part_id) DO UPDATE SET category_id = excluded.category_id`, partID, *categoryID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM part_attribute_values WHERE part_id = ?`, partID); err != nil {
		return err
	}
	for _, spec := range specs {
		var text, number, boolean any
		if spec.Text != nil {
			text = *spec.Text
		}
		if spec.Number != nil {
			number = *spec.Number
		}
		if spec.Boolean != nil {
			boolean = *spec.Boolean
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO part_attribute_values(part_id, attribute_id, value_text, value_number, value_boolean) VALUES(?, ?, ?, ?, ?)`, partID, spec.AttributeID, text, number, boolean); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) SaveSpecifications(ctx context.Context, partID int64, specs []Specification) error {
	if partID <= 0 {
		return ErrInvalid
	}
	category, err := s.ForPart(ctx, partID)
	if errors.Is(err, ErrNotFound) {
		if len(specs) > 0 {
			return ErrAttributeScope
		}
		_, err := s.db.ExecContext(ctx, `DELETE FROM part_attribute_values WHERE part_id = ?`, partID)
		return err
	}
	if err != nil {
		return err
	}
	return s.SetProductCategory(ctx, partID, &category.ID, specs)
}

func (s *Service) Specifications(ctx context.Context, partID int64) ([]AttributeValue, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.id, a.name, a.data_type, a.unit, a.is_required, v.value_text, v.value_number, v.value_boolean
		FROM part_attribute_values v JOIN category_attributes a ON a.id = v.attribute_id WHERE v.part_id = ? ORDER BY a.display_order, a.name COLLATE NOCASE`, partID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []AttributeValue
	for rows.Next() {
		var value AttributeValue
		var typ string
		var required bool
		if err := rows.Scan(&value.Attribute.ID, &value.Attribute.Name, &typ, &value.Attribute.Unit, &required, &value.Text, &value.Number, &value.Boolean); err != nil {
			return nil, err
		}
		value.Attribute.DataType, value.Attribute.Required = DataType(typ), required
		result = append(result, value)
	}
	return result, rows.Err()
}

type AttributeValue struct {
	Attribute Attribute
	Text      sql.NullString
	Number    sql.NullFloat64
	Boolean   sql.NullBool
}

func (v AttributeValue) InputValue() string {
	if v.Text.Valid {
		return v.Text.String
	}
	if v.Number.Valid {
		return strconv.FormatFloat(v.Number.Float64, 'f', -1, 64)
	}
	if v.Boolean.Valid {
		if v.Boolean.Bool {
			return "true"
		}
		return "false"
	}
	return ""
}

func nullableInt(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}
