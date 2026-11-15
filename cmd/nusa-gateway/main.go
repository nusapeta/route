// Command nusa-gateway puts the car and the motorcycle service behind one base
// URL with the profile chosen by the profile query parameter.
//
// A server process holds one profile, because region selection snaps to the
// nearest graph and two profiles would shadow each other. Two containers and
// two ports are therefore correct, but a client should not have to know that:
// the gateway forwards to the upstream matching ?profile= and strips the
// parameter, so the upstreams keep seeing exactly the request they document.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

const (
	profileMobil = "mobil"
	profileMotor = "motor"
	// placePrefix is the path the bundled geocoder answers under. The prefix is
	// the only routing signal available: the public hostname is a single tunnel
	// route, so a second hostname cannot reach this process.
	placePrefix = "/place"
)

// commit is stamped by make static through -ldflags. It also versions the
// static asset URLs so a deploy cannot mix new HTML with cached old JS.
var commit = "dev"

type upstream struct {
	name  string
	base  *url.URL
	proxy *httputil.ReverseProxy
}

type gateway struct {
	mobil, motor *upstream
	// place is nil unless -place is set: the bundle is opt-in, so a gateway
	// started without it behaves exactly as before.
	place        *upstream
	placeLimit   *limiter
	def          string
	client       *http.Client
	tr           *http.Transport
	trustProxy   bool
	blockMetrics bool
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	mobil := flag.String("mobil", "http://127.0.0.1:18080", "car profile upstream")
	motor := flag.String("motor", "http://127.0.0.1:18081", "motorcycle profile upstream")
	def := flag.String("default", profileMobil, "profile served when the request has no profile parameter")
	trustProxy := flag.Bool("trust-proxy", false, "take the client IP from CF-Connecting-IP or X-Forwarded-For (only behind a trusted proxy)")
	blockMetrics := flag.Bool("block-metrics", false, "answer /metrics with 404 instead of proxying it")
	place := flag.String("place", "", "geocoder upstream mounted at /place; empty disables the bundle")
	placeRate := flag.Float64("place-rate", 240, "requests per minute per IP for /place (0 disables the limiter)")
	placeBurst := flag.Float64("place-burst", 40, "burst allowance per IP for /place")
	flag.Parse()

	if *def != profileMobil && *def != profileMotor {
		log.Fatalf("-default must be %q or %q", profileMobil, profileMotor)
	}
	g := newGateway(*mobil, *motor, *def, *trustProxy, *blockMetrics)
	if *place != "" {
		if err := g.attachPlace(*place, *placeRate, *placeBurst); err != nil {
			log.Fatal(err)
		}
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           g,
		ReadHeaderTimeout: 5 * time.Second,
		// The upstream deadline is 10s; the write side only needs to outlast the
		// transfer of an already computed route.
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	log.Printf("gateway on %s: default %s, mobil %s, motor %s", *addr, *def, g.mobil.base, g.motor.base)
	if g.place != nil {
		log.Printf("bundle: %s -> place %s", placePrefix, g.place.base)
	}
	log.Fatal(srv.ListenAndServe())
}

func newGateway(mobil, motor, def string, trustProxy, blockMetrics bool) *gateway {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConnsPerHost = 32
	tr.IdleConnTimeout = 90 * time.Second
	// The compressed body must reach the client untouched: the upstream already
	// decided whether to gzip.
	tr.DisableCompression = true

	g := &gateway{
		def:          def,
		client:       &http.Client{Timeout: 15 * time.Second},
		tr:           tr,
		trustProxy:   trustProxy,
		blockMetrics: blockMetrics,
	}
	for _, u := range []struct {
		name string
		raw  string
		dst  **upstream
	}{
		{profileMobil, mobil, &g.mobil},
		{profileMotor, motor, &g.motor},
	} {
		base, err := url.Parse(u.raw)
		if err != nil || base.Host == "" {
			log.Fatalf("%s upstream %q is not a URL", u.name, u.raw)
		}
		up := &upstream{name: u.name, base: base}
		up.proxy = &httputil.ReverseProxy{
			Transport: tr,
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.SetURL(base)
				// Overwrite X-Forwarded-For instead of appending: the upstream
				// rate limits on the first entry, so a client-supplied value
				// must not survive the hop.
				pr.Out.Header.Del("X-Forwarded-For")
				pr.Out.Header.Del("X-Real-IP")
				pr.SetXForwarded()
				if g.trustProxy {
					if ip := forwardedClientIP(pr.In.Header); ip != "" {
						pr.Out.Header.Set("X-Forwarded-For", ip)
						pr.Out.Header.Set("X-Real-IP", ip)
					}
				}
			},
			ModifyResponse: func(res *http.Response) error {
				res.Header.Set("X-Route-Profile", up.name)
				exposed := res.Header.Get("Access-Control-Expose-Headers")
				if exposed != "" && !strings.Contains(exposed, "X-Route-Profile") {
					res.Header.Set("Access-Control-Expose-Headers", exposed+", X-Route-Profile")
				}
				return nil
			},
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				log.Printf("%s upstream: %v", up.name, err)
				writeError(w, http.StatusBadGateway, "upstream unavailable")
			},
		}
		*u.dst = up
	}
	return g
}

