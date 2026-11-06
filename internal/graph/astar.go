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
	g    *Graph
	pool chan *workspace
	eps  float32 // heuristic weight: 1.0 = optimal, 1.25 = ~4x faster
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
	r := &Router{g: g, pool: make(chan *workspace, concurrency), eps: 1.5}
	for i := 0; i < concurrency; i++ {
		r.pool <- newWorkspace(slots)
	}
	return r
}

// Epsilon reports the heuristic weight.
func (r *Router) Epsilon() float32 { return r.eps }

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

// Route finds the fastest path between two node indexes.
func (r *Router) Route(src, dst uint32) (Route, error) {
	g := r.g
	if src >= g.NodeCount || dst >= g.NodeCount {
		return Route{}, ErrNoRoute
	}
	if src == dst {
		return Route{Nodes: []uint32{src}}, nil
	}
	w := <-r.pool
	defer func() { r.pool <- w }()
	w.reset()

	w.set(src, 0, src)
	w.heap = append(w.heap, item{src, r.heur(src, dst)})

	var expanded int
	for len(w.heap) > 0 {
		cur := heapPop(&w.heap)
		gCur, _, ok := w.get(cur.node)
		if !ok {
			continue
		}
		if cur.node == dst {
			res, err := r.reconstruct(w, src, dst)
			res.Expansions = expanded
			return res, err
		}
		if cur.f > gCur+r.heur(cur.node, dst)+0.5 {
			continue // stale
		}
		expanded++
		if expanded > maxExpansions {
			return Route{}, ErrTooFar
		}
		for e := g.Off[cur.node]; e < g.Off[cur.node+1]; e++ {
			to := g.To[e]
			ng := gCur + float32(g.Time[e])
			if old, _, seen := w.get(to); !seen || ng < old {
				if !w.set(to, ng, cur.node) {
					return Route{}, ErrTooFar
				}
				heapPush(&w.heap, item{to, ng + r.heur(to, dst)})
			}
		}
	}
	return Route{}, ErrNoRoute
}

func (r *Router) heur(a, b uint32) float32 {
	lat1, lon1 := r.g.Coords(a)
	lat2, lon2 := r.g.Coords(b)
	d := Haversine(lat1, lon1, lat2, lon2)
	return r.eps * float32(d/maxSpeedMps*10) // deciseconds
}

func (r *Router) reconstruct(w *workspace, src, dst uint32) (Route, error) {
	var rev []uint32
	n := dst
	for {
		rev = append(rev, n)
		if n == src {
			break
		}
		_, came, ok := w.get(n)
		if !ok || came == n {
			break
		}
		n = came
		if len(rev) > 2_000_000 {
			return Route{}, ErrNoRoute
		}
	}
	nodes := make([]uint32, len(rev))
	for i := range rev {
		nodes[len(rev)-1-i] = rev[i]
	}
	var dist, tm uint32
	for i := 0; i+1 < len(nodes); i++ {
		for e := r.g.Off[nodes[i]]; e < r.g.Off[nodes[i]+1]; e++ {
			if r.g.To[e] == nodes[i+1] {
				dist += r.g.Len[e]
				tm += r.g.Time[e]
				break
			}
		}
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
