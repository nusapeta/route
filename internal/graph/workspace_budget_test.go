package graph

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const (
	// workspaceBudgetBytes mirrors the server's default 384 MB per workspace.
	workspaceBudgetBytes = 384 << 20
	// anonRSSBudgetBytes is the hard anon-RSS budget from docs/ROADMAP.md.
	anonRSSBudgetBytes = 700 << 20
)

// TestWorkspaceBudgetLongRoute routes across a long synthetic chain. It pins
// the two bounds a long query can violate: the open-addressing table must stay
// under the workspace budget, and the process must stay under the anon-RSS
// budget. The graph is large enough to force at least one table growth.
func TestWorkspaceBudgetLongRoute(t *testing.T) {
	const nodes = 300_000
	coords := make([][2]float64, nodes)
	edges := make([]testEdge, 0, nodes-1)
	for i := 0; i < nodes; i++ {
		coords[i] = [2]float64{-7.8 + float64(i)*0.0001, 110.4}
		if i > 0 {
			edges = append(edges, testEdge{from: uint32(i - 1), to: uint32(i), kmh: 60})
		}
	}
	path := filepath.Join(t.TempDir(), "long.graph")
	if err := smallGraph(t, coords, edges).Write(path); err != nil {
		t.Fatalf("write: %v", err)
	}
	g, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer g.Close()

	pool := NewWorkspacePool(1, uint32(workspaceBudgetBytes/workspaceSlotBytes))
	r := NewRouterWithPool(g, pool)
	rt, err := r.Route(0, nodes-1)
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if len(rt.Nodes) != nodes {
		t.Fatalf("route nodes = %d, want %d", len(rt.Nodes), nodes)
	}
	st := pool.Stats()
	if st.GrowsTotal == 0 {
		t.Error("workspace never grew; the test graph is too small to exercise the budget")
	}
	if st.TotalBytes > workspaceBudgetBytes {
		t.Errorf("workspace = %d bytes, budget %d", st.TotalBytes, workspaceBudgetBytes)
	}

	rss, anon := procRSS(t)
	if anon > anonRSSBudgetBytes {
		t.Errorf("RssAnon = %d bytes, budget %d", anon, anonRSSBudgetBytes)
	}
	if rss > anonRSSBudgetBytes {
		t.Errorf("VmRSS = %d bytes, budget %d", rss, anonRSSBudgetBytes)
	}
	t.Logf("route %.0f km, workspace %.1f MiB, VmRSS %.1f MiB, RssAnon %.1f MiB",
		float64(rt.DistanceM)/1000,
		float64(st.TotalBytes)/(1<<20), float64(rss)/(1<<20), float64(anon)/(1<<20))
}

// procRSS returns VmRSS and RssAnon from /proc/self/status. Tests on a
// non-Linux host skip the assertion instead of inventing a number.
func procRSS(t *testing.T) (rss, anon uint64) {
	t.Helper()
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Skipf("cannot read process RSS: %v", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		v, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "VmRSS:":
			rss = v << 10
		case "RssAnon:":
			anon = v << 10
		}
	}
	return rss, anon
}
