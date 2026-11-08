package graph

import "testing"

// spurGraph builds a main line at -6.21 together with an isolated spur closer
// to the query point, so the nearest segment is a dead end and the next ones are
// usable roads.
func spurGraph(t *testing.T) *Graph {
	t.Helper()
	b := &Builder{
		Lat: []int32{-62100000, -62100000, -62100000, -62000000, -62005000, -62100000},
		Lon: []int32{1068000000, 1068100000, 1068200000, 1068050000, 1068050000, 1068300000},
		Off: []uint32{0, 1, 2, 4, 4, 5, 6},
		To:  []uint32{1, 2, 1, 5, 3, 2},
		Len: []uint16{1100, 1100, 1100, 60, 1100, 1100},
		Cls: []uint8{60, 60, 60, 60, 60, 60},
	}
	b.BuildGrid(0.02)
	path := t.TempDir() + "/spur.graph"
	if err := b.Write(path); err != nil {
		t.Fatal(err)
	}
	g, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	return g
}

const spurQueryLat, spurQueryLon = -6.20025, 106.80500

// TestSnapEdgesOrder checks that candidates come back nearest first, one per
// node, that k truncates the tail, and that k=1 keeps the single-snap answer.
func TestSnapEdgesOrder(t *testing.T) {
	g := spurGraph(t)

	cands := g.SnapEdges(spurQueryLat, spurQueryLon, 3000, 3)
	if len(cands) != 3 {
		t.Fatalf("candidates = %d, want 3: %+v", len(cands), cands)
	}
	// The spur projects to ~0 m and is the dead end; the line 1.1 km away is
	// the fallback a failed route needs.
	if cands[0].Dist > 1 || cands[0].Node != 3 && cands[0].Node != 4 {
		t.Fatalf("nearest = node %d at %.1f m, want the spur at ~0 m", cands[0].Node, cands[0].Dist)
	}
	for i := 1; i < len(cands); i++ {
		if cands[i].Dist <= cands[i-1].Dist {
			t.Fatalf("candidate %d at %.1f m not farther than %.1f m", i, cands[i].Dist, cands[i-1].Dist)
		}
	}
	if cands[1].Dist < 1000 || cands[1].Dist > 1300 {
		t.Fatalf("second candidate at %.1f m, want the main line at ~1.1 km", cands[1].Dist)
	}
	seen := map[uint32]bool{}
	for _, s := range cands {
		if seen[s.Node] {
			t.Fatalf("node %d repeated", s.Node)
		}
		seen[s.Node] = true
	}

	if got := g.SnapEdges(spurQueryLat, spurQueryLon, 3000, 2); len(got) != 2 || got[1] != cands[1] {
		t.Fatalf("k=2 = %+v, want the first two of %+v", got, cands)
	}

	single, ok := g.SnapEdge(spurQueryLat, spurQueryLon, 3000)
	if !ok {
		t.Fatal("SnapEdge found nothing")
	}
	one := g.SnapEdges(spurQueryLat, spurQueryLon, 3000, 1)
	if len(one) != 1 || one[0] != single {
		t.Fatalf("SnapEdges(k=1) = %v, want the SnapEdge result %v", one, single)
	}
}

// TestSnapEdgesRadius checks the maxMeters cut and the empty answer beyond it.
func TestSnapEdgesRadius(t *testing.T) {
	g := spurGraph(t)

	if got := g.SnapEdges(spurQueryLat, spurQueryLon, 500, 4); len(got) != 1 {
		t.Fatalf("candidates within 500 m = %d, want 1", len(got))
	}
	if _, ok := g.SnapEdge(-6.2, 107.5, 500); ok {
		t.Fatal("snap far from the graph found a segment")
	}
}
