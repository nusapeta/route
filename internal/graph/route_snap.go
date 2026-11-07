package graph

import (
	"context"
	"math"
)

// SnapRoute is a route between two snapped points. Head and Tail are the
// projected coordinates (lat, lon); Nodes holds the node path with the snapped
// endpoints dropped when the path leaves along the snapped segment (trimmed),
// so geometry is Head + Nodes + Tail.
type SnapRoute struct {
	Route
	Head, Tail [2]float64
	TrimStart  bool // the path traverses the snapped segment, so no spur
	TrimEnd    bool
}

// otherEndpoint returns the far end of the segment a was snapped to.
func otherEndpoint(s Snap) uint32 {
	if s.Node == s.To {
		return s.From
	}
	return s.To
}

// alongTime converts Along (metres from Node to the projection) into
// deciseconds at the segment's own speed.
func (g *Graph) alongTime(s Snap) float64 {
	l := float64(g.EdgeLenM(s.Edge))
	if l == 0 {
		return 0
	}
	return s.Along * float64(g.EdgeTimeDs(s.Edge)) / l
}

// RouteSnap routes between two snapped points. When the returned path leaves
// the start node along the snapped segment (or arrives at the end node along it),
// the covered part is trimmed from the distance and the node is dropped from the
// geometry; otherwise the offset is added as a spur. This keeps the reported
// distance consistent with the geometry without mutating the graph.
func (r *Router) RouteSnap(a, b Snap, strict bool) (SnapRoute, error) {
	return r.RouteSnapCtx(context.Background(), a, b, strict)
}

// RouteSnapCtx is RouteSnap with cancellation; a canceled search releases its
// workspace immediately instead of running to the expansion cap.
func (r *Router) RouteSnapCtx(ctx context.Context, a, b Snap, strict bool) (SnapRoute, error) {
	res, err := r.RouteStrictCtx(ctx, a.Node, b.Node, strict)
	if err != nil {
		return SnapRoute{}, err
	}
	g := r.g
	out := SnapRoute{
		Route: res,
		Head:  [2]float64{a.Lat, a.Lon},
		Tail:  [2]float64{b.Lat, b.Lon},
	}
	dist := float64(res.DistanceM)
	tds := float64(res.TimeDs)
	nodes := res.Nodes

	// start: does the path leave along the snapped segment?
	if len(nodes) >= 2 && nodes[1] == otherEndpoint(a) {
		dist -= a.Along
		tds -= g.alongTime(a)
		out.TrimStart = true
		nodes = nodes[1:]
	} else {
		dist += a.Along
		tds += g.alongTime(a)
	}

	// end: does the path arrive along the snapped segment?
	if len(nodes) >= 2 && nodes[len(nodes)-2] == otherEndpoint(b) {
		dist -= b.Along
		tds -= g.alongTime(b)
		out.TrimEnd = true
		nodes = nodes[:len(nodes)-1]
	} else {
		dist += b.Along
		tds += g.alongTime(b)
	}

	// both endpoints on the same segment: the node path is degenerate, so take
	// the distance along the segment when possible.
	if len(nodes) <= 1 && a.Edge == b.Edge {
		dist = math.Abs(a.T-b.T) * float64(g.EdgeLenM(a.Edge))
		tds = math.Abs(a.T-b.T) * float64(g.EdgeTimeDs(a.Edge))
		nodes = nil
	}
	if dist < 0 {
		dist = 0
	}
	if tds < 0 {
		tds = 0
	}
	// ferry metres follow the same trim/spur logic as distance
	ferry := float64(res.FerryM)
	if g.IsFerry(a.Edge) {
		if out.TrimStart {
			ferry -= a.Along
		} else {
			ferry += a.Along
		}
	}
	if g.IsFerry(b.Edge) {
		if out.TrimEnd {
			ferry -= b.Along
		} else {
			ferry += b.Along
		}
	}
	if len(nodes) <= 1 && a.Edge == b.Edge {
		ferry = 0
		if g.IsFerry(a.Edge) {
			ferry = math.Abs(a.T-b.T) * float64(g.EdgeLenM(a.Edge))
		}
	}
	if ferry < 0 {
		ferry = 0
	}
	out.Nodes = nodes
	out.DistanceM = uint32(math.Round(dist))
	out.TimeDs = uint32(math.Round(tds))
	out.FerryM = uint32(math.Round(ferry))
	return out, nil
}
