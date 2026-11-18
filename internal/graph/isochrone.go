package graph

import (
	"context"
	"errors"
	"math"
)

var (
	ErrIsoTooLarge = errors.New("isochrone too large")
	ErrIsoNoArea   = errors.New("isochrone has no area")
	ErrIsoNoSnap   = errors.New("isochrone start does not snap")
)

const (
	DefaultIsoGridM      = 250
	DefaultIsoSimplifyM  = 25
	DefaultIsoMaxCells   = 40_000
	DefaultIsoMaxVisited = 1_500_000
	DefaultIsoSnapM      = 3000
	MaxIsoMeters         = 100_000
	isoMinRingAreaM2     = 2000
	isoMinRingVertices   = 8
)

// IsoOptions bounds one reachability search and its rasterisation. Zero fields
// take the contract defaults.
type IsoOptions struct {
	Minutes    float64
	Meters     float64
	GridM      float64
	SimplifyM  float64
	MaxCells   int
	MaxVisited int
}

// IsoResult is the reachable area. Rings are closed [lat, lon] loops.
type IsoResult struct {
	Rings        [][2]float64 // flattened rings, each closed
	RingLengths  []int
	GridM        float64
	NodesVisited int
	AreaM2       float64
}

type isoXY struct{ x, y float64 }

// Isochrone floods the graph from a coordinate up to a travel-time cutoff,
// rasterises the visited nodes, and traces the outline of the reached cells.
func Isochrone(ctx context.Context, r *Router, lat, lon float64, opt IsoOptions) (IsoResult, error) {
	var out IsoResult
	if opt.Minutes < 1 || opt.Minutes > 60 {
		return out, errors.New("isochrone minutes must be 1..60")
	}
	meters := opt.Meters
	if meters <= 0 {
		meters = MaxIsoMeters
	}
	if meters > MaxIsoMeters {
		return out, errors.New("isochrone meters exceeds limit")
	}
	gridM := opt.GridM
	if gridM <= 0 {
		gridM = DefaultIsoGridM
	}
	maxCells := opt.MaxCells
	if maxCells <= 0 {
		maxCells = DefaultIsoMaxCells
	}
	maxVisited := opt.MaxVisited
	if maxVisited <= 0 {
		maxVisited = DefaultIsoMaxVisited
	}
	simplifyM := opt.SimplifyM
	if simplifyM < 0 {
		simplifyM = 0
	}

	snap, ok := r.SnapEdge(lat, lon, DefaultIsoSnapM)
	if !ok {
		return out, ErrIsoNoSnap
	}
	cutoffDs := float32(opt.Minutes * 600)
	// The fastest edge in the graph times the budget is an upper bound on how
	// far a reachable node can be. Cap it with the caller's distance limit.
	extent := math.Min(meters, r.g.MaxSpeedMps()*opt.Minutes*60) + snap.Dist + gridM
	gridM = isoGridM(extent, maxCells)
	n := int(math.Ceil(2*extent/gridM)) + 2
	if n < 3 {
		n = 3
	}
	cell := make([]float32, n*n)
	for i := range cell {
		cell[i] = float32(math.Inf(1))
	}
	kx := metersPerDegLon * math.Cos(lat*math.Pi/180)
	ky := metersPerDegLat
	mark := func(node uint32, cost float32) {
		la, lo := r.g.Coords(node)
		i := int(math.Floor((lo-lon)*kx/gridM)) + n/2
		j := int(math.Floor((la-lat)*ky/gridM)) + n/2
		if i < 0 || i >= n || j < 0 || j >= n {
			return
		}
		k := j*n + i
		if cost < cell[k] {
			cell[k] = cost
		}
	}
	visited, truncated, err := isoFlood(ctx, r, snap.Node, cutoffDs, maxVisited, mark)
	if err != nil {
		return out, err
	}
	if truncated {
		return out, ErrIsoTooLarge
	}

	mask := make([]bool, n*n)
	for i, c := range cell {
		mask[i] = c <= cutoffDs
	}
	loops := isoRings(mask, n)
	type ranked struct {
		pts  []isoXY
		area float64
	}
	rankedRings := make([]ranked, 0, len(loops))
	for _, loop := range loops {
		area := isoArea(loop) * gridM * gridM
		if area < isoMinRingAreaM2 {
			continue
		}
		simple := isoSimplifyRing(loop, simplifyM/gridM, isoMinRingVertices)
		if len(simple) < isoMinRingVertices {
			continue
		}
		rankedRings = append(rankedRings, ranked{simple, area})
	}
	if len(rankedRings) == 0 {
		return out, ErrIsoNoArea
	}
	for i := 1; i < len(rankedRings); i++ {
		for j := i; j > 0 && rankedRings[j].area > rankedRings[j-1].area; j-- {
			rankedRings[j], rankedRings[j-1] = rankedRings[j-1], rankedRings[j]
		}
	}
	out.GridM = gridM
	out.NodesVisited = visited
	for _, rr := range rankedRings {
		out.AreaM2 += rr.area
		out.RingLengths = append(out.RingLengths, len(rr.pts))
		for _, p := range rr.pts {
			x := (p.x - float64(n)/2) * gridM
			y := (p.y - float64(n)/2) * gridM
			out.Rings = append(out.Rings, [2]float64{lat + y/ky, lon + x/kx})
		}
	}
	return out, nil
}

