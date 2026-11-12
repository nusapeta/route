// Package graph implements the on-disk road graph format and routing.
//
// File layout (little endian, mmap friendly):
//
//	header (320 bytes, fixed)
//	section 0  Lat      int32[nodeCount]
//	section 1  Lon      int32[nodeCount]
//	section 2  Off      uint32[nodeCount+1]   CSR: edge indexes of node
//	section 3  To       uint32[edgeCount]
//	section 4  Len      uint16[edgeCount]      metres / 4 (LenUnit)
//	section 5  Cls      uint8[edgeCount]       car speed class (km/h * 2)
//	section 6  MCls     uint8[edgeCount]       motor speed class; 0 = no access
//	section 7  Ferry    uint32[ferryCount]    sorted edge indexes that are ferry crossings
//	section 8  CLat     int32[coordCount]      interior shape points
//	section 9  CLon     int32[coordCount]
//	section 10 GOff     uint32[cellCount+1]    byte offsets into the encoded grid
//	section 11 GNodes   varint delta of node ids, grouped per grid cell
//	section 12 TurnOff  uint64[banCount]       sorted packed (from,to) bans
//	section 13 TurnTo   uint64[onlyCount]      sorted packed only_* successors
//
// Motor-only turn restrictions live in two extra sections whose offsets and
// counts sit in the reserved header tail (see hBanMOff and friends).
package graph

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const (
	// Version of the file format.
	//
	// v2 adds sparse turn restriction sections (sTurnOff = banned
	// (from,to) edge pairs, sTurnTo = allowed pairs for only_* rules) plus
	// header counters. v3 adds provenance: SHA-256 of the source extract,
	// build time, and a key=value metadata blob appended after the last
	// section.
	//
	// v4 compacts the edge record: Off is uint32 (was uint64), Len is
	// uint16 in units of LenUnit metres (was uint32 metres) and the Time
	// section is gone: travel time is derived from Len and a per-edge
	// speed class (Cls), which costs 1 byte instead of 4 while keeping
	// both metrics. That is 4 B/node + 5 B/edge less (~280 MB on Java).
	//
	// v5 stores one car and one motor class per edge (MCls, section 6
	// replaces the unused GO), so a single artefact serves both profiles
	// and the duplicated motor graph is gone. Motor-only turn
	// restrictions move to their own sections (hBanMOff/hOnlyMOff). The
	// snap grid compacts too: GOff is uint32 byte offsets and GNodes is a
	// varint delta blob (-68 MB on Java). v1-v4 files are rejected;
	// re-run the extractor.
	Version = 5
	// VersionMin is the oldest readable format version.
	VersionMin = 5

	magicLE     = 0x47444952 // "RIDG"
	headerSize  = 320
	numSections = 14

	// Header offsets of the v3 provenance fields. The slice 212..256 was
	// unused in v1/v2, so older readers simply ignore them.
	hPBFHash   = 212 // [32]byte SHA-256 of the source .osm.pbf
	hBuildUnix = 244 // uint32   seconds since Unix epoch (UTC), 0 = unknown
	hMetaOff   = 248 // uint64   file offset of the metadata blob, 0 = none
	// hFerry (v4) is the number of entries in the sparse ferry edge list. The
	// header grew to 320 bytes for it; sections start after the header.
	hFerry = 256 // uint32   count of ferry edges
	// v5 extra sections live in the reserved header tail.
	hBanMOff   = 260 // uint64   file offset of the motor ban list
	hOnlyMOff  = 268 // uint64   file offset of the motor only_* list
	hBanMCount = 276 // uint32   motor ban entries
	hOnlyM     = 280 // uint32   motor only_* entries
	hGridBytes = 284 // uint32   bytes in the encoded GNodes blob
)

const (
	sLat = iota
	sLon
	sOff
	sTo
	sLen
	sCls
	sMCls // v5: motor speed class; was the reserved GO geometry section
	sFerry
	sCLat
	sCLon
	sGOff
	sGNodes
	sTurnOff
	sTurnTo
)

// E7 is the fixed point scale for coordinates (1e-7 degree, ~1.1cm).
const E7 = 1e7

