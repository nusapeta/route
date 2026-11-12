package extract

import (
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/paulmach/osm"
	"github.com/paulmach/osm/osmpbf"

	"routeid/internal/graph"
)

// RestrictionStats counts how the OSM restriction relations were handled.
// Usable counts stored rules only; every reason a rule was not stored is part
// of Skipped(), so Usable + Skipped() == Relations. ReachCap counts searches
// that hit the edge-state cap; those bans are relaxed conservatively.
type RestrictionStats struct {
	Profile       string // "car" or "motor" for v5 union builds
	Relations     uint32 // type=restriction relations seen
	Usable        uint32 // from+to resolved and the rule stored in ban/only list
	ViaNode       uint32 // resolved through a single via node
	ViaWay        uint32 // resolved through one or more via ways
	Only          uint32 // only_* rules stored in the allow list
	Ban           uint32 // no_* pairs stored in the ban list
	NotCar        uint32 // relation type or from/to way not usable by this profile
	NotClass      uint32 // only restriction:<other vehicle class> tags present
	Missing       uint32 // relation lacks from/to members
	Conditional   uint32 // restriction:conditional (time dependent, not modelled)
	Except        uint32 // except=motorcar/motor_vehicle/vehicle
	BadType       uint32 // no usable restriction= or unknown restriction value
	Unresolved    uint32 // via node not present in the graph / no matching edge
	ChainFail     uint32 // multi via-way chain could not be ordered
	ViaWayMissing uint32 // a via way is absent from the graph
	Relaxed       uint32 // ban omitted because no alternate path exists
	ReachCap      uint32 // reachability search hit its state limit

	// fine-grained reasons behind Unresolved
	RNoNode   uint32 // via node id absent from the graph
	RNotOnWay uint32 // via node is not part of the from/to way node list
	RNoIn     uint32 // no directed edge entering the via node from the from way
	RNoOut    uint32 // no directed edge leaving the via node into the to way
}

func (s *RestrictionStats) String() string {
	u := func(v uint32) string { return strconv.FormatUint(uint64(v), 10) }
	name := "restrictions"
	if s.Profile != "" {
		name += "(" + s.Profile + ")"
	}
	return name + ": seen=" + u(s.Relations) +
		" usable=" + u(s.Usable) +
		" via_node=" + u(s.ViaNode) +
		" via_way=" + u(s.ViaWay) +
		" ban=" + u(s.Ban) + " only=" + u(s.Only) +
		" skipped{not_car=" + u(s.NotCar) +
		" not_class=" + u(s.NotClass) +
		" missing=" + u(s.Missing) +
		" conditional=" + u(s.Conditional) +
		" except=" + u(s.Except) +
		" bad_type=" + u(s.BadType) +
		" unresolved=" + u(s.Unresolved) +
		" chain=" + u(s.ChainFail) +
		" via_way_missing=" + u(s.ViaWayMissing) +
		" relaxed=" + u(s.Relaxed) +
		" reach_cap=" + u(s.ReachCap) +
		" why{no_node=" + u(s.RNoNode) +
		" not_on_way=" + u(s.RNotOnWay) +
		" no_in=" + u(s.RNoIn) +
		" no_out=" + u(s.RNoOut) +
		"}}"
}

// restView is the read-only view of the built graph that restriction
// resolution needs: way membership, way node sequences, CSR adjacency and the
// node id -> rank mapping.
type restView struct {
	wayIDs   []int64
	wayIndex map[int64]int32
	flat     []int64
	wayOff   []int
	off      []uint32
	to       []uint32
	ids      []int64  // osm node ids, sorted (pre-permutation order)
	perm     []uint32 // pre-rank -> final rank, nil when order=osm
	cls      []uint8  // per-edge speed class; 0 = unusable by this profile

	// scratch for the build-time restriction reachability check
	seen  []uint32
	gen   uint32
	queue []uint32
}

