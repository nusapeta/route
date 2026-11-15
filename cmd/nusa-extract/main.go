package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"runtime"
	"time"

	"github.com/nusapeta/route/internal/extract"
)

func main() {
	pbf := flag.String("pbf", "", "input .osm.pbf")
	out := flag.String("out", "map.graph", "output graph file")
	grid := flag.Float64("grid", 0.02, "snap grid cell size in degrees")
	workers := flag.Int("workers", runtime.GOMAXPROCS(0), "pbf decoder workers")
	order := flag.String("order", "hilbert", "node numbering: hilbert|osm")
	noRest := flag.Bool("no-restrictions", false, "skip OSM turn restriction relations")
	flag.Parse()

	if *pbf == "" {
		fmt.Fprintln(os.Stderr, "usage: nusa-extract -pbf indonesia-latest.osm.pbf -out map.graph")
		os.Exit(2)
	}

	start := time.Now()
	err := extract.Build(context.Background(), *pbf, *out, extract.Options{
		GridStep:       *grid,
		Workers:        *workers,
		Order:          *order,
		NoRestrictions: *noRest,
		OnRestrictions: func(s extract.RestrictionStats) {
			log.Printf("%s", s.String())
		},
		Progress: func(phase string, n int64) {
			log.Printf("%-14s %d  (%s)", phase, n, time.Since(start).Round(time.Second))
		},
	})
	if err != nil {
		log.Fatalf("extract: %v", err)
	}
	st, err := os.Stat(*out)
	if err == nil {
		log.Printf("wrote %s: %.0f MB in %s", *out, float64(st.Size())/1e6, time.Since(start).Round(time.Second))
	}
}
