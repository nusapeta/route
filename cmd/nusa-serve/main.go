package main

import (
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/nusapeta/route/internal/graph"
)

// Build information, injected with
// -ldflags "-X main.commit=... -X main.buildTime=...".
var (
	commit    = "dev"
	buildTime = "unknown"
)

// region is one graph file plus the box it covers.
type region struct {
	name string
	g    *graph.Graph
	r    *graph.Router
	// padded extent in degrees
	minLat, minLon, maxLat, maxLon float64
}

func (rg *region) contains(lat, lon float64) bool {
	return lat >= rg.minLat && lat <= rg.maxLat && lon >= rg.minLon && lon <= rg.maxLon
}

type server struct {
	regions    []*region
	connectors []ferryConnector
	maxSnapM   float64
	maxRouteM  float64 // straight-line/road distance cap; 0 disables it
	timeout    time.Duration
	budget     time.Duration // soft search budget, always <= timeout
	maxAge     int           // Cache-Control max-age for /route, seconds
	fp         string
	m          *metrics
	rl         *limiter
	motor      bool // this process serves the motorcycle profile
}

type strList []string

func (s *strList) String() string { return strings.Join(*s, ",") }
func (s *strList) Set(v string) error {
	for _, p := range strings.Split(v, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			*s = append(*s, p)
		}
	}
	return nil
}

func main() {
	var graphs strList
	flag.Var(&graphs, "graph", "graph file(s); repeat or comma-separate for multi-region")
	addr := flag.String("addr", ":8080", "listen address")
	profile := flag.String("profile", "car", "vehicle profile: car|motor (needs a v5 union graph)")
	conc := flag.Int("conc", 4, "concurrent routing workspaces (shared across all regions)")
	workspaceMB := flag.Int("workspace-mb", 0, "total workspace memory budget in MB across all concurrency slots (0 = default 384 MB/slot)")
	snap := flag.Float64("snap", 3000, "max snap distance in meters")
	maxRouteKm := flag.Float64("max-route-km", 100, "reject routes longer than this in km (straight-line pre-check + driven distance); 0 disables")
	timeout := flag.Duration("timeout", 10*time.Second, "per-request deadline; searches that outrun it get 504")
	budget := flag.Duration("budget", 0, "soft search budget; a search that outruns it gives up with 422 (0 = 60% of -timeout)")
	maxExp := flag.Uint("max-expansions", 4_000_000, "expansion cap per query; exceeding it returns 422 (0 = default)")
	turnPenalty := flag.Float64("turn-penalty", 0.25, "turn cost multiplier; 0 = pure edge time")
	eps := flag.Float64("eps", 1.5, "heuristic weight (1.0 = optimal but slow)")
	rate := flag.Float64("rate", 15, "requests per minute per IP (0 disables the limiter)")
	burst := flag.Float64("burst", 5, "token bucket burst per IP")
	trustProxy := flag.Bool("trust-proxy", false, "read the client IP from X-Forwarded-For (only behind a proxy)")
	maxAge := flag.Int("maxage", 3600, "Cache-Control max-age for /route in seconds (0 disables caching headers)")
	flag.Parse()
	motor := false
	switch strings.ToLower(*profile) {
	case "car":
	case "motor":
		motor = true
	default:
		log.Fatalf("unknown -profile %q (want car|motor)", *profile)
	}

	if len(graphs) == 0 {
		log.Fatal("need at least one -graph")
	}

	var maxSlotsPerSlot uint32
	if *workspaceMB > 0 {
		c := *conc
		if c < 1 {
			c = 1
		}
		mbPerSlot := *workspaceMB / c
		if mbPerSlot < 1 {
			mbPerSlot = 1
		}
		// 12 bytes per slot
		maxSlotsPerSlot = uint32((uint64(mbPerSlot) * 1024 * 1024) / 12)
	}

	softBudget := *budget
	if softBudget <= 0 {
		softBudget = *timeout * 3 / 5
	}
	if softBudget > *timeout {
		softBudget = *timeout
	}

	wsPool := graph.NewWorkspacePool(*conc, maxSlotsPerSlot)
	m := &metrics{
		wsPoolFn: func() (uint64, uint64, uint64, uint64) {
			st := wsPool.Stats()
			return st.TotalSlots, st.TotalBytes, st.GrowsTotal, st.ShrinksTotal
		},
	}
	s := &server{
		connectors: defaultFerryConnectors(),
		maxSnapM:   *snap,
		maxRouteM:  *maxRouteKm * 1000,
		timeout:    *timeout,
		budget:     softBudget,
		maxAge:     *maxAge,
		m:          m,
		motor:      motor,
	}
	for _, p := range expand(graphs) {
		g, err := graph.Open(p)
		if err != nil {
			log.Fatalf("open %s: %v", p, err)
		}
		mnLat, mnLon, mxLat, mxLon := g.Bounds()
		const pad = 0.05
		r := graph.NewRouterWithPool(g, wsPool)
		r.SetMotor(motor)
		r.SetEpsilon(float32(*eps))
		r.SetTurnScale(float32(*turnPenalty))
		r.SetMaxExpansions(uint32(*maxExp))
		rg := &region{
			name: strings.TrimSuffix(filepath.Base(p), ".graph"), g: g, r: r,
			minLat: mnLat - pad, maxLat: mxLat + pad,
			minLon: mnLon - pad, maxLon: mxLon + pad,
		}
		st, _ := os.Stat(p)
		log.Printf("region %-24s %8.2f M nodes %8.2f M edges %7.0f MB  bbox %.2f,%.2f..%.2f,%.2f",
			rg.name, float64(g.NodeCount)/1e6, float64(g.EdgeCount)/1e6, float64(st.Size())/1e6,
			mnLat, mnLon, mxLat, mxLon)
		s.regions = append(s.regions, rg)
	}
	sort.Slice(s.regions, func(i, j int) bool { return s.regions[i].name < s.regions[j].name })

	s.fp = graphFingerprint(s.regions, s.maxRouteM, s.maxSnapM, float32(*eps), float32(*turnPenalty))
	s.rl = newLimiter(*rate, *burst, *trustProxy)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/version", handleVersion)
	mux.HandleFunc("/metrics", s.m.handleMetrics)
	mux.Handle("/route", s.m.wrap(s.handleRoute))

	srv := &http.Server{
		Addr:              *addr,
		Handler:           cors(gzipWrap(mux)),
		ReadHeaderTimeout: 5 * time.Second,
		// The write deadline must outlast the routing deadline, otherwise a
		// -timeout above 60 s is cut off before it can answer 504. The margin
		// covers serializing a long polyline body.
		WriteTimeout: s.timeout + 5*time.Second,
	}
	// Graceful shutdown so `docker stop` / `systemctl restart` can finish the
	// request in flight instead of killing a multi-second long-distance route.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	log.Printf("listening on %s (%d region(s)) fingerprint %s timeout %s rate %.0f/min burst %.0f",
		*addr, len(s.regions), s.fp, s.timeout, *rate, *burst)

	select {
	case err := <-errCh:
		log.Fatalf("serve: %v", err)
	case <-ctx.Done():
		stop()
		log.Printf("shutdown signal received, draining")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown: %v", err)
		}
		log.Printf("stopped")
	}
}

