// Package scan pushes predicates down to immutable segments: it prunes row
// groups with zone stats, decodes only surviving groups, and supports
// resumable batched scans via an opaque cursor. It depends on segment and zone.
package scan

import (
	"encoding/binary"

	"ontology/segment"
	"ontology/zone"
)

type cursorPos struct {
	gi    int  // group index
	ri    int  // next unconsumed offset within matched
	start bool // true when group gi has not been decoded yet
	rows  []ScannedRow
}

// Scanner scans one segment with one predicate. Each Scanner keeps its own
// position and counters, so concurrent scanners never interfere.
type Scanner struct {
	seg  *segment.Segment
	pred zone.Predicate

	groupsDecoded int // number of row groups actually decoded
	valuesDecoded int // number of row cells actually decoded

	cur     cursorPos
	matched []ScannedRow
}

// New returns a scanner over seg for pred, starting before the first group.
func New(seg *segment.Segment, pred zone.Predicate) *Scanner {
	return &Scanner{seg: seg, pred: pred, cur: cursorPos{start: true}}
}

// Resume continues from an opaque cursor returned by Cursor.
func Resume(seg *segment.Segment, pred zone.Predicate, cursor []byte) (*Scanner, error) {
	s := New(seg, pred)
	if len(cursor) == 0 {
		return s, nil
	}
	p, ok := decodeCursor(cursor)
	if !ok || p.gi < 0 || p.gi > seg.GroupCount() || p.ri < 0 || (!p.start && p.ri > len(p.rows)) {
		return nil, ErrBadCursor
	}
	s.cur = p
	return s, nil
}

// Cursor returns the opaque resume token. When it pauses inside a decoded
// group, the token carries that group's buffered matches, so resume never
// decodes the group again.
func (s *Scanner) Cursor() []byte { return encodeCursor(s.cur) }

// GroupsDecoded is the number of row groups actually decoded by this scanner.
func (s *Scanner) GroupsDecoded() int { return s.groupsDecoded }

// ValuesDecoded is the number of row cells actually decoded.
func (s *Scanner) ValuesDecoded() int { return s.valuesDecoded }

// Next returns up to limit matched rows. limit <= 0 means "all remaining".
// done is true only after every row group has been considered.
func (s *Scanner) Next(limit int) (rows []ScannedRow, done bool) {
	if !s.cur.start && s.matched == nil {
		s.matched = s.cur.rows
	}
	take := func() bool {
		for s.cur.ri < len(s.matched) {
			rows = append(rows, s.matched[s.cur.ri])
			s.cur.ri++
			if limit > 0 && len(rows) == limit {
				return true
			}
		}
		return false
	}

	if take() {
		return rows, false
	}
	for {
		if s.cur.gi >= s.seg.GroupCount() {
			return rows, true
		}
		group := s.seg.Group(s.cur.gi)
		if s.cur.start {
			// Predicate pushdown: stats decide before any byte is decoded.
			if !s.pred.CouldHave(group.Stats()) {
				s.cur.gi++
				s.cur.ri = 0
				continue
			}
			vals, err := group.Decode()
			if err != nil {
				// A segment accepted by segment.Parse cannot reach here.
				panic(err)
			}
			s.groupsDecoded++
			s.valuesDecoded += group.Rows()
			offset := s.groupOffset(s.cur.gi)
			s.matched = nil
			for i, v := range vals {
				if s.pred.Match(v) {
					s.matched = append(s.matched, ScannedRow{Index: offset + i, Value: v})
				}
			}
			s.cur.start = false
			s.cur.ri = 0
		}
		if take() {
			s.cur.rows = s.matched
			return rows, false
		}
		s.cur.gi++
		s.cur.ri = 0
		s.cur.start = true
		s.cur.rows = nil
	}
}

func (s *Scanner) groupOffset(gi int) int {
	off := 0
	for i := 0; i < gi; i++ {
		off += s.seg.Group(i).Rows()
	}
	return off
}

func encodeCursor(p cursorPos) []byte {
	b := make([]byte, 14)
	b[0] = 'C'
	binary.BigEndian.PutUint32(b[1:5], uint32(p.gi))
	binary.BigEndian.PutUint32(b[5:9], uint32(p.ri))
	if p.start {
		b[9] = 1
	}
	binary.BigEndian.PutUint32(b[10:14], uint32(len(p.rows)))
	for _, r := range p.rows {
		var u4 [4]byte
		binary.BigEndian.PutUint32(u4[:], uint32(r.Index))
		b = append(b, u4[:]...)
		switch r.Value.Kind {
		case zone.Null:
			b = append(b, 0)
		case zone.Int:
			b = append(b, 1)
			var buf [8]byte
			binary.BigEndian.PutUint64(buf[:], uint64(r.Value.I))
			b = append(b, buf[:]...)
		case zone.Str:
			b = append(b, 2)
			var l [4]byte
			binary.BigEndian.PutUint32(l[:], uint32(len(r.Value.S)))
			b = append(b, l[:]...)
			b = append(b, r.Value.S...)
		}
	}
	return b
}

func decodeCursor(b []byte) (cursorPos, bool) {
	if len(b) < 14 || b[0] != 'C' || b[9] > 1 {
		return cursorPos{}, false
	}
	p := cursorPos{
		gi:    int(binary.BigEndian.Uint32(b[1:5])),
		ri:    int(binary.BigEndian.Uint32(b[5:9])),
		start: b[9] == 1,
	}
	n := int(binary.BigEndian.Uint32(b[10:14]))
	off := 14
	for i := 0; i < n; i++ {
		if off+5 > len(b) {
			return cursorPos{}, false
		}
		idx := int(binary.BigEndian.Uint32(b[off : off+4]))
		k := b[off+4]
		off += 5
		switch k {
		case 0:
			p.rows = append(p.rows, ScannedRow{Index: idx, Value: zone.NullValue()})
		case 1:
			if off+8 > len(b) {
				return cursorPos{}, false
			}
			v := int64(binary.BigEndian.Uint64(b[off : off+8]))
			off += 8
			p.rows = append(p.rows, ScannedRow{Index: idx, Value: zone.IntValue(v)})
		case 2:
			if off+4 > len(b) {
				return cursorPos{}, false
			}
			l := int(binary.BigEndian.Uint32(b[off : off+4]))
			off += 4
			if off+l > len(b) {
				return cursorPos{}, false
			}
			p.rows = append(p.rows, ScannedRow{Index: idx, Value: zone.StringValue(string(b[off : off+l]))})
			off += l
		default:
			return cursorPos{}, false
		}
	}
	if off != len(b) {
		return cursorPos{}, false
	}
	return p, true
}

// ScannedRow is one match: its global row index and three-state value.
type ScannedRow struct {
	Index int
	Value zone.Value
}
