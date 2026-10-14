-- +goose Up
CREATE TABLE categories (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL COLLATE NOCASE UNIQUE CHECK (length(trim(name)) > 0),
    description TEXT NOT NULL DEFAULT '',
    image_path TEXT NOT NULL DEFAULT '',
    parent_id INTEGER,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (parent_id) REFERENCES categories(id) ON DELETE SET NULL
);
CREATE INDEX idx_categories_parent ON categories(parent_id, name);

-- A part may be placed in at most one reusable category. This separate table
-- keeps existing part identities and generated sqlc models unchanged.
CREATE TABLE part_categories (
    part_id INTEGER PRIMARY KEY,
    category_id INTEGER NOT NULL,
    FOREIGN KEY (part_id) REFERENCES parts(id) ON DELETE CASCADE,
    FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE CASCADE
);
CREATE INDEX idx_part_categories_category ON part_categories(category_id, part_id);

CREATE TABLE category_attributes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    category_id INTEGER NOT NULL,
    name TEXT NOT NULL COLLATE NOCASE CHECK (length(trim(name)) > 0),
    data_type TEXT NOT NULL CHECK (data_type IN ('text', 'number', 'boolean')),
    unit TEXT NOT NULL DEFAULT '',
    options TEXT NOT NULL DEFAULT '',
    min_value REAL,
    max_value REAL,
    is_required BOOLEAN NOT NULL DEFAULT 0 CHECK (is_required IN (0, 1)),
    display_order INTEGER NOT NULL DEFAULT 0 CHECK (display_order >= 0),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (min_value IS NULL OR max_value IS NULL OR min_value <= max_value),
    UNIQUE (category_id, name),
    FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE CASCADE
);
CREATE INDEX idx_category_attributes_order ON category_attributes(category_id, display_order, id);

CREATE TABLE part_attribute_values (
    part_id INTEGER NOT NULL,
    attribute_id INTEGER NOT NULL,
    value_text TEXT,
    value_number REAL,
    value_boolean BOOLEAN CHECK (value_boolean IS NULL OR value_boolean IN (0, 1)),
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (part_id, attribute_id),
    CHECK ((value_text IS NOT NULL) + (value_number IS NOT NULL) + (value_boolean IS NOT NULL) = 1),
    FOREIGN KEY (part_id) REFERENCES parts(id) ON DELETE CASCADE,
    FOREIGN KEY (attribute_id) REFERENCES category_attributes(id) ON DELETE CASCADE
);
CREATE INDEX idx_part_attribute_text ON part_attribute_values(value_text);
CREATE INDEX idx_part_attribute_number ON part_attribute_values(value_number);

-- +goose Down
-- Refuse to discard user-authored category data during a migration rollback.
CREATE TEMP TABLE _categories_rollback_guard (ok INTEGER NOT NULL CHECK (ok = 0));
INSERT INTO _categories_rollback_guard (ok)
SELECT 1 WHERE EXISTS (SELECT 1 FROM categories)
   OR EXISTS (SELECT 1 FROM category_attributes)
   OR EXISTS (SELECT 1 FROM part_categories)
   OR EXISTS (SELECT 1 FROM part_attribute_values);
DROP TABLE _categories_rollback_guard;
DROP TABLE part_attribute_values;
DROP TABLE category_attributes;
DROP TABLE part_categories;
DROP TABLE categories;
