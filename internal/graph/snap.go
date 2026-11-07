package graph

import "math"

// cellIndex maps a coordinate to a grid cell, clamped to the grid.
func (g *Graph) cellIndex(lat, lon float64) (int, int) {
	ri := int((lat - g.GridLat0) / g.GridStep)
	ci := int((lon - g.GridLon0) / g.GridStep)
	if ri < 0 {
		ri = 0
	}
	if ri >= g.GridNLat {
		ri = g.GridNLat - 1
	}
	if ci < 0 {
		ci = 0
	}
	if ci >= g.GridNLon {
		ci = g.GridNLon - 1
	}
	return ri, ci
}

// Nearest finds the closest graph node to a coordinate. The grid is expanded
// ring by ring and stops as soon as no closer node can exist.
func (g *Graph) Nearest(lat, lon float64, maxMeters float64) (uint32, float64, bool) {
	if g.GridNLat == 0 || g.GridNLon == 0 || g.GridStep <= 0 {
		return 0, 0, false
	}
	ri, ci := g.cellIndex(lat, lon)

	best := uint32(0)
	bestD := math.MaxFloat64
	cellM := g.GridStep * 111320.0
	maxRing := int(math.Ceil(maxMeters/cellM)) + 1
	if maxRing > 4000 {
		maxRing = 4000
	}

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
	kx := math.Cos(alat*math.Pi/180) * 111320
	ky := 110540.0
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
	if g.GridNLat == 0 || g.GridNLon == 0 || g.GridStep <= 0 {
		return Snap{}, false
	}
	const snapSlackM = 500.0
	ri, ci := g.cellIndex(lat, lon)
	cellM := g.GridStep * 111320.0
	maxRing := int(math.Ceil(maxMeters/cellM)) + 1
	if maxRing > 4000 {
		maxRing = 4000
	}

	best := Snap{Dist: math.MaxFloat64}
	for ring := 0; ring <= maxRing; ring++ {
		if ring > 0 && best.Dist < math.MaxFloat64 && float64(ring-1)*cellM > best.Dist+snapSlackM {
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
				g.snapCell(lat, lon, r, ci-ring, &best)
				g.snapCell(lat, lon, r, ci+ring, &best)
				continue
			}
			for c := c0; c <= c1; c++ {
				g.snapCell(lat, lon, r, c, &best)
			}
		}
	}
	if best.Dist > maxMeters {
		return Snap{}, false
	}
	return best, true
}

// snapCell evaluates every segment leaving the nodes of one grid cell.
func (g *Graph) snapCell(lat, lon float64, r, c int, best *Snap) {
	if c < 0 || c >= g.GridNLon {
		return
	}
	cell := uint64(r*g.GridNLon + c)
	if cell+1 >= uint64(len(g.GOff)) {
		return
	}
	for k := g.GOff[cell]; k < g.GOff[cell+1]; k++ {
		u := g.GNodes[k]
		ulat, ulon := g.Coords(u)
		for e := g.Off[u]; e < g.Off[u+1]; e++ {
			v := g.To[e]
			vlat, vlon := g.Coords(v)
			d, t, plat, plon := projectOnSegment(lat, lon, ulat, ulon, vlat, vlon)
			if d >= best.Dist {
				continue
			}
			s := Snap{Edge: uint32(e), From: u, To: v, T: t, Lat: plat, Lon: plon, Dist: d}
			if t <= 0.5 {
				s.Node, s.Along, s.Reverse = u, t*float64(g.EdgeLenM(e)), false
			} else {
				s.Node, s.Along, s.Reverse = v, (1-t)*float64(g.EdgeLenM(e)), true
			}
			*best = s
		}
	}
}
