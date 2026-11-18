package main

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/nusapeta/route/internal/graph"
)

const maxViaPoints = 5

// parseAvoidValues accepts both comma and pipe separators so avoid composes
// with the via syntax without a second escape layer.
func parseAvoidValues(raw string) (graph.AvoidMask, error) {
	fields := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '|' })
	return graph.ParseAvoid(fields)
}

func parseAltValue(raw string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 || n > 3 {
		return 0, fmt.Errorf("alt must be 0..3")
	}
	return n, nil
}

// parseViaValues parses "lat,lng|lat,lng" and enforces the point cap before a
// single search is spent on it.
func parseViaValues(raw string) ([][2]float64, error) {
	parts := strings.Split(raw, "|")
	if len(parts) > maxViaPoints {
		return nil, fmt.Errorf("via accepts at most %d points", maxViaPoints)
	}
	out := make([][2]float64, 0, len(parts))
	for _, part := range parts {
		lat, lon, err := parsePoint(strings.TrimSpace(part))
		if err != nil {
			return nil, fmt.Errorf("bad via point %q", part)
		}
		out = append(out, [2]float64{lat, lon})
	}
	return out, nil
}

// routeOnce runs one A* search with the request's mask and optional ban set.
func (s *server) routeOnce(ctx context.Context, rg *region, a, b graph.Snap, p routeParams, ban map[uint32]bool) (graph.SnapRoute, error) {
	return rg.r.WithOptions(graph.RouteOptions{Avoid: p.avoid, Ban: ban}).RouteSnapCtx(ctx, a, b, p.restrictions)
}

// pickSnap returns the region whose graph has the closest usable road segment.
// Bounding boxes overlap between island extracts, so snapping decides. The
// avoid mask keeps a pin on a toll road from selecting that road.
func (s *server) pickSnap(lat, lon float64, avoid graph.AvoidMask) (*region, graph.Snap, bool) {
	var bestRg *region
	var best graph.Snap
	bestD := s.maxSnapM
	for _, rg := range s.regions {
		if !rg.contains(lat, lon) {
			continue
		}
		sn, ok := rg.r.WithOptions(graph.RouteOptions{Avoid: avoid}).SnapEdgeAllowed(lat, lon, bestD)
		if ok && sn.Dist <= bestD {
			bestRg, best, bestD = rg, sn, sn.Dist
		}
	}
	if bestRg == nil {
		return nil, graph.Snap{}, false
	}
	return bestRg, best, true
}

func (s *server) snapBothOpts(rg *region, fLat, fLon, tLat, tLon float64, avoid graph.AvoidMask) (graph.Snap, graph.Snap, bool) {
	r := rg.r.WithOptions(graph.RouteOptions{Avoid: avoid})
	a, ok1 := r.SnapEdgeAllowed(fLat, fLon, s.maxSnapM)
	b, ok2 := r.SnapEdgeAllowed(tLat, tLon, s.maxSnapM)
	if !ok1 || !ok2 {
		return graph.Snap{}, graph.Snap{}, false
	}
	return a, b, true
}

// snapRoutePoints builds the same geometry the /route body uses. The helpers
// here pass pin=false because each via leg must share exactly one point with
// its neighbour; the outer leg builder adds the pin points once.
func snapRoutePoints(res graph.SnapRoute, fLat, fLon, tLat, tLon float64, g *graph.Graph, pin bool) [][2]float64 {
	pts := make([][2]float64, 0, len(res.Nodes)+2)
	if pin {
		pts = append(pts, [2]float64{fLat, fLon})
	}
	pts = append(pts, res.Head)
	for _, n := range res.Nodes {
		lat, lon := g.Coords(n)
		pts = append(pts, [2]float64{lat, lon})
	}
	pts = append(pts, res.Tail)
	if pin {
		pts = append(pts, [2]float64{tLat, tLon})
	}
	return pts
}