// expand resolves globs and directories into graph files.
func expand(in []string) []string {
	var out []string
	for _, p := range in {
		if strings.ContainsAny(p, "*?[") {
			m, err := filepath.Glob(p)
			if err == nil {
				out = append(out, m...)
				continue
			}
		}
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			m, _ := filepath.Glob(filepath.Join(p, "*.graph"))
			out = append(out, m...)
			continue
		}
		out = append(out, p)
	}
	return out
}

// pickSnap returns the region whose graph has the closest road segment to the
// point. Bounding boxes overlap between island extracts, so snapping decides.
func (s *server) pickSnap(lat, lon float64) (*region, graph.Snap, bool) {
	var bestRg *region
	var best graph.Snap
	bestD := s.maxSnapM
	for _, rg := range s.regions {
		if !rg.contains(lat, lon) {
			continue
		}
		sn, ok := rg.r.SnapEdge(lat, lon, bestD)
		if ok && sn.Dist <= bestD {
			bestRg, best, bestD = rg, sn, sn.Dist
		}
	}
	if bestRg == nil {
		return nil, graph.Snap{}, false
	}
	return bestRg, best, true
}

// snapBoth attaches both endpoints inside one region, which is how a route
// whose endpoints happen to sit nearest to two overlapping extracts still gets
// served from a single file.
func (s *server) snapBoth(rg *region, fLat, fLon, tLat, tLon float64) (graph.Snap, graph.Snap, bool) {
	a, ok1 := rg.r.SnapEdge(fLat, fLon, s.maxSnapM)
	b, ok2 := rg.r.SnapEdge(tLat, tLon, s.maxSnapM)
	if !ok1 || !ok2 {
		return graph.Snap{}, graph.Snap{}, false
	}
	return a, b, true
}

