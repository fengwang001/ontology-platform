// Package tracker implements a collaborative-editing anchor tracker with
// bias-aware point/range anchors, revision history, and checkpoint
// compaction.
package tracker

import (
	"errors"
	"sync"
)

// Bias selects which side of a gap a point anchor clings to.
type Bias int

const (
	// Left attaches the anchor to the character on its left.
	Left Bias = iota
	// Right attaches the anchor to the character on its right.
	Right
)

// RangeKind selects the endpoint biases of a range anchor.
type RangeKind int

const (
	// Tight ranges have a Right-biased start and a Left-biased end.
	Tight RangeKind = iota
	// Loose ranges have a Left-biased start and a Right-biased end.
	Loose
)

var (
	ErrInvalid    = errors.New("tracker: invalid edit")
	ErrTooLarge   = errors.New("tracker: document exceeds MaxLen")
	ErrOutOfRange = errors.New("tracker: position out of range")
	ErrTooMany    = errors.New("tracker: too many anchors")
	ErrNoAnchor   = errors.New("tracker: no such anchor")
	ErrWrongKind  = errors.New("tracker: anchor kind mismatch")
	ErrFuture     = errors.New("tracker: revision is in the future")
	ErrNotYet     = errors.New("tracker: anchor not yet created at revision")
	ErrCompacted  = errors.New("tracker: revision has been compacted")
	ErrBadFloor   = errors.New("tracker: bad compaction floor")
)

type editKind int

const (
	editReplace editKind = iota
	editMove
)

type edit struct {
	kind    editKind
	p, d, n int // replace: delete d at p, insert n
	length  int // move: segment length
	q       int // move: target gap
}

// mapPointReplace maps a point across Replace(p, d, n).
func mapPointReplace(x int, b Bias, p, d, n int) int {
	switch {
	case x < p:
		return x
	case x > p+d:
		return x - d + n
	default: // p <= x <= p+d
		if b == Left {
			return p
		}
		return p + n
	}
}

// mapPointMove maps a point across Move(p, length, q).
func mapPointMove(x int, b Bias, p, length, q int) int {
	qp := q
	if q > p+length {
		qp = q - length
	}
	moved := (x > p && x < p+length) ||
		(x == p && b == Right) ||
		(x == p+length && b == Left)
	if moved {
		return x - p + qp
	}
	y := x
	if x >= p+length {
		y = x - length
	}
	switch {
	case y < qp:
		return y
	case y > qp:
		return y + length
	default: // y == qp
		if b == Left {
			return qp
		}
		return qp + length
	}
}

func (e edit) mapPoint(x int, b Bias) int {
	if e.kind == editReplace {
		return mapPointReplace(x, b, e.p, e.d, e.n)
	}
	return mapPointMove(x, b, e.p, e.length, e.q)
}

// endpointBiases returns the start/end biases implied by a range kind.
func endpointBiases(kind RangeKind) (Bias, Bias) {
	if kind == Tight {
		return Right, Left
	}
	return Left, Right
}

// mapRange maps both endpoints of [s, e) and collapses if they cross.
func (e edit) mapRange(s, en int, kind RangeKind) (int, int) {
	sb, eb := endpointBiases(kind)
	ns := e.mapPoint(s, sb)
	ne := e.mapPoint(en, eb)
	if ns > ne {
		ns = ne
	}
	return ns, ne
}

type anchorKind int

const (
	anchorPoint anchorKind = iota
	anchorRange
)

type anchor struct {
	kind          anchorKind
	createdRev    int
	checkpointRev int
	// point position at checkpointRev
	pos  int
	bias Bias
	// range position at checkpointRev
	s, e  int
	rkind RangeKind
}

// Tracker tracks anchors across a revisioned edit history. All methods are
// safe for concurrent use; results equal some serial order.
type Tracker struct {
	mu         sync.Mutex
	maxLen     int
	maxAnchors int
	rev        int
	floor      int
	length     int
	baseRev    int // edits[i] carries revision baseRev+i+1
	edits      []edit
	anchors    map[int]*anchor
	nextID     int
	replayed   int
}

// NewTracker creates a tracker for an initial document of length n0
// (0..1e6), a maximum length maxLen >= n0, and an anchor cap maxAnchors >= 1.
func NewTracker(n0, maxLen, maxAnchors int) *Tracker {
	return &Tracker{
		maxLen:     maxLen,
		maxAnchors: maxAnchors,
		length:     n0,
		anchors:    make(map[int]*anchor),
	}
}

// Rev returns the current revision.
func (t *Tracker) Rev() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.rev
}

// Len returns the current document length.
func (t *Tracker) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.length
}

// Floor returns the current compaction floor.
func (t *Tracker) Floor() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.floor
}

// Replace deletes d characters at p and inserts n, returning the new rev.
func (t *Tracker) Replace(p, d, n int) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if p < 0 || d < 0 || n < 0 || d+n < 1 || p+d > t.length {
		return 0, ErrInvalid
	}
	if t.length-d+n > t.maxLen {
		return 0, ErrTooLarge
	}
	t.edits = append(t.edits, edit{kind: editReplace, p: p, d: d, n: n})
	t.length = t.length - d + n
	t.rev++
	return t.rev, nil
}

