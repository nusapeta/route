package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/paulmach/osm"
	"github.com/paulmach/osm/osmpbf"
)

var carHW = map[string]bool{"motorway": true, "motorway_link": true, "trunk": true, "trunk_link": true,
	"primary": true, "primary_link": true, "secondary": true, "secondary_link": true,
	"tertiary": true, "tertiary_link": true, "unclassified": true, "residential": true,
	"living_street": true, "service": true, "road": true}

func main() {
	pbf := flag.String("pbf", "", "")
	n := flag.Int("n", 8, "examples to print")
	flag.Parse()
	f, err := os.Open(*pbf)
	if err != nil {
		panic(err)
	}
	defer f.Close()

	ways := make(map[osm.WayID][]int64, 1<<20)
	sc := osmpbf.New(context.Background(), f, 3)
	sc.SkipNodes = true
	sc.SkipRelations = true
	for sc.Scan() {
		w, ok := sc.Object().(*osm.Way)
		if !ok || !carHW[string(w.Tags.Find("highway"))] {
			continue
		}
		ids := make([]int64, len(w.Nodes))
		for i, nd := range w.Nodes {
			ids[i] = int64(nd.ID)
		}
		ways[w.ID] = ids
	}
	sc.Close()
	fmt.Printf("car ways: %d\n", len(ways))

	f.Seek(0, 0)
	sc = osmpbf.New(context.Background(), f, 3)
	sc.SkipNodes = true
	sc.SkipWays = true
	var tot, inF, inT, inBoth, viaMulti, notCar int
	printed := 0
	for sc.Scan() {
		r, ok := sc.Object().(*osm.Relation)
		if !ok || string(r.Tags.Find("type")) != "restriction" {
			continue
		}
		var from, to osm.WayID
		var vn []int64
		var vw int
		for _, m := range r.Members {
			switch {
			case m.Type == osm.TypeWay && m.Role == "from":
				from = osm.WayID(m.Ref)
			case m.Type == osm.TypeWay && m.Role == "to":
				to = osm.WayID(m.Ref)
			case m.Type == osm.TypeWay && m.Role == "via":
				vw++
			case m.Type == osm.TypeNode && m.Role == "via":
				vn = append(vn, int64(m.Ref))
			}
		}
		if vw > 0 || len(vn) == 0 {
			continue
		}
		tot++
		if len(vn) > 1 {
			viaMulti++
		}
		fw, okf := ways[from]
		tw, okt := ways[to]
		if !okf || !okt {
			notCar++
			continue
		}
		has := func(s []int64, v int64) bool {
			for _, x := range s {
				if x == v {
					return true
				}
			}
			return false
		}
		var hf, ht bool
		for _, v := range vn {
			if has(fw, v) {
				hf = true
			}
			if has(tw, v) {
				ht = true
			}
		}
		if hf {
			inF++
		}
		if ht {
			inT++
		}
		if hf && ht {
			inBoth++
		} else if printed < *n {
			printed++
			fmt.Printf("MISS rel=%d from=%d to=%d via=%v inFrom=%v inTo=%v fromLen=%d toLen=%d\n",
				r.ID, from, to, vn, hf, ht, len(fw), len(tw))
			_ = strings.TrimSpace
		}
	}
	fmt.Printf("via-node rels=%d multi_via=%d not_car=%d inFrom=%d inTo=%d both=%d\n",
		tot, viaMulti, notCar, inF, inT, inBoth)
}
