-- +goose Up
-- An administrator must explicitly attest that physical outputs are wired in
-- continuous WLED logical order before physical drawer allocations can activate.
ALTER TABLE controllers
    ADD COLUMN locate_mapping_verified BOOLEAN NOT NULL DEFAULT 0;
ALTER TABLE controllers
    ADD COLUMN locate_mapping_fingerprint TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE controllers DROP COLUMN locate_mapping_fingerprint;
ALTER TABLE controllers DROP COLUMN locate_mapping_verified;