// Move relocates [p, p+length) to gap q, returning the new rev.
func (t *Tracker) Move(p, length, q int) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if length < 1 || p < 0 || p+length > t.length ||
		q < 0 || q > t.length || (q >= p && q <= p+length) {
		return 0, ErrInvalid
	}
	t.edits = append(t.edits, edit{kind: editMove, p: p, length: length, q: q})
	t.rev++
	return t.rev, nil
}

// AddPoint creates a point anchor at pos with the given bias.
func (t *Tracker) AddPoint(pos int, bias Bias) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if pos < 0 || pos > t.length {
		return 0, ErrOutOfRange
	}
	if len(t.anchors) >= t.maxAnchors {
		return 0, ErrTooMany
	}
	t.nextID++
	t.anchors[t.nextID] = &anchor{
		kind:          anchorPoint,
		createdRev:    t.rev,
		checkpointRev: t.rev,
		pos:           pos,
		bias:          bias,
	}
	return t.nextID, nil
}

// AddRange creates a range anchor [s, e) of the given kind.
func (t *Tracker) AddRange(s, e int, kind RangeKind) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if s < 0 || e < 0 || s > e || e > t.length {
		return 0, ErrOutOfRange
	}
	if len(t.anchors) >= t.maxAnchors {
		return 0, ErrTooMany
	}
	t.nextID++
	t.anchors[t.nextID] = &anchor{
		kind:          anchorRange,
		createdRev:    t.rev,
		checkpointRev: t.rev,
		s:             s,
		e:             e,
		rkind:         kind,
	}
	return t.nextID, nil
}

// Remove deletes an anchor; later queries report ErrNoAnchor.
func (t *Tracker) Remove(id int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.anchors[id]; !ok {
		return ErrNoAnchor
	}
	delete(t.anchors, id)
	return nil
}

// lookup validates a query against the rejection priority order:
// ErrNoAnchor > ErrWrongKind > ErrFuture > ErrNotYet > ErrCompacted.
func (t *Tracker) lookup(id, asRev int, want anchorKind) (*anchor, error) {
	a, ok := t.anchors[id]
	if !ok {
		return nil, ErrNoAnchor
	}
	if a.kind != want {
		return nil, ErrWrongKind
	}
	if asRev > t.rev {
		return nil, ErrFuture
	}
	if asRev < a.createdRev {
		return nil, ErrNotYet
	}
	if asRev < a.checkpointRev {
		return nil, ErrCompacted
	}
	return a, nil
}

// Pos returns the position of a point anchor at revision asRev.
func (t *Tracker) Pos(id, asRev int) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	a, err := t.lookup(id, asRev, anchorPoint)
	if err != nil {
		return 0, err
	}
	x := a.pos
	for r := a.checkpointRev + 1; r <= asRev; r++ {
		x = t.edits[r-t.baseRev-1].mapPoint(x, a.bias)
	}
	t.replayed += asRev - a.checkpointRev
	return x, nil
}

// Range returns (s, e, collapsed) of a range anchor at revision asRev.
func (t *Tracker) Range(id, asRev int) (int, int, bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	a, err := t.lookup(id, asRev, anchorRange)
	if err != nil {
		return 0, 0, false, err
	}
	s, e := a.s, a.e
	for r := a.checkpointRev + 1; r <= asRev; r++ {
		s, e = t.edits[r-t.baseRev-1].mapRange(s, e, a.rkind)
	}
	t.replayed += asRev - a.checkpointRev
	return s, e, s == e, nil
}

// Compact advances the floor to newFloor, materializing anchors whose
// checkpoint predates newFloor and dropping older edits.
func (t *Tracker) Compact(newFloor int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if newFloor < t.floor || newFloor > t.rev {
		return ErrBadFloor
	}
	for _, a := range t.anchors {
		if a.checkpointRev >= newFloor {
			continue
		}
		if a.kind == anchorPoint {
			x := a.pos
			for r := a.checkpointRev + 1; r <= newFloor; r++ {
				x = t.edits[r-t.baseRev-1].mapPoint(x, a.bias)
			}
			a.pos = x
		} else {
			s, e := a.s, a.e
			for r := a.checkpointRev + 1; r <= newFloor; r++ {
				s, e = t.edits[r-t.baseRev-1].mapRange(s, e, a.rkind)
			}
			a.s, a.e = s, e
		}
		t.replayed += newFloor - a.checkpointRev
		a.checkpointRev = newFloor
	}
	// Drop edits with revision < newFloor; edits[i] has rev baseRev+i+1.
	if drop := newFloor - t.baseRev - 1; drop > 0 {
		t.edits = append([]edit(nil), t.edits[drop:]...)
		t.baseRev += drop
	}
	t.floor = newFloor
	return nil
}
