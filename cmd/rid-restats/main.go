package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/paulmach/osm"
	"github.com/paulmach/osm/osmpbf"
)

var carHighways = map[string]bool{
	"motorway": true, "motorway_link": true, "trunk": true, "trunk_link": true,
	"primary": true, "primary_link": true, "secondary": true, "secondary_link": true,
	"tertiary": true, "tertiary_link": true, "unclassified": true, "residential": true,
	"living_street": true, "service": true, "road": true, "track": false,
}

func pass(pbf string, fn func(*osmpbf.Scanner)) {
	f, err := os.Open(pbf)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	sc := osmpbf.New(context.Background(), f, 3)
	defer sc.Close()
	fn(sc)
}

func main() {
	pbf := flag.String("pbf", "", "")
	flag.Parse()
	car := make(map[osm.WayID]struct{}, 4<<20)
	oneway := map[osm.WayID]int8{}
	pass(*pbf, func(sc *osmpbf.Scanner) {
		sc.SkipNodes = true
		sc.SkipRelations = true
		for sc.Scan() {
			w, ok := sc.Object().(*osm.Way)
			if !ok {
				continue
			}
			hw := string(w.Tags.Find("highway"))
			if !carHighways[hw] {
				continue
			}
			if w.Tags.Find("access") == "no" || w.Tags.Find("motor_vehicle") == "no" || w.Tags.Find("motorcar") == "no" {
				continue
			}
			car[w.ID] = struct{}{}
			switch string(w.Tags.Find("oneway")) {
			case "yes", "true", "1":
				oneway[w.ID] = 1
			case "-1", "reverse":
				oneway[w.ID] = -1
			default:
				if hw == "motorway" || hw == "motorway_link" || hw == "trunk_link" {
					oneway[w.ID] = 1
				} else {
					oneway[w.ID] = 0
				}
			}
		}
		if err := sc.Err(); err != nil {
			panic(err)
		}
	})
	fmt.Printf("car ways: %d\n", len(car))

	var tot, ok, fromMissing, toMissing, both = 0, 0, 0, 0, 0
	var viaWaySurvive, viaWayLen1, viaWayLenN = 0, 0, 0
	pass(*pbf, func(sc *osmpbf.Scanner) {
		sc.SkipNodes = true
		sc.SkipWays = false
		sc.SkipWays = true
		for sc.Scan() {
			r, o := sc.Object().(*osm.Relation)
			if !o {
				continue
			}
			if string(r.Tags.Find("type")) != "restriction" {
				continue
			}
			tot++
			var from, to osm.WayID
			viaWays := []osm.WayID{}
			viaNodes := 0
			for _, m := range r.Members {
				switch m.Role {
				case "from":
					if m.Type == osm.TypeWay {
						from = osm.WayID(m.Ref)
					}
				case "to":
					if m.Type == osm.TypeWay {
						to = osm.WayID(m.Ref)
					}
				case "via":
					if m.Type == osm.TypeWay {
						viaWays = append(viaWays, osm.WayID(m.Ref))
					} else {
						viaNodes++
					}
				}
			}
			_, fok := car[from]
			_, tok := car[to]
			if !fok {
				fromMissing++
			}
			if !tok {
				toMissing++
			}
			if fok && tok {
				ok++
				both++
				if len(viaWays) > 0 {
					viaWaySurvive++
					if len(viaWays) == 1 {
						viaWayLen1++
					} else {
						viaWayLenN++
					}
				}
			}
			_ = viaNodes
		}
		if err := sc.Err(); err != nil {
			panic(err)
		}
	})
	fmt.Printf("restrictions total: %d\n", tot)
	fmt.Printf("usable (from+to both car): %d (%.1f%%)\n", ok, float64(ok)*100/float64(tot))
	fmt.Printf("from way not car: %d   to way not car: %d\n", fromMissing, toMissing)
	fmt.Printf("usable via-way: %d (1 via way: %d, multi: %d)\n", viaWaySurvive, viaWayLen1, viaWayLenN)
	fmt.Printf("usable via-node: %d\n", ok-viaWaySurvive)
}
