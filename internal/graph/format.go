// Package graph implements the on-disk road graph format and routing.
//
// File layout (little endian, mmap friendly):
//
//	header (256 bytes, fixed)
//	section 0  Lat      int32[nodeCount]
//	section 1  Lon      int32[nodeCount]
//	section 2  Off      uint64[nodeCount+1]   CSR: edge indexes of node
//	section 3  To       uint32[edgeCount]
//	section 4  Len      uint32[edgeCount]      meters
//	section 5  Time     uint32[edgeCount]      deciseconds
//	section 6  GO       uint32[edgeCount]      geometry offset (point index)
//	section 7  GL       uint16[edgeCount]      geometry point count (interior)
//	section 8  CLat     int32[coordCount]      interior shape points
//	section 9  CLon     int32[coordCount]
//	section 10 GOff     uint64[cellCount+1]    spatial grid over nodes
//	section 11 GNodes   uint32[nodeCount]
//	section 12 TurnOff  uint64[edgeCount+1]    banned transitions (CSR)
//	section 13 TurnTo   uint32[turnCount]
package graph

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"syscall"
	"unsafe"
)

const (
	// Version of the file format.
	Version = 1

	magicLE     = 0x47444952 // "RIDG"
	headerSize  = 256
	numSections = 14
)

const (
	sLat = iota
	sLon
	sOff
	sTo
	sLen
	sTime
	sGO
	sGL
	sCLat
	sCLon
	sGOff
	sGNodes
	sTurnOff
	sTurnTo
)

// E7 is the fixed point scale for coordinates (1e-7 degree, ~1.1cm).
const E7 = 1e7

// Graph is a read-only, memory mapped road graph.
type Graph struct {
	d []byte

	NodeCount  uint32
	EdgeCount  uint32
	CoordCount uint32
	GridCount  uint32
	GridNLat   int
	GridNLon   int
	GridLat0   float64
	GridLon0   float64
	GridStep   float64
	bbox       [4]float64 // minLat, minLon, maxLat, maxLon (zeros = scan)

	Lat, Lon []int32
	Off      []uint64
	To       []uint32
	Len      []uint32
	Time     []uint32
	GO       []uint32
	GL       []uint16
	CLat     []int32
	CLon     []int32
	GOff     []uint64
	GNodes   []uint32
	TurnOff  []uint64
	TurnTo   []uint32
}

// Open memory maps a graph file.
func Open(path string) (*Graph, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() < headerSize {
		return nil, fmt.Errorf("graph: file too small (%d bytes)", st.Size())
	}
	data, err := syscall.Mmap(int(f.Fd()), 0, int(st.Size()), syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("graph: mmap: %w", err)
	}

	g := &Graph{d: data}
	be := binary.LittleEndian
	if got := be.Uint32(data[0:]); got != magicLE {
		syscall.Munmap(data)
		return nil, errors.New("graph: bad magic (not a routeid graph)")
	}
	if v := be.Uint32(data[4:]); v != Version {
		syscall.Munmap(data)
		return nil, fmt.Errorf("graph: version %d, want %d", v, Version)
	}

	g.NodeCount = be.Uint32(data[8:])
	g.EdgeCount = be.Uint32(data[12:])
	g.CoordCount = be.Uint32(data[16:])
	g.GridCount = be.Uint32(data[20:])
	g.GridNLat = int(be.Uint32(data[24:]))
	g.GridNLon = int(be.Uint32(data[28:]))

	c := uint64(32)
	g.GridLat0 = math.Float64frombits(be.Uint64(data[c:]))
	c += 8
	g.GridLon0 = math.Float64frombits(be.Uint64(data[c:]))
	c += 8
	g.GridStep = math.Float64frombits(be.Uint64(data[c:]))
	c += 8

	var off [numSections]uint64
	for i := 0; i < numSections; i++ {
		off[i] = be.Uint64(data[c:])
		c += 8
	}
	for i := 0; i < 4; i++ {
		g.bbox[i] = math.Float64frombits(be.Uint64(data[c:]))
		c += 8
	}

	g.Lat = i32s(data, off[sLat], g.NodeCount)
	g.Lon = i32s(data, off[sLon], g.NodeCount)
	g.Off = u64s(data, off[sOff], g.NodeCount+1)
	g.To = u32s(data, off[sTo], g.EdgeCount)
	g.Len = u32s(data, off[sLen], g.EdgeCount)
	g.Time = u32s(data, off[sTime], g.EdgeCount)
	g.GO = u32s(data, off[sGO], g.EdgeCount)
	g.GL = u16s(data, off[sGL], g.EdgeCount)
	g.CLat = i32s(data, off[sCLat], g.CoordCount)
	g.CLon = i32s(data, off[sCLon], g.CoordCount)
	cells := uint32(0)
	if g.GridNLat > 0 && g.GridNLon > 0 {
		cells = uint32(g.GridNLat * g.GridNLon)
	}
	g.GOff = u64s(data, off[sGOff], cells+1)
	g.GNodes = u32s(data, off[sGNodes], g.GridCount)
	g.TurnOff = u64s(data, off[sTurnOff], 0)
	g.TurnTo = u32s(data, off[sTurnTo], 0)
	return g, nil
}