// isoFlood is the DijkstraOneToMany(src, nil, cutoff) expansion NUSA-8 needs:
// no target set, a hard cost cap, and a bound on visited states. It keys the
// existing WorkspacePool by arrival edge, the same state A* uses, so it needs
// no dense dist[] and returns the workspace before the handler finishes.
func isoFlood(ctx context.Context, r *Router, src uint32, cutoff float32, maxVisited int, mark func(uint32, float32)) (int, bool, error) {
	g := r.g
	w, err := r.wp.get(g)
	if err != nil {
		return 0, false, err
	}
	defer r.wp.put(w)

	mark(src, 0)
	for e := g.Off[src]; e < g.Off[src+1]; e++ {
		if !r.usable(e) {
			continue
		}
		c := r.edgeTimeDs(e)
		if c > cutoff {
			continue
		}
		if !w.set(e, c, invalidEdge) {
			return 0, true, nil
		}
		heapPush(&w.heap, item{e, c})
	}
	visited := 0
	for len(w.heap) > 0 {
		cur := heapPop(&w.heap)
		gCur, _, ok := w.get(cur.node)
		if !ok || cur.f > gCur+0.5 {
			continue
		}
		visited++
		if visited > maxVisited {
			return visited, true, nil
		}
		if visited&255 == 0 && ctx.Err() != nil {
			return visited, false, ctx.Err()
		}
		v := g.To[cur.node]
		mark(v, gCur)
		for e := g.Off[v]; e < g.Off[v+1]; e++ {
			if !r.usable(e) {
				continue
			}
			ng := gCur + r.edgeTimeDs(e)
			if ng > cutoff {
				continue
			}
			if old, _, seen := w.get(e); seen && ng >= old {
				continue
			}
			if !w.set(e, ng, cur.node) {
				return visited, true, nil
			}
			heapPush(&w.heap, item{e, ng})
		}
	}
	return visited, false, nil
}

// isoGridM doubles the raster step until the square grid fits the cell budget.
func isoGridM(extent float64, maxCells int) float64 {
	g := float64(DefaultIsoGridM)
	for {
		n := int(math.Ceil(2*extent/g)) + 2
		if n > 0 && float64(n)*float64(n) <= float64(maxCells) {
			return g
		}
		g *= 2
	}
}

// isoRings traces the boundary of a cell-centred binary mask by cancelling
// edges shared by two reached cells. The surviving directed edges form closed
// loops, the binary-grid case of marching squares. Points are grid indexes.
func isoRings(mask []bool, n int) [][]isoXY {
	type seg struct{ a, b uint64 }
	key := func(x, y int) uint64 { return uint64(uint32(x))<<32 | uint64(uint32(y)) }
	edges := make([]seg, 0, n*2)
	at := make(map[uint64][]int)
	add := func(x0, y0, x1, y1 int) {
		a, b := key(x0, y0), key(x1, y1)
		at[a] = append(at[a], len(edges))
		edges = append(edges, seg{a, b})
	}
	for j := range n {
		for i := range n {
			if !mask[j*n+i] {
				continue
			}
			if i == 0 || !mask[j*n+i-1] {
				add(i, j+1, i, j)
			}
			if j == 0 || !mask[(j-1)*n+i] {
				add(i, j, i+1, j)
			}
			if i+1 == n || !mask[j*n+i+1] {
				add(i+1, j, i+1, j+1)
			}
			if j+1 == n || !mask[(j+1)*n+i] {
				add(i+1, j+1, i, j+1)
			}
		}
	}
	used := make([]bool, len(edges))
	var loops [][]isoXY
	for s := range edges {
		if used[s] {
			continue
		}
		start, cur := edges[s].a, edges[s].b
		used[s] = true
		pts := []uint64{start, cur}
		for cur != start && len(pts) <= len(edges)+1 {
			next := -1
			for _, k := range at[cur] {
				if !used[k] {
					next = k
					break
				}
			}
			if next < 0 {
				break
			}
			used[next] = true
			cur = edges[next].b
			pts = append(pts, cur)
		}
		if cur != start || len(pts) < 5 {
			continue
		}
		loop := make([]isoXY, 0, len(pts))
		for _, p := range pts {
			loop = append(loop, isoXY{float64(uint32(p >> 32)), float64(uint32(p))})
		}
		loops = append(loops, loop)
	}
	return loops
}

