-- This file is for sqlc generation only
-- It helps the sqlc tool understand the hidden columns of the FTS5 table
DROP TABLE IF EXISTS parts_fts;

CREATE TABLE parts_fts (
    name TEXT,
    description TEXT,
    part_number TEXT,
    manufacturer TEXT,
    supplier TEXT,
    barcode_data TEXT,
    
    -- Magic FTS5 columns that sqlc needs to know about explicitly
    parts_fts TEXT, 
    rowid INTEGER,
    rank REAL
);