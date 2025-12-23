-- +goose Up
ALTER TABLE settings ADD COLUMN inspiration_seeds_applied BOOLEAN DEFAULT 0;
