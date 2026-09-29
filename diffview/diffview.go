// Package diffview implements an incremental materialized view of the
// multiset difference between two input relations (left minus right).
//
// Semantics: for each row key, the view multiplicity is
// max(leftCount-rightCount, 0). Rows with zero multiplicity never appear
// in the view. Every accepted input change is validated atomically and,
// if it moves the view multiplicity of its row, appends exactly one entry
// to the change log. Rejected changes leave no trace.
package diffview

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Side identifies which input relation a change belongs to.
type Side int

const (
	Left Side = iota
	Right
)

func (s Side) String() string {
	if s == Left {
		return "left"
	}
	return "right"
}

// Rejection reasons. Each invalid input maps to exactly one of these
// sentinels; use errors.Is to distinguish them.
var (
	ErrEmptyRow        = errors.New("diffview: empty row key")
	ErrZeroDelta       = errors.New("diffview: zero delta")
	ErrDeleteUnderflow = errors.New("diffview: delete underflow")
	ErrRowLimit        = errors.New("diffview: row limit exceeded")
)

// Change is a single input delta on one side. Delta must be non-zero:
// positive inserts copies of the row, negative deletes copies.
type Change struct {
	Side  Side
	Row   string
	Delta int
}

// Entry is one record of the change log. It captures the effect of one
// accepted Change on the view multiplicity of its row.
type Entry struct {
	Seq    int    // 1-based position in the log
	Side   Side   // side of the triggering input change
	Row    string // affected row key
	Delta  int    // triggering input delta
	Before int    // view multiplicity before the change
	After  int    // view multiplicity after the change
}

// ViewDelta returns After-Before, the signed view multiplicity change.
func (e Entry) ViewDelta() int { return e.After - e.Before }

// View is an incremental materialized view of left \ right under
// multiset semantics. It is safe for concurrent use: Apply serializes
// commits while Snapshot and SelfCheck may run concurrently with them.
type View struct {
	mu      sync.RWMutex
	left    map[string]int
	right   map[string]int
	log     []Entry
	maxRows int // max distinct row keys tracked across both sides
}

// New creates an empty View. maxRows caps the number of distinct row
// keys tracked across both sides; 0 means unlimited.
func New(maxRows int) *View {
	return &View{
		left:    make(map[string]int),
		right:   make(map[string]int),
		maxRows: maxRows,
	}
}

// Apply validates and commits one input change. On success it reports
// the appended log entry, or ok=false when the view multiplicity did
// not move (no log entry is emitted). On rejection it returns one of
// the Err* sentinels and leaves all state untouched.
func (v *View) Apply(c Change) (entry Entry, ok bool, err error) {
	if c.Row == "" {
		return Entry{}, false, ErrEmptyRow
	}
	if c.Delta == 0 {
		return Entry{}, false, ErrZeroDelta
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	counts := v.left
	if c.Side == Right {
		counts = v.right
	}
	cur := counts[c.Row]
	if cur+c.Delta < 0 {
		return Entry{}, false, fmt.Errorf("%w: side=%s row=%q have=%d delta=%d",
			ErrDeleteUnderflow, c.Side, c.Row, cur, c.Delta)
	}
	if cur == 0 && c.Delta > 0 && v.maxRows > 0 && v.trackedRowsLocked(c.Row) > v.maxRows {
		return Entry{}, false, fmt.Errorf("%w: limit=%d row=%q",
			ErrRowLimit, v.maxRows, c.Row)
	}

	before := resultCount(v.left[c.Row], v.right[c.Row])
	if cur+c.Delta == 0 {
		delete(counts, c.Row)
	} else {
		counts[c.Row] = cur + c.Delta
	}
	after := resultCount(v.left[c.Row], v.right[c.Row])

	if after == before {
		return Entry{}, false, nil
	}
	entry = Entry{
		Seq:    len(v.log) + 1,
		Side:   c.Side,
		Row:    c.Row,
		Delta:  c.Delta,
		Before: before,
		After:  after,
	}
	v.log = append(v.log, entry)
	return entry, true, nil
}

// trackedRowsLocked counts distinct row keys present on either side,
// treating newRow as potentially added. Caller must hold the lock.
func (v *View) trackedRowsLocked(newRow string) int {
	n := len(v.left)
	for row := range v.right {
		if _, ok := v.left[row]; !ok {
			n++
		}
	}
	if _, ok := v.left[newRow]; !ok {
		if _, ok := v.right[newRow]; !ok {
			n++
		}
	}
	return n
}

func resultCount(left, right int) int {
	if left > right {
		return left - right
	}
	return 0
}

// Snapshot returns a copy of the current view: rows with positive
// multiplicity only.
func (v *View) Snapshot() map[string]int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := make(map[string]int)
	for row, lc := range v.left {
		if rc := v.right[row]; lc > rc {
			out[row] = lc - rc
		}
	}
	return out
}

