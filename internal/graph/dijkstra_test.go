package graph

import (
	"math"
	"path/filepath"
	"sort"
	"testing"
)

func openDijkstraCity(t *testing.T) *Graph {
	t.Helper()
	path := filepath.Join(t.TempDir(), "city.graph")
	if err := cityGraph(t).Write(path); err != nil {
		t.Fatalf("write: %v", err)
	}
	g, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { g.Close() })
	return g
}

// dijkstraCases returns 100 sources with 10 destinations each. The lattice
// is 30x30, so every node id is in [0,900).
func dijkstraCases(n int) []struct {
	src     uint32
	targets []uint32
} {
	seed := uint64(0x243f6a8885a308d3)
	next := func(mod int) int {
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		return int(seed % uint64(mod))
	}
	out := make([]struct {
		src     uint32
		targets []uint32
	}, n)
	for k := range out {
		out[k].src = uint32(next(900))
		out[k].targets = make([]uint32, 10)
		for i := range out[k].targets {
			out[k].targets[i] = uint32(next(900))
		}
	}
	return out
}

func percentile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(float64(len(sorted)-1) * q)
	return sorted[i]
}

// TestDijkstraOneToManyVsAStar pins the node-based Dijkstra cost against A*
// on the 30x30 lattice. A* carries turn costs; Dijkstra drops turn history,
// so its cost must never exceed A*'s. The relative gap is recorded for the
// paper rather than asserted against a target: the target is a measurement,
// not a gate on a synthetic grid.
func TestDijkstraOneToManyVsAStar(t *testing.T) {
	g := openDijkstraCity(t)
	r := NewRouter(g, 2)
	cases := dijkstraCases(100)
	diffs := make([]float64, 0, len(cases)*10)

	for k, c := range cases {
		res, err := r.DijkstraOneToMany(c.src, c.targets, 0, r.TimeCost())
		if err != nil {
			t.Fatalf("case %d dijkstra: %v", k, err)
		}
		if res.Bound.Truncated {
			t.Fatalf("case %d truncated unexpectedly", k)
		}
		for i, dst := range c.targets {
			want, err := r.Route(c.src, dst)
			if err != nil {
				t.Fatalf("case %d target %d astar: %v", k, i, err)
			}
			got := float64(res.Dists[i])
			if math.IsInf(got, 1) {
				t.Fatalf("case %d target %d unreachable by dijkstra", k, dst)
			}
			if got > float64(want.TimeDs)+0.5 {
				t.Fatalf("case %d target %d: dijkstra %.1f ds > astar %d ds", k, dst, got, want.TimeDs)
			}
			diffs = append(diffs, (float64(want.TimeDs)-got)/float64(want.TimeDs)*100)
		}
	}

	sort.Float64s(diffs)
	t.Logf("n=%d median=%.2f%% p95=%.2f%% max=%.2f%%",
		len(diffs), percentile(diffs, 0.5), percentile(diffs, 0.95), diffs[len(diffs)-1])
}

// TestDijkstraOneToManyVisited uses nil targets: every settled node is
// returned within the cutoff, which is what /isochrone rasterizes.
func TestDijkstraOneToManyVisited(t *testing.T) {
	g := openDijkstraCity(t)
	r := NewRouter(g, 1)
	const src = 15*30 + 15
	res, err := r.DijkstraOneToMany(src, nil, 2000, r.TimeCost())
	if err != nil {
		t.Fatalf("dijkstra: %v", err)
	}
	if len(res.Dists) != 0 || len(res.Visited) == 0 {
		t.Fatalf("dists=%d visited=%d", len(res.Dists), len(res.Visited))
	}
	for _, v := range res.Visited {
		if v.Cost > 2000 {
			t.Fatalf("node %d cost %.1f exceeds cutoff", v.Node, v.Cost)
		}
		if v.Cost < 0 {
			t.Fatalf("negative cost for node %d", v.Node)
		}
	}
	if res.Bound.Truncated {
		t.Fatal("cutoff is a requested bound, not a truncation")
	}
}

// TestDijkstraOneToManyBounds checks the expansion cap and the cost cutoff
// on a straight chain, where the expected costs are exact.
func TestDijkstraOneToManyBounds(t *testing.T) {
	coords := make([][2]float64, 6)
	edges := make([]testEdge, 0, 5)
	for i := range coords {
		coords[i] = [2]float64{-7.8 + float64(i)*0.001, 110.4}
		if i > 0 {
			edges = append(edges, testEdge{from: uint32(i - 1), to: uint32(i), kmh: 36})
		}
	}
	path := filepath.Join(t.TempDir(), "chain.graph")
	if err := smallGraph(t, coords, edges).Write(path); err != nil {
		t.Fatalf("write: %v", err)
	}
	g, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer g.Close()

	r := NewRouter(g, 1)
	targets := []uint32{1, 2, 3, 5}

	res, err := r.DijkstraOneToMany(0, targets, 350, g.LengthCost())
	if err != nil {
		t.Fatalf("cutoff: %v", err)
	}
	for i := range 3 {
		if math.IsInf(float64(res.Dists[i]), 1) {
			t.Fatalf("near target %d unreachable: %v", i, res.Dists)
		}
	}
	if !math.IsInf(float64(res.Dists[3]), 1) {
		t.Fatalf("far target must be past cutoff: %v", res.Dists)
	}
	if res.Bound.Truncated {
		t.Fatal("cutoff must not set Truncated")
	}

	r.SetMaxExpansions(2)
	res, err = r.DijkstraOneToMany(0, targets, 0, r.TimeCost())
	if err != nil {
		t.Fatalf("cap: %v", err)
	}
	if !res.Bound.Truncated {
		t.Fatal("expansion cap must set Truncated")
	}
	if !math.IsInf(float64(res.Dists[3]), 1) {
		t.Fatalf("far target must stay unreachable after cap: %v", res.Dists)
	}
}

// TestDijkstraOneToManyReusesWorkspace runs more queries than the pool has
// slots; a missing put would deadlock on the second acquire.
func TestDijkstraOneToManyReusesWorkspace(t *testing.T) {
	g := openDijkstraCity(t)
	pool := NewWorkspacePool(1, maxSlots)
	r := NewRouterWithPool(g, pool)
	for i := range 50 {
		if _, err := r.DijkstraOneToMany(uint32(i), []uint32{uint32((i + 17) % 900)}, 0, r.TimeCost()); err != nil {
			t.Fatalf("query %d: %v", i, err)
		}
	}
	if st := pool.Stats(); st.TotalBytes > 4<<20 {
		t.Fatalf("workspace = %d bytes for 900 nodes", st.TotalBytes)
	}
}
