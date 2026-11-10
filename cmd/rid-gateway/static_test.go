package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIndexServedLocally(t *testing.T) {
	hits := make(chan hit, 4)
	g := newTestGateway(t, hits)

	res := get(t, g, "/", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", res.StatusCode)
	}
	if got := res.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("Content-Type %q, want text/html", got)
	}
	if got := res.Header.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control %q, want no-cache", got)
	}
	if got := res.Header.Get("Content-Security-Policy"); !strings.Contains(got, "https://tile.openstreetmap.org") {
		t.Errorf("CSP %q does not allow the tile origin", got)
	}

	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), `id="route-button"`) {
		t.Errorf("index body does not contain the route control")
	}
	assertNoUpstream(t, hits)
}

func TestStaticAssetsServedLocally(t *testing.T) {
	hits := make(chan hit, 4)
	g := newTestGateway(t, hits)

	for _, target := range []string{"/assets/app.css", "/assets/app.js", "/assets/vendor/leaflet/leaflet.js"} {
		res := get(t, g, target, nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d, want 200", target, res.StatusCode)
		}
		body, _ := io.ReadAll(res.Body)
		if len(body) == 0 {
			t.Errorf("%s: empty body", target)
		}
		if got := res.Header.Get("Cache-Control"); got != "no-cache" {
			t.Errorf("%s: Cache-Control %q, want no-cache", target, got)
		}
		if got := res.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options %q", target, got)
		}
	}
	assertNoUpstream(t, hits)
}

func TestStaticHeadHasNoBody(t *testing.T) {
	hits := make(chan hit, 1)
	g := newTestGateway(t, hits)

	req := httptest.NewRequest(http.MethodHead, "/assets/app.css", nil)
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD body has %d bytes", rec.Body.Len())
	}
	assertNoUpstream(t, hits)
}

func TestStaticMethodsRefused(t *testing.T) {
	hits := make(chan hit, 2)
	g := newTestGateway(t, hits)

	for _, target := range []string{"/", "/assets/app.css"} {
		req := httptest.NewRequest(http.MethodPost, target, nil)
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status %d, want 405", target, rec.Code)
		}
		if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
			t.Errorf("%s: Allow %q", target, got)
		}
	}
	assertNoUpstream(t, hits)
}

func TestStaticDirectoriesRefused(t *testing.T) {
	hits := make(chan hit, 4)
	g := newTestGateway(t, hits)

	for _, target := range []string{"/assets", "/assets/", "/assets/vendor/leaflet/", "/assets/../index.html"} {
		res := get(t, g, target, nil)
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", target, res.StatusCode)
		}
	}
	assertNoUpstream(t, hits)
}

func assertNoUpstream(t *testing.T, hits chan hit) {
	t.Helper()
	select {
	case got := <-hits:
		t.Fatalf("static request reached upstream: %+v", got)
	default:
	}
}
