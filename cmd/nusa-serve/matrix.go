package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nusapeta/route/internal/graph"
)

const (
	matrixMaxSources     = 5
	matrixMaxTargets     = 25
	matrixMaxCells       = 25
	matrixDefaultMinutes = 60
	matrixMaxMinutes     = 180
	matrixMaxStraightM   = 100_000
	matrixMaxExpansions  = 1_500_000
	matrixDefaultSlots   = 1 << 18
	matrixMaxSlots       = 1 << 21
)

const (
	reasonMatrixTooLarge = "matrix_too_large"
	reasonTooManyTargets = "too_many_targets"
	reasonCutoffTooLarge = "cutoff_too_large"
	cellOK               = "ok"
	cellSnapFailed       = "snap_failed"
	cellNoRoute          = "no_route"
)

type matrixParamError struct {
	reason string
	msg    string
}

func (e *matrixParamError) Error() string { return e.msg }

type matrixParams struct {
	sources [][2]float64
	targets [][2]float64
	minutes float64
}

func parseMatrixPoints(raw []string) ([][2]float64, error) {
	pts := make([][2]float64, 0, len(raw))
	for _, group := range raw {
		for _, item := range strings.Split(group, "|") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			lat, lon, err := parsePoint(item)
			if err != nil {
				return nil, fmt.Errorf("point %q must be \"lat,lng\"", item)
			}
			pts = append(pts, [2]float64{lat, lon})
		}
	}
	return pts, nil
}

func readMatrixParams(req *http.Request) (matrixParams, error) {
	var p matrixParams
	q := req.URL.Query()
	var err error
	if p.sources, err = parseMatrixPoints(q["sources"]); err != nil {
		return p, err
	}
	if p.targets, err = parseMatrixPoints(q["targets"]); err != nil {
		return p, err
	}
	if len(p.sources) == 0 || len(p.targets) == 0 {
		return p, fmt.Errorf("sources and targets are required")
	}
	if len(p.sources) > matrixMaxSources {
		return p, &matrixParamError{reasonMatrixTooLarge, fmt.Sprintf("sources: max %d", matrixMaxSources)}
	}
	if len(p.targets) > matrixMaxTargets {
		return p, &matrixParamError{reasonTooManyTargets, fmt.Sprintf("targets: max %d", matrixMaxTargets)}
	}
	if len(p.sources)*len(p.targets) > matrixMaxCells {
		return p, &matrixParamError{reasonMatrixTooLarge, fmt.Sprintf("sources x targets: max %d", matrixMaxCells)}
	}
	p.minutes = matrixDefaultMinutes
	if raw := q.Get("max_minutes"); raw != "" {
		p.minutes, err = strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(p.minutes) || p.minutes <= 0 {
			return p, fmt.Errorf("max_minutes must be a positive number")
		}
		if p.minutes > matrixMaxMinutes {
			return p, &matrixParamError{reasonCutoffTooLarge, fmt.Sprintf("max_minutes: max %d", matrixMaxMinutes)}
		}
	}
	return p, nil
}

func matrixETag(fp string, p matrixParams) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s", fp)
	for _, pt := range p.sources {
		fmt.Fprintf(h, "|s:%.6f,%.6f", pt[0], pt[1])
	}
	for _, pt := range p.targets {
		fmt.Fprintf(h, "|t:%.6f,%.6f", pt[0], pt[1])
	}
	fmt.Fprintf(h, "|m:%.3f", p.minutes)
	return `"` + fp[:8] + "-" + short(h, 16) + `"`
}

func matrixCacheControl(maxAge int) string {
	if maxAge > 0 {
		return fmt.Sprintf("public, max-age=%d", maxAge)
	}
	return "no-cache"
}

// The scratch pool follows the same budget discipline as graph.WorkspacePool:
// power-of-two slots, 12 bytes per slot, grown on demand and shrunk after use.
// Matrix searches cannot borrow workspace slots (get/put are package-private),
// so they keep their own pool with the same concurrency and byte budget.
type matrixScratch struct {
	keys     []uint32
	cost     []float32
	dist     []float32
	heap     []matrixItem
	nodeBits uint
	nodeMask uint32
	mask     uint32
	gen      uint32
	maxGen   uint32
	used     uint32
	initial  uint32
}

type matrixItem struct {
	node uint32
	cost float32
}

func matrixFloorPow2(v uint32) uint32 {
	if v < 1 {
		return 1
	}
	return uint32(1) << (bits.Len32(v) - 1)
}

