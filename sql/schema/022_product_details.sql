-- +goose Up
CREATE TABLE part_details (
    part_id INTEGER PRIMARY KEY,
    sku TEXT UNIQUE COLLATE NOCASE,
    brand TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    colour TEXT NOT NULL DEFAULT '',
    material TEXT NOT NULL DEFAULT '',
    finish TEXT NOT NULL DEFAULT '',
    condition TEXT NOT NULL DEFAULT '',
    notes TEXT NOT NULL DEFAULT '',
    length_mm REAL CHECK (length_mm IS NULL OR length_mm > 0),
    width_mm REAL CHECK (width_mm IS NULL OR width_mm > 0),
    height_mm REAL CHECK (height_mm IS NULL OR height_mm > 0),
    depth_mm REAL CHECK (depth_mm IS NULL OR depth_mm > 0),
    weight_g REAL CHECK (weight_g IS NULL OR weight_g >= 0),
    purchased_from TEXT NOT NULL DEFAULT '',
    supplier_product_code TEXT NOT NULL DEFAULT '',
    purchase_date TEXT,
    purchase_price REAL CHECK (purchase_price IS NULL OR purchase_price >= 0),
    currency TEXT NOT NULL DEFAULT 'AUD' CHECK (length(currency) = 3),
    product_url TEXT NOT NULL DEFAULT '',
    purchase_notes TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (part_id) REFERENCES parts(id) ON DELETE CASCADE
);
CREATE INDEX idx_part_details_brand_model ON part_details(brand, model);
CREATE INDEX idx_part_details_purchase_code ON part_details(supplier_product_code);

-- +goose Down
CREATE TEMP TABLE _part_details_rollback_guard (ok INTEGER NOT NULL CHECK (ok = 0));
INSERT INTO _part_details_rollback_guard (ok)
SELECT 1 WHERE EXISTS (SELECT 1 FROM part_details);
DROP TABLE _part_details_rollback_guard;
DROP TABLE part_details;
