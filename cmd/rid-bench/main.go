package main

import (
	"bufio"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"time"

	"routeid/internal/graph"
)

type sample struct {
	wantKM float64
	distKM float64
	ms     float64
	expand int
	failed bool
}

func main() {
	graphPath := flag.String("graph", "map.graph", "graph file")
	n := flag.Int("n", 200, "samples per bucket")
	eps := flag.Float64("eps", 1.5, "heuristic weight")
	seed := flag.Int64("seed", 42, "rng seed")
	csvOut := flag.String("csv", "", "write raw samples to CSV")
	pairsIn := flag.String("pairs", "", "route fixed pairs from CSV (from_lat,from_lon,to_lat,to_lon)")
	pairsOut := flag.String("emit-pairs", "", "write the sampled coordinate pairs and exit")
	noStrict := flag.Bool("no-restrictions", false, "ignore turn restrictions (baseline)")
	cpuProf := flag.String("cpuprofile", "", "write CPU profile")
	flag.Parse()

	if *cpuProf != "" {
		f, err := os.Create(*cpuProf)
		if err != nil {
			panic(err)
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			panic(err)
		}
		defer func() { pprof.StopCPUProfile(); f.Close() }()
	}

	g, err := graph.Open(*graphPath)
	if err != nil {
		panic(err)
	}
	defer g.Close()
	r := graph.NewRouter(g, 1)
	if *noStrict {
		r.SetStrict(false)
	}
	r.SetEpsilon(float32(*eps))

	buckets := []struct {
		name   string
		lo, hi float64
	}{
		{"0-5km", 0.5, 5},
		{"5-15km", 5, 15},
		{"15-30km", 15, 30},
		{"30-50km", 30, 50},
		{"50-80km", 50, 80},
	}
	rng := rand.New(rand.NewSource(*seed))

	if *pairsIn != "" {
		runPairs(g, r, *pairsIn, *csvOut)
		return
	}

	if *pairsOut != "" {
		f, err := os.Create(*pairsOut)
		if err != nil {
			panic(err)
		}
		fmt.Fprintln(f, "bucket,from_lat,from_lon,to_lat,to_lon")
		for _, b := range buckets {
			for i := 0; i < *n; i++ {
				src, ok := randomNode(g, rng)
				if !ok {
					continue
				}
				lat, lon := g.Coords(src)
				dKM := b.lo + rng.Float64()*(b.hi-b.lo)
				bearing := rng.Float64() * 2 * math.Pi
				tLat, tLon := offset(lat, lon, dKM, bearing)
				fmt.Fprintf(f, "%s,%.6f,%.6f,%.6f,%.6f\n", b.name, lat, lon, tLat, tLon)
			}
		}
		f.Close()
		fmt.Println("pairs written to", *pairsOut)
		return
	}

	var csv *os.File
	if *csvOut != "" {
		csv, err = os.Create(*csvOut)
		if err != nil {
			panic(err)
		}
		defer csv.Close()
		fmt.Fprintln(csv, "bucket,want_km,route_km,ms,expansions,failed")
	}

	fmt.Printf("%-9s %5s %8s %8s %9s %9s %10s %10s\n",
		"bucket", "n", "route_km", "p50_ms", "p95_ms", "p99_ms", "mean_exp", "fail")
	for _, b := range buckets {
		var ms, expands []float64
		var sumKM float64
		fails := 0
		for i := 0; i < *n; i++ {
			src, ok := randomNode(g, rng)
			if !ok {
				continue
			}
			lat, lon := g.Coords(src)
			dKM := b.lo + rng.Float64()*(b.hi-b.lo)
			bearing := rng.Float64() * 2 * math.Pi
			tLat, tLon := offset(lat, lon, dKM, bearing)
			dst, _, ok := g.Nearest(tLat, tLon, 5000)
			if !ok {
				fails++
				continue
			}
			start := time.Now()
			res, err := r.Route(src, dst)
			el := float64(time.Since(start).Microseconds()) / 1000
			if err != nil {
				fails++
				if csv != nil {
					fmt.Fprintf(csv, "%s,%.2f,,%.3f,0,1\n", b.name, dKM, el)
				}
				continue
			}
			ms = append(ms, el)
			expands = append(expands, float64(res.Expansions))
			sumKM += float64(res.DistanceM) / 1000
			if csv != nil {
				fmt.Fprintf(csv, "%s,%.2f,%.2f,%.3f,%d,0\n", b.name, dKM, float64(res.DistanceM)/1000, el, res.Expansions)
			}
		}
		if len(ms) == 0 {
			fmt.Printf("%-9s %5d %8s %8s %8s %8s %10s %10d\n", b.name, 0, "-", "-", "-", "-", "-", fails)
			continue
		}
		sort.Float64s(ms)
		sort.Float64s(expands)
		fmt.Printf("%-9s %5d %8.1f %8.1f %9.1f %9.1f %10.0f %10d\n",
			b.name, len(ms), sumKM/float64(len(ms)),
			pct(ms, 0.50), pct(ms, 0.95), pct(ms, 0.99),
			expands[len(expands)/2], fails)
	}
}

