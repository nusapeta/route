package main

import (
	"encoding/json"
	"math"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nusapeta/route/internal/graph"
)

const (
	matrixTestLat = -6.2
	matrixTestLon = 106.8
	matrixGridD   = 0.0012
)

func matrixGrid(t *testing.T) *region {
	t.Helper()
	const n = 30
	type edge struct {
		to    uint32
		len4  uint16
		class uint8
	}
	adj := make([][]edge, n*n)
	latOf := func(i int) int32 { return int32(math.Round((matrixTestLat + float64(i)*matrixGridD) * graph.E7)) }
	lonOf := func(j int) int32 { return int32(math.Round((matrixTestLon + float64(j)*matrixGridD) * graph.E7)) }
	add := func(a, c uint32) {
		la, lo := latOf(int(a)/n), lonOf(int(a)%n)
		lc, oc := latOf(int(c)/n), lonOf(int(c)%n)
		m := math.Round(graph.Haversine(float64(la)/graph.E7, float64(lo)/graph.E7, float64(lc)/graph.E7, float64(oc)/graph.E7))
		len4 := uint16(math.Max(1, math.Round(m/graph.LenUnit)))
		class := uint8(math.Round(8 * 3.6 * graph.SpeedClassScale))
		adj[a] = append(adj[a], edge{c, len4, class})
	}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			a := uint32(i*n + j)
			if i+1 < n {
				add(a, uint32((i+1)*n+j))
				add(uint32((i+1)*n+j), a)
			}
			if j+1 < n {
				add(a, uint32(i*n+j+1))
				add(uint32(i*n+j+1), a)
			}
		}
	}
	b := &graph.Builder{
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
	path := filepath.Join(t.TempDir(), "jakarta.graph")
	if err := b.Write(path); err != nil {
		t.Fatalf("write graph: %v", err)
	}
	g, err := graph.Open(path)
	if err != nil {
		t.Fatalf("open graph: %v", err)
	}
	t.Cleanup(func() { g.Close() })
	mnLat, mnLon, mxLat, mxLon := g.Bounds()
	return &region{
		name: "jakarta", g: g, r: graph.NewRouter(g, 1),
		minLat: mnLat - 0.05, maxLat: mxLat + 0.05,
		minLon: mnLon - 0.05, maxLon: mxLon + 0.05,
	}
}

func matrixTestServer(t *testing.T) *server {
	t.Helper()
	rg := matrixGrid(t)
	return &server{
		regions:    []*region{rg},
		maxSnapM:   3000,
		timeout:    5 * time.Second,
		maxAge:     3600,
		fp:         "matrix-fingerprint",
		m:          &metrics{},
		matrixPool: newMatrixPool(1, 0),
	}
}

func matrixRequest(s *server, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.handleMatrix(rec, httptest.NewRequest("GET", target, nil))
	return rec
}

func matrixDecode(t *testing.T, rec *httptest.ResponseRecorder) matrixResponse {
	t.Helper()
	var resp matrixResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return resp
}

