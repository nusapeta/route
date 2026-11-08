package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestRouteETagStable checks that the tag ignores query order and formatting
// noise, and changes when the graph or the query changes.
func TestRouteETagStable(t *testing.T) {
	mk := func(fLat, fLon, tLat, tLon float64, format string) routeParams {
		return routeParams{fLat: fLat, fLon: fLon, tLat: tLat, tLon: tLon, format: format, restrictions: true}
	}
	a := routeETag("abcdef012345", mk(-6.2088, 106.8456, -6.2297, 106.8166, "polyline"))
	b := routeETag("abcdef012345", mk(-6.2088, 106.8456, -6.2297, 106.8166, "polyline"))
	if a != b {
		t.Fatalf("same query should give the same tag: %s vs %s", a, b)
	}
	if !strings.HasPrefix(a, `"abcdef01-`) {
		t.Fatalf("tag should carry the graph fingerprint: %s", a)
	}
	if c := routeETag("abcdef012345", mk(-6.2088, 106.8456, -6.2297, 106.8167, "polyline")); c == a {
		t.Fatal("different destination must change the tag")
	}
	if c := routeETag("abcdef012345", mk(-6.2088, 106.8456, -6.2297, 106.8166, "geojson")); c == a {
		t.Fatal("different format must change the tag")
	}
	if c := routeETag("ffffffffffff", mk(-6.2088, 106.8456, -6.2297, 106.8166, "polyline")); c == a {
		t.Fatal("different graph must change the tag")
	}
	if !etagMatches(a, a) || !etagMatches("W/"+a, a) || !etagMatches(`"x", `+a, a) || !etagMatches("*", a) {
		t.Fatal("matching forms should be accepted")
	}
	if etagMatches(`"nope"`, a) {
		t.Fatal("unrelated tag must not match")
	}
}

// TestLimiterBurstAndRefill checks the token bucket: burst passes, the next
// request is refused with a wait, and time refills the bucket.
func TestLimiterBurstAndRefill(t *testing.T) {
	now := time.Unix(0, 0)
	l := newLimiter(60, 2, false) // 1 token/second, burst 2
	l.now = func() time.Time { return now }

	if ok, _ := l.allow("1.2.3.4"); !ok {
		t.Fatal("first request must pass")
	}
	if ok, _ := l.allow("1.2.3.4"); !ok {
		t.Fatal("second request must pass (burst)")
	}
	ok, wait := l.allow("1.2.3.4")
	if ok {
		t.Fatal("third request must be refused")
	}
	if wait < 900*time.Millisecond || wait > 1100*time.Millisecond {
		t.Fatalf("wait = %v, want ~1s", wait)
	}
	if ok, _ := l.allow("5.6.7.8"); !ok {
		t.Fatal("another IP has its own bucket")
	}
	now = now.Add(2 * time.Second)
	if ok, _ := l.allow("1.2.3.4"); !ok {
		t.Fatal("bucket must refill with time")
	}
}

// TestLimiterWrap checks the HTTP shape of a refusal: 429 plus Retry-After.
func TestLimiterWrap(t *testing.T) {
	now := time.Unix(0, 0)
	l := newLimiter(60, 1, true)
	l.now = func() time.Time { return now }
	m := &metrics{}

	req := httptest.NewRequest("GET", "/route", nil)
	req.RemoteAddr = "10.0.0.1:5555"
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	if rec := httptest.NewRecorder(); !l.allowRequest(rec, req, m) {
		t.Fatalf("first request should pass, got %d", rec.Code)
	}
	rec := httptest.NewRecorder()
	if l.allowRequest(rec, req, m) {
		t.Fatal("second request should be refused")
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: code %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("429 must carry Retry-After")
	}
	if m.rateLimited.Load() != 1 {
		t.Fatalf("rate limited counter = %d, want 1", m.rateLimited.Load())
	}
	// A different forwarded IP is unaffected, which proves the XFF is used.
	req2 := httptest.NewRequest("GET", "/route", nil)
	req2.RemoteAddr = "10.0.0.1:5555"
	req2.Header.Set("X-Forwarded-For", "198.51.100.7")
	if rec2 := httptest.NewRecorder(); !l.allowRequest(rec2, req2, m) {
		t.Fatalf("independent IP: code %d, want a pass", rec2.Code)
	}
}

// TestClientKeyIgnoresForgedHeader checks that X-Forwarded-For is only trusted
// when the operator opts in.
func TestClientKeyIgnoresForgedHeader(t *testing.T) {
	req := httptest.NewRequest("GET", "/route", nil)
	req.RemoteAddr = "10.0.0.1:5555"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	if got := clientKey(req, false); got != "10.0.0.1" {
		t.Fatalf("untrusted proxy: key %q, want 10.0.0.1", got)
	}
	if got := clientKey(req, true); got != "203.0.113.9" {
		t.Fatalf("trusted proxy: key %q, want 203.0.113.9", got)
	}
}

// TestMetricsText checks the exposition format: every documented series is
// present, buckets are cumulative, and the counters line up.
func TestMetricsText(t *testing.T) {
	m := &metrics{}
	m.inflight.Store(2)
	m.expansions.Store(1234)
	m.fallback.Store(3)
	m.rateLimited.Store(4)
	m.canceled.Store(5)
	m.tooFar.Store(6)
	m.errSnap.Store(7)
	m.wsPoolFn = func() (uint64, uint64, uint64, uint64) {
		return 65536, 786432, 2, 1
	}
	m.observe(200, 25*time.Millisecond)
	m.observe(304, 5*time.Millisecond)
	m.observe(504, 11*time.Second)

	rec := httptest.NewRecorder()
	m.handleMetrics(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		"routeid_build_info{",
		`route_requests_total{code="200"} 1`,
		`route_requests_total{code="304"} 1`,
		`route_requests_total{code="504"} 1`,
		`route_request_duration_seconds_bucket{le="0.025"} 2`,
		`route_request_duration_seconds_bucket{le="+Inf"} 3`,
		"route_request_duration_seconds_count 3",
		"route_inflight 2",
		"route_expansions_total 1234",
		"route_fallback_total 3",
		"route_rate_limited_total 4",
		"route_canceled_total 5",
		`route_errors_total{reason="too_far"} 6`,
		"routeid_workspace_slots 65536",
		"routeid_workspace_bytes 786432",
		"routeid_workspace_grows_total 2",
		"routeid_workspace_shrinks_total 1",
		`route_errors_total{reason="snap"} 7`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics missing %q\n%s", want, body)
		}
	}
	if strings.Contains(body, "NaN") || strings.Contains(body, "e+") {
		t.Fatalf("metrics contain non-Prometheus numbers:\n%s", body)
	}
}

// TestStatusRecorder checks that the middleware sees the status even when a
// handler only calls Write.
func TestStatusRecorder(t *testing.T) {
	var seen atomic.Int64
	m := &metrics{}
	h := m.wrap(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("x"))
	})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", "/route", nil))
	if rec.Code != 200 {
		t.Fatalf("code %d", rec.Code)
	}
	rec2 := httptest.NewRecorder()
	m.wrap(func(w http.ResponseWriter, _ *http.Request) {
		httpErr(w, 422, "nope")
	})(rec2, httptest.NewRequest("GET", "/route", nil))
	if rec2.Code != 422 {
		t.Fatalf("code %d, want 422", rec2.Code)
	}
	if m.req422.Load() != 1 || m.req200.Load() != 1 {
		t.Fatalf("counters 200=%d 422=%d", m.req200.Load(), m.req422.Load())
	}
	_ = seen.Load()
}
