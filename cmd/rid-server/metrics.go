package main

import (
	"fmt"
	"math"
	"net/http"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"
)

// latencyBuckets are the histogram bounds in seconds. The server is meant to
// answer 42 km routes in ~0.1 s and refuses what it cannot do in 10 s, so the
// buckets cluster below 1 s and stop at the timeout.
var latencyBuckets = []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// metrics is a dependency-free Prometheus surface: what an operator needs
// (throughput, latency, expansions, refusals, memory) without pulling
// client_golang into a 6.6 MB static binary.
type metrics struct {
	inflight atomic.Int64

	req200, req304, req400, req422, req429, req500, req504 atomic.Int64
	rateLimited                                            atomic.Int64
	expansions, fallback, canceled, tooFar, relaxed        atomic.Int64
	crossRegionFixed                                       atomic.Int64
	errNoRoute, errSnap, errCrossRegion                    atomic.Int64

	hist [12]atomic.Int64 // len(latencyBuckets)+1, the last is +Inf
	sum  atomic.Uint64    // microseconds
	n    atomic.Int64

	wsPoolFn func() (slots uint64, bytes uint64, grows uint64, shrinks uint64)
}

// observe records one /route request.
func (m *metrics) observe(status int, dur time.Duration) {
	switch status {
	case 200:
		m.req200.Add(1)
	case 304:
		m.req304.Add(1)
	case 400:
		m.req400.Add(1)
	case 422:
		m.req422.Add(1)
	case 429:
		m.req429.Add(1)
	case 504:
		m.req504.Add(1)
	default:
		m.req500.Add(1)
	}
	secs := dur.Seconds()
	idx := len(latencyBuckets)
	for i, b := range latencyBuckets {
		if secs <= b {
			idx = i
			break
		}
	}
	m.hist[idx].Add(1)
	m.sum.Add(uint64(dur.Microseconds()))
	m.n.Add(1)
}

// statusRecorder captures the status code so the middleware can count it
// without the handlers having to know about metrics.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wrote {
		r.status, r.wrote = code, true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wrote {
		r.status, r.wrote = 200, true
	}
	return r.ResponseWriter.Write(b)
}

func (m *metrics) wrap(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		m.inflight.Add(1)
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		defer func() {
			m.inflight.Add(-1)
			m.observe(rec.status, time.Since(start))
		}()
		next(rec, req)
	}
}

// handleMetrics renders the Prometheus text format. It is intentionally a
// hand-written writer: no reflection, no registry, no dependencies.
func (m *metrics) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	var total int64
	for i := range m.hist {
		total += m.hist[i].Load()
	}
	fmt.Fprintf(w, "# HELP routeid_build_info Build identity\n# TYPE routeid_build_info gauge\n")
	fmt.Fprintf(w, "routeid_build_info{commit=%q,go_version=%q} 1\n", commit, runtime.Version())

	fmt.Fprintf(w, "# HELP route_requests_total /route requests by status code\n# TYPE route_requests_total counter\n")
	for _, kv := range []struct {
		code string
		v    int64
	}{
		{"200", m.req200.Load()}, {"304", m.req304.Load()}, {"400", m.req400.Load()},
		{"422", m.req422.Load()}, {"429", m.req429.Load()}, {"500", m.req500.Load()},
		{"504", m.req504.Load()},
	} {
		fmt.Fprintf(w, "route_requests_total{code=%q} %d\n", kv.code, kv.v)
	}

	fmt.Fprintf(w, "# HELP route_request_duration_seconds /route wall time\n# TYPE route_request_duration_seconds histogram\n")
	cum := int64(0)
	for i, b := range latencyBuckets {
		cum += m.hist[i].Load()
		fmt.Fprintf(w, "route_request_duration_seconds_bucket{le=%q} %d\n", trimFloat(b), cum)
	}
	cum += m.hist[len(latencyBuckets)].Load()
	fmt.Fprintf(w, "route_request_duration_seconds_bucket{le=\"+Inf\"} %d\n", cum)
	fmt.Fprintf(w, "route_request_duration_seconds_sum %.6f\n", float64(m.sum.Load())/1e6)
	fmt.Fprintf(w, "route_request_duration_seconds_count %d\n", m.n.Load())

	fmt.Fprintf(w, "# HELP route_inflight In-flight /route requests\n# TYPE route_inflight gauge\nroute_inflight %d\n", m.inflight.Load())
	fmt.Fprintf(w, "# HELP route_expansions_total A* expansions summed over requests\n# TYPE route_expansions_total counter\nroute_expansions_total %d\n", m.expansions.Load())
	fmt.Fprintf(w, "# HELP route_fallback_total Routes served without turn restrictions\n# TYPE route_fallback_total counter\nroute_fallback_total %d\n", m.fallback.Load())
	fmt.Fprintf(w, "# HELP route_relaxed_total Long routes served with a greedier heuristic after the budget ran out\n# TYPE route_relaxed_total counter\nroute_relaxed_total %d\n", m.relaxed.Load())
	fmt.Fprintf(w, "# HELP route_rate_limited_total Requests refused by the per-IP limiter\n# TYPE route_rate_limited_total counter\nroute_rate_limited_total %d\n", m.rateLimited.Load())
	fmt.Fprintf(w, "# HELP route_canceled_total Searches stopped by the request deadline\n# TYPE route_canceled_total counter\nroute_canceled_total %d\n", m.canceled.Load())
	fmt.Fprintf(w, "# HELP route_errors_total Failed routes by reason\n# TYPE route_errors_total counter\n")
	fmt.Fprintf(w, "route_errors_total{reason=\"too_far\"} %d\n", m.tooFar.Load())
	fmt.Fprintf(w, "# HELP route_cross_region_resnapped_total Requests whose endpoints snapped to two overlapping region files but routed inside one\n# TYPE route_cross_region_resnapped_total counter\nroute_cross_region_resnapped_total %d\n", m.crossRegionFixed.Load())
	fmt.Fprintf(w, "route_errors_total{reason=\"no_route\"} %d\n", m.errNoRoute.Load())
	fmt.Fprintf(w, "route_errors_total{reason=\"snap\"} %d\n", m.errSnap.Load())
	fmt.Fprintf(w, "route_errors_total{reason=\"cross_region\"} %d\n", m.errCrossRegion.Load())

	if m.wsPoolFn != nil {
		slots, bytes, grows, shrinks := m.wsPoolFn()
		fmt.Fprintf(w, "# HELP routeid_workspace_slots Total open-addressing slots across allocated workspaces\n# TYPE routeid_workspace_slots gauge\nrouteid_workspace_slots %d\n", slots)
		fmt.Fprintf(w, "# HELP routeid_workspace_bytes Estimated total bytes used by workspaces (12 bytes/slot)\n# TYPE routeid_workspace_bytes gauge\nrouteid_workspace_bytes %d\n", bytes)
		fmt.Fprintf(w, "# HELP routeid_workspace_grows_total Workspace capacity grow operations\n# TYPE routeid_workspace_grows_total counter\nrouteid_workspace_grows_total %d\n", grows)
		fmt.Fprintf(w, "# HELP routeid_workspace_shrinks_total Workspace capacity shrink operations\n# TYPE routeid_workspace_shrinks_total counter\nrouteid_workspace_shrinks_total %d\n", shrinks)
	}
	_ = total
}

// trimFloat renders a bucket bound the way Prometheus expects (0.025, not 0.025000).
func trimFloat(f float64) string {
	if f == math.Trunc(f) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}
