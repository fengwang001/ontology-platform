// Package rank defines the ranking order and maintains every live row in
// that order, locating insert/delete positions by binary search.
// It depends on no other package.
package rank

import (
	"math/bits"
	"strconv"
)

// Row is one live (Key, Score) line.
type Row struct {
	Key   string
	Score int64
}

// Before reports whether a outranks b in the global order:
// Score descending, then Key by byte order ascending.
func Before(a, b Row) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	return a.Key < b.Key
}

// equal reports whether two rows are the same live line.
func equal(a, b Row) bool { return a.Score == b.Score && a.Key == b.Key }

// Index keeps all live rows sorted by Before (best row first).
type Index struct {
	rows []Row
	cmps int // unexported: live rows compared during the most recent operation
}

// NewIndex returns an empty ordered index.
func NewIndex() *Index { return &Index{} }

// Len is the number of indexed rows.
func (x *Index) Len() int { return len(x.rows) }

// At returns the row at sorted position i.
func (x *Index) At(i int) Row { return x.rows[i] }

// seek returns the position at which r belongs: the first index i for
// which rows[i] is not strictly worse than r (better rows have smaller
// indices). Every binary-search probe is counted in cmps.
func (x *Index) seek(r Row) int {
	x.cmps = 0
	lo, hi := 0, len(x.rows)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		x.cmps++
		if Before(x.rows[mid], r) { // rows[mid] better than r: r goes right
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// Insert places r in order and returns its position. The caller must
// guarantee r's Key is absent; ties keep the ordering from Before.
func (x *Index) Insert(r Row) int {
	i := x.seek(r)
	x.rows = append(x.rows, Row{})
	copy(x.rows[i+1:], x.rows[i:])
	x.rows[i] = r
	return i
}

// Delete removes the row equal to r; it reports whether it was present.
// Its position is found by binary search, never a full scan.
func (x *Index) Delete(r Row) bool {
	i := x.seek(r)
	if i >= len(x.rows) || !equal(x.rows[i], r) {
		x.cmps++ // the final identity comparison at the probed position
		return false
	}
	x.rows = append(x.rows[:i], x.rows[i+1:]...)
	return true
}

// Rows returns a copy of all rows in sorted order.
func (x *Index) Rows() []Row { return append([]Row(nil), x.rows...) }

// bound is the logarithmic comparison budget for a structure of size m.
func bound(m int) int { return 4*bits.Len(uint(m)) + 4 }

// CheckLogBudget reports, for every scale in ms, that one entering insert
// and one in-board retract each stay within the logarithmic budget. It
// exposes only a boolean; the underlying comparison count is never read.
func CheckLogBudget(ms []int) bool {
	for _, m := range ms {
		x := NewIndex()
		for i := 0; i < m; i++ {
			x.Insert(Row{Key: fmtKey(i), Score: int64(i % 17)})
		}
		top := x.At(0)
		b := bound(m)
		x.Insert(Row{Key: "z-new", Score: 1 << 40})
		if x.cmps <= 0 || x.cmps > b || !x.Delete(top) || x.cmps > b {
			return false
		}
	}
	return true
}

func fmtKey(i int) string { return "k" + strconv.Itoa(i) }
