package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nusapeta/route/internal/graph"
	"github.com/nusapeta/route/internal/stats"
)

func main() {
	graphPath := flag.String("graph", "map.graph", "graph file")
	n := flag.Int("n", 200, "samples per bucket")
	eps := flag.Float64("eps", 1.5, "heuristic weight")
	turnPenalty := flag.Float64("turn-penalty", 0.25, "turn cost multiplier; 0 = pure edge time")
	seed := flag.Int64("seed", 42, "rng seed")
	csvOut := flag.String("csv", "", "write raw samples to CSV")
	pairsIn := flag.String("pairs", "", "route fixed pairs from CSV (bucket,from_lat,from_lon,to_lat,to_lon)")
	pairsOut := flag.String("emit-pairs", "", "write the sampled coordinate pairs and exit")
	noStrict := flag.Bool("no-restrictions", false, "ignore turn restrictions (baseline)")
	profile := flag.String("profile", "car", "vehicle profile: car|motor")
	cpuProf := flag.String("cpuprofile", "", "write CPU profile")
	flag.Parse()

	if *cpuProf != "" {
		f, err := os.Create(*cpuProf)
		if err != nil {
			die(err)
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			die(err)
		}
		defer func() { pprof.StopCPUProfile(); f.Close() }()
	}

	g, err := graph.Open(*graphPath)
	if err != nil {
		die(err)
	}
	defer g.Close()
	motor := *profile == "motor"
	if !motor && *profile != "car" {
		die(fmt.Errorf("unknown -profile %q (want car|motor)", *profile))
	}
	r := graph.NewRouter(g, 1)
	r.SetMotor(motor)
	if *noStrict {
		r.SetStrict(false)
	}
	r.SetEpsilon(float32(*eps))
	r.SetTurnScale(float32(*turnPenalty))

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
		runPairs(g, r, *pairsIn, *csvOut, motor)
		return
	}

	if *pairsOut != "" {
		f, err := os.Create(*pairsOut)
		if err != nil {
			die(err)
		}
		fmt.Fprintln(f, "bucket,from_lat,from_lon,to_lat,to_lon")
		for _, b := range buckets {
			for i := 0; i < *n; i++ {
				src, tLat, tLon, _, ok := samplePair(g, rng, b.lo, b.hi)
				if !ok {
					continue
				}
				lat, lon := g.Coords(src)
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
			die(err)
		}
		defer csv.Close()
		fmt.Fprintln(csv, "bucket,want_km,route_km,ms,expansions,failed")
	}

	printTableHeader()
	for _, b := range buckets {
		var ms, expands []float64
		var sumKM float64
		fails := 0
		for i := 0; i < *n; i++ {
			src, tLat, tLon, dKM, ok := samplePair(g, rng, b.lo, b.hi)
			if !ok {
				continue
			}
			dst, _, ok := g.NearestFor(tLat, tLon, 5000, *profile == "motor")
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
			printRow(b.name, 0, 0, 0, 0, 0, 0, fails)
			continue
		}
		sort.Float64s(ms)
		sort.Float64s(expands)
		printRow(b.name, len(ms), sumKM/float64(len(ms)),
			stats.Percentile(ms, 0.50), stats.Percentile(ms, 0.95), stats.Percentile(ms, 0.99),
			stats.Percentile(expands, 0.50), fails)
	}
}

func printTableHeader() {
	fmt.Printf("%-9s %5s %8s %8s %9s %9s %10s %10s\n",
		"bucket", "n", "route_km", "p50_ms", "p95_ms", "p99_ms", "p50_exp", "fail")
}

// printRow writes one bucket summary; n == 0 leaves the statistics blank.
func printRow(bucket string, n int, km, p50, p95, p99, p50exp float64, fail int) {
	if n == 0 {
		fmt.Printf("%-9s %5d %8s %8s %8s %8s %10s %10d\n", bucket, 0, "-", "-", "-", "-", "-", fail)
		return
	}
	fmt.Printf("%-9s %5d %8.1f %8.1f %9.1f %9.1f %10.0f %10d\n",
		bucket, n, km, p50, p95, p99, p50exp, fail)
}

func die(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

// samplePair draws a random source node and a target dKM away along a random
// bearing, with dKM in [lo,hi).
func samplePair(g *graph.Graph, rng *rand.Rand, lo, hi float64) (src uint32, tLat, tLon, dKM float64, ok bool) {
	src, ok = randomNode(g, rng)
	if !ok {
		return 0, 0, 0, 0, false
	}
	lat, lon := g.Coords(src)
	dKM = lo + rng.Float64()*(hi-lo)
	bearing := rng.Float64() * 2 * math.Pi
	tLat, tLon = offset(lat, lon, dKM, bearing)
	return src, tLat, tLon, dKM, true
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
func runPairs(g *graph.Graph, r *graph.Router, path, csvOut string, motor bool) {
	f, err := os.Open(path)
	if err != nil {
		die(err)
	}
	defer f.Close()
	var csv *os.File
	if csvOut != "" {
		csv, err = os.Create(csvOut)
		if err != nil {
			die(err)
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
	line := 0
	for sc.Scan() {
		line++
		if line == 1 {
			continue
		}
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		parts := strings.Split(text, ",")
		if len(parts) != 5 {
			die(fmt.Errorf("%s:%d: want 5 columns bucket,from_lat,from_lon,to_lat,to_lon, got %d", path, line, len(parts)))
		}
		bucket := strings.TrimSpace(parts[0])
		if bucket == "" {
			die(fmt.Errorf("%s:%d: empty bucket", path, line))
		}
		var coord [4]float64
		for i := range coord {
			v, err := strconv.ParseFloat(strings.TrimSpace(parts[i+1]), 64)
			if err != nil {
				die(fmt.Errorf("%s:%d: %v", path, line, err))
			}
			coord[i] = v
		}
		fla, flo, tla, tlo := coord[0], coord[1], coord[2], coord[3]
		a := byBucket[bucket]
		if a == nil {
			a = &acc{}
			byBucket[bucket] = a
			order = append(order, bucket)
		}
		src, _, ok1 := g.NearestFor(fla, flo, 5000, motor)
		dst, _, ok2 := g.NearestFor(tla, tlo, 5000, motor)
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
	die(sc.Err())
	printTableHeader()
	for _, b := range order {
		a := byBucket[b]
		if len(a.ms) == 0 {
			continue
		}
		sort.Float64s(a.ms)
		sort.Float64s(a.exp)
		printRow(b, a.n, a.km/float64(a.n),
			stats.Percentile(a.ms, 0.50), stats.Percentile(a.ms, 0.95), stats.Percentile(a.ms, 0.99),
			stats.Percentile(a.exp, 0.50), a.fail)
	}
}
