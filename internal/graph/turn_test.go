package graph

import (
	"path/filepath"
	"testing"
)

// tinyGraph builds:
//
//	0 --A-- 1 --B-- 2        direct chain
//	1 --C-- 3 --D-- 0        detour back to 0
//
// and forbids the turn (2->1) then (1->0). Without the restriction the shortest
// path 2->0 is 2->1->0; with it the router must use 2->1->3->0.
func tinyGraph(t *testing.T, withRestriction bool) *Graph {
	t.Helper()
	b := &Builder{
		Lat: []int32{-70000000, -70000000, -70000000, -70010000},
		Lon: []int32{106000000, 106001000, 106002000, 106001000},
		Off: []uint32{0, 1, 3, 4, 5},
		To:  []uint32{1, 0, 2, 1, 0},
		Len: []uint16{25, 25, 25, 25, 35}, // LenUnit metres
		Cls: []uint8{72, 72, 72, 72, 72},  // 36 km/h
	}
	// CSR by from-node:
	//   edge0 0->1, edge1 1->0, edge2 1->3, edge3 2->1, edge4 3->0
	b.Off = []uint32{0, 1, 3, 4, 5}
	b.To = []uint32{1, 0, 3, 1, 0}
	b.BuildGrid(0.02)
	if withRestriction {
		b.Ban = SortUniquePacked([]uint64{PackTurn(3, 1)})
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "tiny.graph")
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

func TestTurnPacking(t *testing.T) {
	k := PackTurn(7, 9)
	if f, to := UnpackTurn(k); f != 7 || to != 9 {
		t.Fatalf("round trip: got %d,%d", f, to)
	}
	if got := SortUniquePacked([]uint64{PackTurn(2, 2), PackTurn(1, 1), PackTurn(2, 2)}); len(got) != 2 {
		t.Fatalf("dedupe: got %v", got)
	}
}

func TestRestrictionRoundTrip(t *testing.T) {
	g := tinyGraph(t, true)
	if len(g.Ban) != 1 {
		t.Fatalf("ban count = %d, want 1", len(g.Ban))
	}
	if !g.Banned(3, 1) {
		t.Fatal("expected transition (2->1)->(1->0) to be banned")
	}
	if g.Banned(3, 2) || g.Banned(0, 1) {
		t.Fatal("unexpected ban")
	}
	if !g.Forbidden(3, 1) || g.Forbidden(3, 2) {
		t.Fatal("Forbidden mismatch")
	}
}

func TestOnlyRule(t *testing.T) {
	b := &Builder{
		Lat:  []int32{-70000000, -70000000, -70000000},
		Lon:  []int32{106000000, 106001000, 106002000},
		Off:  []uint32{0, 1, 3},
		To:   []uint32{1, 0, 2},
		Len:  []uint16{25, 25, 25}, // LenUnit metres
		Cls:  []uint8{72, 72, 72},  // 36 km/h
		Only: SortUniquePacked([]uint64{PackTurn(0, 1)}),
	}
	b.BuildGrid(0.02)
	dir := t.TempDir()
	path := filepath.Join(dir, "only.graph")
	if err := b.Write(path); err != nil {
		t.Fatalf("write: %v", err)
	}
	g, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer g.Close()

	if g.OnlyBlocked(0, 1) {
		t.Fatal("edge 0->1 is the permitted successor, must not be blocked")
	}
	if !g.OnlyBlocked(0, 0) {
		t.Fatal("every other successor must be blocked by an only_* rule")
	}
	if g.OnlyBlocked(1, 0) {
		t.Fatal("edge without an only_* rule must be unrestricted")
	}
}

func TestRouterHonoursRestriction(t *testing.T) {
	loose := tinyGraph(t, false)
	r := NewRouter(loose, 1)
	res, err := r.Route(2, 0)
	if err != nil {
		t.Fatalf("loose route: %v", err)
	}
	if got := res.DistanceM; got != 200 {
		t.Fatalf("loose distance = %d, want 200 (2->1->0)", got)
	}

	strict := tinyGraph(t, true)
	r2 := NewRouter(strict, 1)
	res2, err := r2.Route(2, 0)
	if err != nil {
		t.Fatalf("strict route: %v", err)
	}
	if res2.Fallback {
		t.Fatal("strict route must not need the unrestricted fallback")
	}
	if got := res2.DistanceM; got != 340 {
		t.Fatalf("strict distance = %d, want 340 (2->1->3->0 detour)", got)
	}
	want := []uint32{2, 1, 3, 0}
	if len(res2.Nodes) != len(want) {
		t.Fatalf("strict nodes = %v, want %v", res2.Nodes, want)
	}
	for i := range want {
		if res2.Nodes[i] != want[i] {
			t.Fatalf("strict nodes = %v, want %v", res2.Nodes, want)
		}
	}
}
