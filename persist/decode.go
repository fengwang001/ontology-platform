package persist

import (
	"encoding/binary"
	"errors"
	"fmt"

	"ontology/cell"
	"ontology/geom"
)

// Sentinel errors give callers a decidable classification.
var (
	ErrMagic           = errors.New("persist: bad magic / not an index file")
	ErrShortHeader     = errors.New("persist: header incomplete")
	ErrShortCellHeader = errors.New("persist: cell record header incomplete")
	ErrShortPoints     = errors.New("persist: point data incomplete")
	ErrCRC             = errors.New("persist: CRC mismatch")
	ErrMissingChildren = errors.New("persist: split cell missing children")
	ErrBoundsOverflow  = errors.New("persist: child bounds overflow parent")
	ErrBadTag          = errors.New("persist: unknown record tag")
	ioEOF              = errors.New("persist: clean record boundary")
)

// StructError names the offending cell, axis and direction for invariants.
type StructError struct {
	Kind               error
	Depth              int
	Path               string // quadrant path from root, e.g. "SW.NE"
	Axis, Direction    string // axis "x"/"y", edge "min"/"max"
}

func (e *StructError) Error() string {
	return fmt.Sprintf("%v: cell depth=%d path=%s axis=%s edge=%s",
		e.Kind, e.Depth, e.Path, e.Axis, e.Direction)
}

func (e *StructError) Unwrap() error { return e.Kind }

// recInfo is a fully decoded record for the recovery oracle.
type recInfo struct {
	tag    uint8
	depth  int
	bounds geom.Rect
	points []geom.Point
	path   string
}

// cursor walks the serialized record stream.
type cursor struct {
	data []byte
	off  int
}

// nextRecord parses the next record. For a cut inside the fixed header the
// classification is: cut within the trailing crc field -> ErrCRC, any earlier
// field -> ErrShortCellHeader; a whole header but missing payload ->
// ErrShortPoints; a present-but-wrong crc -> ErrCRC.
func (c *cursor) nextRecord(path string) (recInfo, error) {
	rem := len(c.data) - c.off
	if rem == 0 {
		return recInfo{}, ioEOF
	}
	if rem < recHdrLen {
		if rem > crcOffInH {
			return recInfo{}, ErrCRC // only part of the 4 crc bytes remain
		}
		return recInfo{}, ErrShortCellHeader
	}
	hdr := c.data[c.off : c.off+recHdrLen]
	tag, depth := hdr[0], int(hdr[1])
	n := int(binary.LittleEndian.Uint32(hdr[34:38]))
	wantCRC := binary.LittleEndian.Uint32(hdr[crcOffInH : crcOffInH+crcLen])
	end := c.off + recHdrLen + n*pointLen
	if end > len(c.data) {
		return recInfo{}, ErrShortPoints
	}
	body := c.data[c.off:end]
	if recordCRC(body) != wantCRC {
		return recInfo{}, ErrCRC
	}
	if tag != tagLeaf && tag != tagNode {
		return recInfo{}, ErrBadTag
	}
	ri := recInfo{tag: tag, depth: depth, path: path,
		bounds: geom.Rect{X0: f64(hdr[2:10]), Y0: f64(hdr[10:18]),
			X1: f64(hdr[18:26]), Y1: f64(hdr[26:34])}}
	for i := 0; i < n; i++ {
		o := c.off + recHdrLen + i*pointLen
		ri.points = append(ri.points, geom.Point{
			X: f64(c.data[o : o+8]), Y: f64(c.data[o+8 : o+16]),
			ID: binary.LittleEndian.Uint64(c.data[o+16 : o+24])})
	}
	c.off = end
	return ri, nil
}

// parseSubtree parses one subtree. complete collects fully decoded records in
// preorder even when a later sibling is unreadable; on any failure the node is
// not appended, so "complete" ends exactly at the maximal closed-subtree
// prefix.
func parseSubtree(c *cursor, path string, complete *[]recInfo) (*cell.Cell, error) {
	ri, err := c.nextRecord(path)
	if err != nil {
		return nil, err
	}
	node := &cell.Cell{Bounds: ri.bounds, Depth: ri.depth}
	if ri.tag == tagLeaf {
		node.Points = ri.points
		*complete = append(*complete, ri)
		return node, nil
	}
	names := [4]string{"SW", "SE", "NW", "NE"}
	var kids [4]*cell.Cell
	for i := 0; i < 4; i++ {
		p := names[i]
		if path != "" {
			p = path + "." + names[i]
		}
		ch, perr := parseSubtree(c, p, complete)
		if perr != nil {
			return nil, &StructError{Kind: ErrMissingChildren, Depth: ri.depth,
				Path: path}
		}
		if e := checkChild(ri.bounds, ch.Bounds, names[i], ch.Depth, p); e != nil {
			return nil, e
		}
		kids[i] = ch
	}
	node.SW, node.SE, node.NW, node.NE = kids[0], kids[1], kids[2], kids[3]
	node.Split = true
	*complete = append(*complete, ri)
	return node, nil
}

// checkChild verifies the child lies exactly inside its assigned midpoint
// partition, naming axis/edge on overflow.
func checkChild(parent, ch geom.Rect, name string, depth int, path string) error {
	kids, _, _ := parent.Split()
	want := kids[quadIndex(name)]
	report := func(axis, edge string) error {
		return &StructError{Kind: ErrBoundsOverflow, Depth: depth,
			Path: path, Axis: axis, Direction: edge}
	}
	switch {
	case ch.X0 < want.X0:
		return report("x", "min")
	case ch.X1 > want.X1:
		return report("x", "max")
	case ch.Y0 < want.Y0:
		return report("y", "min")
	case ch.Y1 > want.Y1:
		return report("y", "max")
	}
	return nil
}

func quadIndex(name string) int {
	switch name {
	case "SW":
		return geom.SW
	case "SE":
		return geom.SE
	case "NW":
		return geom.NW
	default:
		return geom.NE
	}
}
