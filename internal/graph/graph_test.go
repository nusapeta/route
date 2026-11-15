package graph_test

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/nusapeta/route/internal/graph"
)

func buildLine(t *testing.T) string {
	t.Helper()
	// 0 -- 1 -- 2 with a spur 1 -- 3, roughly 1.1 km per hop
	coords := [][2]float64{
		{-6.200, 106.800},
		{-6.200, 106.810},
		{-6.200, 106.820},
		{-6.210, 106.810},
	}
	b := &graph.Builder{
		Off: []uint32{0, 1, 4, 5, 6},
		To:  []uint32{1, 0, 2, 3, 1, 1},
		Len: make([]uint16, 6),
		Cls: make([]uint8, 6),
	}
	for _, c := range coords {
		b.Lat = append(b.Lat, int32(math.Round(c[0]*graph.E7)))
		b.Lon = append(b.Lon, int32(math.Round(c[1]*graph.E7)))
	}
	for e := range b.To {
		a, z := edgeEnds(b, uint32(e))
		d := graph.Haversine(
			float64(b.Lat[a])/graph.E7, float64(b.Lon[a])/graph.E7,
			float64(b.Lat[z])/graph.E7, float64(b.Lon[z])/graph.E7,
		)
		b.Len[e] = uint16(math.Round(d / graph.LenUnit))
		b.Cls[e] = 80 // 40 km/h
	}
	b.BuildGrid(0.02)

	path := filepath.Join(t.TempDir(), "test.graph")
	if err := b.Write(path); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func edgeEnds(b *graph.Builder, e uint32) (from, to uint32) {
	for n := 0; n+1 < len(b.Off); n++ {
		if e >= b.Off[n] && e < b.Off[n+1] {
			return uint32(n), b.To[e]
		}
	}
	return 0, 0
}

func TestRoundTripAndRoute(t *testing.T) {
	path := buildLine(t)
	g, err := graph.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer g.Close()

	if g.NodeCount != 4 || g.EdgeCount != 6 {
		t.Fatalf("counts: nodes=%d edges=%d", g.NodeCount, g.EdgeCount)
	}

	r := graph.NewRouter(g, 1)
	res, err := r.Route(0, 2)
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if len(res.Nodes) != 3 || res.Nodes[0] != 0 || res.Nodes[2] != 2 {
		t.Fatalf("path: %v", res.Nodes)
	}
	if res.DistanceM < 2100 || res.DistanceM > 2300 {
		t.Fatalf("distance: %d", res.DistanceM)
	}
	if res.TimeDs <= 0 {
		t.Fatalf("time: %d", res.TimeDs)
	}

	// snapping
	n, d, ok := g.Nearest(-6.2004, 106.8096, 500)
	if !ok || n != 1 || d > 100 {
		t.Fatalf("snap: node=%d dist=%.1f ok=%v", n, d, ok)
	}
	if _, _, ok := g.Nearest(-6.3, 107.0, 500); ok {
		t.Fatal("snap should fail far away")
	}

	// a zero-length route returns the node itself
	if _, err := r.Route(0, 0); err != nil {
		t.Fatalf("zero-length route should work: %v", err)
	}
}

func TestUnreachable(t *testing.T) {
	b := &graph.Builder{
		Off: []uint32{0, 0, 0},
		To:  nil,
		Len: nil,
		Cls: nil,
		Lat: []int32{-6.2 * graph.E7, 0},
		Lon: []int32{106.8 * graph.E7, 0},
	}
	b.Lat[1] = int32(math.Round(-6.2 * graph.E7))
	b.Lon[1] = int32(math.Round(106.9 * graph.E7))
	b.BuildGrid(0.02)
	path := filepath.Join(t.TempDir(), "iso.graph")
	if err := b.Write(path); err != nil {
		t.Fatalf("write: %v", err)
	}
	g, err := graph.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer g.Close()
	r := graph.NewRouter(g, 1)
	if _, err := r.Route(0, 1); err != graph.ErrNoRoute {
		t.Fatalf("want ErrNoRoute, got %v", err)
	}
}

// TestOpenTruncatedFile covers the file parser: a cut-off file must return an
// error from Open, not panic when a section is sliced past the mapping.
func TestOpenTruncatedFile(t *testing.T) {
	path := buildLine(t)
	// 330 bytes is just past the fixed 320-byte header, inside the first
	// section, so the section bounds check is the one that fires.
	if err := os.Truncate(path, 330); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if _, err := graph.Open(path); err == nil {
		t.Fatal("Open accepted a truncated graph")
	}
}
