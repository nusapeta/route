package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// TestMaxRouteFastReject checks that a straight-line distance above the cap is
// refused before snapping, routing, or cache lookups touch a shared resource.
func TestMaxRouteFastReject(t *testing.T) {
	m := &metrics{}
	s := &server{
		maxRouteM: 40_000,
		// Deliberately no regions, limiter, or timeout: the gate must answer
		// before any of those are used.
		m: m,
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/route?from=-6.2088,106.8456&to=-6.9175,107.6191", nil) // Jakarta -> Bandung, ~120 km
	s.handleRoute(rec, req)

	if rec.Code != 422 {
		t.Fatalf("code %d, want 422", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "route too long: straight-line") {
		t.Fatalf("body %q, want straight-line rejection", body)
	}
	if got := m.tooFar.Load(); got != 1 {
		t.Fatalf("tooFar = %d, want 1", got)
	}
	if got := m.errSnap.Load(); got != 0 {
		t.Fatalf("errSnap = %d, want 0 (no snap attempted)", got)
	}
}

// TestMaxRouteDisabled checks that max-route-km=0 lets a long request through
// the distance gate; with no graph loaded it then fails at snapping instead.
func TestMaxRouteDisabled(t *testing.T) {
	m := &metrics{}
	s := &server{maxRouteM: 0, m: m}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/route?from=-6.2088,106.8456&to=-6.9175,107.6191", nil)
	s.handleRoute(rec, req)

	if rec.Code != 422 {
		t.Fatalf("code %d, want 422", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, "route too long") {
		t.Fatalf("body %q, long route was rejected with the cap disabled", body)
	}
	if !strings.Contains(rec.Body.String(), "no road within snap distance") {
		t.Fatalf("body %q, want snap failure", rec.Body.String())
	}
	if got := m.tooFar.Load(); got != 0 {
		t.Fatalf("tooFar = %d, want 0", got)
	}
	if got := m.errSnap.Load(); got != 1 {
		t.Fatalf("errSnap = %d, want 1", got)
	}
}
