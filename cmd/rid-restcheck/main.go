// Command rid-restcheck measures whether the router honours OSM turn
// restrictions.
//
// For every banned transition (in, out) it routes from the tail of `in` to the
// head of `out` twice: once with restrictions enforced and once with them
// ignored. A result is a violation when the returned path still contains the
// forbidden transition. The loose run tells us how often the shortest path
// actually wanted to make that turn, i.e. how many of the cases are
// non-vacuous evidence.
package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"time"

	"routeid/internal/graph"
)

func main() {
	path := flag.String("graph", "", "graph file")
	sample := flag.Int("sample", 0, "max restrictions to test (0 = all)")
	eps := flag.Float64("eps", 0, "heuristic weight (0 = graph default)")
	seed := flag.Int64("seed", 1, "sampling seed")
	jsonOut := flag.String("json", "", "write results as JSON to this path")
	flag.Parse()
	if *path == "" {
		fmt.Fprintln(os.Stderr, "usage: rid-restcheck -graph data/java.graph")
		os.Exit(2)
	}

	g, err := graph.Open(*path)
	if err != nil {
		log.Fatal(err)
	}
	defer g.Close()

	tail := make([]uint32, g.EdgeCount)
	for u := uint32(0); u < g.NodeCount; u++ {
		for e := g.Off[u]; e < g.Off[u+1]; e++ {
			tail[e] = u
		}
	}

	idx := make([]int, len(g.Ban))
	for i := range idx {
		idx[i] = i
	}
	if *sample > 0 && *sample < len(idx) {
		rnd := rand.New(rand.NewSource(*seed))
		rnd.Shuffle(len(idx), func(i, j int) { idx[i], idx[j] = idx[j], idx[i] })
		idx = idx[:*sample]
	}

	r := graph.NewRouter(g, 1)
	if *eps > 0 {
		r.SetEpsilon(float32(*eps))
	}

	edgeOf := func(a, b uint32) (uint32, bool) {
		for e := g.Off[a]; e < g.Off[a+1]; e++ {
			if g.To[e] == b {
				return uint32(e), true
			}
		}
		return 0, false
	}

	violates := func(nodes []uint32, in, out uint32) bool {
		for i := 0; i+1 < len(nodes); i++ {
			e, ok := edgeOf(nodes[i], nodes[i+1])
			if !ok || e != in {
				continue
			}
			if i+2 < len(nodes) {
				next, ok := edgeOf(nodes[i+1], nodes[i+2])
				if ok && next == out {
					return true
				}
			}
		}
		return false
	}

	var samples, strictViol, looseViol, nonVacuous, strictErr, looseErr, strictFallback, looseFallback int
	var strictNs, looseNs int64
	t0 := time.Now()

	for _, bi := range idx {
		in, out := graph.UnpackTurn(g.Ban[bi])
		src := tail[in]
		dst := g.To[out]
		if src == dst {
			continue
		}
		samples++

		r.SetStrict(true)
		a := time.Now()
		rs, errS := r.Route(src, dst)
		strictNs += time.Since(a).Nanoseconds()

		r.SetStrict(false)
		a = time.Now()
		rl, errL := r.Route(src, dst)
		looseNs += time.Since(a).Nanoseconds()

		if errS != nil {
			strictErr++
		} else if rs.Fallback {
			strictFallback++
		} else if violates(rs.Nodes, in, out) {
			strictViol++
		}
		if errL != nil {
			looseErr++
		} else if rl.Fallback {
			looseFallback++
		} else if violates(rl.Nodes, in, out) {
			looseViol++
			nonVacuous++
		}
	}

	fmt.Printf("graph           %s\n", *path)
	fmt.Printf("bans in graph   %d\n", len(g.Ban))
	fmt.Printf("only rules      %d\n", len(g.Only))
	fmt.Printf("samples routed  %d\n", samples)
	fmt.Printf("strict:  violations=%d errors=%d fallback=%d  mean=%s\n",
		strictViol, strictErr, strictFallback, mean(strictNs, samples))
	fmt.Printf("loose:   violations=%d errors=%d fallback=%d  mean=%s\n",
		looseViol, looseErr, looseFallback, mean(looseNs, samples))
	if samples > 0 {
		fmt.Printf("violation rate  strict=%.2f%%  loose=%.2f%%\n",
			ratioPct(strictViol, samples), ratioPct(looseViol, samples))
		fmt.Printf("non-vacuous     %d (%.2f%% of samples would otherwise take the forbidden turn)\n",
			nonVacuous, ratioPct(nonVacuous, samples))
	}
	fmt.Printf("total wall      %s\n", time.Since(t0).Round(time.Millisecond))

	if *jsonOut != "" {
		body := fmt.Sprintf(`{"graph":%q,"bans":%d,"only":%d,"samples":%d,
"strict_violations":%d,"strict_errors":%d,"strict_fallbacks":%d,"loose_violations":%d,"loose_errors":%d,"non_vacuous":%d,
"strict_violation_pct":%.4f,"loose_violation_pct":%.4f}`,
			*path, len(g.Ban), len(g.Only), samples,
			strictViol, strictErr, strictFallback, looseViol, looseErr, nonVacuous,
			ratioPct(strictViol, samples), ratioPct(looseViol, samples))
		if err := os.WriteFile(*jsonOut, []byte(body), 0o644); err != nil {
			log.Fatal(err)
		}
	}
}

func ratioPct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) * 100 / float64(b)
}

func mean(total int64, n int) time.Duration {
	if n == 0 {
		return 0
	}
	return time.Duration(total / int64(n)).Round(time.Microsecond)
}
