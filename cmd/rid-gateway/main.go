// Command rid-gateway puts the car and the motorcycle service behind one base
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
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

const (
	profileMobil = "mobil"
	profileMotor = "motor"
)

type upstream struct {
	name  string
	base  *url.URL
	proxy *httputil.ReverseProxy
}

type gateway struct {
	mobil, motor *upstream
	def          string
	client       *http.Client
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	mobil := flag.String("mobil", "http://127.0.0.1:18080", "car profile upstream")
	motor := flag.String("motor", "http://127.0.0.1:18081", "motorcycle profile upstream")
	def := flag.String("default", profileMobil, "profile served when the request has no profile parameter")
	flag.Parse()

	if *def != profileMobil && *def != profileMotor {
		log.Fatalf("-default must be %q or %q", profileMobil, profileMotor)
	}
	g := newGateway(*mobil, *motor, *def)

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
	log.Fatal(srv.ListenAndServe())
}

func newGateway(mobil, motor, def string) *gateway {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConnsPerHost = 32
	tr.IdleConnTimeout = 90 * time.Second
	// The compressed body must reach the client untouched: the upstream already
	// decided whether to gzip.
	tr.DisableCompression = true

	g := &gateway{def: def, client: &http.Client{Timeout: 15 * time.Second}}
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

func (g *gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
