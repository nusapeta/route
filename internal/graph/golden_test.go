package graph

import (
	"encoding/binary"
	"flag"
	"fmt"
	"hash/fnv"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/golden.txt")

// cityGraph builds a deterministic synthetic city: a 30x30 node lattice with
// bidirectional two-way streets and per-row speed variation. It exercises the
// builder, the on-disk format, snapping, A*, and polyline assembly without
// needing an .osm.pbf in the test environment.
func cityGraph(t testing.TB) *Builder {
	t.Helper()
	const n, d = 30, 0.0012 // ~133 m spacing
	type edge struct {
		to         uint32
		length, ds uint32
	}
	adj := make([][]edge, n*n)
	latOf := func(i int) int32 { return int32(math.Round((-7.8 + float64(i)*d) * E7)) }
	lonOf := func(j int) int32 { return int32(math.Round((110.4 + float64(j)*d) * E7)) }
	node := func(i, j int) uint32 { return uint32(i*n + j) }
	add := func(a, c uint32) {
		la, lo := latOf(int(a)/n), lonOf(int(a)%n)
		lc, oc := latOf(int(c)/n), lonOf(int(c)%n)
		m := uint32(math.Round(Haversine(float64(la)/E7, float64(lo)/E7, float64(lc)/E7, float64(oc)/E7)))
		speed := 8.0
		if int(a)/n%7 == 0 { // every 7th row is a slower local street
			speed = 5.0
		}
		ds := uint32(math.Round(float64(m) / speed * 10))
		if ds == 0 {
			ds = 1
		}
		adj[a] = append(adj[a], edge{c, m, ds})
	}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			a := node(i, j)
			if i+1 < n {
				add(a, node(i+1, j))
				add(node(i+1, j), a)
			}
			if j+1 < n {
				add(a, node(i, j+1))
				add(node(i, j+1), a)
			}
		}
	}
	b := &Builder{
		Lat: make([]int32, n*n),
		Lon: make([]int32, n*n),
		Off: make([]uint64, uint32(n*n)+1),
	}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			k := i*n + j
			b.Lat[k], b.Lon[k] = latOf(i), lonOf(j)
			b.Off[k] = uint64(len(b.To))
			for _, e := range adj[k] {
				b.To = append(b.To, e.to)
				b.Len = append(b.Len, e.length)
				b.Time = append(b.Time, e.ds)
			}
		}
	}
	b.Off[uint32(n*n)] = uint64(len(b.To))
	b.BuildGrid(0.02)
	return b
}

// goldenPairs returns route requests offset slightly off the lattice nodes, so
// snapping is part of the recorded behaviour.
func goldenPairs() []struct{ fLat, fLon, tLat, tLon float64 } {
	out := make([]struct{ fLat, fLon, tLat, tLon float64 }, 0, 48)
	// deterministic pseudo-random walk over the lattice
	seed := uint64(0x9e3779b97f4a7c15)
	next := func(mod int) int {
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		return int(seed % uint64(mod))
	}
	jitter := func() float64 { return (float64(next(200)) - 100) / 1e6 } // +-0.0001 deg
	for k := 0; k < 48; k++ {
		i1, j1, i2, j2 := next(30), next(30), next(30), next(30)
		out = append(out, struct{ fLat, fLon, tLat, tLon float64 }{
			-7.8 + float64(i1)*0.0012 + jitter(),
			110.4 + float64(j1)*0.0012 + jitter(),
			-7.8 + float64(i2)*0.0012 + jitter(),
			110.4 + float64(j2)*0.0012 + jitter(),
		})
	}
	return out
}

func seqHash(nodes []uint32) string {
	h := fnv.New64a()
	var buf [4]byte
	for _, v := range nodes {
		binary.LittleEndian.PutUint32(buf[:], v)
		h.Write(buf[:])
	}
	return fmt.Sprintf("%016x", h.Sum64())
}

