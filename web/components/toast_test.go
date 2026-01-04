package components

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestToast(t *testing.T) {
	props := ToastProps{
		Message: "Test message",
		Type:    ToastSuccess,
	}

	component := Toast(props)
	var buf bytes.Buffer
	err := component.Render(context.Background(), &buf)
	if err != nil {
		t.Fatalf("failed to render toast: %v", err)
	}

	output := buf.String()

	if !strings.Contains(output, "Test message") {
		t.Errorf("output does not contain message: %s", output)
	}

	if !strings.Contains(output, "alert-success") {
		t.Errorf("output does not contain alert-success class: %s", output)
	}

	if !strings.Contains(output, "hx-swap-oob=\"beforeend\"") {
		t.Errorf("output does not contain hx-swap-oob: %s", output)
	}
}
