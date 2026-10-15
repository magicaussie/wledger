-- +goose Up
ALTER TABLE controllers ADD COLUMN controller_type TEXT NOT NULL DEFAULT 'wled'
    CHECK (controller_type IN ('wled', 'wled-esp8266', 'wled-esp32', 'wled-compatible'));
ALTER TABLE controllers ADD COLUMN connection_timeout_ms INTEGER NOT NULL DEFAULT 2000
    CHECK (connection_timeout_ms BETWEEN 100 AND 30000);

CREATE TABLE controller_outputs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    controller_id INTEGER NOT NULL,
    display_order INTEGER NOT NULL CHECK (display_order >= 0),
    name TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 120),
    pixel_count INTEGER NOT NULL CHECK (pixel_count >= 1),
    direction TEXT NOT NULL DEFAULT 'forward' CHECK (direction IN ('forward', 'reverse')),
    pattern TEXT NOT NULL DEFAULT 'linear' CHECK (pattern IN ('linear', 'serpentine_rows', 'serpentine_columns', 'custom')),
    rows INTEGER NOT NULL DEFAULT 1 CHECK (rows >= 1),
    columns INTEGER NOT NULL DEFAULT 1 CHECK (columns >= 1),
    custom_order_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(custom_order_json)),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (controller_id, display_order),
    UNIQUE (id, controller_id),
    FOREIGN KEY (controller_id) REFERENCES controllers(id) ON DELETE CASCADE
);
CREATE INDEX idx_controller_outputs_controller_order
    ON controller_outputs(controller_id, display_order, id);

-- Existing cabinet strings already contain per-controller output order and
-- capacity. Promote those settings while leaving their cabinet path geometry in
-- place as a separate physical-layout projection.
INSERT INTO controller_outputs (controller_id, display_order, name, pixel_count)
SELECT controller_id, display_order, name, pixel_count
FROM cabinet_led_strings
ORDER BY controller_id, display_order, id;

-- Preserve legacy controller capacities when they have no cabinet string rows.
INSERT INTO controller_outputs (controller_id, display_order, name, pixel_count)
SELECT c.id, 0, 'Output 1', c.led_count
FROM controllers c
WHERE c.led_count > 0
  AND NOT EXISTS (SELECT 1 FROM controller_outputs o WHERE o.controller_id = c.id);

ALTER TABLE cabinet_led_strings
    ADD COLUMN controller_output_id INTEGER REFERENCES controller_outputs(id) ON DELETE RESTRICT;
UPDATE cabinet_led_strings
SET controller_output_id = (
    SELECT o.id FROM controller_outputs o
    WHERE o.controller_id = cabinet_led_strings.controller_id
      AND o.display_order = cabinet_led_strings.display_order
);
UPDATE cabinet_controllers
SET strings_configured = 1
WHERE EXISTS (
    SELECT 1 FROM cabinet_led_strings s
    WHERE s.cabinet_id = cabinet_controllers.cabinet_id
      AND s.controller_id = cabinet_controllers.controller_id
);
CREATE UNIQUE INDEX idx_cabinet_led_strings_output
    ON cabinet_led_strings(controller_output_id)
    WHERE controller_output_id IS NOT NULL;

-- +goose StatementBegin
CREATE TRIGGER cabinet_led_strings_output_insert
BEFORE INSERT ON cabinet_led_strings
WHEN NEW.controller_output_id IS NOT NULL
 AND NOT EXISTS (
     SELECT 1 FROM controller_outputs o
     WHERE o.id = NEW.controller_output_id AND o.controller_id = NEW.controller_id
 )
BEGIN
    SELECT RAISE(ABORT, 'cabinet string output must belong to its controller');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER cabinet_led_strings_output_update
BEFORE UPDATE OF controller_id, controller_output_id ON cabinet_led_strings
WHEN NEW.controller_output_id IS NOT NULL
 AND NOT EXISTS (
     SELECT 1 FROM controller_outputs o
     WHERE o.id = NEW.controller_output_id AND o.controller_id = NEW.controller_id
 )
BEGIN
    SELECT RAISE(ABORT, 'cabinet string output must belong to its controller');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER cabinet_led_strings_mark_configured
AFTER INSERT ON cabinet_led_strings
BEGIN
    UPDATE cabinet_controllers SET strings_configured = 1
    WHERE cabinet_id = NEW.cabinet_id AND controller_id = NEW.controller_id;
END;
-- +goose StatementEnd

-- +goose Down
CREATE TEMP TABLE _controller_outputs_rollback_guard (ok INTEGER NOT NULL CHECK (ok = 0));
INSERT INTO _controller_outputs_rollback_guard (ok)
SELECT 1
WHERE EXISTS (
    SELECT 1 FROM controllers
    WHERE controller_type <> 'wled' OR connection_timeout_ms <> 2000
)
OR EXISTS (
    SELECT 1
    FROM controller_outputs o
    JOIN controllers c ON c.id = o.controller_id
    WHERE (o.direction <> 'forward' OR o.pattern <> 'linear' OR o.rows <> 1 OR o.columns <> 1 OR o.custom_order_json <> '[]')
       OR (
           NOT EXISTS (
               SELECT 1 FROM cabinet_led_strings s
               WHERE s.controller_output_id = o.id
                 AND s.controller_id = o.controller_id
                 AND s.display_order = o.display_order
                 AND s.name = o.name
                 AND s.pixel_count = o.pixel_count
           ) AND NOT (
               o.display_order = 0 AND o.name = 'Output 1' AND o.pixel_count = c.led_count
               AND NOT EXISTS (SELECT 1 FROM cabinet_led_strings s WHERE s.controller_id = o.controller_id)
           )
       )
);
DROP TABLE _controller_outputs_rollback_guard;
DROP TRIGGER cabinet_led_strings_mark_configured;
DROP TRIGGER cabinet_led_strings_output_update;
DROP TRIGGER cabinet_led_strings_output_insert;
DROP INDEX idx_cabinet_led_strings_output;
ALTER TABLE cabinet_led_strings DROP COLUMN controller_output_id;
DROP TABLE controller_outputs;
ALTER TABLE controllers DROP COLUMN connection_timeout_ms;
ALTER TABLE controllers DROP COLUMN controller_type;
