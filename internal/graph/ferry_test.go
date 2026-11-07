package graph

import (
	"path/filepath"
	"testing"
)

// ferryLine is 0 -- 1 -- 2 with the middle hop being a ferry crossing.
func ferryLine(t *testing.T) *Graph {
	t.Helper()
	b := &Builder{
		Lat:   []int32{-62000000, -62000000, -62000000},
		Lon:   []int32{106800000, 106810000, 106830000},
		Off:   []uint32{0, 1, 2, 3},
		To:    []uint32{1, 2, 1},
		Len:   []uint16{250, 500, 500}, // 1 km, 2 km, 2 km
		Cls:   []uint8{60, 26, 26},     // road 30 km/h, ferry 13 km/h both ways
		Ferry: []uint32{1, 2},
	}
	b.BuildGrid(0.02)
	path := filepath.Join(t.TempDir(), "ferry.graph")
	if err := b.Write(path); err != nil {
		t.Fatalf("write: %v", err)
	}
	g, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { g.Close() })
	return g
}

func TestFerryFlagRoundTrip(t *testing.T) {
	g := ferryLine(t)
	if g.IsFerry(0) {
		t.Errorf("edge 0 should be a road")
	}
	for _, e := range []uint32{1, 2} {
		if !g.IsFerry(e) {
			t.Errorf("edge %d should be a ferry", e)
		}
	}
}

func TestFerryRouteSpans(t *testing.T) {
	g := ferryLine(t)
	r := NewRouter(g, 1)
	res, err := r.Route(0, 2)
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if res.DistanceM != 3000 {
		t.Errorf("distance = %d, want 3000", res.DistanceM)
	}
	if res.FerryM != 2000 {
		t.Errorf("ferry metres = %d, want 2000", res.FerryM)
	}
	if len(res.FerrySpans) != 1 || res.FerrySpans[0] != [2]uint32{1, 2} {
		t.Errorf("ferry spans = %v, want [[1 2]]", res.FerrySpans)
	}

	// a land-only route must not report a crossing
	res, err = r.Route(1, 1)
	if err != nil {
		t.Fatalf("self route: %v", err)
	}
	if res.FerryM != 0 || len(res.FerrySpans) != 0 {
		t.Errorf("land route reported ferry: %+v", res)
	}
}

func TestLenAndClassDeriveTime(t *testing.T) {
	g := ferryLine(t)
	// edge 0: 250 * 4 m = 1 km at 30 km/h -> 120 s = 1200 ds
	if got := g.EdgeTimeDs(0); got < 1199 || got > 1201 {
		t.Errorf("edge 0 time = %.1f ds, want ~1200", got)
	}
	// edge 1: 500 * 4 m = 2 km at 13 km/h -> 553.8 s = 5538 ds
	if got := g.EdgeTimeDs(1); got < 5530 || got > 5550 {
		t.Errorf("edge 1 time = %.1f ds, want ~5538", got)
	}
	if got := g.EdgeLenM(1); got != 2000 {
		t.Errorf("edge 1 length = %d, want 2000", got)
	}
}
