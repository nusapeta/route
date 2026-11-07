package graph

import (
	"math"
	"testing"
)

// TestWorkspaceGenerationWrap drives the packed key/generation word through more
// queries than it can stamp, so the clear-on-wrap path is exercised: an entry
// written in query N must never be readable in a later query, and the table must
// keep accepting writes after a wrap.
func TestWorkspaceGenerationWrap(t *testing.T) {
	const entries = 4096
	w := newWorkspace(1024, entries)
	t.Logf("nodeBits=%d genBits=%d maxGen=%d", w.nodeBits, w.genBits, w.maxGen)
	if w.nodeBits+w.genBits != 32 {
		t.Fatalf("bit budget %d+%d != 32", w.nodeBits, w.genBits)
	}
	if w.maxGen < 8 {
		t.Fatalf("only %d generations before a full clear", w.maxGen)
	}
	// Three wraps worth of queries.
	for q := 0; q < int(w.maxGen)*3+5; q++ {
		w.reset()
		for i := uint32(0); i < 50; i++ {
			n := (uint32(q)*37 + i*11) % entries
			if !w.set(n, float32(q), n) {
				t.Fatalf("set(%d) failed in query %d", n, q)
			}
		}
		for i := uint32(0); i < 50; i++ {
			n := (uint32(q)*37 + i*11) % entries
			g, came, ok := w.get(n)
			if !ok || g != float32(q) || came != n {
				t.Fatalf("query %d: get(%d) = %v,%v,%v", q, n, g, came, ok)
			}
		}
		// A node never written in this query must read as absent.
		absent := uint32(q)*37 + 1000
		absent %= entries
		if _, _, ok := w.get(absent); ok {
			t.Fatalf("query %d: node %d leaked from an earlier query", q, absent)
		}
	}
}

// TestHeuristicAdmissible checks that the planar estimate never exceeds the
// great-circle one: otherwise weighted A* would lose its admissibility and the
// route distance could regress.
func TestHeuristicAdmissible(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/city.graph"
	if err := cityGraph(t).Write(path); err != nil {
		t.Fatalf("write: %v", err)
	}
	g, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer g.Close()
	r := NewRouter(g, 1)
	r.SetEpsilon(1) // measure the raw heuristic

	var worst float64
	for dst := uint32(0); dst < g.NodeCount; dst += 7 {
		h := r.newHctx(dst)
		for n := uint32(0); n < g.NodeCount; n += 11 {
			lat, lon := g.Coords(n)
			got := float64(h.est(lat, lon))
			tlat, tlon := g.Coords(dst)
			want := Haversine(lat, lon, tlat, tlon) / maxSpeedMps * 10
			if got > want {
				worst = math.Max(worst, got-want)
				t.Fatalf("overestimate at node %d->%d: got %.6f want %.6f", n, dst, got, want)
			}
		}
	}
	// Country-scale separation: the flat-earth error grows quadratically with the
	// angular separation, so check a 10-degree span independently of the graph.
	h := r.newHctx(0)
	lat0, lon0 := g.Coords(0)
	for dLat := -10.0; dLat <= 10.0; dLat += 0.5 {
		for dLon := -10.0; dLon <= 10.0; dLon += 0.5 {
			got := float64(h.est(lat0+dLat, lon0+dLon))
			want := Haversine(lat0+dLat, lon0+dLon, lat0, lon0) / maxSpeedMps * 10
			if got > want {
				t.Fatalf("country-scale overestimate at %+.1f %+.1f: got %.4f want %.4f", dLat, dLon, got, want)
			}
			worst = math.Max(worst, got-want)
		}
	}
	t.Logf("max slack %.4f deciseconds (negative is safe)", worst)
}
