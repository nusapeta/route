package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nusapeta/route/internal/graph"
)

// testRegion writes a three-node line around Jakarta and opens it, so handler
// tests exercise real snapping and routing instead of a stub.
func testRegion(t *testing.T) *region {
	t.Helper()
	return testRegionLine(t, "line", -62000000, 1068000000)
}

// testRegionLine builds the same three-node line around an arbitrary coordinate
// pair, so cross-region tests can place two disjoint graphs.
func testRegionLine(t *testing.T, name string, lat, lon int32) *region {
	t.Helper()
	b := &graph.Builder{
		Lat: []int32{lat, lat - 50000, lat - 100000},
		Lon: []int32{lon, lon + 50000, lon + 100000},
		Off: []uint32{0, 1, 2, 3},
		To:  []uint32{1, 2, 1},
		Len: []uint16{1000, 1000, 1000},
		Cls: []uint8{60, 60, 60},
	}
	b.BuildGrid(0.02)
	b.PBFHash = make([]byte, 32)
	for i := range b.PBFHash {
		b.PBFHash[i] = byte(i + 1)
	}
	b.BuildUnix = 1_700_000_000
	b.Meta = map[string]string{
		"source":         "line.osm.pbf",
		"extractor":      "test",
		"profile":        "car",
		"profile_sha256": strings.Repeat("a", 64),
	}
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

func testServer(t *testing.T, snapM float64) *server {
	t.Helper()
	rg := testRegion(t)
	return &server{
		regions:    []*region{rg},
		maxSnapM:   snapM,
		timeout:    5 * time.Second,
		budget:     3 * time.Second,
		maxAge:     3600,
		m:          &metrics{},
		matrixPool: newMatrixPool(1, 0),
		fp:         graphFingerprint([]*region{rg}, 0, snapM, 1.5, 0.25),
	}
}

func testHandler(s *server) http.Handler {
	return cors(gzipWrap(s.m.wrap(s.handleRoute)))
}

func TestHealthProfiles(t *testing.T) {
	s := testServer(t, 3000)
	rec := httptest.NewRecorder()
	s.handleHealth(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 200 {
		t.Fatalf("code %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Attribution string `json:"attribution"`
		DataLicense string `json:"data_license"`
		Regions     []struct {
			Profile   string `json:"profile"`
			ProfileID string `json:"profile_sha256"`
			PBFHash   string `json:"pbf_sha256"`
			BuiltAt   string `json:"built_at"`
		} `json:"regions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Attribution != "© OpenStreetMap contributors" || body.DataLicense != "ODbL 1.0" {
		t.Fatalf("attribution = %q / %q", body.Attribution, body.DataLicense)
	}
	if len(body.Regions) != 1 {
		t.Fatalf("regions = %d, want 1", len(body.Regions))
	}
	r := body.Regions[0]
	if r.Profile != "car" || r.ProfileID != strings.Repeat("a", 64) || r.PBFHash == "" || r.BuiltAt == "" {
		t.Fatalf("health provenance = %+v", r)
	}
}

const routeQuery = "/route?from=-6.2,106.8&to=-6.21,106.81"

// TestRouteCacheHeaders covers the 200/304 contract and the gzip wrapper:
// success carries ETag, max-age, and Vary; a revalidation answers 304 without
// Content-Encoding.
func TestRouteCacheHeaders(t *testing.T) {
	h := testHandler(testServer(t, 3000))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", routeQuery, nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(rec, req)
	res := rec.Result()
	if res.StatusCode != 200 {
		t.Fatalf("code %d, want 200 (%s)", res.StatusCode, rec.Body.String())
	}
	etag := res.Header.Get("ETag")
	if etag == "" {
		t.Fatal("200 must carry an ETag")
	}
	if got := res.Header.Get("Cache-Control"); got != "public, max-age=3600" {
		t.Fatalf("Cache-Control %q", got)
	}
	if got := res.Header.Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
		t.Fatalf("Vary %q, want Accept-Encoding", got)
	}
	if got := res.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding %q, want gzip", got)
	}

	// Matching validator: 304 with the validator, no encoding header.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", routeQuery, nil)
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("If-None-Match", etag)
	h.ServeHTTP(rec, req)
	res = rec.Result()
	if res.StatusCode != 304 {
		t.Fatalf("code %d, want 304", res.StatusCode)
	}
	if got := res.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("304 carries Content-Encoding %q", got)
	}
	if got := res.Header.Get("ETag"); got != etag {
		t.Fatalf("304 ETag %q, want %q", got, etag)
	}
	if got := res.Header.Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
		t.Fatalf("304 Vary %q, want Accept-Encoding", got)
	}

	// Non-matching validator: full answer again.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", routeQuery, nil)
	req.Header.Set("If-None-Match", `"nope"`)
	h.ServeHTTP(rec, req)
	if got := rec.Result().StatusCode; got != 200 {
		t.Fatalf("code %d, want 200", got)
	}
}

// TestRouteCanceled checks that a canceled request maps to 504 and that the
// error response is uncacheable.
func TestRouteCanceled(t *testing.T) {
	h := testHandler(testServer(t, 3000))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", routeQuery, nil).WithContext(ctx)
	h.ServeHTTP(rec, req)
	res := rec.Result()
	if res.StatusCode != 504 {
		t.Fatalf("code %d, want 504 (%s)", res.StatusCode, rec.Body.String())
	}
	if got := res.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control %q, want no-store", got)
	}
	if got := res.Header.Get("ETag"); got != "" {
		t.Fatalf("error response carries ETag %q", got)
	}
	if !strings.Contains(rec.Body.String(), "deadline exceeded") {
		t.Fatalf("body %q, want deadline message", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"reason":"deadline"`) {
		t.Fatalf("body %q, want deadline reason", rec.Body.String())
	}
}

// TestRouteBudgetExceeded checks the soft-budget contract: when the search
// gives up on its own the answer is a 422 the client can act on, not a 504.
func TestRouteBudgetExceeded(t *testing.T) {
	s := testServer(t, 3000)
	s.budget = time.Nanosecond
	h := testHandler(s)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", routeQuery, nil))
	res := rec.Result()
	if res.StatusCode != 422 {
		t.Fatalf("code %d, want 422 (%s)", res.StatusCode, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "search budget") {
		t.Fatalf("body %q, want budget message", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"reason":"budget"`) {
		t.Fatalf("body %q, want budget reason", rec.Body.String())
	}
	if got := res.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control %q, want no-store", got)
	}
}

// TestRouteNoRouteReason checks that an unreachable destination keeps the
// stable error text and adds no_route for the demo panel.
func TestRouteNoRouteReason(t *testing.T) {
	s := &server{timeout: 5 * time.Second, m: &metrics{}}
	rec := httptest.NewRecorder()
	s.failRoute(rec, graph.ErrNoRoute, 0)

	if rec.Code != 422 {
		t.Fatalf("code %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, `"error":"no route"`) {
		t.Fatalf("body %q, want stable error text", body)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"reason":"no_route"`) {
		t.Fatalf("body %q, want no_route reason", body)
	}
	if got := s.m.errNoRoute.Load(); got != 1 {
		t.Fatalf("errNoRoute = %d, want 1", got)
	}
}

// TestRouteCrossRegionReason checks the 422 path for two regions without a
// registered ferry connector. The reason lets the panel explain the problem
// without showing region names from the server.
func TestRouteCrossRegionReason(t *testing.T) {
	java := testRegionLine(t, "java", -62000000, 1068000000)
	sumatra := testRegionLine(t, "sumatra", -63000000, 1069000000)
	s := &server{
		regions:  []*region{java, sumatra},
		maxSnapM: 3000,
		timeout:  5 * time.Second,
		budget:   3 * time.Second,
		m:        &metrics{},
	}
	h := testHandler(s)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/route?from=-6.2,106.8&to=-6.3,106.9", nil))

	if rec.Code != 422 {
		t.Fatalf("code %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["reason"] != "cross_region" {
		t.Fatalf("reason %q, want cross_region", body["reason"])
	}
	if !strings.Contains(body["error"], "no ferry connector") {
		t.Fatalf("error %q, want stable connector text", body["error"])
	}
	if got := s.m.errCrossRegion.Load(); got != 1 {
		t.Fatalf("errCrossRegion = %d, want 1", got)
	}
}

// TestRouteRateLimited checks the 429 contract: Retry-After present, no
// validator, and no storeable cache headers.
func TestRouteRateLimited(t *testing.T) {
	s := testServer(t, 3000)
	s.rl = newLimiter(1, 1, false)
	h := testHandler(s)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", routeQuery, nil))
	if got := rec.Result().StatusCode; got != 200 {
		t.Fatalf("first request code %d, want 200", got)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", routeQuery, nil))
	res := rec.Result()
	if res.StatusCode != 429 {
		t.Fatalf("code %d, want 429", res.StatusCode)
	}
	if got := res.Header.Get("Retry-After"); got == "" {
		t.Fatal("429 must carry Retry-After")
	}
	if got := res.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control %q, want no-store", got)
	}
	if got := res.Header.Get("ETag"); got != "" {
		t.Fatalf("429 carries ETag %q", got)
	}
	if got := s.m.rateLimited.Load(); got != 1 {
		t.Fatalf("rateLimited = %d, want 1", got)
	}
}

// TestErrorWithoutReason keeps the old 400/429/500 contract: those paths use
// httpErr, so they must not grow a reason field.
func TestErrorWithoutReason(t *testing.T) {
	rec := httptest.NewRecorder()
	httpErr(rec, http.StatusBadRequest, "bad point")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code %d, want 400", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "bad point" {
		t.Fatalf("error %q, want bad point", body["error"])
	}
	if reason, ok := body["reason"]; ok {
		t.Fatalf("400 unexpectedly carries reason %q", reason)
	}
}

// TestFingerprintTracksSnap checks that the snap radius changes both the
// fingerprint and the ETag it prefixes.
func TestFingerprintTracksSnap(t *testing.T) {
	rg := testRegion(t)
	a := graphFingerprint([]*region{rg}, 40_000, 3000, 1.5, 0.25)
	b := graphFingerprint([]*region{rg}, 40_000, 2500, 1.5, 0.25)
	if a == b {
		t.Fatal("snap radius must change the fingerprint")
	}
	if a != graphFingerprint([]*region{rg}, 40_000, 3000, 1.5, 0.25) {
		t.Fatal("fingerprint must be stable for the same settings")
	}

	s1 := testServer(t, 3000)
	s2 := testServer(t, 3000)
	// Same graph content, different snap radius: tags must differ.
	s2.fp = graphFingerprint(s2.regions, 0, 2500, 1.5, 0.25)
	tag1 := routeETagFrom(t, testHandler(s1))
	tag2 := routeETagFrom(t, testHandler(s2))
	if tag1 == tag2 {
		t.Fatalf("ETag did not change with -snap: %s", tag1)
	}
}

func routeETagFrom(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", routeQuery, nil))
	if got := rec.Result().StatusCode; got != 200 {
		t.Fatalf("code %d, want 200 (%s)", got, rec.Body.String())
	}
	tag := rec.Result().Header.Get("ETag")
	if tag == "" {
		t.Fatal("missing ETag")
	}
	return tag
}

// TestSimplifyValidation checks that a non-numeric or negative simplify value
// is a 400 like a bad point, not a silently ignored parameter.
func TestSimplifyValidation(t *testing.T) {
	bad := []string{"abc", "-5", "NaN", "Inf"}
	for _, v := range bad {
		req := httptest.NewRequest("GET", routeQuery+"&simplify="+v, nil)
		if _, err := readRouteParams(req); err == nil {
			t.Fatalf("simplify=%s accepted", v)
		}
		rec := httptest.NewRecorder()
		testHandler(&server{m: &metrics{}}).ServeHTTP(rec, req)
		if got := rec.Result().StatusCode; got != 400 {
			t.Fatalf("simplify=%s code %d, want 400", v, got)
		}
		if got := rec.Result().Header.Get("Cache-Control"); got != "no-store" {
			t.Fatalf("simplify=%s Cache-Control %q, want no-store", v, got)
		}
		if got := rec.Result().Header.Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
			t.Fatalf("simplify=%s Vary %q, want Accept-Encoding", v, got)
		}
	}
	req := httptest.NewRequest("POST", "/route", strings.NewReader(`{"from":"-6.2,106.8","to":"-6.21,106.81","simplify":-1}`))
	if _, err := readRouteParams(req); err == nil {
		t.Fatal("negative POST simplify accepted")
	}
	req = httptest.NewRequest("GET", routeQuery+"&simplify=12.5", nil)
	p, err := readRouteParams(req)
	if err != nil || p.simplify != 12.5 {
		t.Fatalf("simplify=12.5: %v, %v", p.simplify, err)
	}
}

// TestSimplifyIndexMap checks the index bookkeeping that keeps ferry spans
// pointing at the same road after Douglas-Peucker drops points.
func TestSimplifyIndexMap(t *testing.T) {
	pts := [][2]float64{{0, 0}, {0, 1}, {0, 2}, {0, 3}, {0, 4}}
	out, kept := simplifyIndexed(pts, 10)
	if len(out) != 2 || kept[0] != 0 || kept[1] != 4 {
		t.Fatalf("kept %v (out %d), want [0 4]", kept, len(out))
	}
	if a, b, ok := projectSpan(kept, 1, 4); !ok || a != 1 || b != 1 {
		t.Fatalf("span 1..4 -> %d..%d ok=%v", a, b, ok)
	}
	if _, _, ok := projectSpan(kept, 1, 3); ok {
		t.Fatal("span with no surviving point must be dropped")
	}
	if _, _, ok := projectSpan(kept, 9, 12); ok {
		t.Fatal("span past the geometry must be dropped")
	}
}

// TestFerrySpanIndexing routes over a graph whose middle edge is a ferry and
// checks that ferry_spans still index the returned geometry after the Head
// offset, the pin point, and Douglas-Peucker. A span must cover the two nodes
// of the crossing, never a land segment.
func TestFerrySpanIndexing(t *testing.T) {
	b := &graph.Builder{
		Lat:   []int32{-62000000, -62000000, -62000000, -62000000},
		Lon:   []int32{1068000000, 1068100000, 1068200000, 1068300000},
		Off:   []uint32{0, 1, 2, 3, 3},
		To:    []uint32{1, 2, 3},
		Len:   []uint16{1000, 1000, 1000},
		Cls:   []uint8{60, 60, 60},
		Ferry: []uint32{1}, // the 1 -> 2 crossing
	}
	b.BuildGrid(0.02)
	path := filepath.Join(t.TempDir(), "ferry.graph")
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
	rg := &region{name: "ferry", g: g, r: graph.NewRouter(g, 1),
		minLat: mnLat - pad, maxLat: mxLat + pad, minLon: mnLon - pad, maxLon: mxLon + pad}
	s := &server{regions: []*region{rg}, maxSnapM: 3000, timeout: 5 * time.Second,
		maxAge: 3600, m: &metrics{}, fp: graphFingerprint([]*region{rg}, 0, 3000, 1.5, 0.25)}
	h := testHandler(s)

	ferryFrom := [2]float64{-6.2, 106.81}
	ferryTo := [2]float64{-6.2, 106.82}
	// The second query taps the middle of the first segment, so the path leaves
	// along it and RouteSnap trims the source node from the geometry.
	const q = "/route?from=-6.2,106.8&to=-6.2,106.83"
	const qTrim = "/route?from=-6.2,106.8005&to=-6.2,106.83"

	for _, c := range []struct{ name, query string }{
		{"plain", q},
		{"trimmed", qTrim},
		{"simplify", q + "&simplify=500"},
	} {
		simplify := c.name
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", c.query, nil))
		if rec.Code != 200 {
			t.Fatalf("%s: code %d (%s)", c.name, rec.Code, rec.Body.String())
		}
		var body struct {
			Points     [][2]float64 `json:"points"`
			FerrySpans [][2]int     `json:"ferry_spans"`
			FerryM     float64      `json:"ferry_m"`
			Trimmed    struct {
				From bool `json:"from"`
			} `json:"trimmed"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body.FerryM == 0 {
			t.Fatalf("simplify=%q: ferry_m = 0", simplify)
		}
		// A dense simplification of a straight line keeps only its ends, so the
		// crossing can lose every sample and the span is dropped; that is legal.
		// Whatever survives must still index real points.
		for _, sp := range body.FerrySpans {
			a, b := sp[0], sp[1]
			if a < 0 || b >= len(body.Points) || a > b {
				t.Fatalf("simplify=%q: span %d..%d outside %d points", simplify, a, b, len(body.Points))
			}
		}
		if c.name == "trimmed" && !body.Trimmed.From {
			t.Fatalf("trimmed: geometry was not trimmed at the source")
		}
		if simplify != "simplify" {
			if len(body.FerrySpans) != 1 {
				t.Fatalf("unsimplified: spans = %v, want one", body.FerrySpans)
			}
			if a, b := body.FerrySpans[0][0], body.FerrySpans[0][1]; body.Points[a] != ferryFrom || body.Points[b] != ferryTo {
				t.Fatalf("span %d..%d covers %v..%v, want %v..%v",
					a, b, body.Points[a], body.Points[b], ferryFrom, ferryTo)
			}
		}
	}
}

// testRegionDiamond gives two disjoint paths between the endpoints, so avoid
// and alt can be checked through the HTTP response rather than a stub.
func testRegionDiamond(t *testing.T) *region {
	t.Helper()
	const lat, lon = -62_000_000, 1_068_000_000
	b := &graph.Builder{
		Lat: []int32{lat, lat - 50_000, lat - 50_000, lat - 100_000},
		Lon: []int32{lon, lon + 50_000, lon - 50_000, lon},
		Off: []uint32{0, 2, 4, 6, 8},
		To:  []uint32{1, 2, 3, 0, 3, 0, 1, 2},
		Len: []uint16{1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000},
		Cls: []uint8{60, 60, 180, 60, 60, 60, 180, 60},
	}
	b.BuildGrid(0.02)
	b.PBFHash = make([]byte, 32)
	b.BuildUnix = 1_700_000_000
	b.Meta = map[string]string{"source": "diamond.osm.pbf", "extractor": "test", "profile": "car"}
	path := filepath.Join(t.TempDir(), "diamond.graph")
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
		name: "diamond", g: g, r: graph.NewRouter(g, 1),
		minLat: mnLat - pad, maxLat: mxLat + pad,
		minLon: mnLon - pad, maxLon: mxLon + pad,
	}
}

func testDiamondServer(t *testing.T) *server {
	t.Helper()
	rg := testRegionDiamond(t)
	return &server{
		regions: []*region{rg}, maxSnapM: 3000,
		timeout: 5 * time.Second, budget: 3 * time.Second, maxAge: 3600,
		m: &metrics{}, fp: graphFingerprint([]*region{rg}, 0, 3000, 1.5, 0.25),
	}
}

func decodeRoute(t *testing.T, h http.Handler, query string) map[string]json.RawMessage {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", query, nil))
	if rec.Code != 200 {
		t.Fatalf("code %d for %s: %s", rec.Code, query, rec.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body
}

func TestRouteAvoidToll(t *testing.T) {
	s := testDiamondServer(t)
	base := decodeRoute(t, testHandler(s), "/route?from=-6.2,106.8&to=-6.21,106.8")
	avoid := decodeRoute(t, testHandler(s), "/route?from=-6.2,106.8&to=-6.21,106.8&avoid=toll")
	var basePts, avoidPts [][2]float64
	json.Unmarshal(base["points"], &basePts)
	json.Unmarshal(avoid["points"], &avoidPts)
	tollNode := [2]float64{-6.205, 106.805}
	found := false
	for _, p := range basePts {
		if p == tollNode {
			found = true
		}
	}
	if !found {
		t.Fatalf("base route skipped the toll branch: %v", basePts)
	}
	for _, p := range avoidPts {
		if p == tollNode {
			t.Fatalf("avoid=toll still used the toll branch: %v", avoidPts)
		}
	}
	if s.m.expansions.Load() == 0 {
		t.Fatal("avoid route recorded no expansions in metrics")
	}
}

func TestRouteAlternatives(t *testing.T) {
	s := testDiamondServer(t)
	body := decodeRoute(t, testHandler(s), "/route?from=-6.2,106.8&to=-6.21,106.8&alt=1")
	var alts []struct {
		DistanceM uint32       `json:"distance_m"`
		Points    [][2]float64 `json:"points"`
	}
	if err := json.Unmarshal(body["alternatives"], &alts); err != nil {
		t.Fatalf("decode alternatives: %v", err)
	}
	if len(alts) != 1 {
		t.Fatalf("alternatives = %d, want 1", len(alts))
	}
	var primary [][2]float64
	json.Unmarshal(body["points"], &primary)
	if fmt.Sprint(alts[0].Points) == fmt.Sprint(primary) {
		t.Fatal("alternative geometry identical to primary")
	}
}

func TestRouteViaLegs(t *testing.T) {
	s := testServer(t, 3000)
	body := decodeRoute(t, testHandler(s), "/route?from=-6.2,106.8&to=-6.21,106.81&via=-6.205,106.805")
	var legs []struct {
		DistanceM uint32 `json:"distance_m"`
		StartIdx  int    `json:"start_index"`
		EndIdx    int    `json:"end_index"`
	}
	if err := json.Unmarshal(body["legs"], &legs); err != nil {
		t.Fatalf("decode legs: %v", err)
	}
	if len(legs) != 2 {
		t.Fatalf("legs = %d, want 2", len(legs))
	}
	var total uint32
	json.Unmarshal(body["distance_m"], &total)
	var sum uint32
	for i, leg := range legs {
		sum += leg.DistanceM
		if leg.StartIdx < 0 || leg.EndIdx < leg.StartIdx {
			t.Fatalf("leg %d has invalid indexes %d..%d", i, leg.StartIdx, leg.EndIdx)
		}
	}
	if diff := int64(sum) - int64(total); diff < -1 || diff > 1 {
		t.Fatalf("leg sum %d != total %d", sum, total)
	}
}

func TestNearestHandler(t *testing.T) {
	s := testServer(t, 3000)
	rec := httptest.NewRecorder()
	http.HandlerFunc(s.handleNearest).ServeHTTP(rec, httptest.NewRequest("GET", "/nearest?lat=-6.2&lon=106.8&k=2", nil))
	if rec.Code != 200 {
		t.Fatalf("code %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Candidates []struct {
			DistanceM float64 `json:"distance_m"`
			Node      uint32  `json:"node"`
			Edge      uint32  `json:"edge"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want, ok := s.regions[0].r.SnapEdge(-6.2, 106.8, 3000)
	if !ok || len(body.Candidates) == 0 {
		t.Fatalf("no candidates: ok=%v body=%s", ok, rec.Body.String())
	}
	if body.Candidates[0].Node != want.Node || body.Candidates[0].Edge != want.Edge {
		t.Fatalf("first candidate = %+v, want node %d edge %d", body.Candidates[0], want.Node, want.Edge)
	}
	if s.m.expansions.Load() != 0 {
		t.Fatal("/nearest ran a graph search")
	}
}

func TestControlParamValidation(t *testing.T) {
	for _, query := range []string{
		"/route?from=-6.2,106.8&to=-6.21,106.81&alt=4",
		"/route?from=-6.2,106.8&to=-6.21,106.81&avoid=tol",
		"/route?from=-6.2,106.8&to=-6.21,106.81&via=-6.2,106.8|-6.2,106.8|-6.2,106.8|-6.2,106.8|-6.2,106.8|-6.2,106.8",
	} {
		rec := httptest.NewRecorder()
		testHandler(testServer(t, 3000)).ServeHTTP(rec, httptest.NewRequest("GET", query, nil))
		if rec.Code != 400 {
			t.Fatalf("code %d for %s, want 400", rec.Code, query)
		}
	}
}

func TestETagIncludesControls(t *testing.T) {
	s := testServer(t, 3000)
	p := routeParams{fLat: -6.2, fLon: 106.8, tLat: -6.21, tLon: 106.81}
	base := routeETag(s.fp, p)
	p2 := p
	p2.avoid = graph.AvoidToll
	p2.alt = 2
	p2.via = [][2]float64{{-6.205, 106.805}}
	if got := routeETag(s.fp, p2); got == base {
		t.Fatal("avoid/alt/via did not change the ETag")
	}
}
