-- +goose Up
ALTER TABLE part_details ADD COLUMN dimension_unit TEXT NOT NULL DEFAULT 'mm' CHECK (dimension_unit IN ('mm', 'cm', 'in'));
ALTER TABLE part_details ADD COLUMN weight_unit TEXT NOT NULL DEFAULT 'g' CHECK (weight_unit IN ('g', 'kg', 'oz', 'lb'));

-- +goose Down
DROP TABLE IF EXISTS _product_measurement_units_rollback_guard;
CREATE TEMP TABLE _product_measurement_units_rollback_guard (ok INTEGER NOT NULL CHECK (ok = 0));
INSERT INTO _product_measurement_units_rollback_guard (ok)
SELECT 1 WHERE EXISTS (
    SELECT 1 FROM part_details
    WHERE dimension_unit <> 'mm' OR weight_unit <> 'g'
);
DROP TABLE _product_measurement_units_rollback_guard;
ALTER TABLE part_details DROP COLUMN weight_unit;
ALTER TABLE part_details DROP COLUMN dimension_unit;
