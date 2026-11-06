package main

import (
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"routeid/internal/graph"
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
	regions  []*region
	maxSnapM float64
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
	conc := flag.Int("conc", 4, "concurrent routing workspaces per region")
	snap := flag.Float64("snap", 3000, "max snap distance in meters")
	flag.Parse()

	if len(graphs) == 0 {
		log.Fatal("need at least one -graph")
	}

	s := &server{maxSnapM: *snap}
	for _, p := range expand(graphs) {
		g, err := graph.Open(p)
		if err != nil {
			log.Fatalf("open %s: %v", p, err)
		}
		mnLat, mnLon, mxLat, mxLon := g.Bounds()
		const pad = 0.05
		rg := &region{
			name: strings.TrimSuffix(filepath.Base(p), ".graph"), g: g, r: graph.NewRouter(g, *conc),
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

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/route", s.handleRoute)

	srv := &http.Server{
		Addr:              *addr,
		Handler:           gzipWrap(mux),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      60 * time.Second,
	}
	log.Printf("listening on %s (%d region(s))", *addr, len(s.regions))
	log.Fatal(srv.ListenAndServe())
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

// pickSnap returns the region whose graph has the closest node to the point.
// Bounding boxes overlap between island extracts, so snapping decides.
func (s *server) pickSnap(lat, lon float64) (*region, uint32, float64, bool) {
	var bestRg *region
	var bestNode uint32
	bestD := s.maxSnapM
	for _, rg := range s.regions {
		if !rg.contains(lat, lon) {
			continue
		}
		n, d, ok := rg.g.Nearest(lat, lon, bestD)
		if ok && d <= bestD {
			bestRg, bestNode, bestD = rg, n, d
		}
	}
	if bestRg == nil {
		return nil, 0, 0, false
	}
	return bestRg, bestNode, bestD, true
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
	}
	out := struct {
		OK      bool   `json:"ok"`
		Regions []info `json:"regions"`
	}{OK: true}
	for _, rg := range s.regions {
		rs := rg.g.Restrictions()
		out.Regions = append(out.Regions, info{rg.name, rg.g.NodeCount, rg.g.EdgeCount, rg.minLat, rg.minLon, rg.maxLat, rg.maxLon,
			rs.Ban, rs.Only, rs.Skipped})
	}
	writeJSON(w, out)
}

func (s *server) handleRoute(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	fLat, fLon, err1 := parsePoint(q.Get("from"))
	tLat, tLon, err2 := parsePoint(q.Get("to"))
	if err1 != nil || err2 != nil {
		httpErr(w, 400, "from/to must be lat,lng")
		return
	}
	if !inIndonesia(fLat, fLon) || !inIndonesia(tLat, tLon) {
		httpErr(w, 400, "coordinates outside Indonesia")
		return
	}

	start := time.Now()
	rgF, src, d1, ok1 := s.pickSnap(fLat, fLon)
	rgT, dst, d2, ok2 := s.pickSnap(tLat, tLon)
	if !ok1 || !ok2 {
		httpErr(w, 422, "no road within snap distance (region not loaded?)")
		return
	}
	if rgF != rgT {
		httpErr(w, 422, "cross-region route not supported ("+rgF.name+" -> "+rgT.name+")")
		return
	}
	rg := rgF
	strict := q.Get("restrictions") != "0"
	res, err := rg.r.RouteStrict(src, dst, strict)
	if err != nil {
		httpErr(w, 422, err.Error())
		return
	}

	pts := make([][2]float64, 0, len(res.Nodes)+2)
	pts = append(pts, [2]float64{fLat, fLon})
	for _, n := range res.Nodes {
		lat, lon := rg.g.Coords(n)
		pts = append(pts, [2]float64{lat, lon})
	}
	pts = append(pts, [2]float64{tLat, tLon})
	if tol, _ := strconv.ParseFloat(q.Get("simplify"), 64); tol > 0 {
		pts = Simplify(pts, tol)
	}

	w.Header().Set("X-Route-Ms", strconv.FormatInt(time.Since(start).Milliseconds(), 10))
	w.Header().Set("X-Route-Region", rg.name)
	w.Header().Set("X-Route-Expansions", strconv.Itoa(res.Expansions))
	body := map[string]any{
		"distance_m":  res.DistanceM,
		"duration_s":  float64(res.TimeDs) / 10,
		"region":      rg.name,
		"snap_from_m": math.Round(d1),
		"snap_to_m":   math.Round(d2),
	}
	if res.Fallback {
		body["fallback"] = true
	}
	if q.Get("format") == "polyline" {
		body["polyline"] = EncodePolyline(pts, 5)
	} else {
		body["points"] = pts
	}
	writeJSON(w, body)
}

func parsePoint(s string) (lat, lon float64, err error) {
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
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

func gzipWrap(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			h.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		defer gz.Close()
		h.ServeHTTP(&gzipRW{ResponseWriter: w, w: gz}, r)
	})
}

type gzipRW struct {
	http.ResponseWriter
	w io.Writer
}

func (g *gzipRW) Write(b []byte) (int, error) { return g.w.Write(b) }

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
	if len(pts) < 3 {
		return pts
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
	for i, k := range keep {
		if k {
			out = append(out, pts[i])
		}
	}
	return out
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
