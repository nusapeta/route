package main

import (
	"context"
	"fmt"

	"github.com/nusapeta/route/internal/graph"
)

// routeLeg is one piece of a stitched cross-region route. Start/End are
// inclusive indexes into the pre-simplify geometry; the JSON writer projects
// them after Douglas-Peucker so a client can draw each leg separately.
type routeLeg struct {
	mode       string
	region     string
	name       string
	distanceM  uint32
	durationDs uint32
	from, to   [2]float64
	start, end int
	timeSource string
}

// stitchedRoute is a cross-region answer. It embeds SnapRoute so the handler's
// metric and error paths stay shared with single-region routing, but geometry
// comes from two graphs and therefore cannot be a node sequence in one of them.
type stitchedRoute struct {
	graph.SnapRoute
	pts             [][2]float64
	legs            []routeLeg
	ferrySpans      [][2]int
	region          string
	ferryTimeSource string
}

// routeStitched routes start -> port A and port B -> destination in their own
// graphs, then inserts the static crossing. Port snapping deliberately skips
// ferry edges: those belong to the crossing table, and routing a land leg onto
// the water edge would count the same metres twice.
func (s *server) routeStitched(ctx context.Context, rgF, rgT *region, snapF, snapT graph.Snap, c ferryConnector, portF, portT ferryPort, p routeParams) (*stitchedRoute, error) {
	snapPortF, ok := s.snapPort(rgF, portF)
	if !ok {
		return nil, fmt.Errorf("%w: %s port %s has no road", graph.ErrNoRoute, c.name, portF.name)
	}
	snapPortT, ok := s.snapPort(rgT, portT)
	if !ok {
		return nil, fmt.Errorf("%w: %s port %s has no road", graph.ErrNoRoute, c.name, portT.name)
	}

	legA, err := rgF.r.RouteSnapCtx(ctx, snapF, snapPortF, p.restrictions)
	if err != nil {
		return nil, fmt.Errorf("%s to %s: %w", rgF.name, portF.name, err)
	}
	legB, err := rgT.r.RouteSnapCtx(ctx, snapPortT, snapT, p.restrictions)
	if err != nil {
		return nil, fmt.Errorf("%s to %s: %w", portT.name, rgT.name, err)
	}

	pts := make([][2]float64, 0, len(legA.Nodes)+len(legB.Nodes)+8)
	if p.pin {
		pts = append(pts, [2]float64{p.fLat, p.fLon})
	}
	legAStart := len(pts)
	pts = appendSnapGeometry(pts, rgF, legA)
	legAEnd := len(pts) - 1

	ferryStart := legAEnd
	ferryEnd := ferryStart
	if !samePoint(legA.Tail, legB.Head) {
		pts = append(pts, legB.Head)
		ferryEnd = len(pts) - 1
	}
	legBStart := ferryEnd
	for _, n := range legB.Nodes {
		lat, lon := rgT.g.Coords(n)
		pts = append(pts, [2]float64{lat, lon})
	}
	pts = append(pts, legB.Tail)
	legBEnd := len(pts) - 1
	if p.pin {
		pts = append(pts, [2]float64{p.tLat, p.tLon})
	}

	legs := []routeLeg{
		{mode: "road", region: rgF.name, distanceM: legA.DistanceM, durationDs: legA.TimeDs,
			from: legA.Head, to: legA.Tail, start: legAStart, end: legAEnd},
		{mode: "ferry", name: c.name, distanceM: c.distanceM, durationDs: c.durationDs,
			timeSource: ferryTimeSource(c), from: legA.Tail, to: legB.Head, start: ferryStart, end: ferryEnd},
		{mode: "road", region: rgT.name, distanceM: legB.DistanceM, durationDs: legB.TimeDs,
			from: legB.Head, to: legB.Tail, start: legBStart, end: legBEnd},
	}

	var spans [][2]int
	// Ferry spans inside a leg index that leg's Nodes, whose first entry sits
	// one point after the leg Head in the combined geometry.
	for _, sp := range legA.FerrySpans {
		spans = appendSpan(spans, [2]int{legAStart + 1 + int(sp[0]), legAStart + 1 + int(sp[1])})
	}
	if ferryEnd > ferryStart {
		spans = appendSpan(spans, [2]int{ferryStart, ferryEnd})
	}
	for _, sp := range legB.FerrySpans {
		spans = appendSpan(spans, [2]int{legBStart + 1 + int(sp[0]), legBStart + 1 + int(sp[1])})
	}

	eps, turn := legA.Epsilon, legA.TurnScale
	if legB.Relaxed && (!legA.Relaxed || legB.Epsilon > legA.Epsilon) {
		eps, turn = legB.Epsilon, legB.TurnScale
	}
	res := graph.SnapRoute{
		Route: graph.Route{
			DistanceM:  legA.DistanceM + c.distanceM + legB.DistanceM,
			TimeDs:     legA.TimeDs + c.durationDs + legB.TimeDs,
			FerryM:     legA.FerryM + c.distanceM + legB.FerryM,
			Expansions: legA.Expansions + legB.Expansions,
			Fallback:   legA.Fallback || legB.Fallback,
			Relaxed:    legA.Relaxed || legB.Relaxed,
			Epsilon:    eps,
			TurnScale:  turn,
		},
		Head:      legA.Head,
		Tail:      legB.Tail,
		TrimStart: legA.TrimStart,
		TrimEnd:   legB.TrimEnd,
	}
	return &stitchedRoute{
		SnapRoute:       res,
		pts:             pts,
		legs:            legs,
		ferrySpans:      spans,
		ferryTimeSource: ferryTimeSource(c),
		region:          rgF.name + "+" + rgT.name,
	}, nil
}

