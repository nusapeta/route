package graph

import (
	"context"
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

// ErrCanceled is returned when the caller's context ends before the search does.
// The workspace is released immediately, so a timed-out query does not hold a
// concurrency slot for the rest of its run.
var ErrCanceled = errors.New("route canceled")

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
		r.pool <- newWorkspace(slots, g.EdgeCount)
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

// workspace is the per-query open-addressing table. Keys and generation stamps
// share one word (gen<<nodeBits | node+1) so a probe touches one array instead of
// two and the table costs 12 B/slot instead of 16 B/slot.
type workspace struct {
	keys     []uint32
	gs       []float32
	came     []uint32
	mask     uint32
	nodeMask uint32 // low bits holding node+1
	nodeBits uint
	genBits  uint
	maxGen   uint32
	used     int
	cur      uint32
	heap     itemHeap
}

// workspaceBits picks how many bits the search state needs, leaving the rest for
// the generation stamp. The state is an edge index, so edge count sets the budget.
func workspaceBits(entries uint32) (bits uint) {
	bits = 1
	for (uint32(1) << bits) <= entries {
		bits++
	}
	return bits
}

func newWorkspace(slots, entries uint32) *workspace {
	bits := workspaceBits(entries)
	if bits > 28 {
		// Keep at least 4 generation bits; beyond 2^28 edges the packed table
		// would wrap too often and is not worth the 25% saving.
		panic("graph: edge count exceeds the packed workspace limit")
	}
	genBits := 32 - bits
	return &workspace{
		keys:     make([]uint32, slots),
		gs:       make([]float32, slots),
		came:     make([]uint32, slots),
		mask:     slots - 1,
		nodeBits: bits,
		genBits:  genBits,
		nodeMask: (uint32(1) << bits) - 1,
		maxGen:   (uint32(1) << genBits) - 1,
		cur:      1,
	}
}

func (w *workspace) reset() {
	w.cur++
	if w.cur > w.maxGen {
		for i := range w.keys {
			w.keys[i] = 0
		}
		w.cur = 1
	}
	w.used = 0
	w.heap = w.heap[:0]
}

func (w *workspace) probe(n uint32) (uint32, bool) {
	i := (n * 2654435761) & w.mask
	want := n + 1
	for {
		k := w.keys[i]
		if k>>w.nodeBits != w.cur {
			return i, false
		}
		if k&w.nodeMask == want {
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
		if (w.used+1)*10 >= len(w.keys)*8 {
			if !w.grow() {
				return false
			}
			i, _ = w.probe(n)
		}
		w.used++
	}
	w.keys[i] = w.cur<<w.nodeBits | (n + 1)
	w.gs[i] = g
	w.came[i] = came
	return true
}

func (w *workspace) grow() bool {
	if uint32(len(w.keys)) >= maxSlots {
		return false
	}
	oldK, oldG, oldC := w.keys, w.gs, w.came
	n := len(oldK) * 2
	w.keys = make([]uint32, n)
	w.gs = make([]float32, n)
	w.came = make([]uint32, n)
	w.mask = uint32(n - 1)
	w.used = 0
	for i := range oldK {
		k := oldK[i]
		if k>>w.nodeBits != w.cur {
			continue
		}
		_ = w.set(k&w.nodeMask-1, oldG[i], oldC[i])
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
	return r.RouteCtx(context.Background(), src, dst)
}

// RouteCtx is Route with cancellation: the search stops when ctx ends and
// returns ErrCanceled (wrapped with the context error).
func (r *Router) RouteCtx(ctx context.Context, src, dst uint32) (Route, error) {
	return r.RouteStrictCtx(ctx, src, dst, r.strict)
}

// RouteStrict overrides the router's restriction policy for a single query.
func (r *Router) RouteStrict(src, dst uint32, strict bool) (Route, error) {
	return r.RouteStrictCtx(context.Background(), src, dst, strict)
}

// RouteStrictCtx is RouteStrict with cancellation.
func (r *Router) RouteStrictCtx(ctx context.Context, src, dst uint32, strict bool) (Route, error) {
	g := r.g
	if err := ctx.Err(); err != nil {
		return Route{}, ErrCanceled
	}
	if src >= g.NodeCount || dst >= g.NodeCount {
		return Route{}, ErrNoRoute
	}
	if src == dst {
		return Route{Nodes: []uint32{src}}, nil
	}
	res, err := r.search(ctx, src, dst, strict)
	if err != nil && err != ErrCanceled && ctx.Err() == nil && strict && (len(g.Ban) > 0 || len(g.Only) > 0) {
		// A restriction may have made the destination unreachable (bad OSM
		// data, or our conservative via-way handling). Retry unrestricted and
		// flag the answer so callers can count it.
		if res2, err2 := r.search(ctx, src, dst, false); err2 == nil {
			res2.Fallback = true
			return res2, nil
		}
	}
	return res, err
}

func (r *Router) search(ctx context.Context, src, dst uint32, restrict bool) (Route, error) {
	g := r.g
	w := <-r.pool
	defer func() { r.pool <- w }()
	w.reset()
	h := r.newHctx(dst)

	for e := g.Off[src]; e < g.Off[src+1]; e++ {
		ee := uint32(e)
		g0 := float32(g.Time[ee])
		if !w.set(ee, g0, invalidEdge) {
			return Route{}, ErrTooFar
		}
		heapPush(&w.heap, item{ee, g0 + h.estEdge(g, ee)})
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
		if cur.f > gCur+h.estEdge(g, cur.node)+0.5 {
			continue // stale entry
		}
		expanded++
		if expanded > maxExpansions {
			return Route{}, ErrTooFar
		}
		// Checking a context costs a load and a compare; once per 256 pops it is
		// noise next to an expansion, and a canceled request frees its workspace
		// within a few milliseconds instead of running to the expansion cap.
		if expanded&255 == 0 {
			if err := ctx.Err(); err != nil {
				return Route{}, ErrCanceled
			}
		}
		head := g.Off[g.To[cur.node]]
		headEnd := g.Off[g.To[cur.node]+1]
		var span RestrictionSpan
		if restrict {
			span = g.Span(cur.node)
		}
		for e := head; e < headEnd; e++ {
			ee := uint32(e)
			if restrict && !span.Empty() && span.Forbidden(ee) {
				continue
			}
			ng := gCur + float32(g.Time[ee])
			if old, _, seen := w.get(ee); seen && ng >= old {
				continue
			}
			if !w.set(ee, ng, cur.node) {
				return Route{}, ErrTooFar
			}
			heapPush(&w.heap, item{ee, ng + h.estEdge(g, ee)})
		}
	}
	return Route{}, ErrNoRoute
}

// hctx holds the parts of the heuristic that do not change during a query, so
// the hot loop costs one table lookup and one square root instead of three
// trigonometry calls per relaxation.
type hctx struct {
	lat, lon float64 // destination, degrees
	scale    float32 // eps * 10 / maxSpeedMps, i.e. metres to weighted deciseconds
}

// Per-degree scale factors: the smallest value each direction can take, so the
// planar estimate never exceeds the true great-circle distance.
const (
	metersPerDegLat = 110540.0
	metersPerDegLon = 111320.0
	cosStep         = 20 // cosine table resolution: 1/20 degree
)

// cosTab[i] = cos(i/cosStep degrees). Rows are rounded down (see cosFloor).
var cosTab = func() []float64 {
	t := make([]float64, 90*cosStep+3)
	for i := range t {
		t[i] = math.Cos(float64(i) / cosStep * math.Pi / 180)
	}
	return t
}()

// cosFloor returns a cosine no larger than the true one at either latitude, so
// the east-west term is always an underestimate.
func cosFloor(a, b float64) float64 {
	m := math.Abs(a)
	if x := math.Abs(b); x > m {
		m = x
	}
	i := int(m*cosStep) + 1 // next bin up means higher latitude, smaller cosine
	if i >= len(cosTab) {
		i = len(cosTab) - 1
	}
	return cosTab[i] * (1 - 1e-9)
}

func (r *Router) newHctx(dst uint32) hctx {
	lat, lon := r.g.Coords(dst)
	return hctx{lat: lat, lon: lon, scale: r.eps * 10 / float32(maxSpeedMps)}
}

// heuristicMargin keeps the flat-earth estimate below the great-circle distance.
// The flat-earth error is second order in the angular separation
// (~(dLambda*cos(lat))^2/24, i.e. 0.15% for 10 degrees, 1.4% for 30 degrees), so
// 1% covers every route this router is used for (< 2000 km) and the estimate stays
// a lower bound on the true travel time.
const heuristicMargin = 0.99

// est estimates the remaining cost from a coordinate to the destination in
// weighted deciseconds.
func (h *hctx) est(lat, lon float64) float32 {
	dlat := (lat - h.lat) * metersPerDegLat
	dlon := (lon - h.lon) * metersPerDegLon * cosFloor(lat, h.lat)
	return h.scale * heuristicMargin * float32(math.Sqrt(dlat*dlat+dlon*dlon))
}

// estEdge estimates the remaining cost from the head of edge e.
func (h *hctx) estEdge(g *Graph, e uint32) float32 {
	lat, lon := g.Coords(g.To[e])
	return h.est(lat, lon)
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

// heapArity is the branching factor of the frontier heap. Four children give a
// shallower tree than binary and touch fewer cache lines per sift.
const heapArity = 4

func heapPush(h *itemHeap, it item) {
	*h = append(*h, it)
	i := len(*h) - 1
	for i > 0 {
		p := (i - 1) / heapArity
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
		first := heapArity*i + 1
		if first >= last {
			break
		}
		small := i
		end := first + heapArity
		if end > last {
			end = last
		}
		for c := first; c < end; c++ {
			if (*h)[c].f < (*h)[small].f {
				small = c
			}
		}
		if small == i {
			break
		}
		(*h)[i], (*h)[small] = (*h)[small], (*h)[i]
		i = small
	}
	return top
}