func (s *server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	type info struct {
		Name   string  `json:"name"`
		Nodes  uint32  `json:"nodes"`
		Edges  uint32  `json:"edges"`
		MinLat float64 `json:"min_lat"`
		MinLon float64 `json:"min_lon"`
		MaxLat float64 `json:"max_lat"`
		MaxLon float64 `json:"max_lon"`
		Bans   int     `json:"bans"`
		Only   int     `json:"only_rules"`
		Skip   uint32  `json:"restrictions_skipped"`

		Format    uint32 `json:"format"`
		PBFHash   string `json:"pbf_sha256,omitempty"`
		BuiltAt   string `json:"built_at,omitempty"`
		Source    string `json:"source,omitempty"`
		Extractor string `json:"extractor,omitempty"`
		Profile   string `json:"profile,omitempty"`
		ProfileID string `json:"profile_sha256,omitempty"`
	}
	out := struct {
		OK          bool   `json:"ok"`
		Version     string `json:"version"`
		Commit      string `json:"commit"`
		Attribution string `json:"attribution"`
		DataLicense string `json:"data_license"`
		Regions     []info `json:"regions"`
	}{OK: true, Version: versionString(), Commit: commit,
		Attribution: "© OpenStreetMap contributors",
		DataLicense: "ODbL 1.0"}
	prof, profKey := "car", "profile_sha256"
	if s.motor {
		prof, profKey = "motor", "motor_sha256"
	}
	for _, rg := range s.regions {
		rs := rg.g.RestrictionsFor(s.motor)
		prov := rg.g.Provenance()
		out.Regions = append(out.Regions, info{rg.name, rg.g.NodeCount, rg.g.EdgeCount, rg.minLat, rg.minLon, rg.maxLat, rg.maxLon,
			rs.Ban, rs.Only, rs.Skipped,
			prov.Format, prov.PBFHash, prov.BuiltAt, rg.g.MetaValue("source"), rg.g.MetaValue("extractor"), prof, rg.g.MetaValue(profKey)})
	}
	writeJSON(w, out)
}

func versionString() string {
	if buildTime == "unknown" {
		return commit
	}
	return commit + " (" + buildTime + ")"
}

func handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, struct {
		Commit    string `json:"commit"`
		BuiltAt   string `json:"built_at"`
		GoVersion string `json:"go_version"`
		GOOS      string `json:"goos"`
		GOARCH    string `json:"goarch"`
	}{commit, buildTime, runtime.Version(), runtime.GOOS, runtime.GOARCH})
}

type routeParams struct {
	fLat, fLon, tLat, tLon float64
	format                 string
	simplify               float64
	restrictions           bool
	pin                    bool // include the raw pin as first/last geometry point
}

func trimLabel(from, to bool) string {
	switch {
	case from && to:
		return "both"
	case from:
		return "from"
	case to:
		return "to"
	}
	return "none"
}

func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 }

// snapRetryCandidates is how many alternative segment snaps each endpoint
// offers once the nearest one yields no route; snapRetryTries caps how many of
// the resulting pairs are searched. Both stay small: the nearest snap is right
// almost every time, and a retry costs a full search.
const (
	snapRetryCandidates = 32
	snapRetryTries      = 4
	// snapRetryDeep is the wider scan used when every candidate near the pin
	// reaches almost nothing, which is what a pin inside a dead pocket looks
	// like: a park, a private forecourt, a few service roads behind a gate.
	snapRetryDeep = 512
	// snapRetryPocket is the reachability floor for a usable start. Below it
	// the search would walk a few dozen nodes and give up.
	snapRetryPocket = 512
)

// Error reasons are shared by the HTTP body and route_errors_total, so the
// metric labels and the client-facing vocabulary cannot drift apart.
const (
	reasonSnap        = "snap"
	reasonTooFar      = "too_far"
	reasonBudget      = "budget"
	reasonNoRoute     = "no_route"
	reasonCrossRegion = "cross_region"
	reasonDeadline    = "deadline"
)

// failRoute maps a routing error to the documented HTTP contract. Cross-region
// legs use the same mapping as a single-region search. searchCtx is the soft
// budget context: when it expired but the request deadline did not, the engine
// gave up on a route it could not finish, which is a 422, not a 504.
func (s *server) failRoute(w http.ResponseWriter, err error, budget time.Duration) {
	switch {
	case errors.Is(err, graph.ErrCanceled):
		if budget > 0 {
			s.m.budget.Add(1)
			httpErrReason(w, 422, reasonBudget, "search budget "+budget.String()+" exceeded (route too long)")
			return
		}
		s.m.canceled.Add(1)
		httpErrReason(w, 504, reasonDeadline, "deadline exceeded ("+s.timeout.String()+")")
	case errors.Is(err, graph.ErrTooFar):
		s.m.tooFar.Add(1)
		httpErrReason(w, 422, reasonTooFar, err.Error())
	case errors.Is(err, graph.ErrNoRoute):
		s.m.errNoRoute.Add(1)
		httpErrReason(w, 422, reasonNoRoute, err.Error())
	default:
		// Anything else is a server-side failure; the text stays in the log
		// surface, not the response.
		log.Printf("route error: %v", err)
		httpErr(w, 500, "internal error")
	}
}

