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
