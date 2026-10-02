// Package streammatch implements a streaming approximate substring matcher:
// it compares an arbitrarily chunked byte stream against a fixed pattern P
// using byte-level Levenshtein distance, computes for every end position e
// the minimum distance D(e) over all substrings ending at e together with
// the leftmost start, folds each run of consecutive hit end positions into
// a single representative match, and suppresses representatives that
// overlap an already reported match.
package streammatch

import (
	"errors"
	"sync"
)

// Distinguishable rejection reasons, reported in this order; a rejected
// operation never changes the matcher state.
var (
	// ErrInvalidPattern: pattern is empty or longer than 64 bytes.
	ErrInvalidPattern = errors.New("streammatch: invalid pattern (length must be 1..64 bytes)")
	// ErrInvalidK: k is negative or not smaller than m.
	ErrInvalidK = errors.New("streammatch: invalid threshold (need 0 <= k < m)")
	// ErrClosed: Feed or a second Close after the matcher was closed.
	ErrClosed = errors.New("streammatch: closed")
)

// Report is one representative match: the entry of a hit segment with the
// smallest Dist (ties broken by smallest End) whose Start does not overlap
// an already reported match (Start >= lastEnd).
type Report struct {
	End   int // end position (exclusive): End bytes consumed
	Dist  int // D(End), always <= k
	Start int // leftmost start achieving Dist
}

// Stats is a snapshot of matcher counters.
type Stats struct {
	BytesConsumed uint64 // bytes consumed so far
	Reports       uint64 // reported matches
	Suppressed    uint64 // suppressed representatives
}

// Matcher is a streaming approximate substring matcher. Feed, Close and
// Stats may be called concurrently; the result equals some serial order.
type Matcher struct {
	mu  sync.Mutex
	pat []byte
	m   int
	k   int

	// Sellers DP: two columns of (dist, leftmost start), O(m) space.
	prevDist  []int
	prevStart []int
	currDist  []int
	currStart []int

	pos int // bytes consumed so far, i.e. current end position e

	// Representative of the currently open hit segment: O(1) state,
	// independent of segment length.
	segOpen  bool
	repDist  int
	repEnd   int
	repStart int

	lastEnd    int // max End of reported matches, initially 0
	reports    uint64
	suppressed uint64

	cellUpdates uint64 // DP cell updates, exactly m * bytes consumed

	closed bool
}

// NewMatcher builds a matcher for pattern P (length 1..64) and threshold
// k (0..m-1). ErrInvalidPattern takes precedence over ErrInvalidK.
func NewMatcher(pattern []byte, k int) (*Matcher, error) {
	m := len(pattern)
	if m == 0 || m > 64 {
		return nil, ErrInvalidPattern
	}
	if k < 0 || k >= m {
		return nil, ErrInvalidK
	}
	pat := make([]byte, m)
	copy(pat, pattern)
	mt := &Matcher{
		pat:       pat,
		m:         m,
		k:         k,
		prevDist:  make([]int, m+1),
		prevStart: make([]int, m+1),
		currDist:  make([]int, m+1),
		currStart: make([]int, m+1),
	}
	// Row 0: empty text prefix, Lev(P[:j], "") = j with start 0.
	for j := 0; j <= m; j++ {
		mt.prevDist[j] = j
		mt.prevStart[j] = 0
	}
	return mt, nil
}

// advance consumes one byte and pushes the DP one column forward (exactly
// m cell updates); each cell also tracks the leftmost start of its optimum.
func (mt *Matcher) advance(c byte) {
	mt.pos++
	mt.currDist[0] = 0
	mt.currStart[0] = mt.pos // empty pattern vs empty suffix: start here
	for j := 1; j <= mt.m; j++ {
		// Delete text char: from previous row, same column.
		best := mt.prevDist[j] + 1
		start := mt.prevStart[j]
		// Substitute or match: from the diagonal.
		cost := 0
		if c != mt.pat[j-1] {
			cost = 1
		}
		if d := mt.prevDist[j-1] + cost; d < best || (d == best && mt.prevStart[j-1] < start) {
			best = d
			start = mt.prevStart[j-1]
		}
		// Insert pattern char: from the left cell of this row.
		if d := mt.currDist[j-1] + 1; d < best || (d == best && mt.currStart[j-1] < start) {
			best = d
			start = mt.currStart[j-1]
		}
		mt.currDist[j] = best
		mt.currStart[j] = start
	}
	mt.cellUpdates += uint64(mt.m)
	mt.prevDist, mt.currDist = mt.currDist, mt.prevDist
	mt.prevStart, mt.currStart = mt.currStart, mt.prevStart
}

// finishSegment emits the segment representative: a representative whose
// Start is below lastEnd is suppressed (Suppressed += 1, lastEnd kept);
// Start == lastEnd counts as adjacent, not overlapping, and is kept.
func (mt *Matcher) finishSegment() (Report, bool) {
	r := Report{End: mt.repEnd, Dist: mt.repDist, Start: mt.repStart}
	if r.Start < mt.lastEnd {
		mt.suppressed++
		return Report{}, false
	}
	mt.lastEnd = r.End
	mt.reports++
	return r, true
}

// Feed consumes one chunk of the byte stream (an empty chunk is legal and
// changes nothing) and returns the reports of the segments that end inside
// this chunk, ordered by ascending End.
func (mt *Matcher) Feed(chunk []byte) ([]Report, error) {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	if mt.closed {
		return nil, ErrClosed
	}
	var out []Report
	for _, c := range chunk {
		mt.advance(c)
		d := mt.prevDist[mt.m]
		if d <= mt.k {
			// Hit end position: open a segment or lower its
			// representative. Ties keep the earlier End because
			// positions arrive in increasing order.
			if !mt.segOpen || d < mt.repDist {
				mt.segOpen = true
				mt.repDist = d
				mt.repEnd = mt.pos
				mt.repStart = mt.prevStart[mt.m]
			}
		} else if mt.segOpen {
			mt.segOpen = false
			if r, ok := mt.finishSegment(); ok {
				out = append(out, r)
			}
		}
	}
	return out, nil
}

// Close ends the stream: a still-open segment ends now and yields its
// report (if any). The first call succeeds; later calls return ErrClosed
// without changing state.
func (mt *Matcher) Close() ([]Report, error) {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	if mt.closed {
		return nil, ErrClosed
	}
	mt.closed = true
	var out []Report
	if mt.segOpen {
		mt.segOpen = false
		if r, ok := mt.finishSegment(); ok {
			out = append(out, r)
		}
	}
	return out, nil
}

// Stats returns a counter snapshot; safe to call concurrently with Feed.
func (mt *Matcher) Stats() Stats {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	return Stats{
		BytesConsumed: uint64(mt.pos),
		Reports:       mt.reports,
		Suppressed:    mt.suppressed,
	}
}
