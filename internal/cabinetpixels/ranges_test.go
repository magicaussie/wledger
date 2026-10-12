package cabinetpixels

import (
	"errors"
	"reflect"
	"testing"
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
