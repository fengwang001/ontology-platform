package anchor

import (
	"errors"
	"sync"
)

var (
	ErrInvalid    = errors.New("invalid edit")
	ErrTooLarge   = errors.New("document too large")
	ErrOutOfRange = errors.New("position out of range")
	ErrTooMany    = errors.New("too many anchors")
	ErrNoAnchor   = errors.New("anchor does not exist")
	ErrWrongKind  = errors.New("wrong anchor kind")
	ErrFuture     = errors.New("revision is in the future")
	ErrNotYet     = errors.New("revision predates anchor creation")
	ErrCompacted  = errors.New("revision was compacted")
	ErrBadFloor   = errors.New("bad compaction floor")
)

type Bias int

const (
	Left Bias = iota
	Right
)

type RangeKind int

const (
	Tight RangeKind = iota
	Loose
)

type anchorKind int

const (
	pointAnchor anchorKind = iota
	rangeAnchor
)

type editKind int

const (
	replaceEdit editKind = iota
	moveEdit
)

type edit struct {
	kind editKind
	p    int
	d    int
	n    int
	len  int
	q    int
}

type anchorState struct {
	kind      anchorKind
	bias      Bias
	rangeKind RangeKind
	createdAt int
	floorRev  int
	point     int
	start     int
	end       int
	removed   bool
}

type Tracker struct {
	mu         sync.RWMutex
	replayMu   sync.Mutex
	length     int
	maxLength  int
	maxAnchors int
	rev        int
	floor      int
	nextID     int
	live       int
	edits      []edit
	anchors    map[int]*anchorState
	replayed   int
}

func New(initialLength, maxLength, maxAnchors int) *Tracker {
	return &Tracker{
		length:     initialLength,
		maxLength:  maxLength,
		maxAnchors: maxAnchors,
		anchors:    make(map[int]*anchorState),
	}
}

func (t *Tracker) Replace(position, deleted, inserted int) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if position < 0 || deleted < 0 || inserted < 0 || deleted+inserted == 0 ||
		position+deleted > t.length {
		return t.rev, ErrInvalid
	}
	newLength := t.length - deleted + inserted
	if newLength > t.maxLength {
		return t.rev, ErrTooLarge
	}

	t.edits = append(t.edits, edit{
		kind: replaceEdit,
		p:    position,
		d:    deleted,
		n:    inserted,
	})
	t.length = newLength
	t.rev++
	return t.rev, nil
}

func (t *Tracker) Move(position, length, destination int) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if length < 1 || position < 0 || position+length > t.length ||
		destination < 0 || destination > t.length ||
		!(destination < position || destination > position+length) {
		return t.rev, ErrInvalid
	}

	t.edits = append(t.edits, edit{
		kind: moveEdit,
		p:    position,
		len:  length,
		q:    destination,
	})
	t.rev++
	return t.rev, nil
}

func (t *Tracker) AddPoint(position int, bias Bias) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if position < 0 || position > t.length {
		return 0, ErrOutOfRange
	}
	if t.live >= t.maxAnchors {
		return 0, ErrTooMany
	}
	return t.addAnchor(&anchorState{
		kind:      pointAnchor,
		bias:      bias,
		createdAt: t.rev,
		floorRev:  t.rev,
		point:     position,
	}), nil
}

func (t *Tracker) AddRange(start, end int, kind RangeKind) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if start < 0 || end < 0 || start > t.length || end > t.length || start > end {
		return 0, ErrOutOfRange
	}
	if t.live >= t.maxAnchors {
		return 0, ErrTooMany
	}
	return t.addAnchor(&anchorState{
		kind:      rangeAnchor,
		rangeKind: kind,
		createdAt: t.rev,
		floorRev:  t.rev,
		start:     start,
		end:       end,
	}), nil
}

func (t *Tracker) Remove(id int) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	a := t.anchors[id]
	if a == nil || a.removed {
		return ErrNoAnchor
	}
	a.removed = true
	t.live--
	return nil
}

func (t *Tracker) Pos(id, asRev int) (int, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	a, count, err := t.prepareQuery(id, asRev, pointAnchor)
	if err != nil {
		return 0, err
	}
	result := a.point
	t.setReplayed(count)
	replayFromCheckpoint(a, asRev, t.edits, t.floor, func(ed edit) {
		result = mapPoint(ed, result, a.bias)
	})
	return result, nil
}

