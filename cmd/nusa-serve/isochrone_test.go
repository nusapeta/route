package main

import (
	"encoding/json"
	"math"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/nusapeta/route/internal/graph"
)

func isoServer(t *testing.T) *server {
	t.Helper()
	const n, d = 10, 0.0012
	type edge struct {
		to    uint32
		len4  uint16
		class uint8
	}
	adj := make([][]edge, n*n)
	latOf := func(i int) int32 { return int32(math.Round((-6.2 + float64(i)*d) * graph.E7)) }
	lonOf := func(j int) int32 { return int32(math.Round((106.8 + float64(j)*d) * graph.E7)) }
	node := func(i, j int) uint32 { return uint32(i*n + j) }
	add := func(a, c uint32) {
		la, lo := latOf(int(a)/n), lonOf(int(a)%n)
		lc, oc := latOf(int(c)/n), lonOf(int(c)%n)
		m := uint32(math.Round(graph.Haversine(float64(la)/graph.E7, float64(lo)/graph.E7, float64(lc)/graph.E7, float64(oc)/graph.E7)))
		adj[a] = append(adj[a], edge{c, uint16(math.Max(1, math.Round(float64(m)/graph.LenUnit))), uint8(40 * graph.SpeedClassScale)})
	}
	for i := range n {
		for j := range n {
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
	b := &graph.Builder{Lat: make([]int32, n*n), Lon: make([]int32, n*n), Off: make([]uint32, uint32(n*n)+1)}
	for i := range n {
		for j := range n {
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
	path := filepath.Join(t.TempDir(), "iso.graph")
	if err := b.Write(path); err != nil {
		t.Fatalf("write: %v", err)
	}
	g, err := graph.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { g.Close() })
	mnLat, mnLon, mxLat, mxLon := g.Bounds()
	const pad = 0.05
	rg := &region{name: "iso", g: g, r: graph.NewRouter(g, 1),
		minLat: mnLat - pad, maxLat: mxLat + pad, minLon: mnLon - pad, maxLon: mxLon + pad}
	return &server{regions: []*region{rg}, maxSnapM: 3000, timeout: 5 * time.Second, m: &metrics{}}
}

func TestIsochroneHandler(t *testing.T) {
	s := isoServer(t)
	rec := httptest.NewRecorder()
	s.handleIsochrone(rec, httptest.NewRequest("GET", "/isochrone?lat=-6.1954&lon=106.8054&minutes=5", nil))
	if rec.Code != 200 {
		t.Fatalf("code %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Type     string `json:"type"`
		Features []struct {
			Geometry struct {
				Type        string         `json:"type"`
				Coordinates [][][2]float64 `json:"coordinates"`
			} `json:"geometry"`
		} `json:"features"`
		Meta struct {
			GridM float64 `json:"grid_m"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Type != "FeatureCollection" || len(body.Features) == 0 || body.Meta.GridM <= 0 {
		t.Fatalf("bad body: %+v", body)
	}
	for _, f := range body.Features {
		if f.Geometry.Type != "Polygon" || len(f.Geometry.Coordinates) == 0 {
			t.Fatalf("bad geometry: %+v", f.Geometry)
		}
		ring := f.Geometry.Coordinates[0]
		if ring[0] != ring[len(ring)-1] {
			t.Fatal("ring not closed")
		}
	}
}

func TestIsochroneHandlerErrors(t *testing.T) {
	s := isoServer(t)
	for _, tc := range []struct {
		url    string
		code   int
		reason string
	}{
		{"/isochrone?lat=-6.2&lon=106.8&minutes=0", 400, ""},
		{"/isochrone?lat=0&lon=0&minutes=5", 400, ""},
		{"/isochrone?lat=-6.2&lon=106.8&minutes=5&meters=100001", 400, ""},
	} {
		rec := httptest.NewRecorder()
		s.handleIsochrone(rec, httptest.NewRequest("GET", tc.url, nil))
		if rec.Code != tc.code {
			t.Fatalf("%s: code %d want %d: %s", tc.url, rec.Code, tc.code, rec.Body.String())
		}
	}
}
