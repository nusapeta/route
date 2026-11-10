package main

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"routeid/internal/graph"
)

// lineRegion builds a tiny two-way line at a fixed latitude. Ports and query
// points sit on it, so the test exercises real snapping and leg routing.
func lineRegion(t *testing.T, name string, lat, lon float64) *region {
	t.Helper()
	const n = 6
	b := &graph.Builder{
		Lat: make([]int32, n),
		Lon: make([]int32, n),
		Off: make([]uint32, n+1),
		Cls: make([]uint8, 0, 2*(n-1)),
		Len: make([]uint16, 0, 2*(n-1)),
	}
	for i := range n {
		b.Lat[i] = int32(lat * 1e7)
		b.Lon[i] = int32((lon + 0.01*float64(i)) * 1e7)
	}
	for i := range n - 1 {
		b.To = append(b.To, uint32(i+1), uint32(i))
		b.Cls = append(b.Cls, 60, 60)
		b.Len = append(b.Len, 1000, 1000)
	}
	for i := range b.Off {
		b.Off[i] = uint32(2 * i)
		if b.Off[i] > uint32(len(b.To)) {
			b.Off[i] = uint32(len(b.To))
		}
	}
	b.BuildGrid(0.02)
	path := filepath.Join(t.TempDir(), name+".graph")
	if err := b.Write(path); err != nil {
		t.Fatalf("write graph: %v", err)
	}
	g, err := graph.Open(path)
	if err != nil {
		t.Fatalf("open graph: %v", err)
	}
	t.Cleanup(func() { g.Close() })
	mnLat, mnLon, mxLat, mxLon := g.Bounds()
	const pad = 0.05
	return &region{
		name: name, g: g, r: graph.NewRouter(g, 1),
		minLat: mnLat - pad, maxLat: mxLat + pad,
		minLon: mnLon - pad, maxLon: mxLon + pad,
	}
}

func stitchServer(t *testing.T, connectors []ferryConnector) *server {
	t.Helper()
	west := lineRegion(t, "west", -6, 106)
	east := lineRegion(t, "east", -6, 107)
	return &server{
		regions:    []*region{west, east},
		connectors: connectors,
		maxSnapM:   3000,
		timeout:    5 * time.Second,
		maxAge:     3600,
		m:          &metrics{},
		fp:         graphFingerprint([]*region{west, east}, 0, 3000, 1.5, 0.25),
	}
}

func testConnector() ferryConnector {
	return ferryConnector{
		name:       "Test-Strait",
		a:          ferryPort{"WestPort", -6, 106.02},
		b:          ferryPort{"EastPort", -6, 107.01},
		regionA:    []string{"west"},
		regionB:    []string{"east"},
		distanceM:  50_000,
		durationDs: 36_000,
	}
}

