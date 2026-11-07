package graph

import (
	"context"
	"errors"
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
		h := r.newHctx(dst, r.eps)
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
	h := r.newHctx(0, r.eps)
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

// cancelAfter reports a live context for the first n Err() calls and then
// cancels, which lets a test cancel a search deterministically mid-run.
type cancelAfter struct {
	context.Context
	n int
}

func (c *cancelAfter) Err() error {
	c.n--
	if c.n <= 0 {
		return context.Canceled
	}
	return nil
}

// TestRouteCanceled checks that a canceled request stops the search and frees
// its workspace instead of running until the expansion cap.
func TestRouteCanceled(t *testing.T) {
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

	// Pre-canceled: must not even start.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.RouteCtx(ctx, 0, g.NodeCount-1); !errors.Is(err, ErrCanceled) {
		t.Fatalf("pre-canceled: got %v, want ErrCanceled", err)
	}

	// Canceled a few checks into the search.
	if _, err := r.RouteCtx(&cancelAfter{Context: context.Background(), n: 3}, 0, g.NodeCount-1); !errors.Is(err, ErrCanceled) {
		t.Fatalf("mid-search cancel: got %v, want ErrCanceled", err)
	}

	// A live context still routes, and the workspace is reusable afterwards.
	if _, err := r.RouteCtx(context.Background(), 0, g.NodeCount-1); err != nil {
		t.Fatalf("live context: %v", err)
	}
	res, err := r.RouteCtx(context.Background(), 0, g.NodeCount-1)
	if err != nil {
		t.Fatalf("second route: %v", err)
	}
	t.Logf("nodes=%d expansions=%d", len(res.Nodes), res.Expansions)
}
