// Package persist writes a grid index to a self-describing local file and
// reads it back, detecting truncation, CRC corruption and broken tree
// invariants, always recovering the maximal valid prefix.
package persist

import (
	"encoding/binary"
	"hash/crc32"
	"math"

	"ontology/cell"
	"ontology/geom"
)

// On-disk constants (all integers little-endian); see DESIGN.md §6.
const (
	magic      = "ONTGRID1"
	version    = 1
	hdrLen     = 49
	recHdrLen  = 42 // tag,depth,4 bounds,count,crc
	pointLen   = 24 // x,y f64 + id u64
	tailLen    = 5  // eof marker 0xFF + whole-file crc32
	tagLeaf    = 1
	tagNode    = 2
	eofMarker  = 0xFF
	crcLen     = 4
	crcOffInH  = 38 // offset of crc within the 42-byte record header
	ieeePoly   = crc32.IEEETable
)

var crcTable = crc32.MakeTable(crc32.IEEE)

// FileMeta is the decoded self-describing header.
type FileMeta struct {
	Version  int
	Cap      int
	MaxDepth int
	Bounds   geom.Rect
	Skipped  uint64 // rejected NaN/Inf count
}

// encHeader writes the fixed 49-byte header.
func encHeader(m FileMeta) []byte {
	b := make([]byte, hdrLen)
	copy(b[0:8], magic)
	b[8] = version
	binary.LittleEndian.PutUint16(b[9:11], uint16(m.Cap))
	b[11] = byte(m.MaxDepth)
	putF64(b[12:20], m.Bounds.X0)
	putF64(b[20:28], m.Bounds.Y0)
	putF64(b[28:36], m.Bounds.X1)
	putF64(b[36:44], m.Bounds.Y1)
	binary.LittleEndian.PutUint64(b[44:49], m.Skipped)
	return b
}

func decHeader(b []byte) FileMeta {
	return FileMeta{
		Version:  int(b[8]),
		Cap:      int(binary.LittleEndian.Uint16(b[9:11])),
		MaxDepth: int(b[11]),
		Bounds: geom.Rect{
			X0: f64(b[12:20]), Y0: f64(b[20:28]),
			X1: f64(b[28:36]), Y1: f64(b[36:44]),
		},
		Skipped: binary.LittleEndian.Uint64(b[44:49]),
	}
}

// encodeTree serializes cells in preorder (node, then SW,SE,NW,NE).
func encodeTree(c *cell.Cell, w []byte) []byte {
	if c.Split {
		w = append(w, encRecord(tagNode, c.Depth, c.Bounds, 0, nil)...)
		for _, ch := range c.Children() {
			w = encodeTree(ch, w)
		}
		return w
	}
	return append(w, encRecord(tagLeaf, c.Depth, c.Bounds, len(c.Points), c.Points)...)
}

// encRecord builds one record; crc covers every record byte except the 4 crc
// bytes themselves.
func encRecord(tag uint8, depth int, b geom.Rect, n int, pts []geom.Point) []byte {
	body := make([]byte, recHdrLen+pointLen*n)
	body[0] = tag
	body[1] = byte(depth)
	putF64(body[2:10], b.X0)
	putF64(body[10:18], b.Y0)
	putF64(body[18:26], b.X1)
	putF64(body[26:34], b.Y1)
	binary.LittleEndian.PutUint32(body[34:38], uint32(n))
	for i, p := range pts {
		o := recHdrLen + i*pointLen
		putF64(body[o:o+8], p.X)
		putF64(body[o+8:o+16], p.Y)
		binary.LittleEndian.PutUint64(body[o+16:o+24], p.ID)
	}
	binary.LittleEndian.PutUint32(body[crcOffInH:crcOffInH+crcLen], recordCRC(body))
	return body
}

func recordCRC(body []byte) uint32 {
	h := crc32.New(crcTable)
	h.Write(body[:crcOffInH])
	h.Write(body[crcOffInH+crcLen:])
	return h.Sum32()
}

func putF64(b []byte, v float64) { binary.LittleEndian.PutUint64(b, math.Float64bits(v)) }
func f64(b []byte) float64       { return math.Float64frombits(binary.LittleEndian.Uint64(b)) }
