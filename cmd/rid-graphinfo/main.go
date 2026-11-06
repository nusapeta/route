// Command rid-graphinfo prints graph header/grid details and can probe snapping.
//
//	rid-graphinfo -graph data/java.graph
//	rid-graphinfo -graph data/sumatra.graph -at -3.5952,98.6722
package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"routeid/internal/graph"
)

// runScan prints a coarse map of snap coverage: '.' has road within 1 km,
// '+' within 5 km, '#' nothing. Useful to spot holes in a region build.
func runScan(g *graph.Graph, spec string) {
	f := strings.Split(spec, ",")
	if len(f) < 4 {
		fmt.Fprintln(os.Stderr, "-scan needs minLat,minLon,maxLat,maxLon[,step]")
		os.Exit(2)
	}
	v := make([]float64, len(f))
	for i, s := range f {
		x, err := strconv.ParseFloat(s, 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "-scan: %v\n", err)
			os.Exit(2)
		}
		v[i] = x
	}
	step := 0.25
	if len(v) > 4 && v[4] > 0 {
		step = v[4]
	}
	minLat, minLon, maxLat, maxLon := v[0], v[1], v[2], v[3]
	fmt.Printf("scan %.3f,%.3f .. %.3f,%.3f step %.3f  ('.'<=1km '+'<=5km '#'=hole)\n",
		minLat, minLon, maxLat, maxLon, step)
	gaps, total := 0, 0
	for lat := maxLat; lat >= minLat-1e-9; lat -= step {
		fmt.Printf("%8.3f ", lat)
		for lon := minLon; lon <= maxLon+1e-9; lon += step {
			total++
			_, d, ok := g.Nearest(lat, lon, 5000)
			switch {
			case ok && d <= 1000:
				fmt.Print(".")
			case ok:
				fmt.Print("+")
			default:
				fmt.Print("#")
				gaps++
			}
		}
		fmt.Println()
	}
	fmt.Printf("sel tanpa jalan <=5km: %d/%d (%.1f%%)\n", gaps, total, 100*float64(gaps)/float64(total))
}

func main() {
	path := flag.String("graph", "", "graph file")
	at := flag.String("at", "", "probe lat,lng")
	scan := flag.String("scan", "", "scan area: minLat,minLon,maxLat,maxLon[,step]  (default step 0.25)")
	flag.Parse()
	if *path == "" {
		fmt.Fprintln(os.Stderr, "-graph required")
		os.Exit(2)
	}
	g, err := graph.Open(*path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("file       : %s\n", *path)
	fmt.Printf("nodes      : %d\n", g.NodeCount)
	fmt.Printf("edges      : %d\n", g.EdgeCount)
	fmt.Printf("coords     : %d (interior shape points)\n", g.CoordCount)
	minLat, minLon, maxLat, maxLon := g.Bounds()
	fmt.Printf("bbox       : %.6f,%.6f .. %.6f,%.6f\n", minLat, minLon, maxLat, maxLon)
	fmt.Printf("grid       : %dx%d cells, step %.4f, origin %.4f,%.4f, entries %d\n",
		g.GridNLat, g.GridNLon, g.GridStep, g.GridLat0, g.GridLon0, g.GridCount)
	if *scan != "" {
		runScan(g, *scan)
		return
	}
	if *at == "" {
		return
	}
	parts := strings.Split(*at, ",")
	if len(parts) != 2 {
		fmt.Fprintln(os.Stderr, "-at must be lat,lng")
		os.Exit(2)
	}
	lat, _ := strconv.ParseFloat(parts[0], 64)
	lon, _ := strconv.ParseFloat(parts[1], 64)
	for _, lim := range []float64{50, 500, 3000, 20000, 200000} {
		id, d, ok := g.Nearest(lat, lon, lim)
		if !ok {
			fmt.Printf("snap <=%7.0fm: none\n", lim)
			continue
		}
		la, lo := g.Coords(id)
		fmt.Printf("snap <=%7.0fm: node %d at %.6f,%.6f (%0.1fm)\n", lim, id, la, lo, d)
	}
}