// Log returns a copy of the change log emitted so far.
func (v *View) Log() []Entry {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := make([]Entry, len(v.log))
	copy(out, v.log)
	return out
}

// Batch computes the multiset difference of two relations directly.
// Inputs must have non-negative multiplicities; negative entries are
// treated as zero. Only rows with positive results appear.
func Batch(left, right map[string]int) map[string]int {
	out := make(map[string]int)
	for row, lc := range left {
		if lc <= 0 {
			continue
		}
		rc := right[row]
		if rc < 0 {
			rc = 0
		}
		if lc > rc {
			out[row] = lc - rc
		}
	}
	return out
}

// Replay applies the first n entries of a change log to an empty view
// and returns the resulting multiplicities. It reports an error if any
// prefix would produce a negative or inconsistent multiplicity.
func Replay(log []Entry, n int) (map[string]int, error) {
	if n > len(log) {
		n = len(log)
	}
	view := make(map[string]int)
	for i := 0; i < n; i++ {
		e := log[i]
		cur := view[e.Row]
		if cur != e.Before {
			return nil, fmt.Errorf("diffview: log entry %d row=%q expects before=%d, replayed=%d",
				e.Seq, e.Row, e.Before, cur)
		}
		next := cur + e.ViewDelta()
		if next < 0 || next != e.After {
			return nil, fmt.Errorf("diffview: log entry %d row=%q yields negative or mismatched multiplicity %d",
				e.Seq, e.Row, next)
		}
		if next == 0 {
			delete(view, e.Row)
		} else {
			view[e.Row] = next
		}
	}
	return view, nil
}

// SelfCheck verifies internal consistency:
//  1. every prefix of the change log replays to a non-negative view,
//  2. the full log replays to exactly the current snapshot,
//  3. the snapshot equals the batch recomputation from the raw counts.
//
// It is safe to call concurrently with Apply and from multiple
// goroutines.
func (v *View) SelfCheck() error {
	v.mu.RLock()
	defer v.mu.RUnlock()

	view := make(map[string]int)
	for _, e := range v.log {
		cur := view[e.Row]
		if cur != e.Before {
			return fmt.Errorf("diffview: self-check: entry %d row=%q before=%d, replayed=%d",
				e.Seq, e.Row, e.Before, cur)
		}
		next := cur + e.ViewDelta()
		if next < 0 {
			return fmt.Errorf("diffview: self-check: entry %d row=%q negative multiplicity %d",
				e.Seq, e.Row, next)
		}
		if next != e.After {
			return fmt.Errorf("diffview: self-check: entry %d row=%q after=%d, replayed=%d",
				e.Seq, e.Row, e.After, next)
		}
		if next == 0 {
			delete(view, e.Row)
		} else {
			view[e.Row] = next
		}
	}

	batch := Batch(v.left, v.right)
	if !equalCounts(view, batch) {
		return fmt.Errorf("diffview: self-check: replayed %v != batch %v",
			sortedCounts(view), sortedCounts(batch))
	}
	return nil
}

func equalCounts(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		if vb, ok := b[k]; !ok || vb != va {
			return false
		}
	}
	return true
}

func sortedCounts(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := ""
	for i, k := range keys {
		if i > 0 {
			out += " "
		}
		out += fmt.Sprintf("%s:%d", k, m[k])
	}
	return "{" + out + "}"
}
