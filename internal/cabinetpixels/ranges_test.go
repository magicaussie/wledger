package cabinetpixels

import (
	"errors"
	"reflect"
	"sort"
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

func TestSplitTopologyRangeAppliesEveryWiringPatternToEveryExactRange(t *testing.T) {
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
		name     string
		topology Topology
		mapping  []int64
	}{
		{name: "left-to-right", topology: makeTopology("linear", "forward", 1, 1, nil), mapping: []int64{1, 2, 3, 4, 5, 6}},
		{name: "right-to-left", topology: makeTopology("linear", "reverse", 1, 1, nil), mapping: []int64{6, 5, 4, 3, 2, 1}},
		{name: "serpentine rows", topology: makeTopology("serpentine_rows", "forward", 2, 3, nil), mapping: []int64{1, 2, 3, 6, 5, 4}},
		{name: "reversed serpentine rows", topology: makeTopology("serpentine_rows", "reverse", 2, 3, nil), mapping: []int64{6, 5, 4, 1, 2, 3}},
		{name: "serpentine columns", topology: makeTopology("serpentine_columns", "forward", 2, 3, nil), mapping: []int64{1, 4, 5, 2, 3, 6}},
		{name: "reversed serpentine columns", topology: makeTopology("serpentine_columns", "reverse", 2, 3, nil), mapping: []int64{6, 3, 2, 5, 4, 1}},
		{name: "top-to-bottom columns", topology: makeTopology("custom", "forward", 1, 1, []int64{0, 2, 4, 1, 3, 5}), mapping: []int64{1, 3, 5, 2, 4, 6}},
		{name: "bottom-to-top columns", topology: makeTopology("custom", "forward", 1, 1, []int64{1, 3, 5, 0, 2, 4}), mapping: []int64{2, 4, 6, 1, 3, 5}},
		{name: "independently reversed rows", topology: makeTopology("custom", "forward", 1, 1, []int64{2, 1, 0, 3, 4, 5}), mapping: []int64{3, 2, 1, 4, 5, 6}},
		{name: "mixed custom directions", topology: makeTopology("custom", "forward", 1, 1, []int64{0, 3, 1, 4, 2, 5}), mapping: []int64{1, 4, 2, 5, 3, 6}},
		{name: "custom permutation plus reverse", topology: makeTopology("custom", "reverse", 1, 1, []int64{0, 2, 1, 3, 5, 4}), mapping: []int64{6, 4, 5, 3, 1, 2}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			for start := int64(1); start <= 6; start++ {
				for count := int64(1); count <= 7-start; count++ {
					pieces, err := SplitTopologyRange(test.topology, start, count)
					if err != nil {
						t.Fatalf("range %d+%d: %v", start, count, err)
					}
					var actual []int64
					for _, piece := range pieces {
						for pixel := int64(0); pixel < piece.Count; pixel++ {
							actual = append(actual, piece.LocalStart+pixel)
						}
					}
					want := append([]int64(nil), test.mapping[start-1:start+count-1]...)
					sort.Slice(actual, func(i, j int) bool { return actual[i] < actual[j] })
					sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
					if !reflect.DeepEqual(actual, want) {
						t.Errorf("range %d+%d mapped pixels = %v (pieces %+v), want %v", start, count, actual, pieces, want)
					}
					outputPixels, err := MapOutputRange(outputSettings(test.topology.Controllers[0].Strings[0]), start-1, count)
					if err != nil {
						t.Fatalf("canonical output map range %d+%d: %v", start, count, err)
					}
					wantOutputPixels := append([]int64(nil), test.mapping[start-1:start+count-1]...)
					for index := range wantOutputPixels {
						wantOutputPixels[index]--
					}
					if !reflect.DeepEqual(outputPixels, wantOutputPixels) {
						t.Errorf("canonical output map range %d+%d = %v, want zero-based %v", start, count, outputPixels, wantOutputPixels)
					}
				}
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
