package pages

import (
	"encoding/json"

	"github.com/tuxedocurly/wledger/internal/controllerconfig"
)

func controllerOutputsJSON(outputs []controllerconfig.Output) string {
	if outputs == nil {
		outputs = []controllerconfig.Output{}
	}
	encoded, err := json.Marshal(outputs)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}