func (t *Tracker) Range(id, asRev int) (start, end int, collapsed bool, err error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	a, count, err := t.prepareQuery(id, asRev, rangeAnchor)
	if err != nil {
		return 0, 0, false, err
	}
	start, end = a.start, a.end
	t.setReplayed(count)
	replayFromCheckpoint(a, asRev, t.edits, t.floor, func(ed edit) {
		startBias, endBias := rangeBiases(a.rangeKind)
		start = mapPoint(ed, start, startBias)
		end = mapPoint(ed, end, endBias)
		if start > end {
			start = end
		}
	})
	return start, end, start == end, nil
}

func (t *Tracker) Compact(newFloor int) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if newFloor < t.floor || newFloor > t.rev {
		return ErrBadFloor
	}

	t.replayed = 0
	for _, a := range t.anchors {
		if a.removed || a.floorRev >= newFloor {
			continue
		}
		t.replayed += newFloor - a.floorRev
		t.materialize(a, newFloor)
	}

	drop := newFloor - t.floor
	t.edits = append([]edit(nil), t.edits[drop:]...)
	t.floor = newFloor
	return nil
}

func (t *Tracker) Rev() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.rev
}

func (t *Tracker) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.length
}

func (t *Tracker) Floor() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.floor
}

func (t *Tracker) addAnchor(a *anchorState) int {
	t.nextID++
	t.anchors[t.nextID] = a
	t.live++
	return t.nextID
}

func (t *Tracker) prepareQuery(id, asRev int, wanted anchorKind) (*anchorState, int, error) {
	a := t.anchors[id]
	if a == nil || a.removed {
		return nil, 0, ErrNoAnchor
	}
	if a.kind != wanted {
		return nil, 0, ErrWrongKind
	}
	if asRev > t.rev {
		return nil, 0, ErrFuture
	}
	if asRev < a.createdAt {
		return nil, 0, ErrNotYet
	}
	if asRev < t.floor {
		return nil, 0, ErrCompacted
	}
	return cloneAnchor(a), asRev - a.floorRev, nil
}

func (t *Tracker) setReplayed(count int) {
	t.replayMu.Lock()
	t.replayed = count
	t.replayMu.Unlock()
}

func (t *Tracker) materialize(a *anchorState, targetRev int) {
	start := a.floorRev - t.floor
	end := targetRev - t.floor
	for _, ed := range t.edits[start:end] {
		if a.kind == pointAnchor {
			a.point = mapPoint(ed, a.point, a.bias)
			continue
		}
		startBias, endBias := rangeBiases(a.rangeKind)
		a.start = mapPoint(ed, a.start, startBias)
		a.end = mapPoint(ed, a.end, endBias)
		if a.start > a.end {
			a.start = a.end
		}
	}
	a.floorRev = targetRev
}

func cloneAnchor(a *anchorState) *anchorState {
	copyValue := *a
	return &copyValue
}

func replayFromCheckpoint(a *anchorState, asRev int, edits []edit, floor int, apply func(edit)) {
	start := a.floorRev - floor
	end := asRev - floor
	for _, ed := range edits[start:end] {
		apply(ed)
	}
}

func rangeBiases(kind RangeKind) (Bias, Bias) {
	if kind == Tight {
		return Right, Left
	}
	return Left, Right
}

func mapPoint(ed edit, position int, bias Bias) int {
	if ed.kind == replaceEdit {
		switch {
		case position < ed.p:
			return position
		case position > ed.p+ed.d:
			return position - ed.d + ed.n
		case bias == Left:
			return ed.p
		default:
			return ed.p + ed.n
		}
	}

	destination := ed.q
	if destination > ed.p+ed.len {
		destination -= ed.len
	}
	inMoved := position > ed.p && position < ed.p+ed.len
	if position == ed.p {
		inMoved = bias == Right
	}
	if position == ed.p+ed.len {
		inMoved = bias == Left
	}
	if inMoved {
		return position - ed.p + destination
	}

	mapped := position
	if position >= ed.p+ed.len {
		mapped = position - ed.len
	}
	switch {
	case mapped < destination:
		return mapped
	case mapped > destination:
		return mapped + ed.len
	case bias == Left:
		return destination
	default:
		return destination + ed.len
	}
}
