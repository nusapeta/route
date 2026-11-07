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

// Banned reports whether the transition from edge e into edge f is forbidden
// by an OSM restriction such as no_left_turn or no_u_turn.
func (g *Graph) Banned(e, f uint32) bool {
	if len(g.Ban) == 0 {
		return false
	}
	key := PackTurn(e, f)
	i := sort.Search(len(g.Ban), func(i int) bool { return g.Ban[i] >= key })
	return i < len(g.Ban) && g.Ban[i] == key
}

// OnlyBlocked reports whether edge e carries an only_* restriction and f is
// not one of its permitted successors. Unlike Banned, this forbids every
// successor except the explicitly allowed ones.
func (g *Graph) OnlyBlocked(e, f uint32) bool {
	if len(g.Only) == 0 {
		return false
	}
	lo := PackTurn(e, 0)
	hi := PackTurn(e+1, 0)
	i := sort.Search(len(g.Only), func(i int) bool { return g.Only[i] >= lo })
	if i >= len(g.Only) || g.Only[i] >= hi {
		return false // edge e has no only_* restriction
	}
	key := PackTurn(e, f)
	for ; i < len(g.Only) && g.Only[i] < hi; i++ {
		if g.Only[i] == key {
			return false // explicitly allowed
		}
	}
	return true
}

// RestrictionSpan is the set of banned and only_* successors of one edge,
// fetched once per expansion so the neighbour loop does no binary search.
type RestrictionSpan struct {
	ban, only []uint64
}

// Span returns the restriction entries that apply to outgoing edge e.
func (g *Graph) Span(e uint32) RestrictionSpan {
	var sp RestrictionSpan
	lo := PackTurn(e, 0)
	hi := PackTurn(e+1, 0)
	if len(g.Ban) > 0 {
		a := sort.Search(len(g.Ban), func(i int) bool { return g.Ban[i] >= lo })
		b := sort.Search(len(g.Ban), func(i int) bool { return g.Ban[i] >= hi })
		sp.ban = g.Ban[a:b]
	}
	if len(g.Only) > 0 {
		a := sort.Search(len(g.Only), func(i int) bool { return g.Only[i] >= lo })
		b := sort.Search(len(g.Only), func(i int) bool { return g.Only[i] >= hi })
		sp.only = g.Only[a:b]
	}
	return sp
}

// Forbidden reports whether the transition e -> f is blocked by this span.
func (sp RestrictionSpan) Forbidden(f uint32) bool {
	for _, k := range sp.ban {
		if uint32(k) == f {
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
func (sp RestrictionSpan) Empty() bool { return len(sp.ban) == 0 && len(sp.only) == 0 }

// Forbidden is the router-facing check: true when the transition e -> f must
// not be taken.
func (g *Graph) Forbidden(e, f uint32) bool {
	if len(g.Ban) == 0 && len(g.Only) == 0 {
		return false
	}
	return g.Banned(e, f) || g.OnlyBlocked(e, f)
}

// RestrictionStats summarises what was loaded from the graph header.
type RestrictionStats struct {
	Ban     int
	Only    int
	Skipped uint32
}

// Restrictions returns the counts of parsed turn restrictions.
func (g *Graph) Restrictions() RestrictionStats {
	return RestrictionStats{Ban: len(g.Ban), Only: len(g.Only), Skipped: g.Skipped}
}
