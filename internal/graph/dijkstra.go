package graph

import "math"

// Bound records which limit ended a DijkstraOneToMany search.
type Bound struct {
	// Truncated is set when the expansion cap stopped the search before the
	// cutoff; the costs returned so far are lower-bound estimates.
	Truncated bool
}

// DijkstraCost is one settled node and its cost from the source.
type DijkstraCost struct {
	Node uint32
	Cost float32
}

// DijkstraResult is the outcome of DijkstraOneToMany.
type DijkstraResult struct {
	// Dists has one entry per target when targets was non-nil: the settled
	// cost, or +Inf when the target was not reached within the limits.
	Dists []float32
	// Visited holds settled nodes in pop order when targets was nil, so an
	// isochrone can fold them into grid cells.
	Visited    []DijkstraCost
	Bound      Bound
	Expansions int
}

// TimeCost returns the router's profile edge time in deciseconds. It is the
// router's edgeTimeDs, for use as a DijkstraOneToMany cost.
func (r *Router) TimeCost() func(uint32) float32 {
	return r.edgeTimeDs
}

// LengthCost returns edge length in metres as a DijkstraOneToMany cost. It
// wraps Graph.EdgeLenM.
func (g *Graph) LengthCost() func(uint32) float32 {
	return func(e uint32) float32 { return float32(g.EdgeLenM(e)) }
}

// DijkstraOneToMany settles least-cost paths from src through a pooled
// workspace, so no dense dist[] sized to the whole graph is allocated. cost
// must be non-negative. With nil targets it visits every node within maxCost;
// otherwise Dists is indexed by target and the search stops once all targets
// are settled. maxCost <= 0 means unbounded; the expansion cap comes from
// SetMaxExpansions. The workspace goes back to the pool before returning.
func (r *Router) DijkstraOneToMany(src uint32, targets []uint32, maxCost float32, cost func(uint32) float32) (DijkstraResult, error) {
	g := r.g
	if src >= g.NodeCount {
		return DijkstraResult{}, ErrNoRoute
	}
	if cost == nil {
		cost = r.edgeTimeDs
	}
	if maxCost <= 0 {
		maxCost = float32(math.Inf(1))
	}

	w, err := r.wp.getNodeKeyed(g)
	if err != nil {
		return DijkstraResult{}, err
	}
	defer r.wp.put(w)

	res := DijkstraResult{Bound: Bound{}}
	var byTarget map[uint32][]int
	remaining := len(targets)
	if targets != nil {
		res.Dists = make([]float32, len(targets))
		for i := range res.Dists {
			res.Dists[i] = float32(math.Inf(1))
		}
		byTarget = make(map[uint32][]int, len(targets))
		for i, n := range targets {
			byTarget[n] = append(byTarget[n], i)
		}
	}

	if !w.set(src, 0, invalidEdge) {
		res.Bound.Truncated = true
		return res, nil
	}
	heapPush(&w.heap, item{src, 0})

	var expanded int
relax:
	for len(w.heap) > 0 {
		cur := heapPop(&w.heap)
		d, _, ok := w.get(cur.node)
		if !ok || cur.f > d {
			continue
		}

		if byTarget != nil {
			if idxs, hit := byTarget[cur.node]; hit {
				for _, i := range idxs {
					res.Dists[i] = d
				}
				remaining -= len(idxs)
				delete(byTarget, cur.node)
				if remaining == 0 {
					break
				}
			}
		} else {
			res.Visited = append(res.Visited, DijkstraCost{cur.node, d})
		}

		if d >= maxCost {
			continue
		}
		expanded++
		if expanded > int(r.capFor()) {
			res.Bound.Truncated = true
			break
		}

		for e := g.Off[cur.node]; e < g.Off[cur.node+1]; e++ {
			ee := uint32(e)
			if !r.usable(ee) {
				continue
			}
			nd := d + cost(ee)
			if nd > maxCost {
				continue
			}
			to := g.To[ee]
			if old, _, seen := w.get(to); seen && nd >= old {
				continue
			}
			if !w.set(to, nd, ee) {
				res.Bound.Truncated = true
				break relax
			}
			heapPush(&w.heap, item{to, nd})
		}
	}
	res.Expansions = expanded
	return res, nil
}
