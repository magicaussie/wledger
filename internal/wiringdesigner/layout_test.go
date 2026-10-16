package wiringdesigner

import "testing"

func TestValidateVariableSerpentineRowsColumnsAndJumper(t *testing.T) {
	pixels := map[int64]int64{11: 8, 12: 8}
	layout := Layout{Version: 1, Revision: 0, Width: 1000, Height: 600, Outputs: []OutputLayout{
		{OutputID: 11, X: 0, Y: 0, Width: 300, Height: 200, Axis: "rows", Rows: []int64{3, 5}, Sections: []int64{4, 4}},
		{OutputID: 12, X: 400, Y: 0, Width: 300, Height: 200, Axis: "columns", Rows: []int64{2, 3, 3}, Sections: []int64{3, 5}},
	}, Jumpers: []Jumper{{ID: "jumper-1", Points: []Point{{X: 300, Y: 100}, {X: 400, Y: 100}}}}}
	if err := Validate(layout, pixels); err != nil {
		t.Fatalf("valid layout rejected: %v", err)
	}
}

func TestValidateRejectsPixelRetargetOutOfBoundsAndInvalidSections(t *testing.T) {
	base := Layout{Version: 1, Width: 100, Height: 100, Outputs: []OutputLayout{{OutputID: 1, X: 0, Y: 0, Width: 20, Height: 20, Axis: "rows", Rows: []int64{2, 2}, Sections: []int64{4}}}}
	if err := Validate(base, map[int64]int64{1: 5}); err == nil {
		t.Fatal("mismatched row pixel total accepted")
	}
	base.Outputs[0].X = 90
	if err := Validate(base, map[int64]int64{1: 4}); err == nil {
		t.Fatal("out-of-bounds output accepted")
	}
	base.Outputs[0].X = 0
	base.Outputs[0].Sections = []int64{2, 1}
	if err := Validate(base, map[int64]int64{1: 4}); err == nil {
		t.Fatal("strip sections that drop pixels accepted")
	}
}

func TestValidateCustomSectionAndPixelGeometry(t *testing.T) {
	base := Layout{Version: 1, Width: 100, Height: 100, Outputs: []OutputLayout{{OutputID: 1, X: 0, Y: 0, Width: 20, Height: 20, Axis: "rows", Rows: []int64{2, 2}, Sections: []int64{4}, SectionLayouts: []SectionLayout{{ID: "section-a", Start: 0, Count: 4, X: 2, Y: 3, Rotation: 90}}, PixelPositions: []PixelPosition{{Index: 0, X: 1, Y: 1}, {Index: 1, X: 2, Y: 1}, {Index: 2, X: 1, Y: 2}, {Index: 3, X: 2, Y: 2}}}}}
	if err := Validate(base, map[int64]int64{1: 4}); err != nil {
		t.Fatalf("valid custom geometry rejected: %v", err)
	}
	base.Outputs[0].PixelPositions[1].Index = 0
	if err := Validate(base, map[int64]int64{1: 4}); err == nil {
		t.Fatal("duplicate pixel reference accepted")
	}
	base.Outputs[0].PixelPositions[1].Index = 1
	base.Outputs[0].SectionLayouts[0].ID = ""
	if err := Validate(base, map[int64]int64{1: 4}); err == nil {
		t.Fatal("empty section identity accepted")
	}
	base.Outputs[0].SectionLayouts = []SectionLayout{{ID: "a", Start: 0, Count: 3}, {ID: "b", Start: 2, Count: 1}}
	if err := Validate(base, map[int64]int64{1: 4}); err == nil {
		t.Fatal("overlapping section intervals accepted")
	}
	base.Outputs[0].SectionLayouts = []SectionLayout{{ID: "a", Start: 0, Count: 4, X: -2, Y: -3, Rotation: -180}}
	base.Outputs[0].PixelPositions = []PixelPosition{{Index: 1, X: 2, Y: 2}}
	if err := Validate(base, map[int64]int64{1: 4}); err != nil {
		t.Fatalf("signed section offsets/rotation or partial pixel overrides rejected: %v", err)
	}
}

func TestValidateRejectsPartialOutputSet(t *testing.T) {
	base := Layout{Version: 1, Width: 100, Height: 100, Outputs: []OutputLayout{{OutputID: 1, Width: 20, Height: 20, Axis: "rows", Rows: []int64{4}, Sections: []int64{4}}}}
	if err := Validate(base, map[int64]int64{1: 4, 2: 4}); err == nil {
		t.Fatal("partial output set accepted")
	}
}

func TestValidateAcceptsBoundedPNGBackground(t *testing.T) {
	layout := Layout{Version: 1, Width: 100, Height: 100, Outputs: []OutputLayout{{OutputID: 1, X: 0, Y: 0, Width: 20, Height: 20, Axis: "rows", Rows: []int64{4}, Sections: []int64{4}}}, Background: &Background{DataURL: "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADUlEQVR4nGP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC", Width: 100, Height: 100, Opacity: .35}}
	if err := Validate(layout, map[int64]int64{1: 4}); err != nil {
		t.Fatalf("valid background rejected: %v", err)
	}
}

func TestValidateRejectsInvalidJumperAndStaleOutput(t *testing.T) {
	base := Layout{Version: 1, Width: 100, Height: 100, Outputs: []OutputLayout{{OutputID: 1, X: 0, Y: 0, Width: 20, Height: 20, Axis: "rows", Rows: []int64{4}, Sections: []int64{4}}}, Jumpers: []Jumper{{ID: "j", Points: []Point{{X: 1, Y: 1}}}}}
	if err := Validate(base, map[int64]int64{1: 4}); err == nil {
		t.Fatal("one-point zero-pixel jumper accepted")
	}
	base.Jumpers = nil
	if err := Validate(base, map[int64]int64{2: 4}); err == nil {
		t.Fatal("unknown output identity accepted")
	}
}