// retryNoRoute retries a failed search from the next-nearest segment snaps.
// Snapping only looks at geometry, so a pin in a park or inside a forecourt
// lands on a dead end while a connected road may sit a few dozen metres away;
// a dead-end start expands nothing, which is why the first search returns in
// microseconds.
func (s *server) retryNoRoute(ctx context.Context, rg *region, p routeParams, fLat, fLon, tLat, tLon float64, failF, failT graph.Snap) (graph.SnapRoute, graph.Snap, graph.Snap, bool) {
	// Only starts that actually reach somewhere are worth a search. A pocket
	// is often dozens of nodes wide, so the first scan can consist entirely of
	// dead starts; widen it once before falling back to plain distance order.
	starts := rg.r.SnapEdges(fLat, fLon, s.maxSnapM, snapRetryCandidates)
	usable := usableStarts(rg.g, starts)
	if len(usable) == 0 {
		starts = rg.r.SnapEdges(fLat, fLon, s.maxSnapM, snapRetryDeep)
		usable = usableStarts(rg.g, starts)
	}
	if len(usable) == 0 {
		usable = starts
	}
	candsT := rg.r.SnapEdges(tLat, tLon, s.maxSnapM, snapRetryCandidates)
	type pair struct {
		a, b graph.Snap
		d    float64
	}
	pairs := make([]pair, 0, len(usable)*len(candsT))
	for _, a := range usable {
		for _, b := range candsT {
			if a.Node == failF.Node && b.Node == failT.Node {
				continue
			}
			pairs = append(pairs, pair{a, b, a.Dist + b.Dist})
		}
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].d < pairs[j].d })
	for i := 0; i < len(pairs) && i < snapRetryTries; i++ {
		if ctx.Err() != nil {
			break
		}
		if res, err := rg.r.RouteSnapCtx(ctx, pairs[i].a, pairs[i].b, p.restrictions); err == nil {
			return res, pairs[i].a, pairs[i].b, true
		}
	}
	return graph.SnapRoute{}, graph.Snap{}, graph.Snap{}, false
}

// usableStarts keeps the snaps whose start reaches more than a pocket. An
// isolated network is kept when nothing else is nearby, so a small island still
// gets its retry.
func usableStarts(g *graph.Graph, snaps []graph.Snap) []graph.Snap {
	out := make([]graph.Snap, 0, len(snaps))
	for _, a := range snaps {
		if g.ForwardSize(a.Node, snapRetryPocket) > snapRetryPocket {
			out = append(out, a)
		}
	}
	return out
}

