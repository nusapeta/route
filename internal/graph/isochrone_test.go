package graph

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"math"
	"os"
	"path/filepath"
	"testing"
)

var updateIsoGolden = flag.Bool("update-iso", false, "rewrite testdata/iso-monas-15m.geojson")

func isoTestGraph(t *testing.T) *Graph {
	t.Helper()
	path := filepath.Join(t.TempDir(), "iso-city.graph")
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

func isoGeoJSON(res IsoResult, minutes float64) []byte {
	coords := make([][][2]float64, 0, len(res.RingLengths))
	off := 0
	for _, n := range res.RingLengths {
		ring := make([][2]float64, 0, n)
		for _, p := range res.Rings[off : off+n] {
			ring = append(ring, [2]float64{p[1], p[0]})
		}
		coords = append(coords, ring)
		off += n
	}
	type geometry struct {
		Type        string         `json:"type"`
		Coordinates [][][2]float64 `json:"coordinates"`
	}
	type feature struct {
		Type     string   `json:"type"`
		Geometry geometry `json:"geometry"`
	}
	body := struct {
		Type     string         `json:"type"`
		Features []feature      `json:"features"`
		Meta     map[string]any `json:"meta"`
	}{
		Type: "FeatureCollection",
		Meta: map[string]any{
			"grid_m": res.GridM, "nodes_visited": res.NodesVisited,
			"area_m2": res.AreaM2, "minutes": minutes,
		},
	}
	for _, ring := range coords {
		body.Features = append(body.Features, feature{
			Type:     "Feature",
			Geometry: geometry{Type: "Polygon", Coordinates: [][][2]float64{ring}},
		})
	}
	b, _ := json.MarshalIndent(body, "", "  ")
	return append(b, '\n')
}

func TestIsochroneLattice(t *testing.T) {
	g := isoTestGraph(t)
	r := NewRouter(g, 1)
	res, err := Isochrone(context.Background(), r, -7.7826, 110.4174, IsoOptions{Minutes: 15, Meters: 100_000})
	if err != nil {
		t.Fatalf("isochrone: %v", err)
	}
	if len(res.RingLengths) == 0 || res.AreaM2 < isoMinRingAreaM2 {
		t.Fatalf("empty result: %+v", res)
	}
	if res.NodesVisited == 0 || res.GridM < DefaultIsoGridM {
		t.Fatalf("bad meta: %+v", res)
	}
	off := 0
	for k, n := range res.RingLengths {
		if n < isoMinRingVertices {
			t.Fatalf("ring %d has %d vertices", k, n)
		}
		if first, last := res.Rings[off], res.Rings[off+n-1]; first != last {
			t.Fatalf("ring %d is open: %v != %v", k, first, last)
		}
		off += n
	}
	if off != len(res.Rings) {
		t.Fatalf("ring lengths %v do not cover %d points", res.RingLengths, len(res.Rings))
	}

	path := filepath.Join("testdata", "iso-monas-15m.geojson")
	want := isoGeoJSON(res, 15)
	if *updateIsoGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, want, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v (run with -update-iso to create)", err)
	}
	if string(raw) != string(want) {
		t.Fatalf("golden mismatch\nwant:\n%s\ngot:\n%s", raw, want)
	}
}

func TestIsochroneGridEscalation(t *testing.T) {
	g := isoTestGraph(t)
	r := NewRouter(g, 1)
	res, err := Isochrone(context.Background(), r, -7.7826, 110.4174, IsoOptions{Minutes: 60, Meters: 100_000})
	if err != nil {
		t.Fatalf("isochrone: %v", err)
	}
	if res.GridM <= DefaultIsoGridM || res.GridM > 100_000 {
		t.Fatalf("grid did not grow within budget: %v", res.GridM)
	}
	if cells := math.Ceil(200_000/res.GridM) + 2; cells*cells > float64(DefaultIsoMaxCells) {
		t.Fatalf("grid too fine for the cell budget: %v", res.GridM)
	}
}

func TestIsochroneErrors(t *testing.T) {
	g := isoTestGraph(t)
	r := NewRouter(g, 1)
	if _, err := Isochrone(context.Background(), r, -7.7826, 110.4174, IsoOptions{Minutes: 15, MaxVisited: 1}); !errors.Is(err, ErrIsoTooLarge) {
		t.Fatalf("want too large, got %v", err)
	}
	if _, err := Isochrone(context.Background(), r, 0, 0, IsoOptions{Minutes: 15}); !errors.Is(err, ErrIsoNoSnap) {
		t.Fatalf("want no snap, got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Isochrone(ctx, r, -7.7826, 110.4174, IsoOptions{Minutes: 15}); !errors.Is(err, context.Canceled) {
		t.Fatalf("want canceled, got %v", err)
	}
}

func TestIsoResampleRing(t *testing.T) {
	square := []isoXY{{0, 0}, {1, 0}, {1, 1}, {0, 1}, {0, 0}}
	out := isoResampleRing(square, isoMinRingVertices)
	if len(out) != isoMinRingVertices || out[0] != out[len(out)-1] {
		t.Fatalf("bad resample: %+v", out)
	}
}