// LenUnit is the resolution of the Len section, in metres: an edge stores
// round(metres/4) in a uint16, so it can describe up to ~262 km. Every edge is
// one OSM node-to-node hop (metres to a few km), so the quantisation error is
// at most 2 m per edge, ~0.1% of a city route.
const LenUnit = 4

// MaxLen4 is the largest storable Len value (in LenUnit metres).
const MaxLen4 = 1<<16 - 1

// SpeedClassScale converts km/h to the Cls byte: c = round(km/h * 2). Class 0
// means unknown/zero speed and is never written; 254 caps at 127 km/h, above
// anything the profiles emit.
const SpeedClassScale = 2

// classRate[c] is the time in deciseconds to cover one LenUnit (4 m) at the
// speed encoded by class c: 4 m at v m/s is 40/v ds and v = c/7.2 m/s, so the
// rate is 288/c, exact for every integer km/h (c = 2*km/h).
var classRate = func() (t [256]float32) {
	for c := 1; c < 256; c++ {
		t[c] = 288 / float32(c)
	}
	return t
}()

// MaxSpeedMps is the fastest speed any edge in this graph can carry, used to
// bound the A* heuristic. A motorcycle graph tops out well below a car one, and
// the tighter bound is what keeps long bike routes inside the expansion budget.
// Falls back to the global 130 km/h bound when the extractor did not stamp it.
func (g *Graph) MaxSpeedMps() float64 {
	if v := g.meta["max_speed_kmh"]; v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 1 && f <= 130 {
			return f / 3.6
		}
	}
	return 130.0 / 3.6
}

// EdgeLenM is the length of edge e in metres.
func (g *Graph) EdgeLenM(e uint32) uint32 { return uint32(g.Len[e]) * LenUnit }

// EdgeTimeDs is the travel time of edge e in deciseconds for the car profile.
func (g *Graph) EdgeTimeDs(e uint32) float32 { return float32(g.Len[e]) * classRate[g.Cls[e]] }

// MotorTimeDs is the travel time of edge e in deciseconds for the motor
// profile. Edges with MCls 0 are not usable by a motorcycle.
func (g *Graph) MotorTimeDs(e uint32) float32 { return float32(g.Len[e]) * classRate[g.MCls[e]] }

// edgeUsable reports whether one profile may drive edge e. Class 0 is the v5
// "no access" marker.
func (g *Graph) edgeUsable(e uint32, motor bool) bool {
	if motor {
		return g.MCls[e] != 0
	}
	return g.Cls[e] != 0
}

// GridCell decodes the node ids of one snap cell into buf (reused across
// calls) and returns them appended to buf[:0]. It returns nil when the cell
// does not exist.
func (g *Graph) GridCell(cell uint64, buf []uint32) []uint32 {
	buf = buf[:0]
	if cell+1 >= uint64(len(g.GOff)) {
		return nil
	}
	lo, hi := g.GOff[cell], g.GOff[cell+1]
	prev := uint32(0)
	for lo < hi {
		v, n := uvarint(g.GNodes[lo:hi])
		if n <= 0 {
			break
		}
		lo += uint32(n)
		prev += uint32(v)
		buf = append(buf, prev)
	}
	return buf
}

// uvarint decodes one unsigned LEB128 value, returning the value and the
// number of bytes read (0 on truncation).
func uvarint(b []byte) (uint64, int) {
	var v uint64
	for i := 0; i < len(b) && i < 5; i++ {
		v |= uint64(b[i]&0x7f) << (7 * uint(i))
		if b[i] < 0x80 {
			return v, i + 1
		}
	}
	return 0, 0
}

