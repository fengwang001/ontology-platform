package persist

import (
	"encoding/binary"
	"hash/crc32"
	"os"

	"ontology/cell"
	"ontology/geom"
	"ontology/grid"
)

// Save writes the grid atomically: data is fully written and synced to
// name+".tmp", then renamed over name. A crash leaves only the stale temp
// file, which Load recognizes and removes.
func Save(name string, g *grid.Grid) error {
	g.RLock()
	bounds := g.RootBounds()
	_, nanN := 0, 0
	if a, b := g.Skipped(); true {
		_, nanN = a, b
	}
	buf := encHeader(FileMeta{
		Version: version, Cap: g.Cap(), MaxDepth: cell.MaxDepth,
		Bounds: bounds, Skipped: uint64(nanN),
	})
	buf = encodeTree(g.Root(), buf)
	g.RUnlock()

	buf = append(buf, eofMarker)
	sum := crc32.ChecksumIEEE(buf)
	var tail [crcLen]byte
	binary.LittleEndian.PutUint32(tail[:], sum)
	buf = append(buf, tail[:]...)

	tmp := name + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(buf); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, name)
}

// LoadOutcome reports the recovered grid plus the first error encountered
// (nil for a complete file). The grid is always non-nil and holds the maximal
// recoverable prefix.
type LoadOutcome struct {
	Grid         *grid.Grid
	Err          error
	Meta         FileMeta
	Recovered    []geom.Point // points of fully recovered leaves
	RecoveredSet int          // number of fully recovered cells
}

// Load reads an index file back. A leftover name+".tmp" is treated as an
// aborted write and removed before reading.
func Load(name string) (*LoadOutcome, error) {
	tmp := name + ".tmp"
	if _, err := os.Stat(tmp); err == nil {
		if rmErr := os.Remove(tmp); rmErr != nil {
			return nil, rmErr
		}
	}
	raw, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	return Decode(raw)
}

// Decode parses raw bytes, returning the maximal recoverable prefix and the
// first classification error.
func Decode(raw []byte) (*LoadOutcome, error) {
	out := &LoadOutcome{}
	if len(raw) < hdrLen {
		if len(raw) >= 8 && string(raw[:8]) != magic {
			return out, ErrMagic
		}
		return out, ErrShortHeader
	}
	if string(raw[:8]) != magic {
		return out, ErrMagic
	}
	meta := decHeader(raw[:hdrLen])
	if meta.Version != version {
		return out, ErrMagic
	}
	out.Meta = meta

	// The whole-file crc (last 4 bytes) covers everything before it; its
	// absence is reported as ErrCRC only when the record prefix itself is
	// otherwise intact.
	stream := raw[hdrLen:]
	c := &cursor{data: stream}
	var complete []recInfo
	root, perr := parseSubtree(c, "", &complete)
	if perr == ioEOF {
		perr = nil
	}
	out.RecoveredSet = len(complete)
	g := grid.NewWithCap(meta.Bounds, meta.Cap)
	for _, ri := range complete {
		out.Recovered = append(out.Recovered, ri.points...)
	}
	loadPoints(g, out.Recovered)
	g.SetSkipped(int(meta.Skipped), 0)
	out.Grid = g

	switch {
	case perr != nil:
		out.Err = perr
		return out, perr
	case root != nil:
		g.ReplaceTree(root, countCells(root))
	}
	// Whole record prefix parsed: verify the EOF marker + whole-file crc.
	tailOK := false
	if c.off+tailLen <= len(stream) {
		tail := stream[c.off:]
		given := binary.LittleEndian.Uint32(tail[1:5])
		cover := raw[:hdrLen+c.off+1]
		tailOK = len(tail) == tailLen && tail[0] == eofMarker &&
			given == crc32.ChecksumIEEE(cover)
	}
	if !tailOK {
		out.Err = ErrCRC
	}
	return out, out.Err
}

// loadPoints bulk-imports recovered points into g, preserving their IDs.
func loadPoints(g *grid.Grid, pts []geom.Point) {
	for _, p := range pts {
		id, err := g.Insert(p.X, p.Y)
		if err == nil {
			_ = id
		}
	}
}

func countCells(c *cell.Cell) int {
	if !c.Split {
		return 1
	}
	n := 1
	for _, ch := range c.Children() {
		n += countCells(ch)
	}
	return n
}
