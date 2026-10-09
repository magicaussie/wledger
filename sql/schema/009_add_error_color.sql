-- +goose Up
ALTER TABLE settings ADD COLUMN color_error TEXT DEFAULT '#FF0000';
