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
	"strings"
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
	h := sha256.New()
	for _, name := range []string{string(ProfileCar), string(ProfileMotor)} {
		speeds := speedTable(Profile(name))
		keys := make([]string, 0, len(speeds))
		for k := range speeds {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(h, "speed %s %s %.3f\n", name, k, speeds[k])
		}
	}
	skip := make([]string, 0, len(skipHighway))
	for k := range skipHighway {
		skip = append(skip, k)
	}
	sort.Strings(skip)
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
	// Profile selects the vehicle the graph is built for: "car" (default) or
	// "motor". The choice is baked into the graph (access rules + speeds), so
	// one graph serves one vehicle.
	Profile string
	Workers int // pbf decode workers
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

// motorSpeedKMH is the motorcycle table. Bikes filter through traffic, so the
// small classes are faster than the car table while the big ones are slower
// (no sustained high speed, no toll motorway).
var motorSpeedKMH = map[string]float64{
	"motorway":       90,
	"motorway_link":  50,
	"trunk":          70,
	"trunk_link":     45,
	"primary":        55,
	"primary_link":   40,
	"secondary":      45,
	"secondary_link": 35,
	"tertiary":       40,
	"tertiary_link":  30,
	"unclassified":   30,
	"residential":    28,
	"living_street":  12,
	"service":        20,
	"track":          18,
	"road":           22,
}

// motorExtraKMH lets a motorcycle use classes that the car table skips, but
// only when the way says so (motorcycle=yes/designated/permissive). Kampung
// alleys and shortcuts are frequently mapped as path/footway in Indonesia, so
// without this the bike graph is systematically disconnected at the last mile.
var motorExtraKMH = map[string]float64{
	"path": 12, "footway": 10, "cycleway": 15, "pedestrian": 8, "steps": 4,
}

// Profile selects the vehicle assumptions baked into a graph.
type Profile string

const (
	// ProfileCar is the default: cars and other 4-wheeled vehicles.
	ProfileCar Profile = "car"
	// ProfileMotor is a motorcycle. Toll motorways are dropped unless OSM
	// explicitly allows motorcycles (they are banned on most Indonesian toll
	// roads, and OSM does not tag every toll section).
	ProfileMotor Profile = "motor"
)

// ParseProfile maps a flag value to a Profile.
func ParseProfile(s string) (Profile, error) {
	switch Profile(strings.ToLower(s)) {
	case "", ProfileCar:
		return ProfileCar, nil
	case ProfileMotor:
		return ProfileMotor, nil
	}
	return "", fmt.Errorf("extract: unknown profile %q (want car|motor)", s)
}

func speedTable(p Profile) map[string]float64 {
	if p == ProfileMotor {
		return motorSpeedKMH
	}
	return defaultSpeedKMH
}

// wayInfo is what the way pass remembers per routable way.
type wayInfo struct {
	speed  float64 // km/h, used directly unless durSec applies (ferries)
	dir    int8    // 1 forward only, -1 backward only, 0 both
	durSec float64 // crossing time from route=duration, 0 = unknown
	ferry  bool
}

