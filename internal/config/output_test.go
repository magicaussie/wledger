package config

import (
	"os"
	"testing"
)

func TestPhysicalWLEDOutputEnabledIsExplicitOptIn(t *testing.T) {
	t.Run("unset", func(t *testing.T) {
		t.Setenv("WLEDGER_PHYSICAL_OUTPUT_ENABLED", "true")
		if err := os.Unsetenv("WLEDGER_PHYSICAL_OUTPUT_ENABLED"); err != nil {
			t.Fatal(err)
		}
		if PhysicalWLEDOutputEnabled() {
			t.Fatal("unset setting unexpectedly enabled physical output")
		}
	})
	for _, value := range []string{"", "false", "0", "no", "off", "unexpected"} {
		t.Run("disabled_"+value, func(t *testing.T) {
			t.Setenv("WLEDGER_PHYSICAL_OUTPUT_ENABLED", value)
			if PhysicalWLEDOutputEnabled() {
				t.Fatalf("value %q unexpectedly enabled physical output", value)
			}
		})
	}
	for _, value := range []string{"1", "true", "TRUE", " yes ", "On"} {
		t.Run("enabled_"+value, func(t *testing.T) {
			t.Setenv("WLEDGER_PHYSICAL_OUTPUT_ENABLED", value)
			if !PhysicalWLEDOutputEnabled() {
				t.Fatalf("value %q did not enable physical output", value)
			}
		})
	}
}
