-- Enable Foreign Keys
PRAGMA foreign_keys = ON;

-- Users & Auth
CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    email TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    role TEXT CHECK(role IN ('admin', 'editor', 'viewer')) NOT NULL DEFAULT 'viewer',
    change_password_required BOOLEAN DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
    
);

-- Sessions (Required for SCS with SQLite)
CREATE TABLE IF NOT EXISTS sessions (
    token TEXT PRIMARY KEY,
    data BLOB NOT NULL,
    expiry REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_expiry_idx ON sessions(expiry);

-- App Settings (Singleton Row)
CREATE TABLE IF NOT EXISTS settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    require_auth_for_read BOOLEAN DEFAULT 1,
    locate_timeout_seconds INTEGER DEFAULT 10, -- Default 10 seconds
    enable_locate_timeout BOOLEAN DEFAULT 0,    -- Default off (indefinite)
    
    -- Global Colors
    color_locate TEXT DEFAULT '#0000FF',       -- Blue
    color_stock_ok TEXT DEFAULT '#00FF00',     -- Green
    color_stock_low TEXT DEFAULT '#FFFF00',    -- Yellow
    color_stock_critical TEXT DEFAULT '#FF0000', -- Red

    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- WLED Controllers
CREATE TABLE IF NOT EXISTS controllers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    ip_address TEXT NOT NULL,
    port INTEGER DEFAULT 80,
    mac_address TEXT,
    is_online BOOLEAN DEFAULT 0,
    led_count INTEGER NOT NULL DEFAULT 0,
    config_json TEXT DEFAULT '{"type":"grid","rows":8,"cols":8}', -- Stores the UI configuration (Linear vs Grid vs Compound definitions)
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Bins (Physical Locations)
CREATE TABLE IF NOT EXISTS bins (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL, -- e.g., "A1", "B2"
    controller_id INTEGER,
    led_index INTEGER, -- The specific LED start index (0-based) on the strip
    width INTEGER DEFAULT 1, -- How many LEDs wide is this bin?
    grid_x INTEGER, -- Visual representation X
    grid_y INTEGER, -- Visual representation Y
    FOREIGN KEY(controller_id) REFERENCES controllers(id) ON DELETE CASCADE,
    UNIQUE(controller_id, led_index)
);
CREATE INDEX IF NOT EXISTS idx_bins_controller_id ON bins(controller_id);

-- Parts (Inventory Items)
CREATE TABLE IF NOT EXISTS parts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    description TEXT,
    part_number TEXT,
    manufacturer TEXT,
    supplier TEXT,
    unit_cost REAL DEFAULT 0.00,
    reorder_level INTEGER DEFAULT 0,
    min_stock_threshold INTEGER DEFAULT 0,
    barcode_data TEXT UNIQUE, -- Unique constraint ensures no part barcode overlap
    image_path TEXT, -- Local path relative to /app/uploads
    is_favorite BOOLEAN DEFAULT 0,
    tags TEXT, -- Denormalized cache for FTS
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- FTS5 Virtual Table for High-Performance Search
CREATE VIRTUAL TABLE IF NOT EXISTS parts_fts USING fts5(
    name,
    description,
    part_number,
    manufacturer,
    supplier,
    barcode_data,
    tags,
    content='parts',
    content_rowid='id'
);

-- AI/Inspiration (LLM Cache)
CREATE TABLE IF NOT EXISTS part_ai_prompts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    part_id INTEGER NOT NULL,
    prompt_text TEXT NOT NULL,
    ai_response TEXT,
    model_used TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(part_id) REFERENCES parts(id) ON DELETE CASCADE
);

-- External Links
CREATE TABLE IF NOT EXISTS part_links (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    part_id INTEGER NOT NULL,
    url TEXT NOT NULL,
    label TEXT,
    FOREIGN KEY(part_id) REFERENCES parts(id) ON DELETE CASCADE
);

-- Part Documents (Datasheets)
CREATE TABLE IF NOT EXISTS part_docs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    part_id INTEGER NOT NULL,
    file_path TEXT NOT NULL,
    file_name TEXT NOT NULL,
    FOREIGN KEY(part_id) REFERENCES parts(id) ON DELETE CASCADE
);

-- Assignments (Many-to-Many: Parts in Bins)
-- This is the Source of truth for part quantity
CREATE TABLE IF NOT EXISTS part_assignments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    part_id INTEGER NOT NULL,
    bin_id INTEGER,
    quantity INTEGER NOT NULL DEFAULT 1 CHECK (quantity >= 0),
    FOREIGN KEY(part_id) REFERENCES parts(id) ON DELETE CASCADE,
    FOREIGN KEY(bin_id) REFERENCES bins(id) ON DELETE SET NULL,
    UNIQUE(part_id, bin_id)
);
CREATE INDEX IF NOT EXISTS idx_part_assignments_bin_id ON part_assignments(bin_id);

-- Tags
CREATE TABLE IF NOT EXISTS tags (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT UNIQUE NOT NULL);
CREATE TABLE IF NOT EXISTS part_tags (
    part_id INTEGER NOT NULL, tag_id INTEGER NOT NULL,
    PRIMARY KEY(part_id, tag_id),
    FOREIGN KEY(part_id) REFERENCES parts(id) ON DELETE CASCADE,
    FOREIGN KEY(tag_id) REFERENCES tags(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_part_tags_tag_id ON part_tags(tag_id);

-- Audit Logs (Business Logic Log)
CREATE TABLE IF NOT EXISTS audit_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER,
    action_type TEXT NOT NULL, -- e.g., 'CREATE', 'UPDATE', 'DELETE', 'ADJUST_STOCK'
    entity_type TEXT NOT NULL, -- e.g., 'PART', 'BIN'
    entity_id INTEGER NOT NULL,
    details TEXT, -- Human readable summary
    old_value JSON, -- Snapshot of data before change
    new_value JSON, -- Snapshot of data after change
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_audit_logs_entity ON audit_logs(entity_type, entity_id);

-- Inspiration Templates
CREATE TABLE IF NOT EXISTS inspiration_templates (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL,
    template_content TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Seed Default Templates
INSERT OR IGNORE INTO inspiration_templates (id, title, template_content) VALUES 
(1, 'Project Ideas', 'Based on the following list of electronic components, suggest 5 creative project ideas I could build. For each project, list the key components from the list that would be used and briefly explain how it works.\n\nInventory:\n'),
(2, 'Use Together', 'I have the following components in my inventory. Please identify which of these parts commonly work well together or are often used in the same circuits. Provide 3 examples of sub-circuits or modules I could form with them.\n\nInventory:\n'),
(3, 'Learning Path', 'Analyze the following list of components and suggest a \"Learning Path\". What fundamental electronics or programming concepts can I learn by experimenting with these specific parts? Group the concepts by difficulty level (Beginner, Intermediate, Advanced).\n\nInventory:\n'),
(4, 'Datasheet Summary', 'For the integrated circuits and sensors in the following list, provide a very brief \"Datasheet Summary\". Include the operating voltage, communication protocol (if any, like I2C/SPI), and one key feature for each.\n\nInventory:\n');