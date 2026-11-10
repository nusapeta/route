// Command rid-tagstats counts the OSM way tags that decide motorcycle access,
// direction, and speed. It skips nodes and relations, so it is cheap enough to
// run before every profile change.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"

	"routeid/internal/extract"
)

func main() {
	pbf := flag.String("pbf", "", "input .osm.pbf")
	workers := flag.Int("workers", runtime.GOMAXPROCS(0), "pbf decoder workers")
	asJSON := flag.Bool("json", false, "emit the census as JSON")
	flag.Parse()

	if *pbf == "" {
		fmt.Fprintln(os.Stderr, "usage: rid-tagstats -pbf indonesia-latest.osm.pbf [-json]")
		os.Exit(2)
	}
	census, err := extract.ScanTags(context.Background(), *pbf, *workers, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tagstats: %v\n", err)
		os.Exit(1)
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(census); err != nil {
			fmt.Fprintf(os.Stderr, "tagstats: %v\n", err)
			os.Exit(1)
		}
		return
	}
	fmt.Print(census.String())
}
