-- SQLC FTS5 Workaround
-- 
-- This file is NOT a database migration and should not be executed against a live database.
-- It exists solely to help the 'sqlc' tool understand the structure of the FTS5 virtual tables,
-- as sqlc cannot currently introspect virtual tables or hidden columns like 'rank' and 'rowid'
-- directly from the SQLite schema.
--
-- For more info, see: https://sqlc.dev/

DROP TABLE IF EXISTS parts_fts;

CREATE TABLE parts_fts (
    name TEXT,
    description TEXT,
    part_number TEXT,
    manufacturer TEXT,
    supplier TEXT,
    barcode_data TEXT,
    tags TEXT,
    
    -- Magic FTS5 columns that sqlc needs to know about explicitly
    parts_fts TEXT, 
    rowid INTEGER,
    rank REAL
);
