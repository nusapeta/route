package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type hit struct {
	path, query, xff, method, body string
}

// stubUpstream stands in for one profile process and records what it received.
func stubUpstream(t *testing.T, name string, hits chan hit) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		hits <- hit{path: r.URL.Path, query: r.URL.RawQuery, xff: r.Header.Get("X-Forwarded-For"), method: r.Method, body: string(body)}
		if r.URL.Path == "/healthz" {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"ok":true,"regions":[{"name":"`+name+`"}]}`)
			return
		}
		w.Header().Set("ETag", `"`+name+`-tag"`)
		w.Header().Set("Access-Control-Expose-Headers", "X-Route-Ms, ETag")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		io.WriteString(w, `{"error":"no route"}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newTestGateway(t *testing.T, hits chan hit) *gateway {
	t.Helper()
	mobil := stubUpstream(t, "mobil", hits)
	motor := stubUpstream(t, "motor", hits)
	return newGateway(mobil.URL, motor.URL, profileMobil)
}

func get(t *testing.T, h http.Handler, target string, header map[string]string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

// TestProfileSelectsUpstream checks that the profile parameter chooses the
// process and does not reach it.
func TestProfileSelectsUpstream(t *testing.T) {
	hits := make(chan hit, 4)
	g := newTestGateway(t, hits)

	for _, c := range []struct {
		query, want string
	}{
		{"profile=motor", "motor"},
		{"profile=mobil", "mobil"},
		{"", "mobil"}, // default
	} {
		res := get(t, g, "/route?from=-6.2,106.8&to=-6.21,106.81&"+c.query, nil)
		got := <-hits
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("%q: status %d, want the upstream 422", c.query, res.StatusCode)
		}
		q, err := url.ParseQuery(got.query)
		if err != nil {
			t.Fatalf("%q: upstream query %q: %v", c.query, got.query, err)
		}
		if q.Get("from") != "-6.2,106.8" || q.Get("to") != "-6.21,106.81" {
			t.Errorf("%q: upstream query %q lost the route parameters", c.query, got.query)
		}
		if _, ok := q["profile"]; ok {
			t.Errorf("%q: upstream query %q kept the profile parameter", c.query, got.query)
		}
		if tag, want := res.Header.Get("ETag"), `"`+c.want+`-tag"`; tag != want {
			t.Errorf("%q: ETag %q, want %q", c.query, tag, want)
		}
		if got := res.Header.Get("X-Route-Profile"); got != c.want {
			t.Errorf("%q: X-Route-Profile %q, want %q", c.query, got, c.want)
		}
		if got := res.Header.Get("Access-Control-Expose-Headers"); !strings.Contains(got, "X-Route-Profile") {
			t.Errorf("%q: expose headers %q missing X-Route-Profile", c.query, got)
		}
	}
}

// TestUnknownProfileRefused checks that a typo fails loudly instead of quietly
// serving the wrong profile.
func TestUnknownProfileRefused(t *testing.T) {
	hits := make(chan hit, 1)
	g := newTestGateway(t, hits)

	res := get(t, g, "/route?from=-6.2,106.8&to=-6.21,106.81&profile=truk", nil)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", res.StatusCode)
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil || !strings.Contains(body.Error, "profile") {
		t.Fatalf("body %+v, decode error %v", body, err)
	}
	if len(hits) != 0 {
		t.Fatal("upstream was called for an unknown profile")
	}
}

// TestForwardedForReplaced checks that a client cannot pick its own rate limit
// bucket by forging X-Forwarded-For.
func TestForwardedForReplaced(t *testing.T) {
	hits := make(chan hit, 1)
	g := newTestGateway(t, hits)

	get(t, g, "/route?from=-6.2,106.8&to=-6.21,106.81&profile=motor",
		map[string]string{"X-Forwarded-For": "1.2.3.4", "X-Real-IP": "1.2.3.4"})
	got := <-hits
	if strings.Contains(got.xff, "1.2.3.4") {
		t.Errorf("X-Forwarded-For %q kept the forged address", got.xff)
	}
	if got.xff != "192.0.2.1" {
		t.Errorf("X-Forwarded-For %q, want the client address", got.xff)
	}
}

