package ontology

import (
	"errors"
	"sort"
)

// Mode selects the join mode.
type Mode int

const (
	// Inner emits only matched pairs.
	Inner Mode = iota
	// Left emits matched pairs plus every unmatched left row.
	Left
)

// Result holds the output of a Join.
type Result struct {
	// Rows are the output rows, in the deterministic order documented in
	// the package overview. Each row is a deep copy, isolated from inputs.
	Rows []map[string]any
	// RightColumns lists the right-side column names as they appear in
	// output rows (collision-renamed as "right.<name>"), sorted.
	RightColumns []string

	leftNullKeys  int // left rows whose key is empty (never match)
	leftUnmatched int // left rows with a full key but no right match
	maxFanout     int // largest per-key m*n expansion
}

// RowCount returns the number of output rows produced by the join.
func (r *Result) RowCount() int { return len(r.Rows) }

// MaxFanout returns the largest single-key expansion m*n over all keys.
func (r *Result) MaxFanout() int { return r.maxFanout }

// LeftNullKeyCount returns how many left rows failed to match because
// their join key was empty (absent, nil, or NaN). It never includes rows
// counted by LeftUnmatchedCount.
func (r *Result) LeftNullKeyCount() int { return r.leftNullKeys }

// LeftUnmatchedCount returns how many left rows had a full key but no
// matching right row.
func (r *Result) LeftUnmatchedCount() int { return r.leftUnmatched }

// outRow is an output row plus its sort keys.
type outRow struct {
	vals      []any // nil for empty-key left rows
	leftID    string
	rightID   string // "" for unmatched left rows
	unmatched bool
	row       map[string]any
}

// Join equi-joins left and right on the ordered key columns. It returns a
// *KeyTypeError (matchable with errors.As) when a key column has
// incomparable types.
func Join(left, right []map[string]any, keys []string, mode Mode) (*Result, error) {
	if len(keys) == 0 {
		return nil, errors.New("join: at least one key column is required")
	}
	if err := checkKeyTypes(left, right, keys); err != nil {
		return nil, err
	}
	renames, rightCols := rightRenames(left, right, keys)

	type rrow struct {
		row map[string]any
	}
	rightByKey := make(map[string][]rrow)
	for _, rr := range right {
		vals, empty := keyOf(rr, keys)
		if empty {
			continue // empty keys never match, not even each other
		}
		enc := encodeKey(vals)
		rightByKey[enc] = append(rightByKey[enc], rrow{row: rr})
	}

	var out []outRow
	res := &Result{RightColumns: rightCols}
	fanout := make(map[string]int) // per-key matched-pair count
	for _, lr := range left {
		vals, empty := keyOf(lr, keys)
		if empty {
			res.leftNullKeys++
			if mode == Left {
				out = append(out, outRow{
					leftID: rowID(lr), unmatched: true,
					row: buildRow(lr, nil, keys, renames),
				})
			}
			continue
		}
		enc := encodeKey(vals)
		matches := rightByKey[enc]
		if len(matches) == 0 {
			res.leftUnmatched++
			if mode == Left {
				out = append(out, outRow{
					vals: vals, leftID: rowID(lr), unmatched: true,
					row: buildRow(lr, nil, keys, renames),
				})
			}
			continue
		}
		fanout[enc] += len(matches)
		if n := fanout[enc]; n > res.maxFanout {
			res.maxFanout = n
		}
		for _, m := range matches {
			out = append(out, outRow{
				vals: vals, leftID: rowID(lr), rightID: rowID(m.row),
				row: buildRow(lr, m.row, keys, renames),
			})
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return lessOut(out[i], out[j]) })
	res.Rows = make([]map[string]any, len(out))
	for i, o := range out {
		res.Rows[i] = o.row
	}
	return res, nil
}

// lessOut orders output rows: by key columns ascending; empty keys last;
// within a key, matched pairs before unmatched left rows; pairs by
// (leftID, rightID); unmatched rows by leftID.
func lessOut(a, b outRow) bool {
	if (a.vals == nil) != (b.vals == nil) {
		return a.vals != nil // keyed rows before empty-key rows
	}
	for i := range a.vals {
		if c := compareValues(a.vals[i], b.vals[i]); c != 0 {
			return c < 0
		}
	}
	if a.unmatched != b.unmatched {
		return !a.unmatched
	}
	if a.leftID != b.leftID {
		return a.leftID < b.leftID
	}
	return a.rightID < b.rightID
}
