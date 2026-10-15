package cabinetpixels

import (
	"errors"
	"reflect"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

func TestSplitCabinetRangeAcrossControllers(t *testing.T) {
	spans, total, err := BuildSpans([]Capacity{
		{ControllerID: 3, Order: 2, Pixels: 200},
		{ControllerID: 1, Order: 0, Pixels: 200},
		{ControllerID: 2, Order: 1, Pixels: 250},
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != 650 {
		t.Fatalf("total=%d, want 650", total)
	}
	got, err := SplitRange(225, 100, spans)
	if err != nil {
		t.Fatal(err)
	}
	want := []LocalRange{{ControllerID: 2, Start: 25, Count: 100}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pieces=%+v, want %+v", got, want)
	}
	got, err = SplitRange(151, 150, spans)
	if err != nil {
		t.Fatal(err)
	}
	want = []LocalRange{{ControllerID: 1, Start: 151, Count: 50}, {ControllerID: 2, Start: 1, Count: 100}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cross-controller pieces=%+v, want %+v", got, want)
	}
}

func TestSplitTopologyRangeAppliesOutputWiringPatterns(t *testing.T) {
	makeTopology := func(pattern, direction string, rows, columns int64, custom []int64) Topology {
		return Topology{Total: 6, Controllers: []Controller{{
			Record: db.Controller{ID: 1},
			Span:   Span{ControllerID: 1, Order: 0, Start: 1, Pixels: 6},
			Strings: []String{{
				Record:          db.CabinetLedString{ID: 9, ControllerID: 1, PixelCount: 6},
				ControllerStart: 1, CabinetStart: 1, Direction: direction, Pattern: pattern,
				Rows: rows, Columns: columns, CustomOrder: custom,
			}},
		}}}
	}
	cases := []struct {
		name        string
		topology    Topology
		start       int64
		count       int64
		localStarts []int64
	}{
		{name: "serpentine rows", topology: makeTopology("serpentine_rows", "forward", 2, 3, nil), start: 3, count: 2, localStarts: []int64{3, 6}},
		{name: "serpentine columns", topology: makeTopology("serpentine_columns", "forward", 2, 3, nil), start: 2, count: 3, localStarts: []int64{4, 5, 2}},
		{name: "custom order", topology: makeTopology("custom", "forward", 1, 1, []int64{0, 2, 1, 3, 5, 4}), start: 2, count: 2, localStarts: []int64{3, 2}},
		{name: "reversed serpentine rows", topology: makeTopology("serpentine_rows", "reverse", 2, 3, nil), start: 3, count: 2, localStarts: []int64{4, 1}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			pieces, err := SplitTopologyRange(test.topology, test.start, test.count)
			if err != nil {
				t.Fatal(err)
			}
			var actual []int64
			for _, piece := range pieces {
				for pixel := int64(0); pixel < piece.Count; pixel++ {
					actual = append(actual, piece.LocalStart+pixel)
				}
			}
			if !reflect.DeepEqual(actual, test.localStarts) {
				t.Errorf("mapped pixels = %v (pieces %+v), want %v", actual, pieces, test.localStarts)
			}
		})
	}
	invalidCustom := makeTopology("custom", "forward", 1, 1, []int64{0, 0, 2, 3, 4, 5})
	if _, err := SplitTopologyRange(invalidCustom, 1, 1); !errors.Is(err, ErrInvalidTopology) {
		t.Fatalf("duplicate custom pixel mapping error = %v, want invalid topology", err)
	}
}

func TestSplitCabinetRangeRejectsInvalidBoundsAndTopology(t *testing.T) {
	spans, _, err := BuildSpans([]Capacity{{ControllerID: 1, Order: 0, Pixels: 100}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ start, count int64 }{{0, 1}, {1, 0}, {100, 2}} {
		if _, err := SplitRange(tc.start, tc.count, spans); !errors.Is(err, ErrRangeOutOfBounds) {
			t.Errorf("SplitRange(%d,%d)=%v, want bounds error", tc.start, tc.count, err)
		}
	}
	if _, _, err := BuildSpans([]Capacity{{ControllerID: 1, Order: 0, Pixels: 10}, {ControllerID: 2, Order: 0, Pixels: 10}}); !errors.Is(err, ErrInvalidTopology) {
		t.Fatalf("duplicate order topology error=%v", err)
	}
}
