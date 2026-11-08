package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"routeid/internal/graph"
)

// testRegion writes a three-node line around Jakarta and opens it, so handler
// tests exercise real snapping and routing instead of a stub.
func testRegion(t *testing.T) *region {
	t.Helper()
	b := &graph.Builder{
		Lat: []int32{-62000000, -62050000, -62100000},
		Lon: []int32{1068000000, 1068050000, 1068100000},
		Off: []uint32{0, 1, 2, 3},
		To:  []uint32{1, 2, 1},
		Len: []uint16{1000, 1000, 1000},
		Cls: []uint8{60, 60, 60},
	}
	b.BuildGrid(0.02)
	path := filepath.Join(t.TempDir(), "line.graph")
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
		name: "line", g: g, r: graph.NewRouter(g, 1),
		minLat: mnLat - pad, maxLat: mxLat + pad,
		minLon: mnLon - pad, maxLon: mxLon + pad,
	}
}

func testServer(t *testing.T, snapM float64) *server {
	t.Helper()
	rg := testRegion(t)
	return &server{
		regions:  []*region{rg},
		maxSnapM: snapM,
		timeout:  5 * time.Second,
		maxAge:   3600,
		m:        &metrics{},
		fp:       graphFingerprint([]*region{rg}, 0, snapM, 1.5, 0.25),
	}
}

func testHandler(s *server) http.Handler {
	return cors(gzipWrap(s.m.wrap(s.handleRoute)))
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
