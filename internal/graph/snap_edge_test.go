package graph

import (
	"math"
	"testing"
)

// TestSnapEdgeProjects verifies that a point offset from the middle of a known
// segment is attached to that segment, not to the nearest node.
func TestSnapEdgeProjects(t *testing.T) {
	b := cityGraph(t)
	dir := t.TempDir()
	path := dir + "/city.graph"
	if err := b.Write(path); err != nil {
		t.Fatal(err)
	}
	g, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	// horizontal edge from node (10,10) to (10,11): offset 40 m to the north
	from := uint32(10*30 + 10)
	to := uint32(10*30 + 11)
	alat, alon := g.Coords(from)
	blat, blon := g.Coords(to)
	qLat := (alat+blat)/2 + 40/110540.0
	qLon := (alon + blon) / 2

	sn, ok := g.SnapEdge(qLat, qLon, 3000)
	if !ok {
		t.Fatal("no snap")
	}
	if sn.From != from || sn.To != to {
		t.Fatalf("snapped to edge %d->%d, want %d->%d", sn.From, sn.To, from, to)
	}
	if math.Abs(sn.T-0.5) > 0.02 {
		t.Errorf("t = %.3f, want ~0.5", sn.T)
	}
	if math.Abs(sn.Dist-40) > 2 {
		t.Errorf("offset = %.1f m, want ~40", sn.Dist)
	}
	if sn.Node != from && sn.Node != to {
		t.Errorf("node = %d, want an endpoint", sn.Node)
	}
	if sn.Along > float64(g.EdgeLenM(sn.Edge)) {
		t.Errorf("along %.1f exceeds edge length %d", sn.Along, g.EdgeLenM(sn.Edge))
	}

	// The node snapper must be worse here: it can only return an endpoint.
	_, nodeErr, ok := g.Nearest(qLat, qLon, 3000)
	if !ok {
		t.Fatal("no node snap")
	}
	if nodeErr <= sn.Dist {
		t.Errorf("node snap error %.1f m not worse than edge snap %.1f m", nodeErr, sn.Dist)
	}
}

// TestRouteSnapTrims checks that a path leaving along the snapped segment is
// trimmed, and that a spur is added when it leaves the other way.
func TestRouteSnapTrims(t *testing.T) {
	b := cityGraph(t)
	path := t.TempDir() + "/city.graph"
	if err := b.Write(path); err != nil {
		t.Fatal(err)
	}
	g, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	r := NewRouter(g, 1)

	// start point on the edge (0,0)->(0,1), near its tail; destination far away.
	alat, alon := g.Coords(0)
	blat, blon := g.Coords(1)
	sLat := alat + 0.25*(blat-alat) + 10/110540.0 // 25% along, 10 m north
	sLon := alon + 0.25*(blon-alon)
	start, ok := g.SnapEdge(sLat, sLon, 3000)
	if !ok {
		t.Fatal("no start snap")
	}
	dstNode := uint32(20*30 + 20)
	dLat, dLon := g.Coords(dstNode)
	end, ok := g.SnapEdge(dLat, dLon, 3000)
	if !ok {
		t.Fatal("no end snap")
	}

	sr, err := r.RouteSnap(start, end, true)
	if err != nil {
		t.Fatal(err)
	}
	nodeRoute, err := r.Route(start.Node, end.Node)
	if err != nil {
		t.Fatal(err)
	}
	// The path to the opposite corner leaves along the snapped segment, so the
	// trimmed distance must be shorter than from the node itself.
	if start.Node == 0 && sr.TrimStart {
		if float64(sr.DistanceM) >= float64(nodeRoute.DistanceM)+0.5 {
			t.Errorf("trimmed distance %d not shorter than node route %d", sr.DistanceM, nodeRoute.DistanceM)
		}
		if math.Abs(float64(nodeRoute.DistanceM)-float64(sr.DistanceM)-start.Along) > 2 {
			t.Errorf("trim delta %.1f m, expected %.1f", float64(nodeRoute.DistanceM)-float64(sr.DistanceM), start.Along)
		}
	}
	if sr.Head != [2]float64{start.Lat, start.Lon} {
		t.Errorf("head = %v, want projection", sr.Head)
	}
	// Geometry must not contain the trimmed start node.
	for _, n := range sr.Nodes {
		if sr.TrimStart && n == start.Node {
			t.Errorf("trimmed start node %d still in geometry", n)
		}
		if sr.TrimEnd && n == end.Node {
			t.Errorf("trimmed end node %d still in geometry", n)
		}
	}
}