// Close releases the mapping.
func (g *Graph) Close() error {
	if g.d == nil {
		return nil
	}
	err := syscall.Munmap(g.d)
	g.d = nil
	return err
}

func i32s(d []byte, off uint64, n uint32) []int32 {
	if n == 0 {
		return nil
	}
	return unsafe.Slice((*int32)(unsafe.Pointer(&d[off])), n)
}

func u32s(d []byte, off uint64, n uint32) []uint32 {
	if n == 0 {
		return nil
	}
	return unsafe.Slice((*uint32)(unsafe.Pointer(&d[off])), n)
}

func u16s(d []byte, off uint64, n uint32) []uint16 {
	if n == 0 {
		return nil
	}
	return unsafe.Slice((*uint16)(unsafe.Pointer(&d[off])), n)
}

func u64s(d []byte, off uint64, n uint32) []uint64 {
	if n == 0 {
		return nil
	}
	return unsafe.Slice((*uint64)(unsafe.Pointer(&d[off])), n)
}

// Coords returns lat/lon of a node in degrees.
func (g *Graph) Coords(n uint32) (lat, lon float64) {
	return float64(g.Lat[n]) / E7, float64(g.Lon[n]) / E7
}

// Builder accumulates a graph in memory and serializes it.
type Builder struct {
	Lat, Lon      []int32
	Off           []uint64
	To, Len, Time []uint32
	GO            []uint32
	GL            []uint16
	CLat, CLon    []int32
	TurnOff       []uint64
	TurnTo        []uint32

	GridStep           float64
	GridLat0, GridLon0 float64
	GridNLat, GridNLon int
	GOff               []uint64
	GNodes             []uint32
	MinLat, MinLon     float64
	MaxLat, MaxLon     float64
}

// BuildGrid creates the uniform grid used for snapping.
func (b *Builder) BuildGrid(step float64) {
	if len(b.Lat) == 0 {
		return
	}
	minLat, maxLat := b.Lat[0], b.Lat[0]
	minLon, maxLon := b.Lon[0], b.Lon[0]
	for i := range b.Lat {
		if b.Lat[i] < minLat {
			minLat = b.Lat[i]
		}
		if b.Lat[i] > maxLat {
			maxLat = b.Lat[i]
		}
		if b.Lon[i] < minLon {
			minLon = b.Lon[i]
		}
		if b.Lon[i] > maxLon {
			maxLon = b.Lon[i]
		}
	}
	b.GridStep = step
	b.MinLat = float64(minLat) / E7
	b.MinLon = float64(minLon) / E7
	b.MaxLat = float64(maxLat) / E7
	b.MaxLon = float64(maxLon) / E7
	b.GridLat0 = float64(minLat)/E7 - step
	b.GridLon0 = float64(minLon)/E7 - step
	b.GridNLat = int((float64(maxLat)/E7-b.GridLat0)/step) + 1
	b.GridNLon = int((float64(maxLon)/E7-b.GridLon0)/step) + 1
	cells := b.GridNLat * b.GridNLon

	counts := make([]uint32, cells+1)
	cell := make([]uint32, len(b.Lat))
	for i := range b.Lat {
		c := b.cellOf(float64(b.Lat[i])/E7, float64(b.Lon[i])/E7)
		cell[i] = uint32(c)
		counts[c+1]++
	}
	for i := 1; i <= cells; i++ {
		counts[i] += counts[i-1]
	}
	b.GOff = make([]uint64, cells+1)
	for i := range b.GOff {
		b.GOff[i] = uint64(counts[i])
	}
	b.GNodes = make([]uint32, len(b.Lat))
	cur := make([]uint32, cells)
	copy(cur, counts[:cells])
	for i := range b.Lat {
		c := cell[i]
		b.GNodes[cur[c]] = uint32(i)
		cur[c]++
	}
}