// attachPlace mounts the geocoder upstream at /place. It is separate from the
// profile upstreams because the geocoder has no profiles: the path prefix is
// the only routing signal, and it leaves the documented paths of both services
// unchanged under one public base URL.
func (g *gateway) attachPlace(raw string, perMinute, burst float64) error {
	base, err := url.Parse(raw)
	if err != nil || base.Host == "" {
		return fmt.Errorf("place upstream %q is not a URL", raw)
	}
	up := &upstream{name: "place", base: base}
	up.proxy = &httputil.ReverseProxy{
		Transport: g.tr,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(base)
			// Same rule as the profile upstreams: a client-supplied forwarding
			// header must not survive the hop, or it could pick its own bucket.
			pr.Out.Header.Del("X-Forwarded-For")
			pr.Out.Header.Del("X-Real-IP")
			pr.SetXForwarded()
			if g.trustProxy {
				if ip := forwardedClientIP(pr.In.Header); ip != "" {
					pr.Out.Header.Set("X-Forwarded-For", ip)
					pr.Out.Header.Set("X-Real-IP", ip)
				}
			}
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("place upstream: %v", err)
			writeError(w, http.StatusBadGateway, "upstream unavailable")
		},
	}
	g.place = up
	g.placeLimit = newLimiter(perMinute, burst)
	return nil
}

// forwardedClientIP reads the address a trusted proxy put in front of the
// request. Cloudflare Tunnel sets CF-Connecting-IP; other proxies only set
// X-Forwarded-For. Invalid values fall back to the transport address, because
// using an arbitrary string as a rate-limit key would let one client mint
// unlimited buckets.
func forwardedClientIP(h http.Header) string {
	for _, name := range []string{"CF-Connecting-IP", "X-Forwarded-For", "X-Real-IP"} {
		raw := h.Get(name)
		if raw == "" {
			continue
		}
		if i := strings.IndexByte(raw, ','); i >= 0 {
			raw = raw[:i]
		}
		ip := strings.TrimSpace(raw)
		if host, _, err := net.SplitHostPort(ip); err == nil {
			ip = host
		}
		if net.ParseIP(ip) != nil {
			return ip
		}
	}
	return ""
}

func (g *gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if g.serveStatic(w, r) {
		return
	}
	if r.URL.Path == placePrefix || strings.HasPrefix(r.URL.Path, placePrefix+"/") {
		g.servePlace(w, r)
		return
	}
	if g.blockMetrics && r.URL.Path == "/metrics" {
		http.NotFound(w, r)
		return
	}
	if r.URL.Path == "/healthz" && r.URL.Query().Get("profile") == "" {
		g.health(w, r)
		return
	}
	name := r.URL.Query().Get("profile")
	if name == "" {
		name = g.def
	}
	up := g.pick(name)
	if up == nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("profile must be %q or %q", profileMobil, profileMotor))
		return
	}
	stripProfile(r)
	up.proxy.ServeHTTP(w, r)
}

// servePlace strips the bundle prefix and forwards the path the geocoder
// documents. /place/metrics is closed here for the same reason the routing
// metrics are: it publishes index size and age, and this is the public
// listener. The bare prefix answers 404 because the geocoder has no index page.
func (g *gateway) servePlace(w http.ResponseWriter, r *http.Request) {
	if g.place == nil || r.URL.Path == placePrefix {
		http.NotFound(w, r)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, placePrefix)
	if g.blockMetrics && rest == "/metrics" {
		http.NotFound(w, r)
		return
	}
	if !g.placeLimit.allowRequest(w, r, g.clientKey(r)) {
		return
	}
	r.URL.Path = rest
	r.URL.RawPath = ""
	g.place.proxy.ServeHTTP(w, r)
}

// clientKey identifies the caller for the /place bucket. Behind the tunnel the
// real address is in CF-Connecting-IP; trusting it is opt-in, because on a
// public listener anyone can forge it.
func (g *gateway) clientKey(r *http.Request) string {
	if g.trustProxy {
		if ip := forwardedClientIP(r.Header); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (g *gateway) pick(name string) *upstream {
	switch name {
	case profileMobil:
		return g.mobil
	case profileMotor:
		return g.motor
	}
	return nil
}

// stripProfile removes the profile parameter so the upstream sees the request
// it documents, and so its cache key and logs do not carry a gateway concern.
func stripProfile(r *http.Request) {
	q := r.URL.Query()
	if q.Get("profile") == "" {
		return
	}
	q.Del("profile")
	r.URL.RawQuery = q.Encode()
}

// health answers for both profiles at once, which is what a single base URL
// needs: one probe that fails when either upstream is down.
func (g *gateway) health(w http.ResponseWriter, r *http.Request) {
	type result struct {
		OK   bool            `json:"ok"`
		Body json.RawMessage `json:"upstream,omitempty"`
		Err  string          `json:"error,omitempty"`
	}
	out := map[string]result{}
	allOK := true
	for _, up := range []*upstream{g.mobil, g.motor} {
		body, err := g.fetch(up, r)
		res := result{OK: err == nil}
		if err != nil {
			res.Err = err.Error()
			allOK = false
		} else {
			res.Body = body
		}
		out[up.name] = res
	}
	code := http.StatusOK
	if !allOK {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]any{"ok": allOK, "profiles": out})
}

func (g *gateway) fetch(up *upstream, r *http.Request) (json.RawMessage, error) {
	u := *up.base
	u.Path = "/healthz"
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	res, err := g.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", res.StatusCode)
	}
	if !json.Valid(body) {
		return nil, fmt.Errorf("invalid health body")
	}
	return bytes.TrimSpace(body), nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, struct {
		Error string `json:"error"`
	}{msg})
}
