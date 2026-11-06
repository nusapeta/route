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
	"strconv"
	"strings"
	"time"

	"routeid/internal/graph"
)

type server struct {
	g        *graph.Graph
	r        *graph.Router
	maxSnapM float64
}

func main() {
	path := flag.String("graph", "map.graph", "graph file")
	addr := flag.String("addr", ":8080", "listen address")
	conc := flag.Int("conc", 4, "concurrent routing workspaces")
	snap := flag.Float64("snap", 3000, "max snap distance in meters")
	flag.Parse()

	g, err := graph.Open(*path)
	if err != nil {
		log.Fatalf("open graph: %v", err)
	}
	st, _ := os.Stat(*path)
	log.Printf("graph: %d nodes, %d edges, %.0f MB on disk (mmap)", g.NodeCount, g.EdgeCount, float64(st.Size())/1e6)

	s := &server{g: g, r: graph.NewRouter(g, *conc), maxSnapM: *snap}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("/route", s.handleRoute)

	srv := &http.Server{
		Addr:              *addr,
		Handler:           gzipWrap(mux),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	log.Printf("listening on %s", *addr)
	log.Fatal(srv.ListenAndServe())
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
	src, d1, ok1 := s.g.Nearest(fLat, fLon, s.maxSnapM)
	dst, d2, ok2 := s.g.Nearest(tLat, tLon, s.maxSnapM)
	if !ok1 || !ok2 {
		httpErr(w, 422, "no road within snap distance")
		return
	}
	res, err := s.r.Route(src, dst)
	if err != nil {
		httpErr(w, 422, err.Error())
		return
	}

	pts := make([][2]float64, 0, len(res.Nodes)+2)
	pts = append(pts, [2]float64{fLat, fLon})
	for _, n := range res.Nodes {
		lat, lon := s.g.Coords(n)
		pts = append(pts, [2]float64{lat, lon})
	}
	pts = append(pts, [2]float64{tLat, tLon})
	if tol, _ := strconv.ParseFloat(q.Get("simplify"), 64); tol > 0 {
		pts = Simplify(pts, tol)
	}

	w.Header().Set("X-Route-Ms", strconv.FormatInt(time.Since(start).Milliseconds(), 10))
	switch q.Get("format") {
	case "polyline":
		writeJSON(w, map[string]any{
			"distance_m":  res.DistanceM,
			"duration_s":  float64(res.TimeDs) / 10,
			"polyline":    EncodePolyline(pts, 5),
			"snap_from_m": math.Round(d1),
			"snap_to_m":   math.Round(d2),
		})
	default:
		writeJSON(w, map[string]any{
			"distance_m":  res.DistanceM,
			"duration_s":  float64(res.TimeDs) / 10,
			"points":      pts,
			"snap_from_m": math.Round(d1),
			"snap_to_m":   math.Round(d2),
		})
	}
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