// TestPostBodyForwarded checks the JSON form keeps working through the gateway.
func TestPostBodyForwarded(t *testing.T) {
	hits := make(chan hit, 1)
	g := newTestGateway(t, hits)

	req := httptest.NewRequest(http.MethodPost, "/route?profile=motor", strings.NewReader(`{"from":[-6.2,106.8],"to":[-6.21,106.81]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	got := <-hits
	if got.method != http.MethodPost || !strings.Contains(got.body, "-6.2") {
		t.Errorf("upstream got %s %q", got.method, got.body)
	}
}

// TestHealthAggregatesBothProfiles covers the single base URL probe: one call
// that fails when either profile is down.
func TestHealthAggregatesBothProfiles(t *testing.T) {
	hits := make(chan hit, 8)
	g := newTestGateway(t, hits)

	res := get(t, g, "/healthz", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", res.StatusCode)
	}
	var body struct {
		OK       bool                       `json:"ok"`
		Profiles map[string]json.RawMessage `json:"profiles"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.OK || len(body.Profiles) != 2 {
		t.Fatalf("body %+v", body)
	}
	for _, name := range []string{"mobil", "motor"} {
		if _, ok := body.Profiles[name]; !ok {
			t.Errorf("health is missing profile %q", name)
		}
	}
}

func TestHealthFailsWhenOneUpstreamIsDown(t *testing.T) {
	hits := make(chan hit, 8)
	mobil := stubUpstream(t, "mobil", hits)
	g := newGateway(mobil.URL, "http://127.0.0.1:1", profileMobil)

	res := get(t, g, "/healthz", nil)
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", res.StatusCode)
	}
	var body struct {
		OK       bool `json:"ok"`
		Profiles map[string]struct {
			OK  bool   `json:"ok"`
			Err string `json:"error"`
		} `json:"profiles"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.OK || body.Profiles["motor"].OK || body.Profiles["motor"].Err == "" {
		t.Fatalf("body %+v", body)
	}
	if !body.Profiles["mobil"].OK {
		t.Errorf("mobil should stay healthy: %+v", body.Profiles["mobil"])
	}
}

// TestHealthSingleProfile checks the per-profile probe, which monitoring uses to
// tell which side is down.
func TestHealthSingleProfile(t *testing.T) {
	hits := make(chan hit, 4)
	g := newTestGateway(t, hits)

	res := get(t, g, "/healthz?profile=motor", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), "motor") {
		t.Errorf("body %s does not come from the motor stub", body)
	}
	got := <-hits
	if got.query != "" {
		t.Errorf("upstream query %q, want it stripped", got.query)
	}
}

// TestUpstreamDownIs502 checks that a dead profile answers like a proxy, not
// like a router, so the client is not told "no route".
func TestUpstreamDownIs502(t *testing.T) {
	hits := make(chan hit, 1)
	mobil := stubUpstream(t, "mobil", hits)
	g := newGateway(mobil.URL, "http://127.0.0.1:1", profileMobil)

	res := get(t, g, "/route?from=-6.2,106.8&to=-6.21,106.81&profile=motor", nil)
	if res.StatusCode != http.StatusBadGateway {
		t.Fatalf("status %d, want 502", res.StatusCode)
	}
	if tag := res.Header.Get("ETag"); tag != "" {
		t.Errorf("ETag %q on an error response", tag)
	}
}

// TestConcurrentProfilesIsolation is a cheap guard that the per-upstream
// proxies are not swapped under load.
func TestConcurrentProfilesIsolation(t *testing.T) {
	hits := make(chan hit, 32)
	g := newTestGateway(t, hits)

	const n = 16
	served := make(chan string, n)
	for i := range n {
		q := "profile=motor"
		if i%2 == 0 {
			q = "profile=mobil"
		}
		req := httptest.NewRequest(http.MethodGet, "/route?from=-6.2,106.8&to=-6.21,106.81&"+q, nil)
		go func() {
			rec := httptest.NewRecorder()
			g.ServeHTTP(rec, req)
			served <- rec.Result().Header.Get("X-Route-Profile")
		}()
	}
	counts := map[string]int{}
	for range n {
		counts[<-served]++
		<-hits
	}
	if counts["mobil"] != n/2 || counts["motor"] != n/2 {
		t.Errorf("got %v, want %d each", counts, n/2)
	}
}