func (s *server) snapPort(rg *region, p ferryPort) (graph.Snap, bool) {
	cands := rg.r.SnapEdges(p.lat, p.lon, s.maxSnapM, 8)
	for _, sn := range cands {
		if !rg.g.IsFerry(sn.Edge) {
			return sn, true
		}
	}
	return graph.Snap{}, false
}

func appendSnapGeometry(pts [][2]float64, rg *region, res graph.SnapRoute) [][2]float64 {
	pts = append(pts, res.Head)
	for _, n := range res.Nodes {
		lat, lon := rg.g.Coords(n)
		pts = append(pts, [2]float64{lat, lon})
	}
	return append(pts, res.Tail)
}

func samePoint(a, b [2]float64) bool {
	return a[0] == b[0] && a[1] == b[1]
}

func appendSpan(spans [][2]int, sp [2]int) [][2]int {
	if sp[0] > sp[1] {
		return spans
	}
	if len(spans) > 0 && sp[0] <= spans[len(spans)-1][1]+1 {
		if sp[1] > spans[len(spans)-1][1] {
			spans[len(spans)-1][1] = sp[1]
		}
		return spans
	}
	return append(spans, sp)
}

// legJSON renders one leg. Point ranges are omitted when simplification kept
// no sample in the leg; the metrics still describe it.
func (l routeLeg) legJSON(keep []int) map[string]any {
	out := map[string]any{
		"mode":       l.mode,
		"distance_m": l.distanceM,
		"duration_s": float64(l.durationDs) / 10,
		"from":       []float64{round6(l.from[0]), round6(l.from[1])},
		"to":         []float64{round6(l.to[0]), round6(l.to[1])},
	}
	if l.mode == "ferry" && l.timeSource != "" {
		out["ferry_time_source"] = l.timeSource
	}
	if l.region != "" {
		out["region"] = l.region
	}
	if l.name != "" {
		out["name"] = l.name
	}
	a, b, ok := l.start, l.end, l.start <= l.end
	if ok && keep != nil {
		a, b, ok = projectSpan(keep, a, b)
	}
	if ok {
		out["point_start"] = a
		out["point_end"] = b
	}
	return out
}

// ferrySpanJSON projects the stitched crossing onto the final geometry.
func ferrySpansJSON(spans [][2]int, keep []int) [][2]int {
	out := make([][2]int, 0, len(spans))
	for _, sp := range spans {
		a, b := sp[0], sp[1]
		var ok bool
		if keep != nil {
			a, b, ok = projectSpan(keep, a, b)
		} else {
			ok = a <= b
		}
		if ok {
			out = append(out, [2]int{a, b})
		}
	}
	return out
}