func newMatrixScratch(slots uint32) *matrixScratch {
	return &matrixScratch{
		keys:    make([]uint32, slots),
		cost:    make([]float32, slots),
		dist:    make([]float32, slots),
		mask:    slots - 1,
		initial: slots,
	}
}

func (m *matrixScratch) configure(g *graph.Graph) {
	nodeBits := uint(1)
	for (uint32(1) << nodeBits) <= g.NodeCount+1 {
		nodeBits++
	}
	if nodeBits > 31 {
		nodeBits = 31
	}
	genBits := 32 - nodeBits
	maxGen := uint32(1)<<genBits - 1
	if m.nodeBits == nodeBits && m.gen > 0 && m.gen < maxGen {
		m.gen++
	} else {
		for i := range m.keys {
			m.keys[i] = 0
		}
		m.nodeBits = nodeBits
		m.nodeMask = uint32(1)<<nodeBits - 1
		m.maxGen = maxGen
		m.gen = 1
	}
	m.used = 0
	m.heap = m.heap[:0]
}

func (m *matrixScratch) probe(n uint32) (uint32, bool) {
	i := (n * 2654435761) & m.mask
	want := n + 1
	for {
		k := m.keys[i]
		if k>>m.nodeBits != m.gen {
			return i, false
		}
		if k&m.nodeMask == want {
			return i, true
		}
		i = (i + 1) & m.mask
	}
}

func (m *matrixScratch) get(n uint32) (float32, float32, bool) {
	i, ok := m.probe(n)
	if !ok {
		return 0, 0, false
	}
	return m.cost[i], m.dist[i], true
}

func (m *matrixScratch) set(n uint32, cost, dist float32, maxSlots uint32) bool {
	i, ok := m.probe(n)
	if !ok {
		if (m.used+1)*10 >= uint32(len(m.keys))*8 && !m.resize(uint32(len(m.keys))*2, maxSlots) {
			return false
		}
		i, _ = m.probe(n)
		m.used++
	}
	m.keys[i] = m.gen<<m.nodeBits | (n + 1)
	m.cost[i] = cost
	m.dist[i] = dist
	return true
}

func (m *matrixScratch) resize(want, maxSlots uint32) bool {
	if want < m.initial {
		want = m.initial
	}
	if want > maxSlots {
		want = maxSlots
	}
	if want == uint32(len(m.keys)) {
		return false
	}
	oldKeys, oldCost, oldDist := m.keys, m.cost, m.dist
	m.keys = make([]uint32, want)
	m.cost = make([]float32, want)
	m.dist = make([]float32, want)
	m.mask = want - 1
	m.used = 0
	for i, k := range oldKeys {
		if k == 0 || k>>m.nodeBits != m.gen {
			continue
		}
		node := (k & m.nodeMask) - 1
		j, _ := m.probe(node)
		m.keys[j] = k
		m.cost[j] = oldCost[i]
		m.dist[j] = oldDist[i]
		m.used++
	}
	return true
}

func (m *matrixScratch) release() {
	if uint32(len(m.keys)) > m.initial*4 && m.used*4 < uint32(len(m.keys)) {
		m.resize(m.initial, m.initial)
	}
	if cap(m.heap) > 1<<16 {
		m.heap = nil
	}
}

func (m *matrixScratch) push(it matrixItem) {
	m.heap = append(m.heap, it)
	i := len(m.heap) - 1
	for i > 0 {
		p := (i - 1) / 4
		if m.heap[p].cost <= m.heap[i].cost {
			break
		}
		m.heap[p], m.heap[i] = m.heap[i], m.heap[p]
		i = p
	}
}

func (m *matrixScratch) pop() matrixItem {
	old := m.heap
	n := len(old)
	top := old[0]
	old[0] = old[n-1]
	m.heap = old[:n-1]
	i := 0
	for {
		first := 4*i + 1
		if first >= n-1 {
			break
		}
		small := i
		end := first + 4
		if end > n-1 {
			end = n - 1
		}
		for c := first; c < end; c++ {
			if m.heap[c].cost < m.heap[small].cost {
				small = c
			}
		}
		if small == i {
			break
		}
		m.heap[i], m.heap[small] = m.heap[small], m.heap[i]
		i = small
	}
	return top
}

type matrixPool struct {
	sp       sync.Pool
	maxSlots uint32
}

func newMatrixPool(concurrency, workspaceMB int) *matrixPool {
	slots := uint32(matrixDefaultSlots)
	if workspaceMB > 0 && concurrency > 0 {
		if per := uint32((workspaceMB * 1024 * 1024) / concurrency / 12); per > slots {
			slots = per
		}
	}
	if slots > matrixMaxSlots {
		slots = matrixMaxSlots
	}
	slots = matrixFloorPow2(slots)
	p := &matrixPool{maxSlots: slots}
	p.sp.New = func() any { return newMatrixScratch(slots) }
	return p
}