func (s *server) handleRoute(w http.ResponseWriter, req *http.Request) {
	p, err := readRouteParams(req)
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	fLat, fLon, tLat, tLon := p.fLat, p.fLon, p.tLat, p.tLon
	if !inIndonesia(fLat, fLon) || !inIndonesia(tLat, tLon) {
		httpErr(w, 400, "coordinates outside Indonesia")
		return
	}
	// Cheap contract check before any graph work: the great-circle distance is
	// a lower bound on the driven distance, so it rejects out-of-range requests
	// without spending a snap, a workspace, or a cache entry. It cannot catch a
	// short-line/long-detour route; the post-route check below does that.
	if s.maxRouteM > 0 {
		if d := graph.Haversine(fLat, fLon, tLat, tLon); d > s.maxRouteM {
			s.m.tooFar.Add(1)
			httpErrReason(w, 422, reasonTooFar, fmt.Sprintf("route too long: straight-line %.1f km exceeds %.0f km limit", d/1000, s.maxRouteM/1000))
			return
		}
	}

	start := time.Now()

	// Cache hits are free, so the revalidation check runs before the limiter: a
	// 304 never spends routing budget. The validator is only emitted on the
	// 304 and 200 paths, so a throttled client cannot harvest one from a 429.
	var etag string
	if s.maxAge > 0 {
		etag = routeETag(s.fp, p)
		if match := req.Header.Get("If-None-Match"); match != "" && etagMatches(match, etag) {
			w.Header().Set("ETag", etag)
			w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", s.maxAge))
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}

	// Everything past this point competes for a routing slot.
	if !s.rl.allowRequest(w, req, s.m) {
		return
	}

	rgF, snapF, ok1 := s.pickSnap(fLat, fLon)
	rgT, snapT, ok2 := s.pickSnap(tLat, tLon)
	if !ok1 || !ok2 {
		s.m.errSnap.Add(1)
		httpErrReason(w, 422, reasonSnap, "no road within snap distance (region not loaded?)")
		return
	}
	// The deadline follows the client: if it disconnects, req.Context() ends and
	// the search stops at the next check instead of burning a workspace slot.
	ctx, cancel := context.WithTimeout(req.Context(), s.timeout)
	defer cancel()
	// The search works against the soft budget, which expires first. A search
	// that cannot finish then reports a route-level failure the client can act
	// on, instead of racing the hard deadline and surfacing as a 504 whose
	// occurrence depends on how busy the box is.
	budget := s.budget
	if budget <= 0 || budget > s.timeout {
		budget = s.timeout
	}
	sctx, scancel := context.WithTimeout(ctx, budget)
	defer scancel()
	// fail closes over both contexts: only the soft budget expiring on its own
	// is the engine's own decision, and only that maps to 422.
	fail := func(err error) {
		var b time.Duration
		if sctx.Err() != nil && ctx.Err() == nil {
			b = budget
		}
		s.failRoute(w, err, b)
	}

	var cross *stitchedRoute
	rg := rgF
	if rgF != rgT {
		// Region extracts overlap (the Java file carries Bali, the Nusa Tenggara
		// file starts there), so the nearest region per endpoint can differ even
		// when one file holds both ends. Retry inside each candidate before
		// declaring the route cross-region.
		if sa, sb, ok := s.snapBoth(rgF, fLat, fLon, tLat, tLon); ok {
			rg, snapF, snapT = rgF, sa, sb
		} else if sa, sb, ok := s.snapBoth(rgT, fLat, fLon, tLat, tLon); ok {
			rg, snapF, snapT = rgT, sa, sb
		} else if c, portF, portT, ok := connectorFor(s.connectors, rgF.name, rgT.name); ok {
			cross, err = s.routeStitched(sctx, rgF, rgT, snapF, snapT, c, portF, portT, p)
			if err != nil {
				fail(err)
				return
			}
			s.m.crossRegionStitched.Add(1)
		} else {
			s.m.errCrossRegion.Add(1)
			httpErrReason(w, 422, reasonCrossRegion, "no ferry connector between "+rgF.name+" and "+rgT.name)
			return
		}
		if cross == nil {
			s.m.crossRegionFixed.Add(1)
		}
	}

	var res graph.SnapRoute
	if cross == nil {
		res, err = rg.r.RouteSnapCtx(sctx, snapF, snapT, p.restrictions)
		if err != nil && errors.Is(err, graph.ErrNoRoute) {
			// A dead-end snap is worth one more look before the client sees 422.
			if res2, a, b, ok := s.retryNoRoute(sctx, rg, p, fLat, fLon, tLat, tLon, snapF, snapT); ok {
				res, snapF, snapT, err = res2, a, b, nil
				s.m.snapRetry.Add(1)
			}
		}
	}
	if err != nil {
		fail(err)
		return
	}
	if cross != nil {
		res = cross.SnapRoute
	}
	if s.maxRouteM > 0 && float64(res.DistanceM) > s.maxRouteM {
		s.m.tooFar.Add(1)
		httpErrReason(w, 422, reasonTooFar, fmt.Sprintf("route distance %.1f km exceeds %.0f km limit", float64(res.DistanceM)/1000, s.maxRouteM/1000))
		return
	}
	s.m.expansions.Add(int64(res.Expansions))
	if res.Fallback {
		s.m.fallback.Add(1)
	}
	if res.Relaxed {
		s.m.relaxed.Add(1)
	}

	// Geometry follows the road: it starts and ends at the projected points.
	// The raw pin can still be requested with &pin=1 (straight spur included).
	var pts [][2]float64
	if cross != nil {
		pts = cross.pts
	} else {
		pts = make([][2]float64, 0, len(res.Nodes)+2)
		if p.pin {
			pts = append(pts, [2]float64{fLat, fLon})
		}
		pts = append(pts, res.Head)
		for _, n := range res.Nodes {
			lat, lon := rg.g.Coords(n)
			pts = append(pts, [2]float64{lat, lon})
		}
		pts = append(pts, res.Tail)
		if p.pin {
			pts = append(pts, [2]float64{tLat, tLon})
		}
	}
	// keep maps each surviving geometry point back to its index before
	// Simplify, so index-based fixtures (ferry spans) survive the drop.
	var keep []int
	if p.simplify > 0 {
		pts, keep = simplifyIndexed(pts, p.simplify)
	}

	regionName := rg.name
	if cross != nil {
		regionName = cross.region
	}
	w.Header().Set("X-Route-Ms", strconv.FormatInt(time.Since(start).Milliseconds(), 10))
	w.Header().Set("X-Route-Region", regionName)
	w.Header().Set("X-Route-Expansions", strconv.Itoa(res.Expansions))
	w.Header().Set("X-Route-Trim", trimLabel(res.TrimStart, res.TrimEnd))
	body := map[string]any{
		// echo of what the server actually parsed, so clients can prove the
		// round trip (wrong lat/lng order shows up here immediately)
		"from":        []float64{fLat, fLon},
		"to":          []float64{tLat, tLon},
		"distance_m":  res.DistanceM,
		"duration_s":  float64(res.TimeDs) / 10,
		"region":      regionName,
		"snap_from_m": math.Round(snapF.Dist),
		"snap_to_m":   math.Round(snapT.Dist),
		// projected points (on the road) as [lat, lng], for a pin layer
		"snap_from": []float64{round6(snapF.Lat), round6(snapF.Lon)},
		"snap_to":   []float64{round6(snapT.Lat), round6(snapT.Lon)},
	}
	if res.FerryM > 0 {
		body["ferry_m"] = res.FerryM
		body["has_ferry"] = true
		if cross != nil {
			if spans := ferrySpansJSON(cross.ferrySpans, keep); len(spans) > 0 {
				body["ferry_spans"] = spans
			}
		} else {
			// FerrySpans index the trimmed Nodes; shift past the optional pin
			// and Head, then through keep when the geometry was simplified.
			off := 1
			if p.pin {
				off = 2
			}
			spans := make([][2]int, 0, len(res.FerrySpans))
			for _, sp := range res.FerrySpans {
				a0, a1 := int(sp[0])+off, int(sp[1])+off
				if keep != nil {
					var ok bool
					if a0, a1, ok = projectSpan(keep, a0, a1); !ok {
						continue
					}
				}
				spans = append(spans, [2]int{a0, a1})
			}
			if len(spans) > 0 {
				body["ferry_spans"] = spans
			}
		}
	}
	if cross != nil {
		legs := make([]map[string]any, 0, len(cross.legs))
		for _, leg := range cross.legs {
			legs = append(legs, leg.legJSON(keep))
		}
		body["legs"] = legs
	}
	if res.Relaxed {
		body["relaxed"] = true
		body["epsilon"] = res.Epsilon
		body["turn_penalty"] = res.TurnScale
	}
	if res.Fallback {
		body["fallback"] = true
	}
	if res.TrimStart || res.TrimEnd {
		body["trimmed"] = map[string]bool{"from": res.TrimStart, "to": res.TrimEnd}
	}
	// bbox lets the app fit the camera without decoding the polyline
	minLat, minLon, maxLat, maxLon := pts[0][0], pts[0][1], pts[0][0], pts[0][1]
	for _, pt := range pts[1:] {
		minLat, maxLat = math.Min(minLat, pt[0]), math.Max(maxLat, pt[0])
		minLon, maxLon = math.Min(minLon, pt[1]), math.Max(maxLon, pt[1])
	}
	body["bbox"] = []float64{minLat, minLon, maxLat, maxLon}
	body["points_n"] = len(pts)

	switch p.format {
	case "polyline":
		enc := EncodePolyline(pts, 5)
		body["polyline"] = enc
		// The Google polyline charset includes '\\' (backslash). In JSON that
		// character is displayed as two, so copy-pasting it from a JSON viewer
		// breaks naive decoders. Base64 has no such ambiguity.
		body["polyline_b64"] = base64.StdEncoding.EncodeToString([]byte(enc))
	case "geojson":
		// GeoJSON is safe to copy-paste: no backslash escapes, so it drops
		// straight into geojson.io without tripping a polyline decoder.
		coords := make([][2]float64, len(pts))
		for i, pt := range pts {
			coords[i] = [2]float64{pt[1], pt[0]} // geojson is lng,lat
		}
		body["geojson"] = map[string]any{
			"type": "FeatureCollection",
			"features": []any{
				map[string]any{
					"type":       "Feature",
					"properties": map[string]any{"kind": "route", "distance_m": res.DistanceM},
					"geometry":   map[string]any{"type": "LineString", "coordinates": coords},
				},
			},
		}
	default:
		body["points"] = pts
	}
	if etag != "" {
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", s.maxAge))
	}
	writeJSON(w, body)
}

// readRouteParams accepts both ?from=lat,lng query strings and a JSON POST
// body, so clients can avoid URL escaping.
func readRouteParams(req *http.Request) (routeParams, error) {
	q := req.URL.Query()
	out := routeParams{
		format:       q.Get("format"),
		restrictions: q.Get("restrictions") != "0",
		pin:          q.Get("pin") == "1" || q.Get("pin") == "true",
	}
	fromRaw, toRaw := q.Get("from"), q.Get("to")
	if raw := q.Get("simplify"); raw != "" {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return out, fmt.Errorf("simplify must be a non-negative number")
		}
		out.simplify = v
	}

	if req.Method == http.MethodPost {
		var body struct {
			From         json.RawMessage `json:"from"`
			To           json.RawMessage `json:"to"`
			Format       string          `json:"format"`
			Simplify     *float64        `json:"simplify"`
			Restrictions *bool           `json:"restrictions"`
			Pin          *bool           `json:"pin"`
		}
		dec := json.NewDecoder(io.LimitReader(req.Body, 8<<10))
		if err := dec.Decode(&body); err != nil {
			return out, fmt.Errorf("bad json body: %w", err)
		}
		if len(body.From) > 0 {
			fromRaw = rawPointText(body.From)
		}
		if len(body.To) > 0 {
			toRaw = rawPointText(body.To)
		}
		if body.Format != "" {
			out.format = body.Format
		}
		if body.Simplify != nil {
			if *body.Simplify < 0 || math.IsNaN(*body.Simplify) || math.IsInf(*body.Simplify, 0) {
				return out, fmt.Errorf("simplify must be a non-negative number")
			}
			out.simplify = *body.Simplify
		}
		if body.Restrictions != nil {
			out.restrictions = *body.Restrictions
		}
		if body.Pin != nil {
			out.pin = *body.Pin
		}
	}

	var err error
	if out.fLat, out.fLon, err = parsePoint(fromRaw); err != nil {
		return out, fmt.Errorf("from must be \"lat,lng\" or {\"lat\":..,\"lng\":..}")
	}
	if out.tLat, out.tLon, err = parsePoint(toRaw); err != nil {
		return out, fmt.Errorf("to must be \"lat,lng\" or {\"lat\":..,\"lng\":..}")
	}
	return out, nil
}

