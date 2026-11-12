package graph

import "sort"

// PackTurn packs a directed transition (from edge, to edge) into one key.
// Edge ids must fit in 32 bits, which holds for every extract we build.
func PackTurn(from, to uint32) uint64 { return uint64(from)<<32 | uint64(to) }

// UnpackTurn splits a packed transition key.
func UnpackTurn(k uint64) (from, to uint32) { return uint32(k >> 32), uint32(k) }

// SortUniquePacked sorts and de-duplicates packed keys in place.
func SortUniquePacked(k []uint64) []uint64 {
	if len(k) == 0 {
		return k
	}
	sort.Slice(k, func(i, j int) bool { return k[i] < k[j] })
	out := 1
	for i := 1; i < len(k); i++ {
		if k[i] != k[out-1] {
			k[out] = k[i]
			out++
		}
	}
	return k[:out]
}

// RestrictionSpan is the set of banned and only_* successors of one edge.
// The ban list is a hash lookup; only_* rules are few enough to keep as a
// slice fetched once per expansion.
type RestrictionSpan struct {
	g     *Graph
	from  uint32
	motor bool
	ban   bool     // this edge has at least one ban (bloom hit; verified by hash)
	only  []uint64 // exact only_* successors, nil when none
}

// restrictionIndex is an open-addressed set of packed transition keys plus a
// Bloom filter over the from-edge ids, so a span costs two bit tests instead
// of two binary searches in the hot loop.
type restrictionIndex struct {
	keys []uint64 // open addressing, 0 = empty
	mask uint64
	from []uint64 // 1<<restrictionFromBits bloom bits
}

const restrictionFromBits = 16

func newRestrictionIndex(keys []uint64) restrictionIndex {
	idx := restrictionIndex{from: make([]uint64, 1<<(restrictionFromBits-6))}
	for _, k := range keys {
		e, _ := UnpackTurn(k)
		h := (e * 2654435761) >> (32 - restrictionFromBits)
		idx.from[h>>6] |= 1 << (h & 63)
	}
	if len(keys) == 0 {
		return idx
	}
	size := uint64(4)
	for size < uint64(len(keys))*2 {
		size <<= 1
	}
	idx.keys = make([]uint64, size)
	idx.mask = size - 1
	for _, k := range keys {
		if k == 0 {
			continue
		}
		h := (k * 0x9e3779b97f4a7c15) >> 32 & idx.mask
		for idx.keys[h] != 0 && idx.keys[h] != k {
			h = (h + 1) & idx.mask
		}
		idx.keys[h] = k
	}
	return idx
}

func (idx *restrictionIndex) maybeFrom(e uint32) bool {
	if len(idx.from) == 0 {
		return false
	}
	h := (e * 2654435761) >> (32 - restrictionFromBits)
	return idx.from[h>>6]&(1<<(h&63)) != 0
}

func (idx *restrictionIndex) has(k uint64) bool {
	if len(idx.keys) == 0 {
		return false
	}
	h := (k * 0x9e3779b97f4a7c15) >> 32 & idx.mask
	for {
		cur := idx.keys[h]
		if cur == k {
			return true
		}
		if cur == 0 {
			return false
		}
		h = (h + 1) & idx.mask
	}
}

// buildRestrictionIndex builds the lookup tables once the file is mapped.
func (g *Graph) buildRestrictionIndex() {
	g.banIdx = newRestrictionIndex(g.Ban)
	g.onlyIdx = newRestrictionIndex(g.Only)
	g.banMIdx = newRestrictionIndex(g.BanM)
	g.onlyMIdx = newRestrictionIndex(g.OnlyM)
}

// Span returns the car restriction entries that apply to outgoing edge e.
func (g *Graph) Span(e uint32) RestrictionSpan { return g.SpanFor(e, false) }

// SpanFor returns the restriction entries that apply to outgoing edge e for
// one profile. Car and motor rules differ when a relation carries
// except=motorcycle or references a way only one profile may use.
func (g *Graph) SpanFor(e uint32, motor bool) RestrictionSpan {
	sp := RestrictionSpan{g: g, from: e, motor: motor}
	ban, only := g.Ban, g.Only
	banIdx, onlyIdx := &g.banIdx, &g.onlyIdx
	if motor {
		ban, only = g.BanM, g.OnlyM
		banIdx, onlyIdx = &g.banMIdx, &g.onlyMIdx
	}
	sp.ban = len(ban) > 0 && banIdx.maybeFrom(e)
	if len(only) > 0 && onlyIdx.maybeFrom(e) {
		lo := PackTurn(e, 0)
		hi := PackTurn(e+1, 0)
		a := sort.Search(len(only), func(i int) bool { return only[i] >= lo })
		b := sort.Search(len(only), func(i int) bool { return only[i] >= hi })
		sp.only = only[a:b]
	}
	return sp
}

// Forbidden reports whether the transition e -> f is blocked by this span.
func (sp RestrictionSpan) Forbidden(f uint32) bool {
	if sp.g == nil {
		return false
	}
	if sp.ban {
		idx := &sp.g.banIdx
		if sp.motor {
			idx = &sp.g.banMIdx
		}
		if idx.has(PackTurn(sp.from, f)) {
			return true
		}
	}
	if len(sp.only) == 0 {
		return false
	}
	for _, k := range sp.only {
		if uint32(k) == f {
			return false // explicitly allowed
		}
	}
	return true
}

// Empty reports whether the span carries no restrictions, the common case.
func (sp RestrictionSpan) Empty() bool { return !sp.ban && len(sp.only) == 0 }

// RestrictionStats summarises what was loaded from the graph header.
type RestrictionStats struct {
	Ban     int
	Only    int
	Skipped uint32
}

// Restrictions returns the counts of parsed turn restrictions.
func (g *Graph) Restrictions() RestrictionStats { return g.RestrictionsFor(false) }

// RestrictionsFor counts the rules one profile actually carries.
func (g *Graph) RestrictionsFor(motor bool) RestrictionStats {
	if motor {
		return RestrictionStats{Ban: len(g.BanM), Only: len(g.OnlyM), Skipped: g.Skipped}
	}
	return RestrictionStats{Ban: len(g.Ban), Only: len(g.Only), Skipped: g.Skipped}
}
