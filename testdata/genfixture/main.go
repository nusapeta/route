// Command genfixture writes a small synthetic graph for the CI budget job.
// It has no PBF dependency, so the job can produce a deterministic fixture on
// any runner instead of shipping a multi-hundred-MB graph in the checkout.
package main

import (
	"flag"
	"math"
	"os"

	"github.com/nusapeta/route/internal/graph"
)

func main() {
	out := flag.String("out", "testdata/java-mini.graph", "output graph path")
	nodes := flag.Int("nodes", 600, "chain length")
	flag.Parse()

	const lat0, lon0 = -6.2088, 106.8456
	coords := make([][2]float64, *nodes)

	for i := range coords {
		coords[i] = [2]float64{lat0 - float64(i)*0.0004, lon0 + float64(i)*0.0004}
	}
	adj := make([][2]uint32, *nodes)
	degree := make([]uint32, *nodes)
	for i := 1; i < *nodes; i++ {
		a, b := uint32(i-1), uint32(i)
		adj[a][degree[a]] = b
		degree[a]++
		adj[b][degree[b]] = a
		degree[b]++
	}

	b := &graph.Builder{
		Lat:  make([]int32, *nodes),
		Lon:  make([]int32, *nodes),
		Off:  make([]uint32, uint32(*nodes)+1),
		MCls: make([]uint8, 0, 2*(*nodes-1)),
	}
	for i, c := range coords {
		b.Lat[i] = int32(math.Round(c[0] * graph.E7))
		b.Lon[i] = int32(math.Round(c[1] * graph.E7))
	}
	for i := range coords {
		b.Off[i] = uint32(len(b.To))
		for _, j := range adj[i][:degree[i]] {
			m := graph.Haversine(coords[i][0], coords[i][1], coords[j][0], coords[j][1])
			length := uint16(math.Max(1, math.Round(m/graph.LenUnit)))
			cls := uint8(math.Round(50 * graph.SpeedClassScale))
			b.To = append(b.To, j)
			b.Len = append(b.Len, length)
			b.Cls = append(b.Cls, cls)
			b.MCls = append(b.MCls, cls)
		}
	}
	b.Off[uint32(*nodes)] = uint32(len(b.To))
	b.BuildGrid(0.02)
	b.Meta = map[string]string{"source": "synthetic", "extractor": "genfixture"}
	if err := b.Write(*out); err != nil {
		os.Stderr.WriteString("genfixture: " + err.Error() + "\n")
		os.Exit(1)
	}
}