// TestCrossRegionStitchesLegs checks the full path: west road -> ferry -> east
// road, with metrics that add up and spans that index the returned geometry.
func TestCrossRegionStitchesLegs(t *testing.T) {
	s := stitchServer(t, []ferryConnector{testConnector()})
	h := testHandler(s)
	const q = "/route?from=-6,106.001&to=-6,107.059"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", q, nil))
	if rec.Code != 200 {
		t.Fatalf("code %d (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		DistanceM  uint32       `json:"distance_m"`
		DurationS  float64      `json:"duration_s"`
		Region     string       `json:"region"`
		FerryM     uint32       `json:"ferry_m"`
		FerrySpans [][2]int     `json:"ferry_spans"`
		Points     [][2]float64 `json:"points"`
		Legs       []struct {
			Mode       string  `json:"mode"`
			Region     string  `json:"region"`
			Name       string  `json:"name"`
			DistanceM  uint32  `json:"distance_m"`
			DurationS  float64 `json:"duration_s"`
			PointStart int     `json:"point_start"`
			PointEnd   int     `json:"point_end"`
		} `json:"legs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Region != "west+east" {
		t.Fatalf("region = %q, want west+east", body.Region)
	}
	if len(body.Legs) != 3 {
		t.Fatalf("legs = %d, want 3", len(body.Legs))
	}
	wantModes := []string{"road", "ferry", "road"}
	for i, want := range wantModes {
		if body.Legs[i].Mode != want {
			t.Fatalf("leg %d mode = %q, want %q", i, body.Legs[i].Mode, want)
		}
	}
	if body.Legs[1].Name != "Test-Strait" || body.Legs[1].DistanceM != 50_000 {
		t.Fatalf("ferry leg = %+v", body.Legs[1])
	}
	var sumM uint32
	var sumDs uint32
	for _, leg := range body.Legs {
		sumM += leg.DistanceM
		sumDs += uint32(leg.DurationS * 10)
		if leg.PointStart < 0 || leg.PointEnd < leg.PointStart || leg.PointEnd >= len(body.Points) {
			t.Fatalf("leg range %d..%d outside %d points", leg.PointStart, leg.PointEnd, len(body.Points))
		}
	}
	if sumM != body.DistanceM {
		t.Fatalf("legs distance %d != total %d", sumM, body.DistanceM)
	}
	if sumDs != uint32(body.DurationS*10) {
		t.Fatalf("legs duration %d != total %d", sumDs, uint32(body.DurationS*10))
	}
	if body.FerryM != 50_000 {
		t.Fatalf("ferry_m = %d, want 50000", body.FerryM)
	}
	if len(body.FerrySpans) != 1 {
		t.Fatalf("ferry_spans = %v, want one", body.FerrySpans)
	}
	sp := body.FerrySpans[0]
	if sp[0] < 0 || sp[1] >= len(body.Points) || sp[0] >= sp[1] {
		t.Fatalf("ferry span %v outside %d points", sp, len(body.Points))
	}
	if body.Points[sp[0]] == body.Points[sp[1]] {
		t.Fatalf("ferry span has no length: %v", sp)
	}
	if got := rec.Header().Get("X-Route-Region"); got != "west+east" {
		t.Fatalf("X-Route-Region = %q", got)
	}
	if got := s.m.crossRegionStitched.Load(); got != 1 {
		t.Fatalf("stitched counter = %d, want 1", got)
	}
}

// TestCrossRegionUnlistedPair keeps the coverage boundary explicit: without a
// connector, the old 422 remains.
func TestCrossRegionUnlistedPair(t *testing.T) {
	s := stitchServer(t, nil)
	h := testHandler(s)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/route?from=-6,106.001&to=-6,107.059", nil))
	if rec.Code != 422 {
		t.Fatalf("code %d (%s)", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "no ferry connector between west and east") {
		t.Fatalf("body %q", body)
	}
	if got := s.m.errCrossRegion.Load(); got != 1 {
		t.Fatalf("errCrossRegion = %d, want 1", got)
	}
}

// TestCrossRegionSimplified keeps ferry spans inside the simplified point
// array and drops no leg without a clear metric.
func TestCrossRegionSimplified(t *testing.T) {
	s := stitchServer(t, []ferryConnector{testConnector()})
	h := testHandler(s)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/route?from=-6,106.001&to=-6,107.059&simplify=500", nil))
	if rec.Code != 200 {
		t.Fatalf("code %d (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Points     [][2]float64 `json:"points"`
		FerrySpans [][2]int     `json:"ferry_spans"`
		Legs       []struct {
			PointStart int `json:"point_start"`
			PointEnd   int `json:"point_end"`
		} `json:"legs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, sp := range body.FerrySpans {
		if sp[0] < 0 || sp[1] >= len(body.Points) || sp[0] > sp[1] {
			t.Fatalf("span %v outside %d points", sp, len(body.Points))
		}
	}
	for i, leg := range body.Legs {
		if leg.PointEnd < leg.PointStart || leg.PointEnd >= len(body.Points) {
			t.Fatalf("leg %d range %d..%d outside %d points", i, leg.PointStart, leg.PointEnd, len(body.Points))
		}
	}
}

// TestCrossRegionPinned checks the pin offset used by ferry spans and leg
// ranges: the raw start pin is point 0, so the first road leg starts at 1.
func TestCrossRegionPinned(t *testing.T) {
	s := stitchServer(t, []ferryConnector{testConnector()})
	h := testHandler(s)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/route?from=-6.001,106.001&to=-6,107.059&pin=1", nil))
	if rec.Code != 200 {
		t.Fatalf("code %d (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Points [][2]float64 `json:"points"`
		Legs   []struct {
			PointStart int `json:"point_start"`
		} `json:"legs"`
		FerrySpans [][2]int `json:"ferry_spans"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Points) == 0 || body.Points[0] != [2]float64{-6.001, 106.001} {
		t.Fatalf("first point = %v, want the raw pin", body.Points[0])
	}
	if len(body.Legs) != 3 || body.Legs[0].PointStart != 1 {
		t.Fatalf("first leg start = %+v, want 1", body.Legs)
	}
	for _, sp := range body.FerrySpans {
		if sp[0] < 0 || sp[1] >= len(body.Points) || sp[0] > sp[1] {
			t.Fatalf("span %v outside %d points", sp, len(body.Points))
		}
	}
}
