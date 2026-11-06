package graph

import (
	"errors"
	"math"
)

// Route is a routing result.
type Route struct {
	Nodes      []uint32 // node sequence, src..dst
	DistanceM  uint32
	TimeDs     uint32
	Expansions int
	// Fallback is set when restrictions were dropped to find a path.
	Fallback bool
}

// ErrNoRoute is returned when the destination is unreachable.
var ErrNoRoute = errors.New("no route")

// ErrTooFar is returned when the search space is exhausted (usually >150 km).
var ErrTooFar = errors.New("search space exhausted (route too long)")

const (
	// maxSpeedMps bounds the A* heuristic in m/s (130 km/h).
	maxSpeedMps = 130.0 / 3.6
	// maxExpansions caps a single query.
	maxExpansions = 6_000_000
	// maxSlots caps the open addressing table (33M entries).
	maxSlots = 1 << 25
)

// Router runs weighted A* queries over a Graph with a bounded workspace pool.
type Router struct {
	g      *Graph
	pool   chan *workspace
	eps    float32 // heuristic weight: 1.0 = optimal, 1.25 = ~4x faster
	strict bool    // enforce turn restrictions
}

// NewRouter creates a router; concurrency bounds peak memory.
func NewRouter(g *Graph, concurrency int) *Router {
	if concurrency < 1 {
		concurrency = 1
	}
	slots := uint32(1 << 16)
	for slots < g.NodeCount && slots < 1<<20 {
		slots <<= 1
	}
	r := &Router{g: g, pool: make(chan *workspace, concurrency), eps: 1.5, strict: true}
	for i := 0; i < concurrency; i++ {
		r.pool <- newWorkspace(slots)
	}
	return r
}

// Epsilon reports the heuristic weight.
func (r *Router) Epsilon() float32 { return r.eps }

// Strict reports whether turn restrictions are enforced.
func (r *Router) Strict() bool { return r.strict }

// SetStrict toggles turn restriction enforcement (used for A/B measurement).
func (r *Router) SetStrict(v bool) { r.strict = v }

// SetEpsilon adjusts the heuristic weight (1.0 = optimal but slow).
func (r *Router) SetEpsilon(eps float32) {
	if eps >= 1 {
		r.eps = eps
	}
}

type workspace struct {
	keys []uint32 // node+1
	gen  []uint32 // generation stamp per slot
	gs   []float32
	came []uint32
	mask uint32
	used int
	cur  uint32
	heap itemHeap
}

func newWorkspace(slots uint32) *workspace {
	return &workspace{
		keys: make([]uint32, slots),
		gen:  make([]uint32, slots),
		gs:   make([]float32, slots),
		came: make([]uint32, slots),
		mask: slots - 1,
		cur:  1,
	}
}

func (w *workspace) reset() {
	w.cur++
	if w.cur == 0 {
		for i := range w.gen {
			w.gen[i] = 0
		}
		w.cur = 1
	}
	w.used = 0
	w.heap = w.heap[:0]
}

func (w *workspace) probe(n uint32) (uint32, bool) {
	i := (n * 2654435761) & w.mask
	for {
		if w.gen[i] != w.cur {
			return i, false
		}
		if w.keys[i] == n+1 {
			return i, true
		}
		i = (i + 1) & w.mask
	}
}

func (w *workspace) get(n uint32) (float32, uint32, bool) {
	i, ok := w.probe(n)
	if !ok {
		return 0, 0, false
	}
	return w.gs[i], w.came[i], true
}

// set stores a value, growing the table when it gets crowded.
func (w *workspace) set(n uint32, g float32, came uint32) bool {
	i, ok := w.probe(n)
	if !ok {
		if (w.used+1)*10 >= len(w.keys)*7 {
			if !w.grow() {
				return false
			}
			i, _ = w.probe(n)
		}
		w.used++
	}
	w.keys[i] = n + 1
	w.gen[i] = w.cur
	w.gs[i] = g
	w.came[i] = came
	return true
}

func (w *workspace) grow() bool {
	if uint32(len(w.keys)) >= maxSlots {
		return false
	}
	oldK, oldG, oldC, oldGen := w.keys, w.gs, w.came, w.gen
	n := len(oldK) * 2
	w.keys = make([]uint32, n)
	w.gen = make([]uint32, n)
	w.gs = make([]float32, n)
	w.came = make([]uint32, n)
	w.mask = uint32(n - 1)
	w.used = 0
	for i := range oldK {
		if oldGen[i] != w.cur {
			continue
		}
		_ = w.set(oldK[i]-1, oldG[i], oldC[i])
	}
	return true
}

type item struct {
	node uint32
	f    float32
}

type itemHeap []item

// invalidEdge marks "no predecessor" in the workspace.
const invalidEdge = ^uint32(0)

// Route finds the fastest path between two node indexes.
//
// The search state is the edge just traversed, not the node. That is what
// makes turn restrictions expressible: the transition (arrival edge,
// departure edge) is exactly the pair an OSM restriction forbids.
func (r *Router) Route(src, dst uint32) (Route, error) {
	return r.RouteStrict(src, dst, r.strict)
}

