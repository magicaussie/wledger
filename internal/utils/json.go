package utils

import (
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
