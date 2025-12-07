-- Drop existing triggers to ensure clean slate on restart/update
DROP TRIGGER IF EXISTS parts_ai;
DROP TRIGGER IF EXISTS parts_ad;
DROP TRIGGER IF EXISTS parts_au;

CREATE TRIGGER parts_ai AFTER INSERT ON parts BEGIN
  INSERT INTO parts_fts(rowid, name, description, part_number, manufacturer, supplier, barcode_data) 
  VALUES (new.id, new.name, new.description, new.part_number, new.manufacturer, new.supplier, new.barcode_data);
END;

CREATE TRIGGER parts_ad AFTER DELETE ON parts BEGIN
  INSERT INTO parts_fts(parts_fts, rowid, name, description, part_number, manufacturer, supplier, barcode_data) 
  VALUES('delete', old.id, old.name, old.description, old.part_number, old.manufacturer, old.supplier, old.barcode_data);
END;

CREATE TRIGGER parts_au AFTER UPDATE ON parts BEGIN
  INSERT INTO parts_fts(parts_fts, rowid, name, description, part_number, manufacturer, supplier, barcode_data) 
  VALUES('delete', old.id, old.name, old.description, old.part_number, old.manufacturer, old.supplier, old.barcode_data);
  INSERT INTO parts_fts(rowid, name, description, part_number, manufacturer, supplier, barcode_data) 
  VALUES (new.id, new.name, new.description, new.part_number, new.manufacturer, new.supplier, new.barcode_data);
END;