func pct(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(math.Round(p * float64(len(sorted)-1)))
	return sorted[i]
}

func randomNode(g *graph.Graph, rng *rand.Rand) (uint32, bool) {
	for i := 0; i < 100; i++ {
		n := uint32(rng.Intn(int(g.NodeCount)))
		if g.Off[n+1] > g.Off[n] {
			return n, true
		}
	}
	return 0, false
}

// offset moves a coordinate by dKM along a bearing (flat-earth, fine at <100km).
func offset(lat, lon, dKM, bearing float64) (float64, float64) {
	dLat := dKM / 111.32 * math.Cos(bearing)
	dLon := dKM / (111.32 * math.Cos(lat*math.Pi/180)) * math.Sin(bearing)
	return lat + dLat, lon + dLon
}

// runPairs routes a fixed pair list so that two graph layouts can be compared
// on an identical workload.
func runPairs(g *graph.Graph, r *graph.Router, path, csvOut string) {
	f, err := os.Open(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	var csv *os.File
	if csvOut != "" {
		csv, err = os.Create(csvOut)
		if err != nil {
			panic(err)
		}
		defer csv.Close()
		fmt.Fprintln(csv, "bucket,route_km,ms,expansions,failed,from_lat,from_lon,to_lat,to_lon,time_ds")
	}
	type acc struct {
		ms, exp []float64
		km      float64
		n, fail int
	}
	byBucket := map[string]*acc{}
	var order []string
	sc := bufio.NewScanner(f)
	sc.Scan() // header
	for sc.Scan() {
		parts := strings.Split(strings.TrimSpace(sc.Text()), ",")
		if len(parts) < 5 {
			continue
		}
		bucket := parts[0]
		fla, _ := strconv.ParseFloat(parts[1], 64)
		flo, _ := strconv.ParseFloat(parts[2], 64)
		tla, _ := strconv.ParseFloat(parts[3], 64)
		tlo, _ := strconv.ParseFloat(parts[4], 64)
		a := byBucket[bucket]
		if a == nil {
			a = &acc{}
			byBucket[bucket] = a
			order = append(order, bucket)
		}
		src, _, ok1 := g.Nearest(fla, flo, 5000)
		dst, _, ok2 := g.Nearest(tla, tlo, 5000)
		if !ok1 || !ok2 {
			a.fail++
			continue
		}
		start := time.Now()
		res, err := r.Route(src, dst)
		el := float64(time.Since(start).Microseconds()) / 1000
		if err != nil {
			a.fail++
			if csv != nil {
				fmt.Fprintf(csv, "%s,,%.3f,0,1,%.6f,%.6f,%.6f,%.6f,0\n", bucket, el, fla, flo, tla, tlo)
			}
			continue
		}
		a.ms = append(a.ms, el)
		a.exp = append(a.exp, float64(res.Expansions))
		a.km += float64(res.DistanceM) / 1000
		a.n++
		if csv != nil {
			fmt.Fprintf(csv, "%s,%.2f,%.3f,%d,0,%.6f,%.6f,%.6f,%.6f,%d\n", bucket,
				float64(res.DistanceM)/1000, el, res.Expansions, fla, flo, tla, tlo, res.TimeDs)
		}
	}
	fmt.Printf("%-9s %5s %8s %8s %9s %9s %10s %10s\n", "bucket", "n", "route_km", "p50_ms", "p95_ms", "p99_ms", "mean_exp", "fail")
	for _, b := range order {
		a := byBucket[b]
		if len(a.ms) == 0 {
			continue
		}
		sort.Float64s(a.ms)
		sort.Float64s(a.exp)
		fmt.Printf("%-9s %5d %8.1f %8.1f %9.1f %9.1f %10.0f %10d\n",
			b, a.n, a.km/float64(a.n), pct(a.ms, 0.5), pct(a.ms, 0.95), pct(a.ms, 0.99),
			a.exp[len(a.exp)/2], a.fail)
	}
}