// RouteStrict overrides the router's restriction policy for a single query.
func (r *Router) RouteStrict(src, dst uint32, strict bool) (Route, error) {
	g := r.g
	if src >= g.NodeCount || dst >= g.NodeCount {
		return Route{}, ErrNoRoute
	}
	if src == dst {
		return Route{Nodes: []uint32{src}}, nil
	}
	res, err := r.search(src, dst, strict)
	if err != nil && strict && (len(g.Ban) > 0 || len(g.Only) > 0) {
		// A restriction may have made the destination unreachable (bad OSM
		// data, or our conservative via-way handling). Retry unrestricted and
		// flag the answer so callers can count it.
		if res2, err2 := r.search(src, dst, false); err2 == nil {
			res2.Fallback = true
			return res2, nil
		}
	}
	return res, err
}

func (r *Router) search(src, dst uint32, restrict bool) (Route, error) {
	g := r.g
	w := <-r.pool
	defer func() { r.pool <- w }()
	w.reset()

	for e := g.Off[src]; e < g.Off[src+1]; e++ {
		ee := uint32(e)
		g0 := float32(g.Time[ee])
		if !w.set(ee, g0, invalidEdge) {
			return Route{}, ErrTooFar
		}
		heapPush(&w.heap, item{ee, g0 + r.heurEdge(ee, dst)})
	}

	var expanded int
	for len(w.heap) > 0 {
		cur := heapPop(&w.heap)
		gCur, _, ok := w.get(cur.node)
		if !ok {
			continue
		}
		if g.To[cur.node] == dst {
			res, err := r.reconstruct(w, src, cur.node)
			res.Expansions = expanded
			return res, err
		}
		if cur.f > gCur+r.heurEdge(cur.node, dst)+0.5 {
			continue // stale entry
		}
		expanded++
		if expanded > maxExpansions {
			return Route{}, ErrTooFar
		}
		head := g.Off[g.To[cur.node]]
		headEnd := g.Off[g.To[cur.node]+1]
		for e := head; e < headEnd; e++ {
			ee := uint32(e)
			if restrict && g.Forbidden(cur.node, ee) {
				continue
			}
			ng := gCur + float32(g.Time[ee])
			if old, _, seen := w.get(ee); seen && ng >= old {
				continue
			}
			if !w.set(ee, ng, cur.node) {
				return Route{}, ErrTooFar
			}
			heapPush(&w.heap, item{ee, ng + r.heurEdge(ee, dst)})
		}
	}
	return Route{}, ErrNoRoute
}

// heurEdge estimates the remaining cost from the head of edge e to b.
func (r *Router) heurEdge(e, b uint32) float32 {
	return r.heur(r.g.To[e], b)
}

func (r *Router) heur(a, b uint32) float32 {
	lat1, lon1 := r.g.Coords(a)
	lat2, lon2 := r.g.Coords(b)
	d := Haversine(lat1, lon1, lat2, lon2)
	return r.eps * float32(d/maxSpeedMps*10) // deciseconds
}

// reconstruct walks the came-from chain of edges and turns it into a node
// sequence src..dst plus aggregated length and time.
func (r *Router) reconstruct(w *workspace, src, goal uint32) (Route, error) {
	var edges []uint32
	e := goal
	for {
		edges = append(edges, e)
		_, came, ok := w.get(e)
		if !ok || came == invalidEdge {
			break
		}
		e = came
		if len(edges) > 2_000_000 {
			return Route{}, ErrNoRoute
		}
	}
	nodes := make([]uint32, 0, len(edges)+1)
	nodes = append(nodes, src)
	var dist, tm uint32
	for i := len(edges) - 1; i >= 0; i-- {
		x := edges[i]
		nodes = append(nodes, r.g.To[x])
		dist += r.g.Len[x]
		tm += r.g.Time[x]
	}
	return Route{Nodes: nodes, DistanceM: dist, TimeDs: tm}, nil
}

// Haversine returns the great circle distance in meters.
func Haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const earthR = 6371008.8
	rad := math.Pi / 180
	dLat := (lat2 - lat1) * rad
	dLon := (lon2 - lon1) * rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthR * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

func heapPush(h *itemHeap, it item) {
	*h = append(*h, it)
	i := len(*h) - 1
	for i > 0 {
		p := (i - 1) / 2
		if (*h)[p].f <= (*h)[i].f {
			break
		}
		(*h)[p], (*h)[i] = (*h)[i], (*h)[p]
		i = p
	}
}

func heapPop(h *itemHeap) item {
	old := *h
	n := len(old)
	top := old[0]
	old[0] = old[n-1]
	last := n - 1
	*h = old[:last]
	i := 0
	for {
		l, rr := 2*i+1, 2*i+2
		small := i
		if l < last && (*h)[l].f < (*h)[small].f {
			small = l
		}
		if rr < last && (*h)[rr].f < (*h)[small].f {
			small = rr
		}
		if small == i {
			break
		}
		(*h)[i], (*h)[small] = (*h)[small], (*h)[i]
		i = small
	}
	return top
}