// rawPointText unwraps a JSON value into the text form parsePoint expects:
// a quoted string stays bare, an object keeps its braces.
func rawPointText(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

// parsePoint accepts "lat,lng" and {"lat":..,"lng":..} (also "lon"/"lng"/"long").
func parsePoint(raw string) (lat, lon float64, err error) {
	if strings.HasPrefix(raw, "{") {
		var obj map[string]float64
		if err := json.Unmarshal([]byte(raw), &obj); err != nil {
			return 0, 0, fmt.Errorf("bad point object")
		}
		lat, lon = obj["lat"], obj["lng"]
		if lon == 0 {
			lon = obj["lon"]
		}
		if lon == 0 {
			lon = obj["long"]
		}
		if lat == 0 && lon == 0 {
			return 0, 0, fmt.Errorf("bad point object")
		}
		return lat, lon, nil
	}
	return parsePointString(raw)
}

func parsePointString(s string) (lat, lon float64, err error) {
	parts := strings.Split(s, ",")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("bad point")
	}
	lat, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	lon, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err1 != nil || err2 != nil {
		return 0, 0, fmt.Errorf("bad point")
	}
	return lat, lon, nil
}

func inIndonesia(lat, lon float64) bool {
	return lat > -12 && lat < 8 && lon > 93 && lon < 142
}