func TestMatrixGolden(t *testing.T) {
	s := matrixTestServer(t)
	uri := "/matrix?sources=-6.2,106.8&targets=-6.2,106.8012|-6.1988,106.8024|-6.1964,106.8048"
	rec := matrixRequest(s, uri)
	if rec.Code != 200 {
		t.Fatalf("code %d: %s", rec.Code, rec.Body.String())
	}
	got := matrixDecode(t, rec)
	if len(got.Matrix) != 1 || len(got.Matrix[0]) != 3 {
		t.Fatalf("matrix shape %d x %d", len(got.Matrix), len(got.Matrix[0]))
	}
	for i, cell := range got.Matrix[0] {
		if cell.Status != cellOK || cell.DistanceM <= 0 || cell.DurationS <= 0 {
			t.Fatalf("cell %d = %+v", i, cell)
		}
	}

	path := filepath.Join("..", "..", "testdata", "matrix-jakarta.json")
	if os.Getenv("UPDATE_MATRIX_FIXTURE") == "1" {
		fixture := map[string]any{"uri": uri, "response": got}
		raw, err := json.MarshalIndent(fixture, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fixture struct {
		URI      string         `json:"uri"`
		Response matrixResponse `json:"response"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if fixture.URI != uri {
		t.Fatalf("fixture uri %q != %q", fixture.URI, uri)
	}
	b, _ := json.Marshal(fixture.Response.Matrix)
	a, _ := json.Marshal(got.Matrix)
	if string(a) != string(b) {
		t.Fatalf("matrix drift:\n got %s\nwant %s", a, b)
	}
}

func TestMatrixValidation(t *testing.T) {
	src := "-6.2,106.8"
	target := "-6.2,106.8012"
	cases := []struct {
		name, uri, reason string
	}{
		{"no sources", "/matrix?targets=" + target, ""},
		{"too many sources", "/matrix?sources=" + strings.TrimSuffix(strings.Repeat(src+"|", 6), "|") + "&targets=" + target, reasonMatrixTooLarge},
		{"too many targets", "/matrix?sources=" + src + "&targets=" + strings.TrimSuffix(strings.Repeat(target+"|", 26), "|"), reasonTooManyTargets},
		{"product too large", "/matrix?sources=" + strings.TrimSuffix(strings.Repeat(src+"|", 5), "|") + "&targets=" + strings.TrimSuffix(strings.Repeat(target+"|", 6), "|"), reasonMatrixTooLarge},
		{"cutoff", "/matrix?sources=" + src + "&targets=" + target + "&max_minutes=181", reasonCutoffTooLarge},
	}
	s := matrixTestServer(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := matrixRequest(s, tc.uri)
			if rec.Code != 400 {
				t.Fatalf("code %d: %s", rec.Code, rec.Body.String())
			}
			if tc.reason == "" {
				return
			}
			var body struct {
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Reason != tc.reason {
				t.Fatalf("reason %q, want %q", body.Reason, tc.reason)
			}
		})
	}
}

func TestMatrixSnap(t *testing.T) {
	s := matrixTestServer(t)
	rec := matrixRequest(s, "/matrix?sources=-7.0,110.0&targets=-7.1,110.1")
	if rec.Code != 422 {
		t.Fatalf("all-snap-fail code %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Reason != reasonSnap {
		t.Fatalf("reason %+v: %s", body, rec.Body.String())
	}

	rec = matrixRequest(s, "/matrix?sources=-6.2,106.8&targets=-6.2,106.8012|-7.0,110.0")
	if rec.Code != 200 {
		t.Fatalf("partial snap code %d: %s", rec.Code, rec.Body.String())
	}
	cells := matrixDecode(t, rec).Matrix[0]
	if cells[0].Status != cellOK || cells[1].Status != cellSnapFailed {
		t.Fatalf("partial snap cells %+v", cells)
	}
}

func TestMatrixTooFar(t *testing.T) {
	s := matrixTestServer(t)
	s.regions = append(s.regions, testRegionLine(t, "jawa-tengah", -72000000, 1072000000))
	rec := matrixRequest(s, "/matrix?sources=-6.2,106.8&targets=-7.2,107.2")
	if rec.Code != 200 {
		t.Fatalf("code %d: %s", rec.Code, rec.Body.String())
	}
	cell := matrixDecode(t, rec).Matrix[0][0]
	if cell.Status != reasonTooFar {
		t.Fatalf("cell %+v", cell)
	}
}

func TestMatrixETagAndRateLimit(t *testing.T) {
	s := matrixTestServer(t)
	uri := "/matrix?sources=-6.2,106.8&targets=-6.2,106.8012"
	rec := matrixRequest(s, uri)
	if rec.Code != 200 {
		t.Fatalf("code %d: %s", rec.Code, rec.Body.String())
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}
	req := httptest.NewRequest("GET", uri, nil)
	req.Header.Set("If-None-Match", etag)
	rec = httptest.NewRecorder()
	s.handleMatrix(rec, req)
	if rec.Code != 304 {
		t.Fatalf("revalidation code %d", rec.Code)
	}

	s.rl = newLimiter(1, 1, false)
	if rec := matrixRequest(s, "/matrix?sources=-6.2,106.8&targets=-6.1988,106.8024"); rec.Code != 200 {
		t.Fatalf("first request code %d: %s", rec.Code, rec.Body.String())
	}
	rec = matrixRequest(s, "/matrix?sources=-6.2,106.8&targets=-6.1964,106.8048")
	if rec.Code != 429 {
		t.Fatalf("rate limit code %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMatrixDeadline(t *testing.T) {
	s := matrixTestServer(t)
	s.timeout = time.Nanosecond
	rec := matrixRequest(s, "/matrix?sources=-6.2,106.8&targets=-6.2,106.8012")
	if rec.Code != 504 {
		t.Fatalf("code %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Reason != reasonDeadline {
		t.Fatalf("reason %+v: %s", body, rec.Body.String())
	}
}

func TestMatrixSearchReusesScratch(t *testing.T) {
	s := matrixTestServer(t)
	g := s.regions[0].g
	src, ok := s.regions[0].r.SnapEdge(matrixTestLat, matrixTestLon, s.maxSnapM)
	if !ok {
		t.Fatal("snap source")
	}
	targets := []matrixTarget{{node: src.Node}}
	for i := range 1000 {
		hits, _, _, err := matrixOneToMany(t.Context(), g, false, src, targets, 600, s.matrixPool)
		if err != nil || !hits[0].ok {
			t.Fatalf("iteration %d: %+v %v", i, hits, err)
		}
	}
}
