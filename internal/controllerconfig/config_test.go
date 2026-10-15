package controllerconfig

import "testing"

func TestSettingsNormalizesOutputsAndCalculatesTotal(t *testing.T) {
	settings := Settings{
		ControllerType:      "wled-esp32",
		ConnectionTimeoutMS: 5000,
		Outputs: []Output{
			{DisplayOrder: 1, Name: "Second", PixelCount: 80, Direction: "reverse", Pattern: "serpentine_rows", Rows: 4, Columns: 20},
			{DisplayOrder: 0, Name: " First ", PixelCount: 64, Direction: "forward", Pattern: "linear"},
		},
	}
	total, err := settings.NormalizeAndValidate()
	if err != nil {
		t.Fatal(err)
	}
	if total != 144 || settings.Outputs[0].Name != "First" || settings.Outputs[0].DisplayOrder != 0 || settings.Outputs[1].DisplayOrder != 1 {
		t.Fatalf("normalized settings=%+v total=%d", settings, total)
	}
}

func TestSettingsRejectsUnsupportedControllerCapabilitiesAndPatterns(t *testing.T) {
	cases := []Settings{
		{ControllerType: "not-wled"},
		{ControllerType: "wled-esp8266", Outputs: []Output{{DisplayOrder: 0, Name: "One", PixelCount: 10}, {DisplayOrder: 1, Name: "Two", PixelCount: 10}}},
		{Outputs: []Output{{DisplayOrder: 0, Name: "Bad rows", PixelCount: 12, Pattern: "serpentine_rows", Rows: 2, Columns: 5}}},
		{Outputs: []Output{{DisplayOrder: 0, Name: "Bad custom", PixelCount: 3, Pattern: "custom", CustomOrder: []int64{0, 0, 2}}}},
		{ConnectionTimeoutMS: 99},
		{Outputs: []Output{{DisplayOrder: 0, Name: "Too large", PixelCount: MaxPixelsPerOutput + 1}}},
	}
	for i, settings := range cases {
		if _, err := settings.NormalizeAndValidate(); err != ErrInvalidConfiguration {
			t.Errorf("case %d error=%v, want ErrInvalidConfiguration", i, err)
		}
	}
}