func (b *Builder) cellOf(lat, lon float64) int {
	ri := int((lat - b.GridLat0) / b.GridStep)
	ci := int((lon - b.GridLon0) / b.GridStep)
	if ri < 0 {
		ri = 0
	}
	if ri >= b.GridNLat {
		ri = b.GridNLat - 1
	}
	if ci < 0 {
		ci = 0
	}
	if ci >= b.GridNLon {
		ci = b.GridNLon - 1
	}
	return ri*b.GridNLon + ci
}

func align8(v uint64) uint64 { return (v + 7) &^ 7 }

// Write serializes the graph to path.
func (b *Builder) Write(path string) error {
	cells := 0
	if b.GridNLat > 0 && b.GridNLon > 0 {
		cells = b.GridNLat * b.GridNLon
	}
	sizes := [numSections]uint64{
		4 * uint64(len(b.Lat)),
		4 * uint64(len(b.Lon)),
		8 * uint64(len(b.Off)),
		4 * uint64(len(b.To)),
		4 * uint64(len(b.Len)),
		4 * uint64(len(b.Time)),
		4 * uint64(len(b.GO)),
		2 * uint64(len(b.GL)),
		4 * uint64(len(b.CLat)),
		4 * uint64(len(b.CLon)),
		8 * uint64(len(b.GOff)),
		4 * uint64(len(b.GNodes)),
		8 * uint64(len(b.TurnOff)),
		4 * uint64(len(b.TurnTo)),
	}
	if cells == 0 {
		sizes[sGOff] = 0
	}

	var secOff [numSections]uint64
	cur := uint64(headerSize)
	for i := 0; i < numSections; i++ {
		secOff[i] = cur
		cur = align8(cur + sizes[i])
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	bw := bufio.NewWriterSize(f, 1<<20)
	be := binary.LittleEndian

	hdr := make([]byte, headerSize)
	be.PutUint32(hdr[0:], magicLE)
	be.PutUint32(hdr[4:], Version)
	be.PutUint32(hdr[8:], uint32(len(b.Lat)))
	be.PutUint32(hdr[12:], uint32(len(b.To)))
	be.PutUint32(hdr[16:], uint32(len(b.CLat)))
	be.PutUint32(hdr[20:], uint32(len(b.GNodes)))
	be.PutUint32(hdr[24:], uint32(b.GridNLat))
	be.PutUint32(hdr[28:], uint32(b.GridNLon))
	c := uint64(32)
	hdrF64(hdr, &c, b.GridLat0)
	hdrF64(hdr, &c, b.GridLon0)
	hdrF64(hdr, &c, b.GridStep)
	for i := 0; i < numSections; i++ {
		be.PutUint64(hdr[c:], secOff[i])
		c += 8
	}
	hdrF64(hdr, &c, b.MinLat)
	hdrF64(hdr, &c, b.MinLon)
	hdrF64(hdr, &c, b.MaxLat)
	hdrF64(hdr, &c, b.MaxLon)
	if _, err := bw.Write(hdr); err != nil {
		return err
	}

	written := uint64(headerSize)
	pad := func(to uint64) error {
		for written < to {
			n := to - written
			if n > 1<<20 {
				n = 1 << 20
			}
			if _, err := bw.Write(make([]byte, n)); err != nil {
				return err
			}
			written += n
		}
		return nil
	}
	writeI32s := func(v []int32) error {
		var buf [8192]byte
		for i := 0; i < len(v); i++ {
			off := (i % 2048) * 4
			be.PutUint32(buf[off:], uint32(v[i]))
			if off == 8188 || i == len(v)-1 {
				if _, err := bw.Write(buf[:off+4]); err != nil {
					return err
				}
				written += uint64(off + 4)
			}
		}
		return nil
	}
	writeU32s := func(v []uint32) error {
		var buf [8192]byte
		for i := 0; i < len(v); i++ {
			off := (i % 2048) * 4
			be.PutUint32(buf[off:], v[i])
			if off == 8188 || i == len(v)-1 {
				if _, err := bw.Write(buf[:off+4]); err != nil {
					return err
				}
				written += uint64(off + 4)
			}
		}
		return nil
	}
	writeU64s := func(v []uint64) error {
		var buf [8192]byte
		for i := 0; i < len(v); i++ {
			off := (i % 1024) * 8
			be.PutUint64(buf[off:], v[i])
			if off == 8184 || i == len(v)-1 {
				if _, err := bw.Write(buf[:off+8]); err != nil {
					return err
				}
				written += uint64(off + 8)
			}
		}
		return nil
	}
	writeU16s := func(v []uint16) error {
		var buf [8192]byte
		for i := 0; i < len(v); i++ {
			off := (i % 4096) * 2
			be.PutUint16(buf[off:], v[i])
			if off == 8190 || i == len(v)-1 {
				if _, err := bw.Write(buf[:off+2]); err != nil {
					return err
				}
				written += uint64(off + 2)
			}
		}
		return nil
	}

	steps := []func() error{
		func() error { return writeI32s(b.Lat) },
		func() error { return writeI32s(b.Lon) },
		func() error { return writeU64s(b.Off) },
		func() error { return writeU32s(b.To) },
		func() error { return writeU32s(b.Len) },
		func() error { return writeU32s(b.Time) },
		func() error { return writeU32s(b.GO) },
		func() error { return writeU16s(b.GL) },
		func() error { return writeI32s(b.CLat) },
		func() error { return writeI32s(b.CLon) },
		func() error { return writeU64s(b.GOff) },
		func() error { return writeU32s(b.GNodes) },
		func() error { return writeU64s(b.TurnOff) },
		func() error { return writeU32s(b.TurnTo) },
	}
	for i, step := range steps {
		if err := pad(secOff[i]); err != nil {
			return err
		}
		if err := step(); err != nil {
			return err
		}
	}
	if err := bw.Flush(); err != nil {
		return err
	}
	return f.Sync()
}

func hdrF64(d []byte, c *uint64, v float64) {
	binary.LittleEndian.PutUint64(d[*c:], math.Float64bits(v))
	*c += 8
}

// Bounds returns the geographic extent of the graph in degrees.
func (g *Graph) Bounds() (minLat, minLon, maxLat, maxLon float64) {
	if g.NodeCount == 0 {
		return 0, 0, 0, 0
	}
	if g.bbox[0] != 0 || g.bbox[2] != 0 {
		return g.bbox[0], g.bbox[1], g.bbox[2], g.bbox[3]
	}
	la, lo := g.Lat[0], g.Lon[0]
	mnLat, mxLat, mnLon, mxLon := la, la, lo, lo
	for i := uint32(1); i < g.NodeCount; i++ {
		if g.Lat[i] < mnLat {
			mnLat = g.Lat[i]
		}
		if g.Lat[i] > mxLat {
			mxLat = g.Lat[i]
		}
		if g.Lon[i] < mnLon {
			mnLon = g.Lon[i]
		}
		if g.Lon[i] > mxLon {
			mxLon = g.Lon[i]
		}
	}
	return float64(mnLat) / E7, float64(mnLon) / E7, float64(mxLat) / E7, float64(mxLon) / E7
}
