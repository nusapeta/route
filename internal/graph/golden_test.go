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
		to    uint32
		len4  uint16
		class uint8
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
		len4 := uint16(math.Max(1, math.Round(float64(m)/LenUnit)))
		adj[a] = append(adj[a], edge{c, len4, uint8(math.Round(speed * 3.6 * SpeedClassScale))})
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
		Off: make([]uint32, uint32(n*n)+1),
	}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			k := i*n + j
			b.Lat[k], b.Lon[k] = latOf(i), lonOf(j)
			b.Off[k] = uint32(len(b.To))
			for _, e := range adj[k] {
				b.To = append(b.To, e.to)
				b.Len = append(b.Len, e.len4)
				b.Cls = append(b.Cls, e.class)
			}
		}
	}
	b.Off[uint32(n*n)] = uint32(len(b.To))
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
		got = append(got, fmt.Sprintf("node,%.6f,%.6f,%.6f,%.6f,%d,%d,%d,%s",
			p.fLat, p.fLon, p.tLat, p.tLon, rt.DistanceM, rt.TimeDs, len(rt.Nodes), seqHash(rt.Nodes)))

		// same pair through the edge snapper: pins the projection, the trim/spur
		// correction, and the resulting geometry.
		sa, okA := g.SnapEdge(p.fLat, p.fLon, 3000)
		sb, okB := g.SnapEdge(p.tLat, p.tLon, 3000)
		if !okA || !okB {
			t.Fatalf("edge snap failed for %+v", p)
		}
		sr, err := r.RouteSnap(sa, sb, true)
		if err != nil {
			t.Fatalf("snap route %d->%d: %v", sa.Node, sb.Node, err)
		}
		got = append(got, fmt.Sprintf("snap,%.6f,%.6f,%.6f,%.6f,%d,%d,%d,%s,%t,%t,%.1f,%.1f",
			p.fLat, p.fLon, p.tLat, p.tLon, sr.DistanceM, sr.TimeDs, len(sr.Nodes), seqHash(sr.Nodes),
			sr.TrimStart, sr.TrimEnd, sa.Dist, sb.Dist))
	}

	goldenPath := filepath.Join("testdata", "golden.txt")
	want, err := os.ReadFile(goldenPath)
	if err != nil || *updateGolden {
		if err != nil && !*updateGolden {
			t.Fatalf("read golden: %v (run with -update to create)", err)
		}
		body := "# routeid golden routes\n" +
			"#   node,from_lat,from_lon,to_lat,to_lon,dist_m,time_ds,nodes,seq_hash\n" +
			"#   snap,from_lat,from_lon,to_lat,to_lon,dist_m,time_ds,nodes,seq_hash,trim_start,trim_end,snap_from_m,snap_to_m\n" +
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

// TestProvenanceRoundTrip checks that the v3+ metadata survives a write/read
// cycle and that pre-v4 files are rejected rather than misread.
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

	// A pre-v4 file must be rejected instead of misread: v4 changed the
	// widths of Off/Len and replaced Time with Cls.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(raw[4:], 3)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("v3 file should be rejected by a v4 reader")
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

// testEdge is one bidirectional edge in a tiny synthetic graph. kmh is the
// speed the profile would assign to the source way.
type testEdge struct {
	from, to uint32
	kmh      float64
}

// smallGraph builds a graph from explicit edges, so a test can state exactly
// which ways survived the profile filter.
func smallGraph(t testing.TB, coords [][2]float64, edges []testEdge) *Builder {
	t.Helper()
	adj := make([][]testEdge, len(coords))
	for _, e := range edges {
		adj[e.from] = append(adj[e.from], e)
		adj[e.to] = append(adj[e.to], testEdge{from: e.to, to: e.from, kmh: e.kmh})
	}
	b := &Builder{
		Lat: make([]int32, len(coords)),
		Lon: make([]int32, len(coords)),
		Off: make([]uint32, uint32(len(coords))+1),
	}
	for i, c := range coords {
		b.Lat[i] = int32(math.Round(c[0] * E7))
		b.Lon[i] = int32(math.Round(c[1] * E7))
	}
	for i := range coords {
		b.Off[i] = uint32(len(b.To))
		for _, e := range adj[i] {
			m := Haversine(
				float64(b.Lat[i])/E7, float64(b.Lon[i])/E7,
				float64(b.Lat[e.to])/E7, float64(b.Lon[e.to])/E7,
			)
			b.To = append(b.To, e.to)
			b.Len = append(b.Len, uint16(math.Max(1, math.Round(m/LenUnit))))
			b.Cls = append(b.Cls, uint8(math.Round(e.kmh*SpeedClassScale)))
		}
	}
	b.Off[uint32(len(coords))] = uint32(len(b.To))
	b.BuildGrid(0.02)
	return b
}

// TestUnionStartEdgeProfile guards the first hop of a query: the frontier is
// seeded from every edge leaving the source, and a class-0 edge is free if the
// seed is not filtered. Here the motor-only shortcut A-B is free for a car and
// leads to a car edge, so an unfiltered seed would return a route through it.
func TestUnionStartEdgeProfile(t *testing.T) {
	const lat0, lon0 = -7.8, 110.4
	// A(0) - B(1) is the motor-only shortcut; A(0) - D(2) - C(3) is the car
	// detour, and B(1) - C(3) lets a motorcycle continue past the shortcut.
	coords := [][2]float64{
		{lat0, lon0},
		{lat0, lon0 + 0.001},
		{lat0 + 0.001, lon0},
		{lat0, lon0 + 0.002},
	}
	b := smallGraph(t, coords, []testEdge{
		{0, 1, 50}, {1, 3, 50},
		{0, 2, 50}, {2, 3, 50},
	})
	b.MCls = make([]uint8, len(b.Cls))
	copy(b.MCls, b.Cls)
	for u := uint32(0); u < uint32(len(coords)); u++ {
		for e := b.Off[u]; e < b.Off[u+1]; e++ {
			v := b.To[e]
			if (u == 0 && v == 1) || (u == 1 && v == 0) {
				b.Cls[e] = 0 // motor-only shortcut
			}
		}
	}

	path := filepath.Join(t.TempDir(), "start-edge.graph")
	if err := b.Write(path); err != nil {
		t.Fatalf("write: %v", err)
	}
	g, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer g.Close()
	src, _, ok1 := g.Nearest(lat0, lon0, 3000)
	dst, _, ok2 := g.Nearest(lat0, lon0+0.002, 3000)
	if !ok1 || !ok2 {
		t.Fatal("snap failed")
	}

	for _, c := range []struct {
		name  string
		motor bool
		want  []uint32
	}{
		{"car", false, []uint32{0, 2, 3}},
		{"motor", true, []uint32{0, 1, 3}},
	} {
		r := NewRouter(g, 1)
		r.SetMotor(c.motor)
		rt, err := r.Route(src, dst)
		if err != nil {
			t.Fatalf("%s route: %v", c.name, err)
		}
		if len(rt.Nodes) != len(c.want) {
			t.Fatalf("%s nodes = %v, want %v", c.name, rt.Nodes, c.want)
		}
		for i := range c.want {
			if rt.Nodes[i] != c.want[i] {
				t.Fatalf("%s nodes = %v, want %v", c.name, rt.Nodes, c.want)
			}
		}
	}
}

// TestMotorGoldenRoutes pins the union graph's shape: a path shortcut that
// only motorcycles may use, and a motorway that only cars may use. The access
// and speed decisions themselves are pinned by TestMotorGoldenRules in
// internal/extract; this test pins the routes that result from them.
func TestMotorGoldenRoutes(t *testing.T) {
	const lat0, lon0 = -7.8, 110.4
	// A(0) - B(1) - C(2) is the path shortcut; A(0) - D(3) - E(4) - C(2) is
	// the motorway detour. Both are bidirectional.
	coords := [][2]float64{
		{lat0, lon0},
		{lat0, lon0 + 0.001},
		{lat0, lon0 + 0.002},
		{lat0 + 0.001, lon0},
		{lat0 + 0.001, lon0 + 0.001},
	}
	b := smallGraph(t, coords, []testEdge{
		{0, 1, 12}, {1, 2, 12}, // path + motorcycle=yes
		{0, 3, 100}, {3, 4, 100}, {4, 2, 100}, // motorway
	})
	// v5 union rules: the path has no car class, the motorway no motor class.
	b.MCls = make([]uint8, len(b.Cls))
	copy(b.MCls, b.Cls)
	for u := 0; u < len(coords); u++ {
		for e := b.Off[u]; e < b.Off[u+1]; e++ {
			v := b.To[e]
			path := (u == 0 && v == 1) || (u == 1 && v == 0) ||
				(u == 1 && v == 2) || (u == 2 && v == 1)
			if path {
				b.Cls[e] = 0
			} else {
				b.MCls[e] = 0
			}
		}
	}

	path := filepath.Join(t.TempDir(), "union.graph")
	if err := b.Write(path); err != nil {
		t.Fatalf("write: %v", err)
	}
	g, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer g.Close()
	src, _, ok1 := g.Nearest(lat0, lon0, 3000)
	dst, _, ok2 := g.Nearest(lat0, lon0+0.002, 3000)
	if !ok1 || !ok2 {
		t.Fatal("snap failed")
	}

	var got []string
	for _, c := range []struct {
		name  string
		motor bool
	}{{"motor", true}, {"car", false}} {
		r := NewRouter(g, 1)
		r.SetMotor(c.motor)
		rt, err := r.Route(src, dst)
		if err != nil {
			t.Fatalf("%s route: %v", c.name, err)
		}
		got = append(got, fmt.Sprintf("%s,%.6f,%.6f,%.6f,%.6f,%d,%d,%d,%s",
			c.name, lat0, lon0, lat0, lon0+0.002,
			rt.DistanceM, rt.TimeDs, len(rt.Nodes), seqHash(rt.Nodes)))
	}

	goldenPath := filepath.Join("testdata", "motor-golden.txt")
	want, err := os.ReadFile(goldenPath)
	if err != nil || *updateGolden {
		if err != nil && !*updateGolden {
			t.Fatalf("read golden: %v (run with -update to create)", err)
		}
		body := "# routeid motor golden routes\n" +
			"#   profile,from_lat,from_lon,to_lat,to_lon,dist_m,time_ds,nodes,seq_hash\n" +
			strings.Join(got, "\n") + "\n"
		if err := os.WriteFile(goldenPath, []byte(body), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("motor golden updated: %d routes", len(got))
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
		t.Fatalf("motor golden has %d routes, got %d (run with -update)", len(wantLines), len(got))
	}
	for i := range got {
		if got[i] != wantLines[i] {
			t.Errorf("route %d changed:\n got  %s\n want %s", i, got[i], wantLines[i])
		}
	}
}