// TestGoldenRoutes pins the end-to-end routing result (snap + search +
// path assembly) for a fixed set of pairs. Any unintended change to the
// profile, the search, the ordering, or snapping shows up as a diff here.
//
//	go test ./internal/graph -run TestGoldenRoutes
//	go test ./internal/graph -run TestGoldenRoutes -update   # accept change
func TestGoldenRoutes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "city.graph")
	if err := cityGraph(t).Write(path); err != nil {
		t.Fatalf("write: %v", err)
	}
	g, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer g.Close()
	r := NewRouter(g, 1)

	var got []string
	for _, p := range goldenPairs() {
		src, _, ok1 := g.Nearest(p.fLat, p.fLon, 3000)
		dst, _, ok2 := g.Nearest(p.tLat, p.tLon, 3000)
		if !ok1 || !ok2 {
			t.Fatalf("snap failed for %+v", p)
		}
		rt, err := r.Route(src, dst)
		if err != nil {
			t.Fatalf("route %d->%d: %v", src, dst, err)
		}
		got = append(got, fmt.Sprintf("%.6f,%.6f,%.6f,%.6f,%d,%d,%d,%s",
			p.fLat, p.fLon, p.tLat, p.tLon, rt.DistanceM, rt.TimeDs, len(rt.Nodes), seqHash(rt.Nodes)))
	}

	goldenPath := filepath.Join("testdata", "golden.txt")
	want, err := os.ReadFile(goldenPath)
	if err != nil || *updateGolden {
		if err != nil && !*updateGolden {
			t.Fatalf("read golden: %v (run with -update to create)", err)
		}
		body := "# routeid golden routes: from_lat,from_lon,to_lat,to_lon,dist_m,time_ds,nodes,seq_hash\n" +
			strings.Join(got, "\n") + "\n"
		if err := os.WriteFile(goldenPath, []byte(body), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("golden updated: %d pairs", len(got))
		return
	}

	var wantLines []string
	for _, line := range strings.Split(strings.TrimSpace(string(want)), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		wantLines = append(wantLines, line)
	}
	if len(wantLines) != len(got) {
		t.Fatalf("golden has %d pairs, got %d (run with -update)", len(wantLines), len(got))
	}
	for i := range got {
		if got[i] != wantLines[i] {
			t.Errorf("pair %d changed:\n got  %s\n want %s", i, got[i], wantLines[i])
		}
	}
}

// TestProvenanceRoundTrip checks that the v3 metadata survives a write/read
// cycle and that pre-v3 files still load without provenance.
func TestProvenanceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prov.graph")

	hash := make([]byte, 32)
	for i := range hash {
		hash[i] = byte(i)
	}
	built := uint32(time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC).Unix())

	b := cityGraph(t)
	b.PBFHash = hash
	b.BuildUnix = built
	b.Meta = map[string]string{
		"source":         "java-latest.osm.pbf",
		"extractor":      "test",
		"profile_sha256": "abc123",
		"order":          "hilbert",
	}
	if err := b.Write(path); err != nil {
		t.Fatalf("write: %v", err)
	}
	g, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if got := g.PBFHashHex(); got != "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f" {
		t.Errorf("pbf hash = %q", got)
	}
	if got := g.BuildTime(); !got.Equal(time.Unix(int64(built), 0).UTC()) {
		t.Errorf("build time = %v", got)
	}
	if got := g.MetaValue("source"); got != "java-latest.osm.pbf" {
		t.Errorf("source = %q", got)
	}
	if got := g.MetaValue("profile_sha256"); got != "abc123" {
		t.Errorf("profile = %q", got)
	}
	if len(g.Meta()) != 4 {
		t.Errorf("meta fields = %d, want 4: %v", len(g.Meta()), g.Meta())
	}
	prov := g.Provenance()
	if prov.Format != Version || prov.BuiltAt == "" || prov.Canonical == "" {
		t.Errorf("provenance = %+v", prov)
	}
	g.Close()

	// Simulate a v2 file: same bytes, older version field.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(raw[4:], 2)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	g2, err := Open(path)
	if err != nil {
		t.Fatalf("open v2: %v", err)
	}
	defer g2.Close()
	if g2.PBFHashHex() != "" || g2.Meta() != nil || !g2.BuildTime().IsZero() {
		t.Errorf("v2 file should have no provenance, got %+v", g2.Provenance())
	}
	if g2.NodeCount != uint32(len(cityGraph(t).Lat)) {
		t.Errorf("v2 node count = %d", g2.NodeCount)
	}
}

// TestMetaRejectsInvalid ensures a metadata value cannot corrupt the blob.
func TestMetaRejectsInvalid(t *testing.T) {
	b := cityGraph(t)
	b.Meta = map[string]string{"source": "line\nbreak"}
	if err := b.Write(filepath.Join(t.TempDir(), "bad.graph")); err == nil {
		t.Fatal("expected error for newline in metadata")
	}
}
