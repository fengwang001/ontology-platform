// Package scan evaluates predicates over a segment with pushdown:
// row groups that cannot match (per zone statistics) are skipped
// without decoding. Cursors allow batched scans whose concatenation
// equals a single full scan.
package scan

import (
	"ontology/segment"
	"ontology/zone"
)

// Row is one matching row: global index, value, null flag.
type Row struct {
	Idx  int
	V    int64
	Null bool
}

// Scanner evaluates one predicate over one segment. It is not safe for
// concurrent use; give each goroutine its own Scanner over the shared
// (immutable) Segment.
type Scanner struct {
	seg           *segment.Segment
	pred          zone.Pred
	decodedGroups int
	decodedValues int
}

// New returns a Scanner for pred over seg. No decoding happens until Scan.
func New(seg *segment.Segment, pred zone.Pred) *Scanner {
	return &Scanner{seg: seg, pred: pred}
}

// DecodedGroups reports how many row groups were actually decoded.
func (s *Scanner) DecodedGroups() int { return s.decodedGroups }

// DecodedValues reports how many values were actually decoded.
func (s *Scanner) DecodedValues() int { return s.decodedValues }

// Cursor is an opaque scan position: the next row to examine, as
// (group, row-in-group). The zero value is the segment start. Because
// pruning only skips decoding, never cursor advancement, a cursor
// moves past pruned groups without repeating or skipping rows.
type Cursor struct {
	g, r int
}

// Scan returns up to limit matching rows starting at cur, the cursor
// for the next batch, and whether the segment is exhausted. The
// concatenation of batches over any split equals one full scan.
func (s *Scanner) Scan(cur Cursor, limit int) ([]Row, Cursor, bool, error) {
	if limit < 1 {
		limit = 1
	}
	base := 0
	for i := 0; i < cur.g; i++ {
		base += s.seg.GroupStats(i).Rows
	}
	var rows []Row
	for cur.g < s.seg.NumGroups() && len(rows) < limit {
		st := s.seg.GroupStats(cur.g)
		if !s.pred.MayMatch(st) {
			cur.g, cur.r = cur.g+1, 0
			base += st.Rows
			continue
		}
		vals, err := s.seg.DecodeGroup(cur.g)
		if err != nil {
			return nil, cur, false, err
		}
		s.decodedGroups++
		s.decodedValues += len(vals)
		for cur.r < len(vals) && len(rows) < limit {
			v := vals[cur.r]
			if v.Null && s.pred.MatchNull() || !v.Null && s.pred.Match(v.V) {
				rows = append(rows, Row{Idx: base + cur.r, V: v.V, Null: v.Null})
			}
			cur.r++
		}
		if cur.r == len(vals) {
			cur.g, cur.r = cur.g+1, 0
			base += len(vals)
		}
	}
	return rows, cur, cur.g >= s.seg.NumGroups(), nil
}

// Collect scans from the start to exhaustion in batches of limit and
// returns all matching rows. It is exactly the concatenation of Scan
// batches, so batched and one-shot scans agree by construction.
func Collect(s *Scanner, limit int) ([]Row, error) {
	var out []Row
	var cur Cursor
	for done := false; !done; {
		rows, next, d, err := s.Scan(cur, limit)
		if err != nil {
			return nil, err
		}
		out, cur, done = append(out, rows...), next, d
	}
	return out, nil
}