type restResult struct {
	ban, only []uint64
	banSrc    []uint32 // tail of the banned from-edge
	banKind   []uint8  // 1 via-node, 2 via-way
	stats     RestrictionStats
	prof      Profile
}

// buildRestrictions reads every type=restriction relation and converts it to
// forbidden (fromEdge,toEdge) transitions over the already built CSR.
func buildRestrictions(ctx context.Context, f *os.File, workers int, progress func(string, int64), v *restView, prof Profile) (restResult, error) {
	res := restResult{prof: prof}
	res.stats.Profile = string(prof)

	if _, err := f.Seek(0, 0); err != nil {
		return res, err
	}
	sc := osmpbf.New(ctx, f, workers)
	defer sc.Close()
	sc.SkipNodes = true
	sc.SkipWays = true

	var rels []*osm.Relation
	for sc.Scan() {
		r, ok := sc.Object().(*osm.Relation)
		if !ok {
			continue
		}
		if !strings.HasPrefix(string(r.Tags.Find("type")), "restriction") {
			continue
		}
		rels = append(rels, r)
	}
	if err := sc.Err(); err != nil {
		return res, err
	}
	res.stats.Relations = uint32(len(rels))
	progress("restriction relations", int64(len(rels)))

	for _, r := range rels {
		res.addRelation(v, r)
	}
	res.filterDetours(v)

	res.ban = graph.SortUniquePacked(res.ban)
	res.only = graph.SortUniquePacked(res.only)
	return res, nil
}

// rankOf maps an OSM node id to the final (post Hilbert) node rank.
func (v *restView) rankOf(nodeID int64) (int64, bool) {
	i := lookup(v.ids, nodeID)
	if i < 0 {
		return 0, false
	}
	if v.perm == nil {
		return int64(i), true
	}
	return int64(v.perm[i]), true
}

func (v *restView) wayNodes(w int32) []int64 {
	return v.flat[v.wayOff[w]:v.wayOff[w+1]]
}

// edge finds the directed edge u->t in the CSR, if it exists.
func (v *restView) edge(u, t int64) (uint32, bool) {
	if u < 0 || t < 0 || u == t || u+1 >= int64(len(v.off)) {
		return 0, false
	}
	for e := v.off[u]; e < v.off[u+1]; e++ {
		if v.cls != nil && v.cls[e] == 0 {
			continue
		}
		if v.to[e] == uint32(t) {
			return e, true
		}
	}
	return 0, false
}

// inEdge is the edge that arrives at position i while travelling along the
// way. When i is the first node the travel direction is reversed, which is the
// common no_u_turn shape where the via node sits at the end of the way.
func (v *restView) inEdge(seq []int64, i int) (uint32, bool) {
	if i > 0 {
		return v.edge(seq[i-1], seq[i])
	}
	if len(seq) > 1 {
		return v.edge(seq[1], seq[0])
	}
	return 0, false
}

// outEdge is the edge that leaves position j along the travel direction.
func (v *restView) outEdge(seq []int64, j int) (uint32, bool) {
	if j+1 < len(seq) {
		return v.edge(seq[j], seq[j+1])
	}
	if j > 0 {
		return v.edge(seq[j], seq[j-1])
	}
	return 0, false
}

// indexOf returns the position of rank inside a way node list.
func indexOf(seq []int64, rank int64) int {
	for i, x := range seq {
		if x == rank {
			return i
		}
	}
	return -1
}

// restrictionTag returns the restriction value that applies to prof. The plain
// `restriction` tag applies to every vehicle and is the fallback; a vehicle
// class tag overrides it.
func restrictionTag(t osm.Tags, p Profile) string {
	keys := []string{"restriction:motorcar", "restriction:motor_vehicle", "restriction"}
	if p == ProfileMotor {
		keys = []string{"restriction:motorcycle", "restriction:motor_vehicle", "restriction"}
	}
	for _, k := range keys {
		if v := t.Find(k); v != "" {
			return v
		}
	}
	return ""
}

