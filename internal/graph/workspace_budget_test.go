package graph

import (
	"encoding/json"
	"os"
	"os/exec"
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

// TestBudgetGuard pins the CI budget gate: the recorded baseline must stay
// under the hard limits, the guard must accept a neutral build, and a 5 MB
// global allocation must turn it red. The same script runs in CI.
func TestBudgetGuard(t *testing.T) {
	root := filepath.Join("..", "..")
	root, err := filepath.Abs(root)
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}
	baselinePath := filepath.Join(root, "testdata", "rss-baseline.json")
	script := filepath.Join(root, "scripts", "budget.sh")
	data, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	var b struct {
		Idle     float64 `json:"idle_rss_mb"`
		Loaded   float64 `json:"loaded_rss_mb"`
		Delta    float64 `json:"delta_limit_mb"`
		Hard     float64 `json:"rss_hard_mb"`
		Image    float64 `json:"image_max_mb"`
		Binary   float64 `json:"binary_max_mb"`
		Artifact float64 `json:"artifact_max_mb"`
	}
	if err := json.Unmarshal(data, &b); err != nil {
		t.Fatalf("parse baseline: %v", err)
	}

	for name, v := range map[string]float64{
		"idle_rss_mb": b.Idle, "loaded_rss_mb": b.Loaded, "delta_limit_mb": b.Delta,
		"rss_hard_mb": b.Hard, "image_max_mb": b.Image, "binary_max_mb": b.Binary,
		"artifact_max_mb": b.Artifact,
	} {
		if v <= 0 {
			t.Errorf("%s = %v, want positive", name, v)
		}
	}
	if b.Idle > b.Hard || b.Loaded > b.Hard {
		t.Fatalf("baseline RSS over hard limit: idle %.1f loaded %.1f hard %.1f", b.Idle, b.Loaded, b.Hard)
	}
	if b.Delta > 2 {
		t.Errorf("delta limit %.1f MB, want <= 2", b.Delta)
	}

	run := func(args ...string) (string, error) {
		t.Helper()
		cmd := exec.Command("bash", append([]string{script}, args...)...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := run("--baseline", baselinePath, "--self-test"); err != nil {
		t.Fatalf("budget self-test: %v\n%s", err, out)
	}
	plus5 := strconv.FormatFloat(b.Loaded+5, 'f', 1, 64)
	if out, err := run("--baseline", baselinePath, "--rss-mb", plus5, "--phase", "loaded", "--check-only"); err == nil {
		t.Fatalf("5 MB regression was accepted:\n%s", out)
	}
	neutral := strconv.FormatFloat(b.Loaded, 'f', 1, 64)
	if out, err := run("--baseline", baselinePath, "--rss-mb", neutral, "--phase", "loaded", "--check-only"); err != nil {
		t.Fatalf("neutral build rejected: %v\n%s", err, out)
	}
}
