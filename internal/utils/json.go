package utils

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// JSON returns a JSON string representation of v
// It suppresses errors for use in templates (returns "{}" on failure)
func JSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		fmt.Printf("failed to marshal json in template: %v\n", err)
		return "{}"
	}
	return string(b)
}

// DefaultJSON safely returns the string value from a NullString,
// or the provided default if the value is invalid/empty.
func DefaultJSON(ns sql.NullString, defaultValue string) string {
	if !ns.Valid || ns.String == "" {
		return defaultValue
	}
	return ns.String
}
