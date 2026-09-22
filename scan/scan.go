// Package scan implements a predicate-pushdown scanner over a
// segment: row groups that cannot match are skipped using zone-map
// statistics, and only surviving groups are decoded.
package scan

import (
	"ontology/segment"
	"ontology/zone"
)

// Row is one matched result. Null distinguishes a null value from a
// real zero: (Null=true) is null, (Null=false, Value=0) is zero.
type Row struct {
	Value int64
	Null  bool
}

// Scanner walks a segment with an AND of predicates. Its cursor is
// the pair (group, rowInGroup); a Scanner is single-goroutine, but
// any number of Scanners may share one Segment concurrently.
type Scanner struct {
	seg   *segment.Segment
	preds []zone.Pred

	group      int // next row group to consider
	rowInGroup int // next row inside the cached group

	cachedGroup int // group index of the cached decode, -1 = none
	vals        []int64
	nulls       []bool
	err         error
}

// NewScanner creates a scanner over seg applying preds (ANDed).
func NewScanner(seg *segment.Segment, preds []zone.Pred) *Scanner {
	return &Scanner{seg: seg, preds: preds, cachedGroup: -1}
}

// Err returns the first decode error encountered, if any.
func (sc *Scanner) Err() error { return sc.err }

// Next returns up to limit matched rows. eof is true when the scan
// is exhausted; the returned slice may then be shorter than limit
// (including empty). Skipped row groups are never decoded, and the
// cursor simply advances past them, so batches never repeat or miss
// rows regardless of batch size.
func (sc *Scanner) Next(limit int) (rows []Row, eof bool) {
	if sc.err != nil {
		return nil, true
	}
	for len(rows) < limit {
		if sc.group >= sc.seg.NumRowGroups() {
			return rows, true
		}
		st := sc.seg.GroupStats(sc.group)
		if !zone.MayMatchAll(st, sc.preds) {
			sc.group++
			sc.rowInGroup = 0
			continue
		}
		if sc.cachedGroup != sc.group {
			vals, nulls, err := sc.seg.DecodeRowGroup(sc.group)
			if err != nil {
				sc.err = err
				return rows, true
			}
			sc.vals, sc.nulls = vals, nulls
			sc.cachedGroup = sc.group
		}
		for sc.rowInGroup < len(sc.vals) && len(rows) < limit {
			i := sc.rowInGroup
			if zone.MatchAll(sc.vals[i], sc.nulls[i], sc.preds) {
				rows = append(rows, Row{Value: sc.vals[i], Null: sc.nulls[i]})
			}
			sc.rowInGroup++
		}
		if sc.rowInGroup >= len(sc.vals) {
			sc.group++
			sc.rowInGroup = 0
		}
	}
	return rows, false
}

// All drains the scanner and returns every matched row.
func (sc *Scanner) All() []Row {
	var out []Row
	for {
		rows, eof := sc.Next(1 << 20)
		out = append(out, rows...)
		if eof {
			return out
		}
	}
}
