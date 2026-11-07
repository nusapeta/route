// Package extract turns an OpenStreetMap .osm.pbf into a routeid graph.
//
// Usage notes:
//   - Feed it a pre-filtered extract (osmium tags-filter w/highway ...) to cut
//     memory and preprocessing time drastically.
//   - Every OSM way node becomes a graph node, so the stored geometry is exact
//     (no interior shape point dedup yet).
package extract

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/paulmach/osm"
	"github.com/paulmach/osm/osmpbf"

	"routeid/internal/graph"
)

// Version is the extractor version stamped into every graph's provenance. It is
// overridable at link time: -X routeid/internal/extract.Version=<git describe>.
var Version = "0.1.0"

// ProfileHash is a stable digest of the routing profile (per-class fallback
// speeds plus skipped classes). Two graphs with the same PBF hash but different
// profile hashes produce different routes, so both are recorded.
func ProfileHash() string {
	keys := make([]string, 0, len(defaultSpeedKMH))
	for k := range defaultSpeedKMH {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	skip := make([]string, 0, len(skipHighway))
	for k := range skipHighway {
		skip = append(skip, k)
	}
	sort.Strings(skip)
	h := sha256.New()
	for _, k := range keys {
		fmt.Fprintf(h, "speed %s %.3f\n", k, defaultSpeedKMH[k])
	}
	for _, k := range skip {
		fmt.Fprintf(h, "skip %s\n", k)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// fileSHA256 streams the whole file through SHA-256.
func fileSHA256(ctx context.Context, path string, progress func(string, int64)) ([]byte, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 1<<20)
	var n int64
	for {
		if ctx.Err() != nil {
			return nil, n, ctx.Err()
		}
		k, err := f.Read(buf)
		if k > 0 {
			h.Write(buf[:k])
			n += int64(k)
			if n%(256<<20) < int64(k) {
				progress("sha256", n)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, n, err
		}
	}
	return h.Sum(nil), n, nil
}

// Options tunes the extraction.
type Options struct {
	GridStep float64 // spatial grid cell size in degrees (default 0.02)
	Workers  int     // pbf decode workers
	// Order selects the node numbering: "hilbert" (default, page-local) or
	// "osm" (keep the id order; used for A/B measurements).
	Order string
	// NoRestrictions disables parsing of OSM turn restriction relations.
	NoRestrictions bool
	// OnRestrictions receives the restriction parsing summary, if set.
	OnRestrictions func(RestrictionStats)
	Progress       func(phase string, n int64)
}

// defaultSpeedKMH is the fallback speed per highway class, tuned for Indonesia
// (traffic-calmed residential, slow service roads; no traffic modelling).
var defaultSpeedKMH = map[string]float64{
	"motorway":       100,
	"motorway_link":  60,
	"trunk":          80,
	"trunk_link":     50,
	"primary":        60,
	"primary_link":   45,
	"secondary":      50,
	"secondary_link": 40,
	"tertiary":       40,
	"tertiary_link":  35,
	"unclassified":   30,
	"residential":    25,
	"living_street":  10,
	"service":        15,
	"track":          15,
	"road":           20,
}

var skipHighway = map[string]bool{
	"footway": true, "cycleway": true, "path": true, "pedestrian": true,
	"steps": true, "construction": true, "proposed": true, "bridleway": true,
	"corridor": true, "elevator": true, "escalator": true, "platform": true,
	"bus_stop": true, "crossing": true, "rest_area": true, "services": true,
	"escape": true, "raceway": true, "abandoned": true, "razed": true,
}

// Build parses pbfPath and writes a graph to outPath.
func Build(ctx context.Context, pbfPath, outPath string, opt Options) error {
	if opt.GridStep <= 0 {
		opt.GridStep = 0.02
	}
	if opt.Workers <= 0 {
		opt.Workers = 2
	}
	progress := opt.Progress
	if progress == nil {
		progress = func(string, int64) {}
	}

	f, err := os.Open(pbfPath)
	if err != nil {
		return err
	}
	defer f.Close()

	// provenance: hash the source so the artefact can be audited later
	progress("sha256", 0)
	pbfHash, pbfBytes, err := fileSHA256(ctx, pbfPath, progress)
	if err != nil {
		return err
	}

	// pass 1: ways
	flat, wayOff, speeds, dirs, wayIDs, err := readWays(ctx, f, opt.Workers, progress)
	if err != nil {
		return err
	}
	if len(wayOff) <= 1 {
		return fmt.Errorf("extract: no routable ways found in %s", pbfPath)
	}

	ids := uniqueSorted(flat)
	progress("unique nodes", int64(len(ids)))

	if _, err := f.Seek(0, 0); err != nil {
		return err
	}
	lat, lon, valid, err := readNodes(ctx, f, ids, opt.Workers, progress)
	if err != nil {
		return err
	}

	// remap node ids -> compact indexes
	for i, id := range flat {
		flat[i] = int64(lookup(ids, id))
	}

	// reorder nodes along a Hilbert curve so routing stays page-local
	var perm []uint32
	if opt.Order != "osm" {
		perm = spatialOrder(lat, lon, valid)
		lat, lon = applyPerm(perm, lat, lon)
		for i := range flat {
			flat[i] = int64(perm[flat[i]])
		}
		valid = applyPermBools(perm, valid)
	}

	n := len(ids)
	deg := make([]uint32, n+1)
	nWays := len(wayOff) - 1
	for w := 0; w < nWays; w++ {
		d := dirs[w]
		for i := wayOff[w]; i+1 < wayOff[w+1]; i++ {
			a, b := flat[i], flat[i+1]
			if a == b || !valid[a] || !valid[b] {
				continue
			}
			if d >= 0 {
				deg[a]++
			}
			if d <= 0 {
				deg[b]++
			}
		}
	}
	off := make([]uint64, n+1)
	var total uint64
	for i := 0; i < n; i++ {
		off[i] = total
		total += uint64(deg[i])
	}
	off[n] = total
	progress("edges", int64(total))

	b := &graph.Builder{
		Lat:  make([]int32, n),
		Lon:  make([]int32, n),
		Off:  off,
		To:   make([]uint32, total),
		Len:  make([]uint32, total),
		Time: make([]uint32, total),
	}
	copy(b.Lat, lat)
	copy(b.Lon, lon)

	cur := make([]uint64, n)
	copy(cur, off[:n])
	for w := 0; w < nWays; w++ {
		speed := speeds[w]
		d := dirs[w]
		for i := wayOff[w]; i+1 < wayOff[w+1]; i++ {
			a, b2 := flat[i], flat[i+1]
			if a == b2 || !valid[a] || !valid[b2] {
				continue
			}
			la, lo := float64(lat[a])/graph.E7, float64(lon[a])/graph.E7
			lb, lob := float64(lat[b2])/graph.E7, float64(lon[b2])/graph.E7
			dist := graph.Haversine(la, lo, lb, lob)
			m := uint32(math.Round(dist))
			if m == 0 {
				m = 1
			}
			t := uint32(math.Round(float64(m) / speed * 10))
			if t == 0 {
				t = 1
			}
			if d >= 0 {
				e := cur[a]
				b.To[e], b.Len[e], b.Time[e] = uint32(b2), m, t
				cur[a]++
			}
			if d <= 0 {
				e := cur[b2]
				b.To[e], b.Len[e], b.Time[e] = uint32(a), m, t
				cur[b2]++
			}
		}
		if w%500_000 == 0 && w > 0 {
			progress("ways built", int64(w))
		}
	}

	b.BuildGrid(opt.GridStep)
	progress("grid cells", int64(b.GridNLat*b.GridNLon))

	// turn restrictions: needs the finished CSR plus the way->node mapping
	if !opt.NoRestrictions {
		rv := &restView{
			wayIDs:   wayIDs,
			wayIndex: buildWayIndex(wayIDs),
			flat:     flat,
			wayOff:   wayOff,
			off:      off,
			to:       b.To,
			ids:      ids,
			perm:     perm,
		}
		rs, err := buildRestrictions(ctx, f, opt.Workers, progress, rv)
		if err != nil {
			return err
		}
		b.Ban, b.Only, b.RestSkipped = rs.ban, rs.only, rs.stats.Skipped()
		if opt.OnRestrictions != nil {
			opt.OnRestrictions(rs.stats)
		}
	}

	b.PBFHash = pbfHash
	b.BuildUnix = uint32(time.Now().Unix())
	b.Meta = map[string]string{
		"source":         filepath.Base(pbfPath),
		"pbf_bytes":      strconv.FormatInt(pbfBytes, 10),
		"pbf_sha256":     hex.EncodeToString(pbfHash),
		"extractor":      Version,
		"profile_sha256": ProfileHash(),
		"order":          orderName(opt.Order),
		"grid_step":      strconv.FormatFloat(opt.GridStep, 'f', 4, 64),
		"restrictions":   boolName(!opt.NoRestrictions),
		"tool":           "rid-extract",
	}
	if err := b.Write(outPath); err != nil {
		return err
	}
	return nil
}

func orderName(v string) string {
	if v == "osm" {
		return "osm"
	}
	return "hilbert"
}

func boolName(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

func readWays(ctx context.Context, f *os.File, workers int, progress func(string, int64)) (flat []int64, wayOff []int, speeds []float64, dirs []int8, wayIDs []int64, err error) {
	sc := osmpbf.New(ctx, f, workers)
	defer sc.Close()
	sc.SkipNodes = true
	sc.SkipRelations = true

	wayOff = append(wayOff, 0)
	flat = make([]int64, 0, 1<<22)
	speeds = make([]float64, 0, 1<<20)
	dirs = make([]int8, 0, 1<<20)
	wayIDs = make([]int64, 0, 1<<20)

	var scanned int64
	for sc.Scan() {
		w, isWay := sc.Object().(*osm.Way)
		if !isWay {
			continue
		}
		scanned++
		if scanned%5_000_000 == 0 {
			progress("ways scanned", scanned)
		}
		speed, dir, ok := wayProfile(w.Tags)
		if !ok {
			continue
		}
		for _, nd := range w.Nodes {
			flat = append(flat, int64(nd.ID))
		}
		wayOff = append(wayOff, len(flat))
		speeds = append(speeds, speed)
		dirs = append(dirs, dir)
		wayIDs = append(wayIDs, int64(w.ID))
	}
	if err := sc.Err(); err != nil {
		return nil, nil, nil, nil, nil, err
	}
	progress("ways scanned", scanned)
	return flat, wayOff, speeds, dirs, wayIDs, nil
}

func readNodes(ctx context.Context, f *os.File, ids []int64, workers int, progress func(string, int64)) (lat, lon []int32, valid []bool, err error) {
	lat = make([]int32, len(ids))
	lon = make([]int32, len(ids))
	valid = make([]bool, len(ids))

	sc := osmpbf.New(ctx, f, workers)
	defer sc.Close()
	sc.SkipWays = true
	sc.SkipRelations = true

	var scanned int64
	for sc.Scan() {
		nd, isNode := sc.Object().(*osm.Node)
		if !isNode {
			continue
		}
		scanned++
		if scanned%20_000_000 == 0 {
			progress("nodes scanned", scanned)
		}
		// fast path: nodes are id-sorted in practice
		i := lookup(ids, int64(nd.ID))
		if i < 0 {
			continue
		}
		lat[i] = int32(math.Round(nd.Lat * graph.E7))
		lon[i] = int32(math.Round(nd.Lon * graph.E7))
		valid[i] = true
	}
	if err := sc.Err(); err != nil {
		return nil, nil, nil, err
	}
	progress("nodes scanned", scanned)
	return lat, lon, valid, nil
}

// lookup returns the index of v in sorted ids, or -1.
func lookup(ids []int64, v int64) int {
	lo, hi := 0, len(ids)-1
	for lo <= hi {
		mid := int(uint(lo+hi) >> 1)
		switch {
		case ids[mid] < v:
			lo = mid + 1
		case ids[mid] > v:
			hi = mid - 1
		default:
			return mid
		}
	}
	return -1
}

func uniqueSorted(in []int64) []int64 {
	out := make([]int64, len(in))
	copy(out, in)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	if len(out) == 0 {
		return out
	}
	k := 1
	for i := 1; i < len(out); i++ {
		if out[i] != out[k-1] {
			out[k] = out[i]
			k++
		}
	}
	return out[:k]
}

func wayProfile(tags osm.Tags) (speed float64, dir int8, ok bool) {
	s := string(tags.Find("access"))
	mv := string(tags.Find("motor_vehicle"))
	mc := string(tags.Find("motorcar"))
	if s == "no" || s == "private" || mv == "no" || mc == "no" {
		if mv != "yes" && mc != "yes" && s != "destination" {
			return 0, 0, false
		}
	}

	hw := string(tags.Find("highway"))
	kmh, isRoad := defaultSpeedKMH[hw]
	if !isRoad {
		if skipHighway[hw] {
			return 0, 0, false
		}
		if string(tags.Find("route")) == "ferry" {
			kmh = 20
		} else {
			return 0, 0, false
		}
	}

	if ms := string(tags.Find("maxspeed")); ms != "" {
		if v, good := parseSpeedKMH(ms); good {
			kmh = v
		}
	}

	one := string(tags.Find("oneway"))
	switch one {
	case "yes", "true", "1":
		dir = 1
	case "-1", "reverse":
		dir = -1
	case "no", "false", "0":
		dir = 0
	default:
		j := string(tags.Find("junction"))
		if j == "roundabout" || j == "circular" {
			dir = 1
		}
	}

	return kmh / 3.6, dir, true
}

func parseSpeedKMH(s string) (float64, bool) {
	var v float64
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		v = v*10 + float64(s[n]-'0')
		n++
	}
	if n == 0 || v <= 0 || v > 200 {
		return 0, false
	}
	// "mph" suffixes are not used in Indonesia; treat everything as km/h
	return v, true
}