func httpErr(w http.ResponseWriter, code int, msg string) {
	httpErrReason(w, code, "", msg)
}

// httpErrReason adds the machine-readable reason used by clients to choose a
// human-readable message. The error text stays as-is for existing consumers.
func httpErrReason(w http.ResponseWriter, code int, reason, msg string) {
	// An error body is not a cacheable representation of the route: drop any
	// validator a previous branch may have set and forbid storing the answer.
	w.Header().Del("ETag")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	type errorBody struct {
		Error  string `json:"error"`
		Reason string `json:"reason,omitempty"`
	}
	json.NewEncoder(w).Encode(errorBody{Error: msg, Reason: reason})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

// cors keeps browser-based clients (flutter web, map previews) working.
func cors(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, If-None-Match")
		// ETag/Cache-Control/Retry-After are exposed so a Flutter web client can
		// revalidate a cached route and read the back-off hint on a 429.
		w.Header().Set("Access-Control-Expose-Headers",
			"X-Route-Ms, X-Route-Region, X-Route-Expansions, X-Route-Trim, ETag, Cache-Control, Retry-After")
		// Chrome asks for this before an HTTPS page may reach a LAN/localhost
		// service (Private Network Access), e.g. hoppscotch.io -> localhost.
		if r.Header.Get("Access-Control-Request-Private-Network") == "true" {
			w.Header().Set("Access-Control-Allow-Private-Network", "true")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func gzipWrap(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Whether the body is gzipped or not, the chosen representation
		// depends on Accept-Encoding, so every response must say so.
		w.Header().Add("Vary", "Accept-Encoding")
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			h.ServeHTTP(w, r)
			return
		}
		g := &gzipRW{ResponseWriter: w}
		defer g.close()
		h.ServeHTTP(g, r)
	})
}

