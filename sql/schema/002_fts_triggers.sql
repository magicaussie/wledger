-- Drop existing triggers to ensure clean slate on restart/update
DROP TRIGGER IF EXISTS parts_ai;
DROP TRIGGER IF EXISTS parts_ad;
DROP TRIGGER IF EXISTS parts_au;
DROP TRIGGER IF EXISTS part_tags_ai;
DROP TRIGGER IF EXISTS part_tags_ad;
DROP TRIGGER IF EXISTS tags_au;

-- Parts Triggers (FTS Sync)
CREATE TRIGGER parts_ai AFTER INSERT ON parts BEGIN
  INSERT INTO parts_fts(rowid, name, description, part_number, manufacturer, supplier, barcode_data, tags) 
  VALUES (new.id, new.name, new.description, new.part_number, new.manufacturer, new.supplier, new.barcode_data, new.tags);
END;

CREATE TRIGGER parts_ad AFTER DELETE ON parts BEGIN
  INSERT INTO parts_fts(parts_fts, rowid, name, description, part_number, manufacturer, supplier, barcode_data, tags) 
  VALUES('delete', old.id, old.name, old.description, old.part_number, old.manufacturer, old.supplier, old.barcode_data, old.tags);
END;

CREATE TRIGGER parts_au AFTER UPDATE ON parts BEGIN
  INSERT INTO parts_fts(parts_fts, rowid, name, description, part_number, manufacturer, supplier, barcode_data, tags) 
  VALUES('delete', old.id, old.name, old.description, old.part_number, old.manufacturer, old.supplier, old.barcode_data, old.tags);
  INSERT INTO parts_fts(rowid, name, description, part_number, manufacturer, supplier, barcode_data, tags) 
  VALUES (new.id, new.name, new.description, new.part_number, new.manufacturer, new.supplier, new.barcode_data, new.tags);
END;

-- Cache Maintenance Triggers - Part Tags & Tags

CREATE TRIGGER part_tags_ai AFTER INSERT ON part_tags BEGIN
    UPDATE parts 
    SET tags = (
        SELECT GROUP_CONCAT(t.name, ', ') 
        FROM tags t 
        JOIN part_tags pt ON t.id = pt.tag_id 
        WHERE pt.part_id = new.part_id
    )
    WHERE id = new.part_id;
END;

CREATE TRIGGER part_tags_ad AFTER DELETE ON part_tags BEGIN
    UPDATE parts 
    SET tags = (
        SELECT GROUP_CONCAT(t.name, ', ') 
        FROM tags t 
        JOIN part_tags pt ON t.id = pt.tag_id 
        WHERE pt.part_id = old.part_id
    )
    WHERE id = old.part_id;
END;

CREATE TRIGGER tags_au AFTER UPDATE ON tags BEGIN
    UPDATE parts 
    SET tags = (
        SELECT GROUP_CONCAT(t.name, ', ') 
        FROM tags t 
        JOIN part_tags pt ON t.id = pt.tag_id 
        WHERE pt.part_id = parts.id
    )
    WHERE id IN (SELECT part_id FROM part_tags WHERE tag_id = new.id);
END;
