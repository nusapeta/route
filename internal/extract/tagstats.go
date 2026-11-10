package extract

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/paulmach/osm"
	"github.com/paulmach/osm/osmpbf"
)

// TagCensus counts the OSM tags the motor profile reads or deliberately
// ignores. It scans ways only: nodes and relations do not affect these counts.
type TagCensus struct {
	Ways        int64
	HighwayWays int64
	FerryWays   int64

	ByClass map[string]int64

	Maxspeed              int64
	MaxspeedByClass       map[string]int64
	Motorcycle            map[string]int64
	MotorVehicle          map[string]int64
	Motorcar              map[string]int64
	Access                map[string]int64
	Surface               map[string]int64
	Smoothness            map[string]int64
	Oneway                map[string]int64
	OnewayMotorcycle      map[string]int64
	Conditional           map[string]int64
	MotorcycleCond        int64
	AccessCond            int64
	OnewayMotorcycleSched int64
}

func NewTagCensus() *TagCensus {
	return &TagCensus{
		ByClass:          map[string]int64{},
		MaxspeedByClass:  map[string]int64{},
		Motorcycle:       map[string]int64{},
		MotorVehicle:     map[string]int64{},
		Motorcar:         map[string]int64{},
		Access:           map[string]int64{},
		Surface:          map[string]int64{},
		Smoothness:       map[string]int64{},
		Oneway:           map[string]int64{},
		OnewayMotorcycle: map[string]int64{},
		Conditional:      map[string]int64{},
	}
}

// Add folds one way's tags into the census. Conditional keys are counted for
// every way; the rest are counted only on highway ways.
func (c *TagCensus) Add(tags osm.Tags) {
	var highway, route, maxspeed string
	for _, kv := range tags {
		switch kv.Key {
		case "highway":
			highway = kv.Value
		case "route":
			route = kv.Value
		case "maxspeed":
			maxspeed = kv.Value
		}
		if strings.HasSuffix(kv.Key, ":conditional") {
			c.Conditional[kv.Key]++
			switch kv.Key {
			case "motorcycle:conditional":
				c.MotorcycleCond++
			case "access:conditional":
				c.AccessCond++
			}
		}
	}

	c.Ways++
	if route == "ferry" {
		c.FerryWays++
	}
	if highway == "" {
		return
	}
	c.HighwayWays++
	c.ByClass[highway]++
	if maxspeed != "" {
		c.Maxspeed++
		c.MaxspeedByClass[highway]++
	}
	for _, kv := range tags {
		switch kv.Key {
		case "motorcycle":
			c.Motorcycle[kv.Value]++
		case "motor_vehicle":
			c.MotorVehicle[kv.Value]++
		case "motorcar":
			c.Motorcar[kv.Value]++
		case "access":
			c.Access[kv.Value]++
		case "surface":
			c.Surface[kv.Value]++
		case "smoothness":
			c.Smoothness[kv.Value]++
		case "oneway":
			c.Oneway[kv.Value]++
		case "oneway:motorcycle":
			c.OnewayMotorcycle[kv.Value]++
			if strings.Contains(kv.Value, "@") {
				c.OnewayMotorcycleSched++
			}
		}
	}
}

// ScanTags reads only the way blocks of a .osm.pbf.
func ScanTags(ctx context.Context, path string, workers int, progress func(int64)) (*TagCensus, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sc := osmpbf.New(ctx, f, workers)
	defer sc.Close()
	sc.SkipNodes = true
	sc.SkipRelations = true

	c := NewTagCensus()
	var scanned int64
	for sc.Scan() {
		w, ok := sc.Object().(*osm.Way)
		if !ok {
			continue
		}
		scanned++
		if progress != nil && scanned%5_000_000 == 0 {
			progress(scanned)
		}
		c.Add(w.Tags)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if progress != nil {
		progress(scanned)
	}
	return c, nil
}

// Percent returns 100*n/d, or 0 when d is zero.
func Percent(n, d int64) float64 {
	if d == 0 {
		return 0
	}
	return 100 * float64(n) / float64(d)
}

// SortedKeys returns map keys in lexical order for stable output.
func SortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (c *TagCensus) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "ways %d\n", c.Ways)
	fmt.Fprintf(&b, "highway_ways %d\n", c.HighwayWays)
	fmt.Fprintf(&b, "ferry_ways %d\n", c.FerryWays)
	for _, k := range SortedKeys(c.ByClass) {
		fmt.Fprintf(&b, "class %q %d\n", k, c.ByClass[k])
	}
	fmt.Fprintf(&b, "maxspeed %d %.2f%%\n", c.Maxspeed, Percent(c.Maxspeed, c.HighwayWays))
	for _, k := range SortedKeys(c.MaxspeedByClass) {
		fmt.Fprintf(&b, "maxspeed_class %q %d %.2f%%\n",
			k, c.MaxspeedByClass[k], Percent(c.MaxspeedByClass[k], c.ByClass[k]))
	}
	writeCounts(&b, "motorcycle", c.Motorcycle)
	writeCounts(&b, "motor_vehicle", c.MotorVehicle)
	writeCounts(&b, "motorcar", c.Motorcar)
	writeCounts(&b, "access", c.Access)
	writeCounts(&b, "surface", c.Surface)
	writeCounts(&b, "smoothness", c.Smoothness)
	writeCounts(&b, "oneway", c.Oneway)
	writeCounts(&b, "oneway_motorcycle", c.OnewayMotorcycle)
	fmt.Fprintf(&b, "oneway_motorcycle_scheduled %d\n", c.OnewayMotorcycleSched)
	for _, k := range SortedKeys(c.Conditional) {
		fmt.Fprintf(&b, "conditional %q %d\n", k, c.Conditional[k])
	}
	return b.String()
}

func writeCounts(b *strings.Builder, prefix string, m map[string]int64) {
	for _, k := range SortedKeys(m) {
		fmt.Fprintf(b, "%s %q %d\n", prefix, k, m[k])
	}
}