// gzipRW starts the gzip stream on the first body write, so a status without a
// body (304, 204) keeps Content-Encoding off, as RFC 9110 requires.
type gzipRW struct {
	http.ResponseWriter
	gz    *gzip.Writer
	wrote bool
	live  bool
}

func (g *gzipRW) WriteHeader(code int) {
	if g.wrote {
		return
	}
	g.wrote = true
	if code >= 200 && code != http.StatusNoContent && code != http.StatusNotModified {
		g.Header().Set("Content-Encoding", "gzip")
		g.Header().Del("Content-Length")
		g.gz = gzip.NewWriter(g.ResponseWriter)
		g.live = true
	}
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipRW) Write(b []byte) (int, error) {
	if !g.wrote {
		g.WriteHeader(http.StatusOK)
	}
	if !g.live {
		return g.ResponseWriter.Write(b)
	}
	return g.gz.Write(b)
}

func (g *gzipRW) close() {
	if g.live {
		g.gz.Close()
	}
}

// EncodePolyline encodes points with the given precision (5 = ~1.1m).
func EncodePolyline(pts [][2]float64, precision int) string {
	scale := math.Pow(10, float64(precision))
	var sb strings.Builder
	var pLat, pLon int64
	for _, p := range pts {
		lat := int64(math.Round(p[0] * scale))
		lon := int64(math.Round(p[1] * scale))
		sb.WriteString(encodeInt(lat - pLat))
		sb.WriteString(encodeInt(lon - pLon))
		pLat, pLon = lat, lon
	}
	return sb.String()
}

func encodeInt(v int64) string {
	u := v << 1
	if v < 0 {
		u = ^u
	}
	var b []byte
	for u >= 0x20 {
		b = append(b, byte((0x20|(u&0x1f))+63))
		u >>= 5
	}
	b = append(b, byte(u+63))
	return string(b)
}

// Simplify runs Douglas-Peucker with a tolerance in meters.
func Simplify(pts [][2]float64, tolM float64) [][2]float64 {
	out, _ := simplifyIndexed(pts, tolM)
	return out
}

// simplifyIndexed is Simplify plus the original index of every kept point, so
// callers holding indices into pts can re-project them after the drop.
func simplifyIndexed(pts [][2]float64, tolM float64) ([][2]float64, []int) {
	if len(pts) < 3 {
		idx := make([]int, len(pts))
		for i := range idx {
			idx[i] = i
		}
		return pts, idx
	}
	keep := make([]bool, len(pts))
	keep[0], keep[len(pts)-1] = true, true
	type span struct{ a, b int }
	stack := []span{{0, len(pts) - 1}}
	for len(stack) > 0 {
		s := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		maxD, idx := 0.0, -1
		for i := s.a + 1; i < s.b; i++ {
			d := perpMeters(pts[s.a], pts[s.b], pts[i])
			if d > maxD {
				maxD, idx = d, i
			}
		}
		if idx >= 0 && maxD > tolM {
			keep[idx] = true
			stack = append(stack, span{s.a, idx}, span{idx, s.b})
		}
	}
	out := make([][2]float64, 0, len(pts))
	kept := make([]int, 0, len(pts))
	for i, k := range keep {
		if k {
			out = append(out, pts[i])
			kept = append(kept, i)
		}
	}
	return out, kept
}

// projectSpan maps an inclusive index range in the pre-simplify geometry into
// the kept slice. It shrinks the range onto the nearest surviving points and
// reports false when no sample in it survived.
func projectSpan(kept []int, a, b int) (int, int, bool) {
	i := sort.SearchInts(kept, a)
	j := sort.SearchInts(kept, b+1) - 1
	if i >= len(kept) || j < i {
		return 0, 0, false
	}
	return i, j, true
}

func perpMeters(a, b, p [2]float64) float64 {
	latM := 111320.0
	lonM := 111320.0 * math.Cos(a[0]*math.Pi/180)
	ax, ay := a[1]*lonM, a[0]*latM
	bx, by := b[1]*lonM, b[0]*latM
	px, py := p[1]*lonM, p[0]*latM
	dx, dy := bx-ax, by-ay
	l := math.Hypot(dx, dy)
	if l == 0 {
		return math.Hypot(px-ax, py-ay)
	}
	return math.Abs((px-ax)*dy-(py-ay)*dx) / l
}