// otherClassTag reports whether the relation only carries restriction:<class>
// values for vehicles outside this profile.
func otherClassTag(t osm.Tags) bool {
	for _, kv := range t {
		if strings.HasPrefix(kv.Key, "restriction:") && kv.Key != "restriction:conditional" {
			return true
		}
	}
	return false
}

// exceptApplies reports whether an except token exempts this profile.
func exceptApplies(tok string, p Profile) bool {
	switch strings.TrimSpace(tok) {
	case "vehicle":
		return true
	case "motor_vehicle":
		return true
	case "motorcar":
		return p != ProfileMotor
	case "motorcycle":
		return p == ProfileMotor
	}
	return false
}

func (res *restResult) addRelation(v *restView, r *osm.Relation) {
	s := &res.stats

	if string(r.Tags.Find("type")) != "restriction" {
		// type=restriction:hgv and friends: not this profile.
		s.NotCar++
		return
	}
	if c := string(r.Tags.Find("restriction:conditional")); c != "" {
		s.Conditional++
		return
	}
	kind := restrictionTag(r.Tags, res.prof)
	if kind == "" {
		if otherClassTag(r.Tags) {
			s.NotClass++
		} else {
			s.BadType++
		}
		return
	}
	if ex := string(r.Tags.Find("except")); ex != "" {
		for _, tok := range strings.Split(ex, ";") {
			if exceptApplies(tok, res.prof) {
				s.Except++
				return
			}
		}
	}

	var fromWay, toWay int64
	var viaNodes, viaWays []int64
	for _, m := range r.Members {
		if m.Type == osm.TypeWay {
			switch m.Role {
			case "from":
				fromWay = int64(m.Ref)
			case "to":
				toWay = int64(m.Ref)
			case "via":
				viaWays = append(viaWays, int64(m.Ref))
			}
		} else if m.Type == osm.TypeNode && m.Role == "via" {
			viaNodes = append(viaNodes, int64(m.Ref))
		}
	}
	if fromWay == 0 || toWay == 0 {
		s.Missing++
		return
	}
	wf, okF := v.wayIndex[fromWay]
	wt, okT := v.wayIndex[toWay]
	if !okF || !okT {
		s.NotCar++
		return
	}

	only := strings.HasPrefix(kind, "only_")

	stored := false
	switch {
	case len(viaWays) > 0:
		if res.addViaWay(v, wf, wt, viaWays, only) {
			s.ViaWay++
			stored = true
		}
	case len(viaNodes) > 0:
		if res.addViaNode(v, wf, wt, viaNodes[0], only) {
			s.ViaNode++
			stored = true
		}
	default:
		s.Missing++
	}
	if stored {
		s.Usable++
	}
}

// addViaNode resolves a classic via-node restriction such as no_left_turn or
// only_straight_on.
func (res *restResult) addViaNode(v *restView, wf, wt int32, viaID int64, only bool) bool {
	s := &res.stats
	via, ok := v.rankOf(viaID)
	if !ok {
		s.Unresolved++
		s.RNoNode++
		return false
	}
	fr := v.wayNodes(wf)
	i := indexOf(fr, via)
	if i < 0 {
		s.Unresolved++
		s.RNotOnWay++
		return false
	}
	in, ok := v.inEdge(fr, i)
	if !ok {
		s.Unresolved++
		s.RNoIn++
		return false
	}
	tr := v.wayNodes(wt)
	j := indexOf(tr, via)
	if j < 0 {
		s.Unresolved++
		s.RNotOnWay++
		return false
	}
	out, ok := v.outEdge(tr, j)
	if !ok {
		s.Unresolved++
		s.RNoOut++
		return false
	}
	src := via
	if i > 0 {
		src = fr[i-1]
	} else if i+1 < len(fr) {
		src = fr[i+1]
	}
	if only {
		res.only = append(res.only, graph.PackTurn(in, out))
		s.Only++
	} else {
		res.ban = append(res.ban, graph.PackTurn(in, out))
		res.banSrc = append(res.banSrc, uint32(src))
		res.banKind = append(res.banKind, 1)
		s.Ban++
	}
	return true
}

