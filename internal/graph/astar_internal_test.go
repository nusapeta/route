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

func TestTurnCostBuckets(t *testing.T) {
	const s = float32(math.Sqrt2) / 2 // 45 degrees
	cases := []struct {
		name           string
		ax, ay, bx, by float32
		want           float32
	}{
		{"straight", 1, 0, 1, 0, 0},
		{"30 degree bend", 1, 0, 0.866025, 0.5, turnSlightDs},
		{"right angle", 1, 0, 0, 1, turnSharpDs},
		{"45 degrees", 1, 0, s, s, turnSlightDs},
		{"135 degrees", 1, 0, -s, s, turnSharpDs},
		{"u-turn", 1, 0, -1, 0, turnUturnDs},
		{"unknown incoming", 0, 0, 1, 0, 0},
		{"unknown outgoing", 1, 0, 0, 0, 0},
	}
	for _, c := range cases {
		if got := turnCostDs(c.ax, c.ay, c.bx, c.by); got != c.want {
			t.Errorf("%s: turnCostDs = %v, want %v", c.name, got, c.want)
		}
	}
}
func TestWorkspaceShrinkHysteresis(t *testing.T) {
	wp := NewWorkspacePool(1, 1<<20)
	g := &Graph{EdgeCount: 1 << 19} // ~500K edges so nodeMask doesn't alias
	ws := wp.get(g)

	// Populate enough items to force growing above initialSlots (65536)
	// 65536 * 0.8 = 52428.8, so inserting 60000 items forces grow to 131072.
	for i := uint32(0); i < 60000; i++ {
		if !ws.set(i, float32(i), i) {
			t.Fatalf("failed to set item %d", i)
		}
	}
	if len(ws.keys) <= int(initialSlots) {
		t.Fatalf("expected keys to grow past initialSlots, got %d", len(ws.keys))
	}
	grownCap := len(ws.keys)
	t.Logf("len(keys)=%d used=%d", len(ws.keys), ws.used)
	// Put back with used=60000. len(ws.keys) is 131072.
	// 131072 is NOT > 8 * 60000 (480000), so no shrink should occur yet.
	wp.put(ws)
	st := wp.Stats()
	if st.ShrinksTotal != 0 {
		t.Fatalf("unexpected shrink when used is large: %d", st.ShrinksTotal)
	}
	// Now borrow again, and simulate a query that only uses 10 items.
	ws = wp.get(g)
	for i := uint32(0); i < 10; i++ {
		ws.set(i, float32(i), i)
	}
	// len(keys) is 131072. used is 10.
	// 131072 > 8 * 10 (80), so put should shrink it back to initialSlots.
	wp.put(ws)
	st = wp.Stats()
	if st.ShrinksTotal != 1 {
		t.Fatalf("expected 1 shrink, got %d", st.ShrinksTotal)
	}
	ws = wp.get(g)
	if len(ws.keys) != int(initialSlots) {
		t.Fatalf("expected keys to shrink to initialSlots (%d), got %d (was %d)", initialSlots, len(ws.keys), grownCap)
	}
	wp.put(ws)
}

func TestWorkspaceResetForDifferentGraphs(t *testing.T) {
	wp := NewWorkspacePool(1, 1<<20)
	g1 := tinyGraph(t, false) // 3 nodes, small edge count
	ws := wp.get(g1)
	ws.set(1, 10.0, 0)
	wp.put(ws)

	// Create a dummy second graph with more edges
	g2 := &Graph{
		NodeCount: 50000,
		EdgeCount: 100000,
	}
	ws = wp.get(g2)
	expectedBits := workspaceBits(g2.EdgeCount)
	if ws.nodeBits != expectedBits {
		t.Fatalf("expected nodeBits=%d for g2, got %d", expectedBits, ws.nodeBits)
	}
	if ws.used != 0 {
		t.Fatalf("expected used=0 after resetForGraph, got %d", ws.used)
	}
	wp.put(ws)
}

// TestWorkspaceStatsConcurrent checks that scraping pool metrics while a query
// grows its table is race-free; /metrics is hit while routes are in flight.
func TestWorkspaceStatsConcurrent(t *testing.T) {
	wp := NewWorkspacePool(1, 1<<20)
	g := &Graph{EdgeCount: 1 << 19}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 20 {
			ws := wp.get(g)
			for j := uint32(0); j < 60000; j++ {
				ws.set(j, float32(j), j)
			}
			wp.put(ws)
		}
	}()

	for range 2000 {
		if st := wp.Stats(); st.TotalSlots == 0 || st.TotalBytes != st.TotalSlots*12 {
			t.Fatalf("inconsistent stats while routing: %+v", st)
		}
	}
	<-done

	ws := wp.get(g)
	size := uint64(len(ws.keys))
	if st := wp.Stats(); st.TotalSlots != size {
		t.Fatalf("Stats slots = %d, want %d", st.TotalSlots, size)
	}
	wp.put(ws)
}

// TestWorkspaceBudgetNonPow2 guards the probe invariant: a budget that is not a
// power of two used to reach the table as maxSlots, and "i = (i+1) & mask" then
// cycled over a subset and never found a free slot, hanging the request.
func TestWorkspaceBudgetNonPow2(t *testing.T) {
	const budget = 87381 // 1 MB / 12 B, not a power of two
	wp := NewWorkspacePool(1, budget)
	if got := wp.maxSlots; got != 65536 {
		t.Fatalf("maxSlots = %d, want 65536 (floor to a power of two)", got)
	}
	ws := wp.get(tinyGraph(t, false))
	blocked := false
	for i := uint32(0); i < budget; i++ {
		if !ws.set(i, float32(i), 0) {
			blocked = true
			break
		}
	}
	if !blocked {
		t.Fatal("set never reported the budget, so the cap is not enforced")
	}
	wp.put(ws)
}

func TestWorkspaceMaxSlotsBudget(t *testing.T) {
	// Pool with maxSlots = initialSlots (65536)
	wp := NewWorkspacePool(1, initialSlots)
	g := tinyGraph(t, false)
	ws := wp.get(g)
	// Fill up to 80% of initialSlots -> next insert should fail because grow is blocked by maxSlots
	limit := int(initialSlots * 8 / 10)
	var failed bool
	for i := uint32(0); i <= uint32(limit+10); i++ {
		if !ws.set(i, float32(i), i) {
			failed = true
			break
		}
	}
	if !failed {
		t.Fatalf("expected set to fail when exceeding budget, but succeeded")
	}
	wp.put(ws)
}