// Build parses pbfPath and writes a graph to outPath.
func Build(ctx context.Context, pbfPath, outPath string, opt Options) error {
	if opt.GridStep <= 0 {
		opt.GridStep = 0.02
	}
	if opt.Workers <= 0 {
		opt.Workers = 2
	}
	prof, err := ParseProfile(opt.Profile)
	if err != nil {
		return err
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
	flat, wayOff, ways, wayIDs, err := readWays(ctx, f, opt.Workers, prof, progress)
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
		d := ways[w].dir
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
	off := make([]uint32, n+1)
	var total uint64
	for i := 0; i < n; i++ {
		if total > math.MaxUint32 {
			return fmt.Errorf("extract: %d edges exceed the uint32 CSR limit", total)
		}
		off[i] = uint32(total)
		total += uint64(deg[i])
	}
	if total > math.MaxUint32 {
		return fmt.Errorf("extract: %d edges exceed the uint32 CSR limit", total)
	}
	edgeCount := uint32(total)
	off[n] = edgeCount
	progress("edges", int64(total))

	b := &graph.Builder{
		Lat: make([]int32, n),
		Lon: make([]int32, n),
		Off: off,
		To:  make([]uint32, edgeCount),
		Len: make([]uint16, edgeCount),
		Cls: make([]uint8, edgeCount),
	}
	copy(b.Lat, lat)
	copy(b.Lon, lon)

	// Ferries carry their crossing time in a duration tag; turn that into an
	// average speed, which needs the length of the whole way. Only ferry ways
	// need the pre-pass, and there are a few thousand of them.
	ferryLen := make([]float64, nWays)
	for w := 0; w < nWays; w++ {
		if ways[w].durSec <= 0 {
			continue
		}
		var sum float64
		for i := wayOff[w]; i+1 < wayOff[w+1]; i++ {
			a, b2 := flat[i], flat[i+1]
			if a == b2 || !valid[a] || !valid[b2] {
				continue
			}
			sum += graph.Haversine(
				float64(lat[a])/graph.E7, float64(lon[a])/graph.E7,
				float64(lat[b2])/graph.E7, float64(lon[b2])/graph.E7)
		}
		ferryLen[w] = sum
	}

	cur := make([]uint32, n)
	copy(cur, off[:n])
	var lenClamped int
	var maxCls uint8
	var ferryEdges []uint32 // sorted below; sparse, a few thousand nationwide
	for w := 0; w < nWays; w++ {
		inf := ways[w]
		speed := inf.speed
		if inf.ferry && inf.durSec > 0 && ferryLen[w] > 0 {
			// duration tag wins over the class fallback: a 30 km crossing
			// that takes 2 h is 15 km/h, not 20.
			speed = ferryLen[w] / inf.durSec * 3.6
			if speed < 1 {
				speed = 1
			}
		}
		cls := speedClass(speed)
		if cls > maxCls {
			maxCls = cls
		}
		d := inf.dir
		for i := wayOff[w]; i+1 < wayOff[w+1]; i++ {
			a, b2 := flat[i], flat[i+1]
			if a == b2 || !valid[a] || !valid[b2] {
				continue
			}
			la, lo := float64(lat[a])/graph.E7, float64(lon[a])/graph.E7
			lb, lob := float64(lat[b2])/graph.E7, float64(lon[b2])/graph.E7
			dist := graph.Haversine(la, lo, lb, lob)
			m4 := uint16(math.Round(dist / graph.LenUnit))
			if m4 == 0 {
				m4 = 1
			} else if m4 >= graph.MaxLen4 {
				m4 = graph.MaxLen4
				lenClamped++
			}
			if d >= 0 {
				e := cur[a]
				b.To[e], b.Len[e], b.Cls[e] = uint32(b2), m4, cls
				if inf.ferry {
					ferryEdges = append(ferryEdges, e)
				}
				cur[a]++
			}
			if d <= 0 {
				e := cur[b2]
				b.To[e], b.Len[e], b.Cls[e] = uint32(a), m4, cls
				if inf.ferry {
					ferryEdges = append(ferryEdges, e)
				}
				cur[b2]++
			}
		}
		if w%500_000 == 0 && w > 0 {
			progress("ways built", int64(w))
		}
	}

	sort.Slice(ferryEdges, func(i, j int) bool { return ferryEdges[i] < ferryEdges[j] })
	b.Ferry = ferryEdges

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
		"profile":        string(prof),
		"max_speed_kmh":  strconv.FormatFloat(float64(maxCls)/graph.SpeedClassScale, 'f', 1, 64),
		"order":          orderName(opt.Order),
		"grid_step":      strconv.FormatFloat(opt.GridStep, 'f', 4, 64),
		"restrictions":   boolName(!opt.NoRestrictions),
		"tool":           "rid-extract",
		"len_unit_m":     strconv.Itoa(graph.LenUnit),
		"len_clamped":    strconv.Itoa(lenClamped),
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

func readWays(ctx context.Context, f *os.File, workers int, prof Profile, progress func(string, int64)) (flat []int64, wayOff []int, ways []wayInfo, wayIDs []int64, err error) {
	sc := osmpbf.New(ctx, f, workers)
	defer sc.Close()
	sc.SkipNodes = true
	sc.SkipRelations = true

	wayOff = append(wayOff, 0)
	flat = make([]int64, 0, 1<<22)
	ways = make([]wayInfo, 0, 1<<20)
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
		inf, ok := wayProfile(prof, w.Tags)
		if !ok {
			continue
		}
		for _, nd := range w.Nodes {
			flat = append(flat, int64(nd.ID))
		}
		wayOff = append(wayOff, len(flat))
		ways = append(ways, inf)
		wayIDs = append(wayIDs, int64(w.ID))
	}
	if err := sc.Err(); err != nil {
		return nil, nil, nil, nil, err
	}
	progress("ways scanned", scanned)
	return flat, wayOff, ways, wayIDs, nil
}

// speedClass quantises km/h into the Cls byte (see graph.SpeedClassScale).
func speedClass(kmh float64) uint8 {
	c := int(math.Round(kmh * graph.SpeedClassScale))
	if c < 1 {
		c = 1
	}
	if c > 254 {
		c = 254
	}
	return uint8(c)
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

// wayProfile decides whether a way is routable for p, at what speed and in
// which direction.
func wayProfile(p Profile, tags osm.Tags) (wayInfo, bool) {
	acc := string(tags.Find("access"))
	hw := string(tags.Find("highway"))
	ferry := string(tags.Find("route")) == "ferry"
	mc := string(tags.Find("motorcycle"))
	mcOK := permitted(mc)

	if p == ProfileMotor {
		// A motorcycle tag is the most specific statement about this way, so
		// it wins over access/motor_vehicle. Toll motorways are the opposite
		// case: bikes are banned there, but OSM only tags some sections, so
		// require an explicit motorcycle=yes instead of trusting silence.
		switch {
		case banned(mc):
			return wayInfo{}, false
		case mcOK:
			// allowed regardless of access=no / motor_vehicle=no
		case banned(acc):
			// access covers every vehicle, a motorcycle included;
			// motor_vehicle=no does not, bikes are not motor vehicles.
			if acc != "destination" {
				return wayInfo{}, false
			}
		case hw == "motorway" || hw == "motorway_link":
			return wayInfo{}, false
		}
	} else {
		mv := string(tags.Find("motor_vehicle"))
		mcar := string(tags.Find("motorcar"))
		if banned(acc) || banned(mv) || banned(mcar) {
			if !permitted(mv) && !permitted(mcar) && acc != "destination" {
				return wayInfo{}, false
			}
		}
	}

	kmh, isRoad := speedTable(p)[hw]
	if !isRoad {
		switch {
		case ferry:
			kmh = 20
		case p == ProfileMotor && mcOK && !banned(hw):
			// motorbike shortcut through a kampung alley / footpath
			kmh = motorExtraKMH[hw]
			if kmh == 0 {
				kmh = 10
			}
		case skipHighway[hw]:
			return wayInfo{}, false
		default:
			return wayInfo{}, false
		}
	}

	if ms := string(tags.Find("maxspeed")); ms != "" {
		if v, good := parseSpeedKMH(ms); good {
			kmh = v
		}
	}

	return wayInfo{
		speed:  kmh,
		dir:    direction(tags),
		durSec: ferryDuration(ferry, tags),
		ferry:  ferry,
	}, true
}

// permitted / banned classify the OSM access vocabulary we care about.
func permitted(v string) bool {
	switch v {
	case "yes", "designated", "permissive", "official":
		return true
	}
	return false
}

func banned(v string) bool {
	switch v {
	case "no", "private", "agricultural", "forestry", "military":
		return true
	}
	return false
}

func direction(tags osm.Tags) int8 {
	switch one := string(tags.Find("oneway")); one {
	case "yes", "true", "1":
		return 1
	case "-1", "reverse":
		return -1
	case "no", "false", "0":
		return 0
	}
	if j := string(tags.Find("junction")); j == "roundabout" || j == "circular" {
		return 1
	}
	return 0
}

// ferryDuration reads a route=duration value ("HH:MM", "HH:MM:SS" or plain
// minutes) and returns it in seconds; 0 when absent or unparsable.
func ferryDuration(ferry bool, tags osm.Tags) float64 {
	if !ferry {
		return 0
	}
	v := strings.TrimSpace(string(tags.Find("duration")))
	if v == "" {
		return 0
	}
	parts := strings.Split(v, ":")
	if len(parts) > 3 {
		return 0
	}
	nums := make([]float64, len(parts))
	for i, part := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil || f < 0 {
			return 0
		}
		nums[i] = f
	}
	var sec float64
	switch len(nums) {
	case 1: // plain minutes
		sec = nums[0] * 60
	case 2: // H:MM
		sec = nums[0]*3600 + nums[1]*60
	case 3: // H:MM:SS
		sec = nums[0]*3600 + nums[1]*60 + nums[2]
	}
	if sec <= 0 {
		return 0
	}
	return sec
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
