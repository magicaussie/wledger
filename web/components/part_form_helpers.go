package components

import (
	"database/sql"
	"strconv"

	"github.com/tuxedocurly/wledger/internal/categories"
)

func floatInput(value sql.NullFloat64) string {
	if !value.Valid { return "" }
	return strconv.FormatFloat(value.Float64, 'f', -1, 64)
}

func attributeInputValue(values []categories.AttributeValue, id int64) string {
	for _, value := range values {
		if value.Attribute.ID == id {
			return value.InputValue()
		}
	}
	return ""
}
