// Command nusa-snapcheck measures how far a query point is from the graph
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

	"github.com/nusapeta/route/internal/graph"
	"github.com/nusapeta/route/internal/stats"
)

func main() {
	path := flag.String("graph", "", "graph file")
	n := flag.Int("n", 20000, "edges to sample")
	cell := flag.Float64("cell", 0.002, "spatial hash cell for segment lookup (deg)")
	offset := flag.Float64("offset", 15, "perpendicular offset in metres used to simulate a tap off the road")
	bbox := flag.String("bbox", "", "optional minLat,minLon,maxLat,maxLon: restrict sampling (keeps memory sane on island graphs)")
	flag.Parse()
	if *path == "" || *n <= 0 {
		fmt.Fprintln(os.Stderr, "usage: nusa-snapcheck -graph data/java.graph")
		os.Exit(2)
	}

	g, err := graph.Open(*path)
	if err != nil {
		log.Fatal(err)
	}
	defer g.Close()

	// optional bbox filter: on an island graph the bucket map for every edge does
	// not fit comfortably in RAM, so sample one city at a time.
	var hasBox bool
	var bMinLat, bMinLon, bMaxLat, bMaxLon float64
	if *bbox != "" {
		var f [4]float64
		if _, err := fmt.Sscanf(*bbox, "%g,%g,%g,%g", &f[0], &f[1], &f[2], &f[3]); err != nil {
			fmt.Fprintf(os.Stderr, "-bbox must be minLat,minLon,maxLat,maxLon: %v\n", err)
			os.Exit(2)
		}
		bMinLat, bMinLon, bMaxLat, bMaxLon = f[0], f[1], f[2], f[3]
		hasBox = true
	}
	inBox := func(lat, lon float64) bool {
		if !hasBox {
			return true
		}
		return lat >= bMinLat && lat <= bMaxLat && lon >= bMinLon && lon <= bMaxLon
	}
	var sample []uint32

	// spatial hash of segments so we can answer "true nearest segment".
	type key struct{ r, c int }
	step := *cell
	buckets := make(map[key][]uint32, 1<<20)
	tail := make([]uint32, g.EdgeCount)
	for u := uint32(0); u < g.NodeCount; u++ {
		la, lo := g.Coords(u)
		if !inBox(la, lo) {
			continue
		}
		for e := g.Off[u]; e < g.Off[u+1]; e++ {
			tail[e] = u
			sample = append(sample, uint32(e))
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

	if len(sample) == 0 {
		fmt.Fprintln(os.Stderr, "no edges in the requested bbox")
		os.Exit(1)
	}
	fmt.Printf("edges in scope   %d\n", len(sample))

	rnd := rand.New(rand.NewSource(7))
	var nodeErr, edgeErr, segLen, implErr []float64
	tried, snapFailed := 0, 0
	for tried < *n {
		e := sample[rnd.Intn(len(sample))]
		u, v := tail[e], g.To[e]
		la, lo := g.Coords(u)
		lb, lob := g.Coords(v)
		if la == lb && lo == lob {
			continue
		}
		tried++
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

		_, nodeD, ok := g.Nearest(mlat, mlon, 3000)
		if !ok {
			snapFailed++
			continue
		}
		sn, ok := g.SnapEdge(mlat, mlon, 3000)
		if !ok {
			snapFailed++
			continue
		}
		edgeD, _ := nearestEdge(mlat, mlon)
		segLen = append(segLen, segM)
		nodeErr = append(nodeErr, nodeD)
		edgeErr = append(edgeErr, edgeD)
		implErr = append(implErr, sn.Dist)
	}

	fmt.Printf("graph            %s\n", *path)
	fmt.Printf("samples          %d\n", len(nodeErr))
	if tried > 0 {
		fmt.Printf("snap misses      %d (%.2f%% of %d attempts)\n",
			snapFailed, 100*float64(snapFailed)/float64(tried), tried)
	}
	if len(nodeErr) == 0 {
		return
	}

	segSorted := sortedCopy(segLen)
	nodeSorted := sortedCopy(nodeErr)
	edgeSorted := sortedCopy(edgeErr)
	implSorted := sortedCopy(implErr)
	fmt.Printf("segment length   p50=%.0f m p95=%.0f m p99=%.0f m max=%.0f m\n",
		stats.Percentile(segSorted, 0.5), stats.Percentile(segSorted, 0.95),
		stats.Percentile(segSorted, 0.99), stats.Percentile(segSorted, 1))
	fmt.Printf("node snap error  p50=%.1f m p95=%.1f m p99=%.1f m max=%.0f m\n",
		stats.Percentile(nodeSorted, 0.5), stats.Percentile(nodeSorted, 0.95),
		stats.Percentile(nodeSorted, 0.99), stats.Percentile(nodeSorted, 1))
	fmt.Printf("segment oracle   p50=%.1f m p95=%.1f m p99=%.1f m max=%.0f m  (ground truth)\n",
		stats.Percentile(edgeSorted, 0.5), stats.Percentile(edgeSorted, 0.95),
		stats.Percentile(edgeSorted, 0.99), stats.Percentile(edgeSorted, 1))
	fmt.Printf("router SnapEdge  p50=%.1f m p95=%.1f m p99=%.1f m max=%.0f m\n",
		stats.Percentile(implSorted, 0.5), stats.Percentile(implSorted, 0.95),
		stats.Percentile(implSorted, 0.99), stats.Percentile(implSorted, 1))
	var worse, sum float64
	for i := range implErr {
		d := implErr[i] - edgeErr[i]
		if d > 1 {
			worse++
		}
		sum += d
	}
	fmt.Printf("vs oracle        mean %+.2f m, worse than 1 m on %.2f%% of samples\n",
		sum/float64(len(implErr)), 100*worse/float64(len(implErr)))
	var over100 int
	for _, v := range nodeErr {
		if v > 100 {
			over100++
		}
	}
	fmt.Printf("node snap >100 m %.1f%%\n", float64(over100)*100/float64(len(nodeErr)))

	// Node snapping error is bounded by half a segment, so the gain only shows up
	// where segments are long. Report both snappers per segment-length bucket.
	type bucket struct {
		label    string
		min, max float64
		node     []float64
		imp      []float64
	}
	lenBuckets := []*bucket{
		{"seg <= 20 m", 0, 20, nil, nil},
		{"seg 20-50 m", 20, 50, nil, nil},
		{"seg 50-150 m", 50, 150, nil, nil},
		{"seg > 150 m", 150, math.Inf(1), nil, nil},
	}
	for i, L := range segLen {
		for _, b := range lenBuckets {
			if L > b.min && L <= b.max {
				b.node = append(b.node, nodeErr[i])
				b.imp = append(b.imp, implErr[i])
				break
			}
		}
	}
	fmt.Printf("\nbucket            n     node p50  node p95   edge p50  edge p95\n")
	for _, b := range lenBuckets {
		if len(b.node) == 0 {
			continue
		}
		bNode, bImp := sortedCopy(b.node), sortedCopy(b.imp)
		fmt.Printf("%-15s %5d  %8.1f  %8.1f   %8.1f  %8.1f\n", b.label, len(b.node),
			stats.Percentile(bNode, 0.5), stats.Percentile(bNode, 0.95),
			stats.Percentile(bImp, 0.5), stats.Percentile(bImp, 0.95))
	}
}

func sortedCopy(v []float64) []float64 {
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return s
}
