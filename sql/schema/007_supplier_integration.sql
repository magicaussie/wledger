-- +goose Up
PRAGMA foreign_keys = ON;

-- Tracks which provider imported a part (provider reference)
CREATE TABLE IF NOT EXISTS supplier_references (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    part_id INTEGER NOT NULL,
    provider_key TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    provider_url TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(part_id) REFERENCES parts(id) ON DELETE CASCADE,
    UNIQUE(provider_key, provider_id)
);
CREATE INDEX IF NOT EXISTS idx_supplier_references_part_id ON supplier_references(part_id);
CREATE INDEX IF NOT EXISTS idx_supplier_references_provider ON supplier_references(provider_key, provider_id);

-- Technical parameters fetched from suppliers
CREATE TABLE IF NOT EXISTS part_parameters (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    part_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    value_text TEXT,
    value_typ REAL,
    value_min REAL,
    value_max REAL,
    unit TEXT,
    symbol TEXT,
    param_group TEXT,
    FOREIGN KEY(part_id) REFERENCES parts(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_part_parameters_part_id ON part_parameters(part_id);

-- Price breaks from suppliers
CREATE TABLE IF NOT EXISTS part_pricing (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    part_id INTEGER NOT NULL,
    supplier_ref_id INTEGER NOT NULL,
    min_quantity INTEGER NOT NULL DEFAULT 1,
    price REAL NOT NULL,
    currency TEXT NOT NULL DEFAULT 'USD',
    includes_tax BOOLEAN DEFAULT 0,
    FOREIGN KEY(part_id) REFERENCES parts(id) ON DELETE CASCADE,
    FOREIGN KEY(supplier_ref_id) REFERENCES supplier_references(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_part_pricing_part_id ON part_pricing(part_id);

-- Cache for supplier search results and part details
CREATE TABLE IF NOT EXISTS supplier_cache (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    cache_key TEXT NOT NULL UNIQUE,
    provider_key TEXT NOT NULL,
    data JSON NOT NULL,
    expires_at DATETIME NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_supplier_cache_key ON supplier_cache(cache_key);
CREATE INDEX IF NOT EXISTS idx_supplier_cache_expires ON supplier_cache(expires_at);

-- Supplier API credentials
CREATE TABLE IF NOT EXISTS supplier_credentials (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    provider_key TEXT NOT NULL UNIQUE,
    api_key TEXT,
    api_secret TEXT,
    access_token TEXT,
    refresh_token TEXT,
    token_expires_at DATETIME,
    is_active BOOLEAN DEFAULT 1,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Add footprint column to parts table
ALTER TABLE parts ADD COLUMN footprint TEXT;

-- Add supplier integration settings
ALTER TABLE settings ADD COLUMN supplier_cache_ttl_hours INTEGER DEFAULT 96;
ALTER TABLE settings ADD COLUMN default_currency TEXT DEFAULT 'USD';

-- +goose Down
PRAGMA foreign_keys = ON;

ALTER TABLE parts DROP COLUMN footprint;
ALTER TABLE settings DROP COLUMN supplier_cache_ttl_hours;
ALTER TABLE settings DROP COLUMN default_currency;

DROP TABLE IF EXISTS part_pricing;
DROP TABLE IF EXISTS part_parameters;
DROP TABLE IF EXISTS supplier_references;
DROP TABLE IF EXISTS supplier_cache;
DROP TABLE IF EXISTS supplier_credentials;
