package graph

import "math"

// cellIndex maps a coordinate to a grid cell, clamped to the grid.
func (g *Graph) cellIndex(lat, lon float64) (int, int) {
	ri := clampCell(int((lat-g.GridLat0)/g.GridStep), g.GridNLat)
	ci := clampCell(int((lon-g.GridLon0)/g.GridStep), g.GridNLon)
	return ri, ci
}

// maxSnapRing caps the ring scan so a huge maxMeters on a coarse grid cannot
// turn one query into a full grid walk.
const maxSnapRing = 4000

// gridRing returns the cell containing (lat, lon) and the ring budget for a
// maxMeters scan. ok is false when the graph carries no usable grid.
func (g *Graph) gridRing(lat, lon, maxMeters float64) (ri, ci, maxRing int, cellM float64, ok bool) {
	if g.GridNLat == 0 || g.GridNLon == 0 || g.GridStep <= 0 {
		return 0, 0, 0, 0, false
	}
	ri, ci = g.cellIndex(lat, lon)
	cellM = g.GridStep * metersPerDegLon
	maxRing = int(math.Ceil(maxMeters/cellM)) + 1
	if maxRing > maxSnapRing {
		maxRing = maxSnapRing
	}
	return ri, ci, maxRing, cellM, true
}

// Nearest finds the closest graph node to a coordinate. The grid is expanded
// ring by ring and stops as soon as no closer node can exist.
func (g *Graph) Nearest(lat, lon float64, maxMeters float64) (uint32, float64, bool) {
	ri, ci, maxRing, cellM, ok := g.gridRing(lat, lon, maxMeters)
	if !ok {
		return 0, 0, false
	}

	best := uint32(0)
	bestD := math.MaxFloat64

	for ring := 0; ring <= maxRing; ring++ {
		if ring > 0 && bestD <= float64(ring-1)*cellM {
			break
		}
		for r := ri - ring; r <= ri+ring; r++ {
			if r < 0 || r >= g.GridNLat {
				continue
			}
			if ring == 0 || r == ri-ring || r == ri+ring {
				for c := ci - ring; c <= ci+ring; c++ {
					g.scanCell(lat, lon, r, c, &best, &bestD)
				}
				continue
			}
			g.scanCell(lat, lon, r, ci-ring, &best, &bestD)
			g.scanCell(lat, lon, r, ci+ring, &best, &bestD)
		}
		if bestD <= maxMeters && bestD <= float64(ring)*cellM {
			break
		}
	}
	if bestD == math.MaxFloat64 || bestD > maxMeters {
		return 0, 0, false
	}
	return best, bestD, true
}

func (g *Graph) scanCell(lat, lon float64, r, c int, best *uint32, bestD *float64) {
	if c < 0 || c >= g.GridNLon {
		return
	}
	cell := uint64(r*g.GridNLon + c)
	if cell+1 >= uint64(len(g.GOff)) {
		return
	}
	for k := g.GOff[cell]; k < g.GOff[cell+1]; k++ {
		n := g.GNodes[k]
		nlat, nlon := g.Coords(n)
		d := Haversine(lat, lon, nlat, nlon)
		if d < *bestD {
			*bestD = d
			*best = n
		}
	}
}

// Snap is the attachment of a query coordinate to a road segment.
//
// Node snapping (Nearest) attaches to the closest graph node, which is off by up
// to half a segment length. Edge snapping projects onto the segment instead, so
// the attached point lies on the road the user tapped.
type Snap struct {
	Edge uint32  // edge index (From -> To)
	From uint32  // edge tail node
	To   uint32  // edge head node
	T    float64 // position along the edge, 0 at From, 1 at To
	Lat  float64 // projected point (degrees)
	Lon  float64
	Dist float64 // metres between the query point and the projection
	// Node is the edge endpoint nearest to the projection: the node the router
	// starts from or ends at. Along is the distance in metres from Node to the
	// projection, Reverse says whether Node is the edge head (To).
	Node    uint32
	Along   float64
	Reverse bool
}

// projectOnSegment returns the distance in metres from (plat,plon) to the
// segment (alat,alon)-(blat,blon), the clamped parameter t, and the projected
// coordinate. Uses a local equirectangular frame, accurate to well under a metre
// at city scale.
func projectOnSegment(plat, plon, alat, alon, blat, blon float64) (float64, float64, float64, float64) {
	kx := math.Cos(alat*math.Pi/180) * metersPerDegLon
	ky := metersPerDegLat
	bx, by := (blon-alon)*kx, (blat-alat)*ky
	px, py := (plon-alon)*kx, (plat-alat)*ky
	den := bx*bx + by*by
	t := 0.0
	if den > 0 {
		t = (px*bx + py*by) / den
		if t < 0 {
			t = 0
		} else if t > 1 {
			t = 1
		}
	}
	cx, cy := t*bx, t*by
	d := math.Hypot(px-cx, py-cy)
	return d, t, alat + t*(blat-alat), alon + t*(blon-alon)
}