func (p *matrixPool) get(g *graph.Graph) *matrixScratch {
	s := p.sp.Get().(*matrixScratch)
	s.configure(g)
	return s
}

type matrixTarget struct {
	node  uint32
	along float64
}

type matrixHit struct {
	cost float32
	dist float32
	ok   bool
}

func matrixUsable(g *graph.Graph, e uint32, motor bool) bool {
	if motor {
		return g.MCls[e] != 0
	}
	return g.Cls[e] != 0
}

func matrixEdgeTime(g *graph.Graph, e uint32, motor bool) float32 {
	if motor {
		return g.MotorTimeDs(e)
	}
	return g.EdgeTimeDs(e)
}

// matrixOneToMany settles the whole reachable graph once per source, then reads
// every target off that search. Node-based costs ignore turn state, matching
// the one-to-many search planned for internal/graph (NUSA-6).
func matrixOneToMany(ctx context.Context, g *graph.Graph, motor bool, src graph.Snap, targets []matrixTarget, cutoff float32, pool *matrixPool) ([]matrixHit, int, bool, error) {
	hits := make([]matrixHit, len(targets))
	pending := make(map[uint32][]int, len(targets))
	remaining := 0
	for i, t := range targets {
		pending[t.node] = append(pending[t.node], i)
		remaining++
	}
	s := pool.get(g)
	defer func() { s.release(); pool.sp.Put(s) }()

	if !s.set(src.Node, 0, 0, pool.maxSlots) {
		return hits, 0, true, nil
	}
	s.push(matrixItem{node: src.Node, cost: 0})
	for e := g.Off[src.Node]; e < g.Off[src.Node+1]; e++ {
		if !matrixUsable(g, e, motor) {
			continue
		}
		c := matrixEdgeTime(g, e, motor)
		if c > cutoff {
			continue
		}
		to := g.To[e]
		if old, _, seen := s.get(to); seen && old <= c {
			continue
		}
		if !s.set(to, c, float32(g.EdgeLenM(e)), pool.maxSlots) {
			return hits, 0, true, nil
		}
		s.push(matrixItem{node: to, cost: c})
	}

	var expanded int
	for len(s.heap) > 0 && remaining > 0 {
		it := s.pop()
		cost, dist, ok := s.get(it.node)
		if !ok || it.cost > cost {
			continue
		}
		expanded++
		if expanded > matrixMaxExpansions {
			return hits, expanded, true, nil
		}
		if expanded&255 == 0 {
			if err := ctx.Err(); err != nil {
				return hits, expanded, true, err
			}
		}
		if idxs := pending[it.node]; len(idxs) > 0 {
			for _, i := range idxs {
				hits[i] = matrixHit{cost: cost, dist: dist, ok: true}
				remaining--
			}
			delete(pending, it.node)
			if remaining == 0 {
				break
			}
		}
		if cost >= cutoff {
			break
		}
		for e := g.Off[it.node]; e < g.Off[it.node+1]; e++ {
			if !matrixUsable(g, e, motor) {
				continue
			}
			nc := cost + matrixEdgeTime(g, e, motor)
			if nc > cutoff {
				continue
			}
			to := g.To[e]
			if old, _, seen := s.get(to); seen && old <= nc {
				continue
			}
			nd := dist + float32(g.EdgeLenM(e))
			if !s.set(to, nc, nd, pool.maxSlots) {
				return hits, expanded, true, nil
			}
			s.push(matrixItem{node: to, cost: nc})
		}
	}
	return hits, expanded, false, ctx.Err()
}

type matrixCell struct {
	Status    string  `json:"status"`
	DistanceM float64 `json:"distance_m,omitempty"`
	DurationS float64 `json:"duration_s,omitempty"`
}

type matrixMeta struct {
	MaxMinutes float64 `json:"max_minutes"`
	Expansions int     `json:"expansions"`
	ElapsedMS  int64   `json:"elapsed_ms"`
	Truncated  bool    `json:"truncated,omitempty"`
}

type matrixResponse struct {
	Sources [][2]float64   `json:"sources"`
	Targets [][2]float64   `json:"targets"`
	Matrix  [][]matrixCell `json:"matrix"`
	Meta    matrixMeta     `json:"meta"`
}

type matrixEndpoint struct {
	pt   [2]float64
	rg   *region
	snap graph.Snap
	ok   bool
}