// addViaWay resolves a restriction whose via member is one or more ways. The
// full path is not expressible as a single banned transition, so we forbid
// entering the via way from the restricted from-edge. That is conservative:
// it can block legal exits from the via way too, so callers can retry without
// restrictions when it causes a no-route. The ban covers every destination
// leaving the via way, so wt only gated the from/to presence check above.
func (res *restResult) addViaWay(v *restView, wf, wt int32, viaIDs []int64, only bool) bool {
	s := &res.stats
	if only {
		// only_* with a via way: allowed path is unique; blocking the entry
		// would forbid the permitted path too. Skip and count.
		s.ChainFail++
		return false
	}
	seqs := make([][]int64, 0, len(viaIDs))
	for _, id := range viaIDs {
		w, ok := v.wayIndex[id]
		if !ok {
			s.ViaWayMissing++
			return false
		}
		seqs = append(seqs, v.wayNodes(w))
	}

	// The via way can share either endpoint with the from way, and the
	// junction need not sit at the end of the from way.
	fr := v.wayNodes(wf)
	var start, src int64
	var in uint32
	found := false
	for _, seq := range seqs {
		if len(seq) < 2 {
			continue
		}
		for _, node := range []int64{seq[0], seq[len(seq)-1]} {
			i := indexOf(fr, node)
			if i < 0 {
				continue
			}
			if e, ok := v.inEdge(fr, i); ok {
				start, in = node, e
				if i > 0 {
					src = fr[i-1]
				} else {
					src = fr[i+1]
				}
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		s.Unresolved++
		return false
	}
	chain, ok := chainVia(seqs, start)
	if !ok || len(chain) < 2 {
		s.ChainFail++
		return false
	}
	first, ok := v.edge(chain[0], chain[1])
	if !ok {
		s.Unresolved++
		return false
	}
	res.ban = append(res.ban, graph.PackTurn(in, first))
	res.banSrc = append(res.banSrc, uint32(src))
	res.banKind = append(res.banKind, 2)
	s.Ban++
	return true
}

// filterDetours drops bans whose endpoints cannot be connected without the
// banned transition once every other ban and only_* rule is enforced. Keeping
// such a ban would make the router fall back to an unrestricted search.
func (res *restResult) filterDetours(v *restView) {
	if len(res.ban) == 0 {
		return
	}
	s := &res.stats
	banned := make(map[uint64]struct{}, len(res.ban))
	for _, k := range res.ban {
		banned[k] = struct{}{}
	}
	only := make(map[uint32]map[uint32]struct{})
	for _, k := range res.only {
		in, out := graph.UnpackTurn(k)
		m := only[in]
		if m == nil {
			m = make(map[uint32]struct{})
			only[in] = m
		}
		m[out] = struct{}{}
	}
	srcOf := make(map[uint64]uint32, len(res.ban))
	for i, k := range res.ban {
		if _, ok := srcOf[k]; !ok {
			srcOf[k] = res.banSrc[i]
		}
	}
	keep := make(map[uint64]bool, len(res.ban))
	seen := make(map[uint64]struct{}, len(res.ban))
	for _, k := range res.ban {
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		in, out := graph.UnpackTurn(k)
		ok, capped := v.hasDetour(srcOf[k], in, out, banned, only)
		if capped {
			// A million edge states without a detour is treated as no usable
			// detour: keeping the ban would force the runtime fallback.
			s.ReachCap++
			ok = false
		}
		keep[k] = ok
	}
	keptBan := res.ban[:0]
	keptSrc := res.banSrc[:0]
	keptKind := res.banKind[:0]
	for i, k := range res.ban {
		if keep[k] {
			keptBan = append(keptBan, k)
			keptSrc = append(keptSrc, res.banSrc[i])
			keptKind = append(keptKind, res.banKind[i])
			continue
		}
		s.Ban--
		s.Usable--
		s.Relaxed++
		if res.banKind[i] == 2 {
			s.ViaWay--
		} else {
			s.ViaNode--
		}
	}
	res.ban, res.banSrc, res.banKind = keptBan, keptSrc, keptKind
}

// hasDetour reports whether src can reach the head of out without taking the
// banned transition in -> out. The search is over directed edge states, so it
// enforces every other ban and only_* rule in the graph.
func (v *restView) hasDetour(src, in, out uint32, banned map[uint64]struct{}, only map[uint32]map[uint32]struct{}) (found, capped bool) {
	const limit = 1 << 20
	if int(src) >= len(v.off)-1 || int(in) >= len(v.to) || int(out) >= len(v.to) {
		return false, false
	}
	dst := v.to[out]
	if src == dst {
		return true, false
	}
	if v.seen == nil {
		v.seen = make([]uint32, len(v.to))
	}
	if v.queue == nil {
		v.queue = make([]uint32, 0, 1<<16)
	}
	v.gen++
	if v.gen == 0 {
		clear(v.seen)
		v.gen = 1
	}
	seen := v.seen
	q := v.queue[:0]
	add := func(e uint32) bool {
		if seen[e] == v.gen {
			return true
		}
		seen[e] = v.gen
		if len(q) >= limit {
			return false
		}
		q = append(q, e)
		return true
	}
	for e := v.off[src]; e < v.off[src+1]; e++ {
		if v.cls != nil && v.cls[e] == 0 {
			continue
		}
		if v.to[e] == dst {
			return true, false
		}
		if !add(e) {
			return false, true
		}
	}
	for head := 0; head < len(q); head++ {
		e := q[head]
		u := v.to[e]
		m := only[e]
		for f := v.off[u]; f < v.off[u+1]; f++ {
			if v.cls != nil && v.cls[f] == 0 {
				continue
			}
			if e == in && f == out {
				continue
			}
			if _, ok := banned[graph.PackTurn(e, f)]; ok {
				continue
			}
			if m != nil {
				if _, ok := m[f]; !ok {
					continue
				}
			}
			if v.to[f] == dst {
				return true, false
			}
			if !add(f) {
				return false, true
			}
		}
	}
	if cap(q) > cap(v.queue) {
		v.queue = q[:0]
	}
	return false, false
}

// chainVia orders the via ways so that each one starts where the previous one
// ended, beginning at start. Reversed ways are flipped.
func chainVia(seqs [][]int64, start int64) ([]int64, bool) {
	used := make([]bool, len(seqs))
	chain := []int64{start}
	cur := start
	for k := 0; k < len(seqs); k++ {
		found := false
		for i, s := range seqs {
			if used[i] || len(s) < 2 {
				continue
			}
			if s[0] == cur {
				chain = append(chain, s[1:]...)
				cur = s[len(s)-1]
				used[i] = true
				found = true
				break
			}
			if s[len(s)-1] == cur {
				for j := len(s) - 2; j >= 0; j-- {
					chain = append(chain, s[j])
				}
				cur = s[0]
				used[i] = true
				found = true
				break
			}
		}
		if !found {
			return nil, false
		}
	}
	return chain, true
}

// buildWayIndex maps OSM way id -> index in the way arrays.
func buildWayIndex(wayIDs []int64) map[int64]int32 {
	m := make(map[int64]int32, len(wayIDs))
	for i, id := range wayIDs {
		m[id] = int32(i)
	}
	return m
}

// Skipped is the number of restrictions that could not be represented.
func (s *RestrictionStats) Skipped() uint32 {
	return s.NotCar + s.NotClass + s.Missing + s.Conditional + s.Except + s.BadType + s.Unresolved + s.ChainFail + s.ViaWayMissing + s.Relaxed
}