func isoArea(p []isoXY) float64 {
	var s float64
	for i := range len(p) - 1 {
		s += p[i].x*p[i+1].y - p[i+1].x*p[i].y
	}
	return math.Abs(s) / 2
}

// isoSimplifyRing splits the closed loop at its farthest point so Douglas-
// Peucker keeps two real endpoints instead of collapsing onto the duplicate
// closure vertex.
func isoSimplifyRing(ring []isoXY, tol float64, minVerts int) []isoXY {
	split := 1
	best := -1.0
	for i := 1; i < len(ring)-1; i++ {
		d := math.Hypot(ring[i].x-ring[0].x, ring[i].y-ring[0].y)
		if d > best {
			best, split = d, i
		}
	}
	for _, e := range []float64{tol, tol / 2, tol / 4, 0} {
		a := isoSimplify(ring[:split+1], e)
		b := isoSimplify(ring[split:], e)
		out := append(a, b[1:]...)
		if len(out) >= minVerts {
			return out
		}
	}
	return isoResampleRing(ring, minVerts)
}

// isoResampleRing keeps the shape but raises the vertex count when Douglas-
// Peucker reduced a short loop below the contract minimum.
func isoResampleRing(ring []isoXY, minVerts int) []isoXY {
	if len(ring) < 2 {
		return ring
	}
	cum := make([]float64, len(ring))
	for i := range len(ring) - 1 {
		cum[i+1] = cum[i] + math.Hypot(ring[i+1].x-ring[i].x, ring[i+1].y-ring[i].y)
	}
	perim := cum[len(cum)-1]
	if perim == 0 {
		out := make([]isoXY, 0, minVerts)
		for range minVerts - 1 {
			out = append(out, ring[0])
		}
		return append(out, ring[0])
	}
	out := make([]isoXY, 0, minVerts)
	seg := 0
	for k := range minVerts - 1 {
		target := perim * float64(k) / float64(minVerts-1)
		for seg < len(cum)-2 && cum[seg+1] < target {
			seg++
		}
		span := cum[seg+1] - cum[seg]
		t := 0.0
		if span > 0 {
			t = (target - cum[seg]) / span
		}
		a, b := ring[seg], ring[seg+1]
		out = append(out, isoXY{a.x + (b.x-a.x)*t, a.y + (b.y-a.y)*t})
	}
	return append(out, out[0])
}

func isoSimplify(pts []isoXY, tol float64) []isoXY {
	out := append([]isoXY(nil), pts...)
	if len(out) <= 2 || tol <= 0 {
		return out
	}
	keep := make([]bool, len(out))
	keep[0], keep[len(out)-1] = true, true
	stack := [][2]int{{0, len(out) - 1}}
	for len(stack) > 0 {
		seg := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		lo, hi := seg[0], seg[1]
		if hi <= lo+1 {
			continue
		}
		ax, ay := out[lo].x, out[lo].y
		bx, by := out[hi].x, out[hi].y
		dx, dy := bx-ax, by-ay
		den := math.Hypot(dx, dy)
		far, idx := -1.0, -1
		for k := lo + 1; k < hi; k++ {
			var d float64
			if den == 0 {
				d = math.Hypot(out[k].x-ax, out[k].y-ay)
			} else {
				d = math.Abs(dy*out[k].x-dx*out[k].y+bx*ay-by*ax) / den
			}
			if d > far {
				far, idx = d, k
			}
		}
		if far > tol {
			keep[idx] = true
			stack = append(stack, [2]int{lo, idx}, [2]int{idx, hi})
		}
	}
	res := make([]isoXY, 0, len(out))
	for i, k := range keep {
		if k {
			res = append(res, out[i])
		}
	}
	return res
}