// routeAlternative is one extra path returned for alt>=1. It carries geometry
// so a client can draw it without issuing another request.
type routeAlternative struct {
	DistanceM uint32       `json:"distance_m"`
	DurationS float64      `json:"duration_s"`
	Points    [][2]float64 `json:"points"`
	PointsN   int          `json:"points_n"`
}

// edgeOverlap is the shared fraction of the two paths weighted by edge length.
// A value above 0.8 means less than 20% of the edge length changed.
func edgeOverlap(g *graph.Graph, a, b []uint32) float64 {
	set := make(map[uint32]bool, len(a))
	var total, shared uint32
	for _, e := range a {
		set[e] = true
		total += g.EdgeLenM(e)
	}
	for _, e := range b {
		total += g.EdgeLenM(e)
		if set[e] {
			shared += g.EdgeLenM(e)
		}
	}
	if total == 0 {
		return 1
	}
	return float64(2*shared) / float64(total)
}

// banPath excludes every edge after the first, which is forced by the start
// snap. Later rounds therefore have to diverge from the whole accepted set.
func banPath(ban map[uint32]bool, edges []uint32) {
	for _, e := range edges[minInt(1, len(edges)):] {
		ban[e] = true
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// routeAlternatives performs at most p.alt extra searches. Each round bans the
// edges of every path already accepted, so the returned routes are distinct by
// construction; the overlap check keeps the ≥20% contract.
func (s *server) routeAlternatives(ctx context.Context, rg *region, a, b graph.Snap, p routeParams, base graph.SnapRoute) ([]routeAlternative, int64) {
	ban := make(map[uint32]bool)
	var expansions int64
	banPath(ban, base.Edges)
	out := make([]routeAlternative, 0, p.alt)
	for range p.alt {
		cand, err := s.routeOnce(ctx, rg, a, b, p, ban)
		if err != nil {
			break
		}
		expansions += int64(cand.Expansions)
		banPath(ban, cand.Edges)
		if edgeOverlap(rg.g, base.Edges, cand.Edges) > 0.8 {
			continue
		}
		pts := snapRoutePoints(cand, p.fLat, p.fLon, p.tLat, p.tLon, rg.g, p.pin)
		if p.simplify > 0 {
			pts, _ = simplifyIndexed(pts, p.simplify)
		}
		out = append(out, routeAlternative{
			DistanceM: cand.DistanceM,
			DurationS: float64(cand.TimeDs) / 10,
			Points:    pts,
			PointsN:   len(pts),
		})
	}
	return out, expansions
}

type viaLeg struct {
	DistanceM uint32  `json:"distance_m"`
	DurationS float64 `json:"duration_s"`
	StartIdx  int     `json:"start_index"`
	EndIdx    int     `json:"end_index"`
	PointsN   int     `json:"points_n"`
}

type viaResult struct {
	res  graph.SnapRoute
	f, t graph.Snap
	pts  [][2]float64
	legs []viaLeg
}

// routeVia snaps every waypoint and routes consecutive pairs as independent
// legs. Banning is deliberately per-leg: a via itinerary is not an alternative
// search, and a failed leg must surface as an ordinary 422.
func (s *server) routeVia(ctx context.Context, rg *region, points [][2]float64, p routeParams) (viaResult, error) {
	snapper := rg.r.WithOptions(graph.RouteOptions{Avoid: p.avoid})
	snaps := make([]graph.Snap, len(points))
	for i, pt := range points {
		sn, ok := snapper.SnapEdgeAllowed(pt[0], pt[1], s.maxSnapM)
		if !ok {
			return viaResult{}, fmt.Errorf("%w: via point %d could not snap", graph.ErrNoRoute, i)
		}
		snaps[i] = sn
	}
	var out viaResult
	out.f, out.t = snaps[0], snaps[len(snaps)-1]
	out.legs = make([]viaLeg, 0, len(snaps)-1)
	for i := 0; i+1 < len(snaps); i++ {
		leg, err := s.routeOnce(ctx, rg, snaps[i], snaps[i+1], p, nil)
		if err != nil {
			return viaResult{}, fmt.Errorf("via leg %d: %w", i+1, err)
		}
		lp := snapRoutePoints(leg, points[i][0], points[i][1], points[i+1][0], points[i+1][1], rg.g, false)
		start := len(out.pts)
		if i > 0 && len(lp) > 0 {
			out.pts = append(out.pts, lp[1:]...)
		} else {
			out.pts = append(out.pts, lp...)
		}
		out.legs = append(out.legs, viaLeg{
			DistanceM: leg.DistanceM,
			DurationS: float64(leg.TimeDs) / 10,
			StartIdx:  start,
			EndIdx:    len(out.pts) - 1,
			PointsN:   len(out.pts) - start,
		})
		out.res.DistanceM += leg.DistanceM
		out.res.TimeDs += leg.TimeDs
		out.res.FerryM += leg.FerryM
		out.res.Expansions += leg.Expansions
		out.res.Nodes = append(out.res.Nodes, leg.Nodes...)
		if i == 0 {
			out.res.Head = leg.Head
			out.res.TrimStart = leg.TrimStart
		}
		out.res.Tail = leg.Tail
		out.res.TrimEnd = leg.TrimEnd
		out.res.Fallback = out.res.Fallback || leg.Fallback
		out.res.Relaxed = out.res.Relaxed || leg.Relaxed
	}
	if p.pin {
		out.pts = append([][2]float64{points[0]}, out.pts...)
		out.pts = append(out.pts, points[len(points)-1])
		for i := range out.legs {
			out.legs[i].StartIdx++
			out.legs[i].EndIdx++
		}
	}
	return out, nil
}

// handleNearest snaps a coordinate to road segments without running A*. It is
// the cheap primitive a map client uses while the user is still dragging a pin.
func (s *server) handleNearest(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	lat, lon, err := parsePoint(q.Get("lat") + "," + q.Get("lon"))
	if err != nil {
		httpErr(w, 400, "lat and lon are required as decimal degrees")
		return
	}
	if !inIndonesia(lat, lon) {
		httpErr(w, 422, "coordinates outside Indonesia")
		return
	}
	k := 1
	if raw := q.Get("k"); raw != "" {
		k, err = strconv.Atoi(raw)
		if err != nil || k < 1 || k > 32 {
			httpErr(w, 400, "k must be 1..32")
			return
		}
	}
	maxM := s.maxSnapM
	if raw := q.Get("max"); raw != "" {
		v, e := strconv.ParseFloat(raw, 64)
		if e != nil || v <= 0 || v > 100_000 {
			httpErr(w, 400, "max must be a positive distance in metres")
			return
		}
		maxM = v
	}
	var best *region
	var snaps []graph.Snap
	bestD := maxM
	for _, rg := range s.regions {
		if !rg.contains(lat, lon) {
			continue
		}
		got := rg.r.SnapEdges(lat, lon, bestD, k)
		if len(got) > 0 && got[0].Dist <= bestD {
			best, snaps, bestD = rg, got, got[0].Dist
		}
	}
	if best == nil {
		httpErr(w, 422, "no road segment within max distance")
		return
	}
	type candidate struct {
		DistanceM float64 `json:"distance_m"`
		Lat       float64 `json:"lat"`
		Lon       float64 `json:"lon"`
		Node      uint32  `json:"node"`
		Edge      uint32  `json:"edge"`
		T         float64 `json:"t"`
	}
	out := make([]candidate, 0, len(snaps))
	for _, sn := range snaps {
		out = append(out, candidate{
			DistanceM: math.Round(sn.Dist),
			Lat:       round6(sn.Lat),
			Lon:       round6(sn.Lon),
			Node:      sn.Node,
			Edge:      sn.Edge,
			T:         sn.T,
		})
	}
	writeJSON(w, struct {
		Region     string      `json:"region"`
		Candidates []candidate `json:"candidates"`
	}{best.name, out})
}
