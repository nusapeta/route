package main

import (
	"context"
	"encoding/json"
	"math"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"routeid/internal/graph"
)

// spurRegion serves a main line at -6.21 plus an isolated spur at -6.20 that is
// closer to the query point, which is the shape that used to answer 422: the
// nearest segment starts at a node with no way out.
func spurRegion(t *testing.T) *region {
	t.Helper()
	b := &graph.Builder{
		Lat: []int32{-62100000, -62100000, -62100000, -62000000, -62005000, -62100000},
		Lon: []int32{1068000000, 1068100000, 1068200000, 1068050000, 1068050000, 1068300000},
		Off: []uint32{0, 1, 2, 4, 4, 5, 6},
		To:  []uint32{1, 2, 1, 5, 3, 2},
		Len: []uint16{1100, 1100, 1100, 1100, 60, 1100},
		Cls: []uint8{60, 60, 60, 60, 60, 60},
	}
	b.BuildGrid(0.02)
	path := filepath.Join(t.TempDir(), "spur.graph")
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
		name: "spur", g: g, r: graph.NewRouter(g, 1),
		minLat: mnLat - pad, maxLat: mxLat + pad,
		minLon: mnLon - pad, maxLon: mxLon + pad,
	}
}

// TestRouteRetriesDeadEndSnap covers the Monas failure: the pin snaps to a dead
// end, the first search returns no route, and the retry from the next road over
// still answers 200 with the snapped geometry of that road.
func TestRouteRetriesDeadEndSnap(t *testing.T) {
	rg := spurRegion(t)
	m := &metrics{}
	s := &server{
		regions: []*region{rg}, maxSnapM: 3000, timeout: 5 * time.Second, m: m,
		fp: graphFingerprint([]*region{rg}, 0, 3000, 1.5, 0.25),
	}
	const query = "/route?from=-6.20025,106.805&to=-6.21,106.82"

	// The premise: the nearest snap alone cannot reach the destination.
	ctx := context.Background()
	fSnap, tSnap, ok := s.snapBoth(rg, -6.20025, 106.805, -6.21, 106.82)
	if !ok {
		t.Fatal("snapBoth found no road")
	}
	if _, err := rg.r.RouteSnapCtx(ctx, fSnap, tSnap, true); err == nil {
		t.Fatalf("first snap (node %d -> %d) routed; the test graph no longer models a dead end", fSnap.Node, tSnap.Node)
	}

	rec := httptest.NewRecorder()
	testHandler(s).ServeHTTP(rec, httptest.NewRequest("GET", query, nil))
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("code %d, want 200 (%s)", rec.Code, body)
	}
	var out struct {
		Distance float64 `json:"distance_m"`
		SnapFrom float64 `json:"snap_from_m"`
		SnapTo   float64 `json:"snap_to_m"`
		Region   string  `json:"region"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode body: %v (%s)", err, body)
	}
	if out.Region != "spur" {
		t.Errorf("region = %q, want spur", out.Region)
	}
	if out.SnapFrom < 1000 || out.SnapFrom > 1200 {
		t.Errorf("snap_from_m = %.0f, want the retried line at ~1.1 km", out.SnapFrom)
	}
	if out.SnapTo > 1 {
		t.Errorf("snap_to_m = %.0f, want the node it targets", out.SnapTo)
	}
	if out.Distance < 1000 {
		t.Errorf("distance_m = %.0f, want the ~1.1 km line", out.Distance)
	}
	if got := m.snapRetry.Load(); got != 1 {
		t.Errorf("snapRetry = %d, want 1", got)
	}

	// The counter is exported where an operator looks for refusals.
	rec = httptest.NewRecorder()
	m.handleMetrics(rec, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(rec.Body.String(), "route_snap_retry_total 1") {
		t.Error("metrics do not report route_snap_retry_total 1")
	}
}

// TestRouteNoRouteStaysRefused checks that a request the retry cannot save still
// answers 422 instead of looping through every candidate.
func TestRouteNoRouteStaysRefused(t *testing.T) {
	rg := spurRegion(t)
	m := &metrics{}
	s := &server{
		regions: []*region{rg}, maxSnapM: 3000, timeout: 5 * time.Second, m: m,
		fp: graphFingerprint([]*region{rg}, 0, 3000, 1.5, 0.25),
	}
	// Both ends on the spur, which has no exit.
	rec := httptest.NewRecorder()
	testHandler(s).ServeHTTP(rec, httptest.NewRequest("GET", "/route?from=-6.20025,106.805&to=-6.2,106.805", nil))
	if rec.Code != 200 {
		t.Fatalf("code %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	// A snapshot with no road at all still refuses.
	rec = httptest.NewRecorder()
	testHandler(s).ServeHTTP(rec, httptest.NewRequest("GET", "/route?from=-6.2,107.9&to=-6.21,106.82", nil))
	if rec.Code != 422 {
		t.Fatalf("code %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
}

// pocketRegion models the Monas case: a closed loop of a few roads around the
// pin that connects to nothing, with a long connected line 2 km away.
func pocketRegion(t *testing.T) *region {
	t.Helper()
	const lineN = 520
	const pocketN = 80
	lat := make([]int32, 0, lineN+pocketN)
	lon := make([]int32, 0, lineN+pocketN)
	to := make([]uint32, 0, lineN+pocketN)
	off := make([]uint32, 0, lineN+pocketN+1)
	length := make([]uint16, 0, lineN+pocketN)
	cls := make([]uint8, 0, lineN+pocketN)
	const lineLat, lineLon0 = -6.2200, 106.8000
	for i := 0; i < lineN; i++ {
		lat = append(lat, int32(lineLat*1e7))
		lon = append(lon, int32((lineLon0+float64(i)*0.0005)*1e7))
	}
	const pocketLat, pocketLon = -6.2002, 106.8050
	for i := 0; i < pocketN; i++ {
		a := 2 * math.Pi * float64(i) / pocketN
		lat = append(lat, int32((pocketLat+0.0003*math.Sin(a))*1e7))
		lon = append(lon, int32((pocketLon+0.0004*math.Cos(a))*1e7))
	}
	for i := 0; i < lineN; i++ {
		off = append(off, uint32(len(to)))
		if i < lineN-1 {
			to = append(to, uint32(i+1))
			length = append(length, 55)
			cls = append(cls, 60)
		}
	}
	for i := 0; i < pocketN; i++ {
		off = append(off, uint32(len(to)))
		to = append(to, uint32(lineN+(i+1)%pocketN))
		length = append(length, 3)
		cls = append(cls, 60)
	}
	off = append(off, uint32(len(to)))

	b := &graph.Builder{Lat: lat, Lon: lon, Off: off, To: to, Len: length, Cls: cls}
	b.BuildGrid(0.02)
	path := filepath.Join(t.TempDir(), "pocket.graph")
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
		name: "pocket", g: g, r: graph.NewRouter(g, 1),
		minLat: mnLat - pad, maxLat: mxLat + pad,
		minLon: mnLon - pad, maxLon: mxLon + pad,
	}
}

// TestRouteSkipsPocketStart covers the Monas shape: 80 of the nearest segments
// start in one closed pocket, more than a retry has attempts for, so the retry
// must exclude the whole pocket and reach the line 2 km away.
func TestRouteSkipsPocketStart(t *testing.T) {
	rg := pocketRegion(t)
	m := &metrics{}
	s := &server{
		regions: []*region{rg}, maxSnapM: 3000, timeout: 5 * time.Second, m: m,
		fp: graphFingerprint([]*region{rg}, 0, 3000, 1.5, 0.25),
	}
	rec := httptest.NewRecorder()
	testHandler(s).ServeHTTP(rec, httptest.NewRequest("GET", "/route?from=-6.2002,106.805&to=-6.22,106.95", nil))
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("code %d, want 200 (%s)", rec.Code, body)
	}
	var out struct {
		Distance float64 `json:"distance_m"`
		SnapFrom float64 `json:"snap_from_m"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode body: %v (%s)", err, body)
	}
	if out.SnapFrom < 2000 || out.SnapFrom > 2300 {
		t.Errorf("snap_from_m = %.0f, want the line at ~2.2 km", out.SnapFrom)
	}
	if out.Distance < 10000 {
		t.Errorf("distance_m = %.0f, want a route down the line", out.Distance)
	}
	if got := m.snapRetry.Load(); got != 1 {
		t.Errorf("snapRetry = %d, want 1", got)
	}
}