// SnapEdge attaches a coordinate to the closest road segment within maxMeters.
// The grid is expanded ring by ring; it stops once no segment can beat the best
// projection, using a slack of snapSlackM for segments whose nodes are farther
// away than the projection itself.
func (g *Graph) SnapEdge(lat, lon, maxMeters float64) (Snap, bool) {
	found := g.SnapEdges(lat, lon, maxMeters, 1)
	if len(found) == 0 {
		return Snap{}, false
	}
	return found[0], true
}

// SnapEdges returns up to k segments within maxMeters, nearest first, one per
// node the router would start from. The extra candidates let a caller whose
// closest segment turns out to be a dead end retry from the next road over
// instead of answering "no route".
func (g *Graph) SnapEdges(lat, lon, maxMeters float64, k int) []Snap {
	const snapSlackM = 500.0
	if k < 1 {
		k = 1
	}
	ri, ci, maxRing, cellM, ok := g.gridRing(lat, lon, maxMeters)
	if !ok {
		return nil
	}

	best := make([]Snap, 0, k)
	for ring := 0; ring <= maxRing; ring++ {
		if ring > 0 && len(best) == k && float64(ring-1)*cellM > best[k-1].Dist+snapSlackM {
			break
		}
		for r := ri - ring; r <= ri+ring; r++ {
			if r < 0 || r >= g.GridNLat {
				continue
			}
			var c0, c1 int
			switch {
			case ring == 0 || r == ri-ring || r == ri+ring:
				c0, c1 = ci-ring, ci+ring
			default:
				// only the two edge columns of the ring
				g.snapCell(lat, lon, r, ci-ring, &best, k)
				g.snapCell(lat, lon, r, ci+ring, &best, k)
				continue
			}
			for c := c0; c <= c1; c++ {
				g.snapCell(lat, lon, r, c, &best, k)
			}
		}
	}
	n := 0
	for _, s := range best {
		if s.Dist > maxMeters {
			break
		}
		n++
	}
	return best[:n]
}

// snapCell evaluates every segment leaving the nodes of one grid cell and keeps
// the k nearest, ordered by distance.
func (g *Graph) snapCell(lat, lon float64, r, c int, best *[]Snap, k int) {
	if c < 0 || c >= g.GridNLon {
		return
	}
	cell := uint64(r*g.GridNLon + c)
	if cell+1 >= uint64(len(g.GOff)) {
		return
	}
	for i := g.GOff[cell]; i < g.GOff[cell+1]; i++ {
		u := g.GNodes[i]
		ulat, ulon := g.Coords(u)
		for e := g.Off[u]; e < g.Off[u+1]; e++ {
			v := g.To[e]
			vlat, vlon := g.Coords(v)
			d, t, plat, plon := projectOnSegment(lat, lon, ulat, ulon, vlat, vlon)
			if len(*best) == k && d >= (*best)[k-1].Dist {
				continue
			}
			s := Snap{Edge: uint32(e), From: u, To: v, T: t, Lat: plat, Lon: plon, Dist: d}
			if t <= 0.5 {
				s.Node, s.Along, s.Reverse = u, t*float64(g.EdgeLenM(e)), false
			} else {
				s.Node, s.Along, s.Reverse = v, (1-t)*float64(g.EdgeLenM(e)), true
			}
			*best = insertSnap(*best, s, k)
		}
	}
}

// insertSnap keeps best sorted by distance, at most k long, one entry per
// node. RouteSnap only uses the node, so two segments starting at the same node
// would repeat the same search; the closer projection is the one kept.
func insertSnap(best []Snap, s Snap, k int) []Snap {
	for j := range best {
		if best[j].Node != s.Node {
			continue
		}
		if best[j].Dist <= s.Dist {
			return best
		}
		best = append(best[:j], best[j+1:]...)
		break
	}
	i := 0
	for i < len(best) && best[i].Dist <= s.Dist {
		i++
	}
	if i >= k {
		return best
	}
	if len(best) < k {
		best = append(best, Snap{})
	}
	copy(best[i+1:], best[i:len(best)-1])
	best[i] = s
	return best
}
