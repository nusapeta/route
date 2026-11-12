package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"routeid/internal/graph"
)

func usage() {
	fmt.Fprintln(os.Stderr, "usage: rid-route -graph map.graph -from lat,lng -to lat,lng")
}

func pt(name, s string) (float64, float64) {
	parts := strings.Split(s, ",")
	if len(parts) != 2 {
		fmt.Fprintf(os.Stderr, "-%s must be lat,lng: %q\n", name, s)
		usage()
		os.Exit(2)
	}
	la, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "-%s: %v\n", name, err)
		usage()
		os.Exit(2)
	}
	lo, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "-%s: %v\n", name, err)
		usage()
		os.Exit(2)
	}
	return la, lo
}

func main() {
	graphPath := flag.String("graph", "map.graph", "graph file")
	from := flag.String("from", "", "lat,lng")
	to := flag.String("to", "", "lat,lng")
	eps := flag.Float64("eps", 1.25, "heuristic weight")
	turnPenalty := flag.Float64("turn-penalty", 0.25, "turn cost multiplier; 0 = pure edge time")
	profile := flag.String("profile", "car", "vehicle profile: car|motor")
	strict := flag.Bool("strict", true, "honour turn restrictions")
	flag.Parse()
	if *from == "" || *to == "" {
		usage()
		os.Exit(2)
	}
	fla, flo := pt("from", *from)
	tla, tlo := pt("to", *to)

	g, err := graph.Open(*graphPath)
	if err != nil {
		log.Fatal(err)
	}
	motor := *profile == "motor"
	if !motor && *profile != "car" {
		log.Fatalf("unknown -profile %q (want car|motor)", *profile)
	}
	r := graph.NewRouter(g, 1)
	r.SetMotor(motor)
	r.SetEpsilon(float32(*eps))
	r.SetTurnScale(float32(*turnPenalty))

	t0 := time.Now()
	sa, ok1 := r.SnapEdge(fla, flo, 3000)
	sb, ok2 := r.SnapEdge(tla, tlo, 3000)
	fmt.Printf("snap: from node %d edge %d->%d t=%.3f off=%.0fm | to node %d edge %d->%d t=%.3f off=%.0fm ok=%v/%v\n",
		sa.Node, sa.From, sa.To, sa.T, sa.Dist, sb.Node, sb.From, sb.To, sb.T, sb.Dist, ok1, ok2)
	if !ok1 || !ok2 {
		return
	}
	fmt.Printf("snapped: from=%.6f,%.6f to=%.6f,%.6f\n", sa.Lat, sa.Lon, sb.Lat, sb.Lon)
	res, err := r.RouteSnap(sa, sb, *strict)
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