func snapMatrixPoints(s *server, pts [][2]float64) []matrixEndpoint {
	out := make([]matrixEndpoint, len(pts))
	for i, pt := range pts {
		out[i].pt = pt
		if !inIndonesia(pt[0], pt[1]) {
			continue
		}
		if rg, snap, ok := s.pickSnap(pt[0], pt[1]); ok {
			out[i].rg, out[i].snap, out[i].ok = rg, snap, true
		}
	}
	return out
}

func alongSeconds(g *graph.Graph, snap graph.Snap, motor bool) float64 {
	lenM := g.EdgeLenM(snap.Edge)
	if lenM == 0 {
		return 0
	}
	return snap.Along / float64(lenM) * float64(matrixEdgeTime(g, snap.Edge, motor))
}

func (s *server) handleMatrix(w http.ResponseWriter, req *http.Request) {
	started := time.Now()
	p, err := readMatrixParams(req)
	if err != nil {
		var pe *matrixParamError
		if errors.As(err, &pe) {
			httpErrReason(w, 400, pe.reason, pe.msg)
		} else {
			httpErr(w, 400, err.Error())
		}
		return
	}
	etag := matrixETag(s.fp, p)
	if etagMatches(req.Header.Get("If-None-Match"), etag) {
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", matrixCacheControl(s.maxAge))
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if !s.rl.allowRequest(w, req, s.m) {
		return
	}

	srcs := snapMatrixPoints(s, p.sources)
	tgts := snapMatrixPoints(s, p.targets)
	snapped := 0
	for _, e := range srcs {
		if e.ok {
			snapped++
		}
	}
	for _, e := range tgts {
		if e.ok {
			snapped++
		}
	}
	if snapped == 0 {
		httpErrReason(w, 422, reasonSnap, "points are not on a drivable road")
		return
	}

	ctx, cancel := context.WithTimeout(req.Context(), s.timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		httpErrReason(w, 504, reasonDeadline, "matrix search deadline exceeded")
		return
	}
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", matrixCacheControl(s.maxAge))
	cutoff := float32(p.minutes * 600)
	resp := matrixResponse{
		Sources: p.sources,
		Targets: p.targets,
		Matrix:  make([][]matrixCell, len(srcs)),
		Meta:    matrixMeta{MaxMinutes: p.minutes},
	}
	for i, src := range srcs {
		row := make([]matrixCell, len(tgts))
		if !src.ok {
			for j := range row {
				row[j].Status = cellSnapFailed
			}
			resp.Matrix[i] = row
			continue
		}
		type candidate struct{ target int }
		cands := make([]candidate, 0, len(tgts))
		for j, tgt := range tgts {
			switch {
			case !tgt.ok:
				row[j].Status = cellSnapFailed
			case graph.Haversine(src.pt[0], src.pt[1], tgt.pt[0], tgt.pt[1]) > matrixMaxStraightM:
				row[j].Status = reasonTooFar
				if s.m != nil {
					s.m.tooFar.Add(1)
				}
			case tgt.rg != src.rg:
				row[j].Status = cellNoRoute
			default:
				cands = append(cands, candidate{target: j})
			}
		}
		if len(cands) == 0 {
			resp.Matrix[i] = row
			continue
		}
		searchTargets := make([]matrixTarget, len(cands))
		for k, c := range cands {
			searchTargets[k] = matrixTarget{node: tgts[c.target].snap.Node, along: tgts[c.target].snap.Along}
		}
		hits, expansions, truncated, err := matrixOneToMany(ctx, src.rg.g, s.motor, src.snap, searchTargets, cutoff, s.matrixPool)
		resp.Meta.Expansions += expansions
		resp.Meta.Truncated = resp.Meta.Truncated || truncated
		if err != nil {
			httpErrReason(w, 504, reasonDeadline, "matrix search deadline exceeded")
			return
		}
		for k, hit := range hits {
			j := cands[k].target
			if !hit.ok {
				row[j].Status = cellNoRoute
				continue
			}
			secs := float64(hit.cost)/10 + alongSeconds(src.rg.g, src.snap, s.motor) + alongSeconds(tgts[j].rg.g, tgts[j].snap, s.motor)
			metres := float64(hit.dist) + src.snap.Along + tgts[j].snap.Along
			row[j] = matrixCell{
				Status:    cellOK,
				DistanceM: math.Round(metres),
				DurationS: math.Round(secs*10) / 10,
			}
		}
		resp.Matrix[i] = row
	}
	resp.Meta.ElapsedMS = time.Since(started).Milliseconds()
	w.Header().Set("X-Matrix-Ms", strconv.FormatInt(resp.Meta.ElapsedMS, 10))
	writeJSON(w, resp)
}
