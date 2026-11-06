// Command rid-snapcheck measures how far a query point is from the graph
// element the router attaches it to. With node snapping that error is bounded
// by half a road segment, which is what motivates edge snapping.
package main

import (
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"sort"

	"routeid/internal/graph"
)

func main() {
	path := flag.String("graph", "", "graph file")
	n := flag.Int("n", 20000, "edges to sample")
	cell := flag.Float64("cell", 0.002, "spatial hash cell for segment lookup (deg)")
	offset := flag.Float64("offset", 15, "perpendicular offset in metres used to simulate a tap off the road")
	flag.Parse()
	if *path == "" || *n <= 0 {
		fmt.Fprintln(os.Stderr, "usage: rid-snapcheck -graph data/java.graph")
		os.Exit(2)
	}

	g, err := graph.Open(*path)
	if err != nil {
		log.Fatal(err)
	}
	defer g.Close()

	// spatial hash of segments so we can answer "true nearest segment".
	type key struct{ r, c int }
	step := *cell
	buckets := make(map[key][]uint32, 1<<20)
	tail := make([]uint32, g.EdgeCount)
	for u := uint32(0); u < g.NodeCount; u++ {
		la, lo := g.Coords(u)
		for e := g.Off[u]; e < g.Off[u+1]; e++ {
			tail[e] = u
			v := g.To[e]
			lb, lob := g.Coords(v)
			r0, c0 := int(math.Floor(min(la, lb)/step)), int(math.Floor(min(lo, lob)/step))
			r1, c1 := int(math.Floor(max(la, lb)/step)), int(math.Floor(max(lo, lob)/step))
			for r := r0; r <= r1; r++ {
				for c := c0; c <= c1; c++ {
					buckets[key{r, c}] = append(buckets[key{r, c}], uint32(e))
				}
			}
		}
	}

	// project returns distance from p to segment a-b and the caller's
	// parameter t along the segment.
	project := func(plat, plon, alat, alon, blat, blon float64) (float64, float64) {
		// local equirectangular projection around a
		kx := math.Cos(alat*math.Pi/180) * 111320
		ky := 110540.0
		ax, ay := 0.0, 0.0
		bx, by := (blon-alon)*kx, (blat-alat)*ky
		px, py := (plon-alon)*kx, (plat-alat)*ky
		dx, dy := bx-ax, by-ay
		den := dx*dx + dy*dy
		t := 0.0
		if den > 0 {
			t = ((px-ax)*dx + (py-ay)*dy) / den
			if t < 0 {
				t = 0
			} else if t > 1 {
				t = 1
			}
		}
		cx, cy := ax+t*dx, ay+t*dy
		return math.Hypot(px-cx, py-cy), t
	}

	nearestEdge := func(lat, lon float64) (float64, uint32) {
		k := key{int(math.Floor(lat / step)), int(math.Floor(lon / step))}
		best, bestE := math.Inf(1), uint32(0)
		found := false
		for radius := 0; radius <= 6 && !found; radius++ {
			for r := k.r - radius; r <= k.r+radius; r++ {
				for c := k.c - radius; c <= k.c+radius; c++ {
					for _, e := range buckets[key{r, c}] {
						u, v := tail[e], g.To[e]
						la, lo := g.Coords(u)
						lb, lob := g.Coords(v)
						d, _ := project(lat, lon, la, lo, lb, lob)
						if d < best {
							best, bestE, found = d, e, true
						}
					}
				}
			}
		}
		return best, bestE
	}

	rnd := rand.New(rand.NewSource(7))
	var nodeErr, edgeErr, segLen []float64
	tried := 0
	for tried < *n {
		e := uint32(rnd.Intn(int(g.EdgeCount)))
		u, v := tail[e], g.To[e]
		la, lo := g.Coords(u)
		lb, lob := g.Coords(v)
		if la == lb && lo == lob {
			continue
		}
		tried++
		// simulate a user tapping near (not exactly on) this segment: take a
		// random position along it and push it sideways by a random offset.
		t := 0.15 + 0.7*rnd.Float64()
		alongLat := la + (lb-la)*t
		alongLon := lo + (lob-lo)*t
		segM := graph.Haversine(la, lo, lb, lob)
		nLat, nLon := 0.0, 0.0
		if segM > 0 {
			// unit normal in local metres, converted back to degrees
			kx := math.Cos(alongLat*math.Pi/180) * 111320
			dx := (lb - la) * 110540
			dy := (lob - lo) * kx
			norm := math.Hypot(dx, dy)
			off := *offset * rnd.Float64()
			nLat = -dy / norm * off / 110540
			nLon = dx / norm * off / kx
		}
		mlat, mlon := alongLat+nLat, alongLon+nLon
		segLen = append(segLen, graph.Haversine(la, lo, lb, lob))

		nu, d, ok := g.Nearest(mlat, mlon, 3000)
		if !ok {
			continue
		}
		_ = nu
		nodeErr = append(nodeErr, d)
		de, _ := nearestEdge(mlat, mlon)
		edgeErr = append(edgeErr, de)
	}

	p := func(v []float64, q float64) float64 {
		if len(v) == 0 {
			return 0
		}
		s := append([]float64(nil), v...)
		sort.Float64s(s)
		i := int(q * float64(len(s)-1))
		return s[i]
	}
	fmt.Printf("graph            %s\n", *path)
	fmt.Printf("samples          %d\n", len(nodeErr))
	fmt.Printf("segment length   p50=%.0f m p95=%.0f m p99=%.0f m max=%.0f m\n",
		p(segLen, 0.5), p(segLen, 0.95), p(segLen, 0.99), p(segLen, 1))
	fmt.Printf("node snap error  p50=%.1f m p95=%.1f m p99=%.1f m max=%.0f m\n",
		p(nodeErr, 0.5), p(nodeErr, 0.95), p(nodeErr, 0.99), p(nodeErr, 1))
	fmt.Printf("edge snap error  p50=%.1f m p95=%.1f m p99=%.1f m max=%.0f m\n",
		p(edgeErr, 0.5), p(edgeErr, 0.95), p(edgeErr, 0.99), p(edgeErr, 1))
	var over100 int
	for _, v := range nodeErr {
		if v > 100 {
			over100++
		}
	}
	fmt.Printf("node snap >100 m %.1f%%\n", float64(over100)*100/float64(len(nodeErr)))
}
