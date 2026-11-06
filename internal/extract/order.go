package extract

import (
	"math"
	"sort"
)

type keyed struct {
	k uint64
	i uint32
}

// spatialOrder returns a permutation that sorts nodes along a Hilbert curve,
// so that geographically close nodes end up close in the file. This is what
// keeps A* page-local (low RSS, better cache behaviour).
func spatialOrder(lat, lon []int32, valid []bool) []uint32 {
	n := len(lat)
	if n == 0 {
		return nil
	}
	minLat, maxLat := int32(math.MaxInt32), int32(math.MinInt32)
	minLon, maxLon := int32(math.MaxInt32), int32(math.MinInt32)
	for i := range lat {
		if !valid[i] {
			continue
		}
		if lat[i] < minLat {
			minLat = lat[i]
		}
		if lat[i] > maxLat {
			maxLat = lat[i]
		}
		if lon[i] < minLon {
			minLon = lon[i]
		}
		if lon[i] > maxLon {
			maxLon = lon[i]
		}
	}
	dLat := float64(maxLat-minLat) + 1
	dLon := float64(maxLon-minLon) + 1
	const side = uint64(1 << 16)

	ks := make([]keyed, n)
	for i := 0; i < n; i++ {
		if !valid[i] {
			ks[i] = keyed{k: ^uint64(0), i: uint32(i)}
			continue
		}
		x := uint32(float64(lon[i]-minLon) / dLon * float64(side-1))
		y := uint32(float64(lat[i]-minLat) / dLat * float64(side-1))
		ks[i] = keyed{k: hilbert(side, x, y), i: uint32(i)}
	}
	sort.Slice(ks, func(a, b int) bool { return ks[a].k < ks[b].k })

	perm := make([]uint32, n)
	for newIdx, kv := range ks {
		perm[kv.i] = uint32(newIdx)
	}
	return perm
}

// hilbert returns the Hilbert curve index of (x, y) on a side x side grid.
func hilbert(side uint64, x, y uint32) uint64 {
	xx, yy := uint64(x), uint64(y)
	var d uint64
	for s := side / 2; s > 0; s /= 2 {
		var rx, ry uint64
		if xx&s > 0 {
			rx = 1
		}
		if yy&s > 0 {
			ry = 1
		}
		d += s * s * ((3 * rx) ^ ry)
		if ry == 0 {
			if rx == 1 {
				xx = side - 1 - xx
				yy = side - 1 - yy
			}
			xx, yy = yy, xx
		}
	}
	return d
}

func applyPerm(perm []uint32, lat, lon []int32) ([]int32, []int32) {
	nLat := make([]int32, len(lat))
	nLon := make([]int32, len(lon))
	for i := range lat {
		nLat[perm[i]] = lat[i]
		nLon[perm[i]] = lon[i]
	}
	return nLat, nLon
}