// IsFerry reports whether edge e is a ferry crossing. The list is sparse (a
// few thousand edges even nationwide) and binary searched, which is per-route
// work in reconstruct, not per-expansion work in the hot loop.
func (g *Graph) IsFerry(e uint32) bool {
	lo, hi := 0, len(g.Ferry)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		switch v := g.Ferry[mid]; {
		case v == e:
			return true
		case v < e:
			lo = mid + 1
		default:
			hi = mid
		}
	}
	return false
}

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
	Off      []uint32
	To       []uint32
	Len      []uint16 // metres / LenUnit
	Cls      []uint8  // car speed class, see SpeedClassScale
	MCls     []uint8  // motor speed class; 0 = no motorcycle access
	Ferry    []uint32 // sorted edge indexes of ferry crossings
	CLat     []int32
	CLon     []int32
	GOff     []uint32 // byte offsets into GNodes, one per cell plus end
	GNodes   []byte   // per-cell varint delta of node ids
	// Ban holds sorted packed (fromEdge<<32|toEdge) transitions forbidden by
	// an OSM turn restriction. Only holds the permitted successor of a
	// fromEdge for only_* rules (every other successor is forbidden).
	// The *M variants carry the motor rules, which differ when a relation
	// says except=motorcycle or references motor-only ways.
	Ban     []uint64
	Only    []uint64
	BanM    []uint64
	OnlyM   []uint64
	Skipped uint32 // restrictions dropped at build time

	// lookup tables built by Open; see SpanFor
	banIdx, onlyIdx, banMIdx, onlyMIdx restrictionIndex

	// provenance (v3+)
	pbfHash   [32]byte
	buildUnix uint32
	meta      map[string]string
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
	if v := be.Uint32(data[4:]); v < VersionMin || v > Version {
		syscall.Munmap(data)
		return nil, fmt.Errorf("graph: version %d, supported %d..%d", v, VersionMin, Version)
	}
	banCount := be.Uint32(data[200:])
	onlyCount := be.Uint32(data[204:])
	g.Skipped = be.Uint32(data[208:])
	ferryCount := be.Uint32(data[hFerry:])
	banMCount := be.Uint32(data[hBanMCount:])
	onlyMCount := be.Uint32(data[hOnlyM:])
	banMOff := be.Uint64(data[hBanMOff:])
	onlyMOff := be.Uint64(data[hOnlyMOff:])
	gridBytes := be.Uint32(data[hGridBytes:])
	copy(g.pbfHash[:], data[hPBFHash:hPBFHash+32])
	g.buildUnix = be.Uint32(data[hBuildUnix:])
	if off := be.Uint64(data[hMetaOff:]); off > 0 && off < uint64(len(data)) {
		g.meta = parseMeta(data[off:])
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

	cells := uint64(0)
	if g.GridNLat > 0 && g.GridNLon > 0 {
		cells = uint64(g.GridNLat) * uint64(g.GridNLon)
		if cells > math.MaxUint32-1 {
			syscall.Munmap(data)
			return nil, fmt.Errorf("graph: grid %dx%d exceeds the section limit", g.GridNLat, g.GridNLon)
		}
	}

	// A truncated or corrupt file must fail here, not panic inside unsafe.Slice
	// on the first query. Every section below is checked against the mapping
	// before it is sliced.
	specs := []sectionSpec{
		{"Lat", off[sLat], uint64(g.NodeCount), 4},
		{"Lon", off[sLon], uint64(g.NodeCount), 4},
		{"Off", off[sOff], uint64(g.NodeCount) + 1, 4},
		{"To", off[sTo], uint64(g.EdgeCount), 4},
		{"Len", off[sLen], uint64(g.EdgeCount), 2},
		{"Cls", off[sCls], uint64(g.EdgeCount), 1},
		{"MCls", off[sMCls], uint64(g.EdgeCount), 1},
		{"Ferry", off[sFerry], uint64(ferryCount), 4},
		{"CLat", off[sCLat], uint64(g.CoordCount), 4},
		{"CLon", off[sCLon], uint64(g.CoordCount), 4},
		{"TurnOff", off[sTurnOff], uint64(banCount), 8},
		{"TurnTo", off[sTurnTo], uint64(onlyCount), 8},
		{"BanM", banMOff, uint64(banMCount), 8},
		{"OnlyM", onlyMOff, uint64(onlyMCount), 8},
	}
	if cells > 0 {
		specs = append(specs,
			sectionSpec{"GOff", off[sGOff], cells + 1, 4},
			sectionSpec{"GNodes", off[sGNodes], uint64(gridBytes), 1},
		)
	}
	if err := validateSections(data, specs); err != nil {
		syscall.Munmap(data)
		return nil, err
	}
	// The search packs the state into a workspace key; a graph that cannot be
	// searched must be rejected at load time, not on the first request.
	if workspaceBits(g.EdgeCount) > maxWorkspaceBits {
		syscall.Munmap(data)
		return nil, ErrWorkspaceOverflow
	}

	g.Lat = i32s(data, off[sLat], g.NodeCount)
	g.Lon = i32s(data, off[sLon], g.NodeCount)
	g.Off = u32s(data, off[sOff], g.NodeCount+1)
	g.To = u32s(data, off[sTo], g.EdgeCount)
	g.Len = u16s(data, off[sLen], g.EdgeCount)
	g.Cls = u8s(data, off[sCls], g.EdgeCount)
	g.MCls = u8s(data, off[sMCls], g.EdgeCount)
	g.Ferry = u32s(data, off[sFerry], ferryCount)
	g.CLat = i32s(data, off[sCLat], g.CoordCount)
	g.CLon = i32s(data, off[sCLon], g.CoordCount)
	if cells > 0 {
		g.GOff = u32s(data, off[sGOff], uint32(cells+1))
		g.GNodes = data[off[sGNodes] : off[sGNodes]+uint64(gridBytes)]
		if g.GOff[cells] != gridBytes {
			syscall.Munmap(data)
			return nil, fmt.Errorf("graph: grid offsets end at %d, blob is %d bytes", g.GOff[cells], gridBytes)
		}
	}
	g.Ban = u64s(data, off[sTurnOff], banCount)
	g.Only = u64s(data, off[sTurnTo], onlyCount)
	g.BanM = u64s(data, banMOff, banMCount)
	g.OnlyM = u64s(data, onlyMOff, onlyMCount)
	g.buildRestrictionIndex()
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

// sectionSpec describes one fixed-size section read by Open.
type sectionSpec struct {
	name string
	off  uint64
	n    uint64
	size uint64
}

// validateSections checks that every section fits in the mapped file. An empty
// section is valid: count 0 means the writer emitted no section at all.
func validateSections(d []byte, specs []sectionSpec) error {
	for _, s := range specs {
		if s.n == 0 {
			continue
		}
		size := s.n * s.size
		if s.off < headerSize || s.off > uint64(len(d)) || size > uint64(len(d))-s.off {
			return fmt.Errorf("graph: section %s out of bounds (offset %d, %d bytes, file %d)", s.name, s.off, size, len(d))
		}
	}
	return nil
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

func u8s(d []byte, off uint64, n uint32) []uint8 {
	if n == 0 {
		return nil
	}
	return d[off : off+uint64(n)]
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
	Lat, Lon   []int32
	Off        []uint32
	To         []uint32
	Len        []uint16
	Cls        []uint8  // car speed class
	MCls       []uint8  // motor speed class; nil means "same as Cls"
	Ferry      []uint32 // sorted edge indexes
	CLat, CLon []int32
	TurnOff    []uint64
	TurnTo     []uint32

	GridStep           float64
	GridLat0, GridLon0 float64
	GridNLat, GridNLon int
	GOff               []uint32
	GNodes             []byte // varint delta per cell, see BuildGrid
	Ban, Only          []uint64
	BanM, OnlyM        []uint64
	RestSkipped        uint32
	MinLat, MinLon     float64
	MaxLat, MaxLon     float64

	// Provenance (written as format v3). PBFHash is the SHA-256 of the
	// source extract; BuildUnix the build timestamp; Meta is a free-form
	// key=value blob stored after the last section (source, profile hash,
	// extractor version, order, grid step, ...).
	PBFHash   []byte
	BuildUnix uint32
	Meta      map[string]string
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
	nodes := make([]uint32, len(b.Lat))
	cur := make([]uint32, cells)
	copy(cur, counts[:cells])
	for i := range b.Lat {
		c := cell[i]
		nodes[cur[c]] = uint32(i)
		cur[c]++
	}

	// Encode each cell as ascending deltas from the previous node id. Cells
	// are small (a few dozen entries), so one byte per node is the norm.
	b.GOff = make([]uint32, cells+1)
	blob := make([]byte, 0, len(b.Lat)+len(b.Lat)>>3+64)
	for c := 0; c < cells; c++ {
		b.GOff[c] = uint32(len(blob))
		prev := uint32(0)
		for k := counts[c]; k < counts[c+1]; k++ {
			v := nodes[k]
			blob = appendUvarint(blob, v-prev)
			prev = v
		}
	}
	if uint64(len(blob)) > math.MaxUint32-1 {
		panic("graph: grid blob exceeds uint32")
	}
	b.GOff[cells] = uint32(len(blob))
	b.GNodes = blob
}

func appendUvarint(b []byte, v uint32) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// clampCell maps a computed cell index into [0, n-1]. An empty dimension maps
// to 0; callers reject grids with a non-positive size before using it.
func clampCell(v, n int) int {
	if v < 0 {
		return 0
	}
	if v >= n {
		return n - 1
	}
	return v
}

func (b *Builder) cellOf(lat, lon float64) int {
	ri := clampCell(int((lat-b.GridLat0)/b.GridStep), b.GridNLat)
	ci := clampCell(int((lon-b.GridLon0)/b.GridStep), b.GridNLon)
	return ri*b.GridNLon + ci
}

func align8(v uint64) uint64 { return (v + 7) &^ 7 }

// Write serializes the graph to path.
func (b *Builder) Write(path string) error {
	cells := 0
	if b.GridNLat > 0 && b.GridNLon > 0 {
		cells = b.GridNLat * b.GridNLon
	}
	mcls := b.MCls
	if len(mcls) == 0 {
		// Synthetic builders and the car-only unit tests name just Cls; a
		// motorcycle may use exactly the same edges at the same speed.
		mcls = b.Cls
	}
	sizes := [numSections]uint64{
		4 * uint64(len(b.Lat)),
		4 * uint64(len(b.Lon)),
		4 * uint64(len(b.Off)),
		4 * uint64(len(b.To)),
		2 * uint64(len(b.Len)),
		1 * uint64(len(mcls)),
		1 * uint64(len(mcls)),
		4 * uint64(len(b.Ferry)),
		4 * uint64(len(b.CLat)),
		4 * uint64(len(b.CLon)),
		4 * uint64(len(b.GOff)),
		uint64(len(b.GNodes)),
		8 * uint64(len(b.Ban)),
		8 * uint64(len(b.Only)),
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
	// Motor restriction lists follow the main sections; their offsets and
	// counts live in the reserved header tail.
	banMOff := cur
	cur = align8(cur + 8*uint64(len(b.BanM)))
	onlyMOff := cur
	cur = align8(cur + 8*uint64(len(b.OnlyM)))

	// Build the metadata blob up front: sorted key=value lines, appended
	// after the last section and pointed to by the header.
	metaOff := uint64(0)
	var metaBlob []byte
	if len(b.Meta) > 0 {
		keys := make([]string, 0, len(b.Meta))
		for k := range b.Meta {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var mb bytes.Buffer
		for _, k := range keys {
			v := b.Meta[k]
			if strings.ContainsAny(k, "=\n") || strings.Contains(v, "\n") {
				return fmt.Errorf("graph: bad metadata %q=%q", k, v)
			}
			fmt.Fprintf(&mb, "%s=%s\n", k, v)
		}
		metaBlob = mb.Bytes()
		metaOff = align8(cur)
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
	be.PutUint32(hdr[20:], uint32(len(b.Lat)))
	be.PutUint32(hdr[24:], uint32(b.GridNLat))
	be.PutUint32(hdr[28:], uint32(b.GridNLon))
	be.PutUint32(hdr[200:], uint32(len(b.Ban)))
	be.PutUint32(hdr[204:], uint32(len(b.Only)))
	be.PutUint32(hdr[208:], b.RestSkipped)
	be.PutUint32(hdr[hFerry:], uint32(len(b.Ferry)))
	be.PutUint64(hdr[hBanMOff:], banMOff)
	be.PutUint64(hdr[hOnlyMOff:], onlyMOff)
	be.PutUint32(hdr[hBanMCount:], uint32(len(b.BanM)))
	be.PutUint32(hdr[hOnlyM:], uint32(len(b.OnlyM)))
	be.PutUint32(hdr[hGridBytes:], uint32(len(b.GNodes)))
	copy(hdr[hPBFHash:], b.PBFHash)
	be.PutUint32(hdr[hBuildUnix:], b.BuildUnix)
	be.PutUint64(hdr[hMetaOff:], metaOff)
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
	writeU8s := func(v []uint8) error {
		if len(v) == 0 {
			return nil
		}
		if _, err := bw.Write(v); err != nil {
			return err
		}
		written += uint64(len(v))
		return nil
	}

	steps := []func() error{
		func() error { return writeI32s(b.Lat) },
		func() error { return writeI32s(b.Lon) },
		func() error { return writeU32s(b.Off) },
		func() error { return writeU32s(b.To) },
		func() error { return writeU16s(b.Len) },
		func() error { return writeU8s(b.Cls) },
		func() error { return writeU8s(mcls) },
		func() error { return writeU32s(b.Ferry) },
		func() error { return writeI32s(b.CLat) },
		func() error { return writeI32s(b.CLon) },
		func() error { return writeU32s(b.GOff) },
		func() error { return writeU8s(b.GNodes) },
		func() error { return writeU64s(b.Ban) },
		func() error { return writeU64s(b.Only) },
	}
	for i, step := range steps {
		if err := pad(secOff[i]); err != nil {
			return err
		}
		if err := step(); err != nil {
			return err
		}
	}
	for _, extra := range []struct {
		off  uint64
		step func() error
	}{
		{banMOff, func() error { return writeU64s(b.BanM) }},
		{onlyMOff, func() error { return writeU64s(b.OnlyM) }},
	} {
		if err := pad(extra.off); err != nil {
			return err
		}
		if err := extra.step(); err != nil {
			return err
		}
	}
	if len(metaBlob) > 0 {
		if err := pad(metaOff); err != nil {
			return err
		}
		if _, err := bw.Write(metaBlob); err != nil {
			return err
		}
	}
	if err := bw.Flush(); err != nil {
		return err
	}
	return f.Sync()
}

// parseMeta decodes the key=value metadata blob.
func parseMeta(b []byte) map[string]string {
	m := make(map[string]string, 16)
	for len(b) > 0 {
		nl := bytes.IndexByte(b, '\n')
		if nl < 0 {
			nl = len(b)
		}
		line := b[:nl]
		b = b[nl:]
		if len(b) > 0 {
			b = b[1:]
		}
		if eq := bytes.IndexByte(line, '='); eq > 0 {
			m[string(line[:eq])] = string(line[eq+1:])
		}
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// PBFHashHex returns the hex SHA-256 of the source extract, or "" for graphs
// built before format v3.
func (g *Graph) PBFHashHex() string {
	var zero [32]byte
	if g.pbfHash == zero {
		return ""
	}
	return hex.EncodeToString(g.pbfHash[:])
}

// BuildTime returns the build timestamp of the graph, or the zero time when the
// file predates format v3.
func (g *Graph) BuildTime() time.Time {
	if g.buildUnix == 0 {
		return time.Time{}
	}
	return time.Unix(int64(g.buildUnix), 0).UTC()
}

// Meta returns the free-form provenance map (nil for v1/v2 files).
func (g *Graph) Meta() map[string]string { return g.meta }

// MetaValue returns one metadata field, or "" if absent.
func (g *Graph) MetaValue(k string) string { return g.meta[k] }

// Provenance is a JSON-friendly view of how a graph file was produced.
type Provenance struct {
	PBFHash   string            `json:"pbf_sha256,omitempty"`
	BuiltAt   string            `json:"built_at,omitempty"`
	Fields    map[string]string `json:"fields,omitempty"`
	Format    uint32            `json:"format"`
	Canonical string            `json:"canonical,omitempty"`
}

// Provenance summarises the metadata needed to audit a graph file.
func (g *Graph) Provenance() Provenance {
	p := Provenance{PBFHash: g.PBFHashHex(), Fields: g.meta}
	if t := g.BuildTime(); !t.IsZero() {
		p.BuiltAt = t.Format(time.RFC3339)
	}
	if g.d != nil {
		p.Format = binary.LittleEndian.Uint32(g.d[4:])
	}
	p.Canonical = p.PBFHash
	if len(p.Canonical) > 12 {
		p.Canonical = p.Canonical[:12]
	}
	return p
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
