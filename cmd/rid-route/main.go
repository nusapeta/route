package main

import (
	"flag"
	"fmt"
	"strconv"
	"strings"
	"time"

	"routeid/internal/graph"
)

func pt(s string) (float64, float64) {
	p := strings.Split(s, ",")
	la, _ := strconv.ParseFloat(p[0], 64)
	lo, _ := strconv.ParseFloat(p[1], 64)
	return la, lo
}

func main() {
	graphPath := flag.String("graph", "map.graph", "graph file")
	from := flag.String("from", "", "lat,lng")
	to := flag.String("to", "", "lat,lng")
	eps := flag.Float64("eps", 1.25, "heuristic weight")
	turnPenalty := flag.Float64("turn-penalty", 0.25, "turn cost multiplier; 0 = pure edge time")
	flag.Parse()

	g, err := graph.Open(*graphPath)
	if err != nil {
		panic(err)
	}
	r := graph.NewRouter(g, 1)
	r.SetEpsilon(float32(*eps))
	r.SetTurnScale(float32(*turnPenalty))

	fla, flo := pt(*from)
	tla, tlo := pt(*to)
	t0 := time.Now()
	sa, ok1 := g.SnapEdge(fla, flo, 3000)
	sb, ok2 := g.SnapEdge(tla, tlo, 3000)
	fmt.Printf("snap: from node %d edge %d->%d t=%.3f off=%.0fm | to node %d edge %d->%d t=%.3f off=%.0fm ok=%v/%v\n",
		sa.Node, sa.From, sa.To, sa.T, sa.Dist, sb.Node, sb.From, sb.To, sb.T, sb.Dist, ok1, ok2)
	if !ok1 || !ok2 {
		return
	}
	fmt.Printf("snapped: from=%.6f,%.6f to=%.6f,%.6f\n", sa.Lat, sa.Lon, sb.Lat, sb.Lon)
	res, err := r.RouteSnap(sa, sb, true)
	if err == nil {
		fmt.Printf("trim: from=%v to=%v head=%.6f,%.6f tail=%.6f,%.6f\n",
			res.TrimStart, res.TrimEnd, res.Head[0], res.Head[1], res.Tail[0], res.Tail[1])
	}
	fmt.Printf("route: err=%v nodes=%d dist=%dm time=%.1fs expansions=%d wall=%s\n",
		err, len(res.Nodes), res.DistanceM, float64(res.TimeDs)/10, res.Expansions, time.Since(t0))
	if err == nil && len(res.Nodes) > 0 {
		a, b := g.Coords(res.Nodes[0])
		c, d := g.Coords(res.Nodes[len(res.Nodes)-1])
		fmt.Printf("path ends: %.6f,%.6f -> %.6f,%.6f\n", a, b, c, d)
		missing := 0
		for i := 0; i+1 < len(res.Nodes); i++ {
			n := res.Nodes[i]
			found := false
			for e := g.Off[n]; e < g.Off[n+1]; e++ {
				if g.To[e] == res.Nodes[i+1] {
					found = true
					break
				}
			}
			if !found {
				missing++
				if missing <= 5 {
					la, lo := g.Coords(n)
					lb, lob := g.Coords(res.Nodes[i+1])
					fmt.Printf("  GAP at %d: node %d (%.5f,%.5f) -> node %d (%.5f,%.5f)\n", i, n, la, lo, res.Nodes[i+1], lb, lob)
				}
			}
		}
		fmt.Printf("missing edges in path: %d / %d\n", missing, len(res.Nodes)-1)
	}
}
