package otdoc

import (
	"errors"
	"sync"
)

// CompKind identifies the kind of an operation component.
type CompKind int

const (
	Retain CompKind = iota
	Insert
	Delete
)

// Comp is one component of an operation. For Retain/Delete N is a positive
// rune count; for Insert Text is a non-empty valid UTF-8 string.
type Comp struct {
	Kind CompKind
	N    int
	Text string
}

// Op is a sequence of components.
type Op []Comp

// Result is the recorded outcome of an accepted submission.
type Result struct {
	Rev     int
	Applied bool
	Op      Op
}

// Sentinel errors returned by Doc methods.
var (
	ErrInvalid  = errors.New("otdoc: invalid argument")
	ErrStaleSeq = errors.New("otdoc: stale sequence number")
	ErrSeqGap   = errors.New("otdoc: sequence gap")
	ErrFuture   = errors.New("otdoc: base revision in the future")
	ErrTooOld   = errors.New("otdoc: base revision below floor")
	ErrLength   = errors.New("otdoc: operation base length mismatch")
	ErrTooLarge = errors.New("otdoc: resulting document exceeds MaxLen")
	ErrBadFloor = errors.New("otdoc: invalid compact floor")
	ErrBadRange = errors.New("otdoc: invalid history range")
)

// Doc is a concurrent-safe server-side text document supporting operational
// transformation with revision history and per-site sequence deduplication.
type Doc struct {
	mu     sync.Mutex
	maxLen int
	rev    int
	floor  int
	doc    string
	// lens[k] is the document length (runes) at revision floor+k.
	lens []int
	// history[k] transforms revision floor+k into floor+k+1.
	history []Op
	sites   map[string]*siteState
}

type siteState struct {
	last   int
	result *Result
}

// New creates an empty document with the given rune-count capacity.
func New(maxLen int) (*Doc, error) {
	if maxLen < 1 || maxLen > 1_000_000 {
		return nil, ErrInvalid
	}
	d := &Doc{
		maxLen: maxLen,
		lens:   []int{0},
		sites:  map[string]*siteState{},
	}
	return d, nil
}

// Rev returns the current revision number.
func (d *Doc) Rev() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.rev
}

// Floor returns the oldest revision still present in history.
func (d *Doc) Floor() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.floor
}

// Text returns the current document contents.
func (d *Doc) Text() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.doc
}

// Submit transforms op based at baseRev from (site, seq) onto the head and
// applies it. Replays return the recorded result verbatim.
func (d *Doc) Submit(site string, seq, baseRev int, op Op) (Result, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	// Priority 1: invalid parameters.
	if site == "" || seq < 1 || baseRev < 0 {
		return Result{}, ErrInvalid
	}
	canon, err := normalize(op)
	if err != nil {
		return Result{}, ErrInvalid
	}

	st := d.sites[site]
	// Priority 2: sequence-number class.
	if st != nil && seq <= st.last {
		if seq == st.last {
			// Duplicate: return the recorded result verbatim.
			res := *st.result
			res.Op = cloneOp(st.result.Op)
			return res, nil
		}
		return Result{}, ErrStaleSeq
	}
	if st == nil {
		if seq != 1 {
			return Result{}, ErrSeqGap
		}
	} else if seq > st.last+1 {
		return Result{}, ErrSeqGap
	}

	// Priority 3: revision window.
	if baseRev > d.rev {
		return Result{}, ErrFuture
	}
	if baseRev < d.floor {
		return Result{}, ErrTooOld
	}

	// Priority 4: base length must cover the base revision document.
	if baseLength(canon) != d.lens[baseRev-d.floor] {
		return Result{}, ErrLength
	}

	// Transform against every revision committed after baseRev. The client
	// intent is kept as a per-position document across all hops (insert
	// anchors survive canonical Insert/Delete reordering); it is rendered
	// back to a canonical Op only after the final hop.
	xd := buildXDoc(canon, baseLength(canon))
	for k := baseRev - d.floor; k < len(d.history); k++ {
		var steps int
		xd, steps = xformOnce(d.history[k], xd)
		transformStepBound++
		assertStepBound(len(d.history[k]), len(canon), steps)
	}
	xformed := xdocToOp(xd)

	// Priority 5: size is measured on the transformed operation.
	newLen := resultLength(d.lens[len(d.lens)-1], xformed)
	if newLen > d.maxLen {
		return Result{}, ErrTooLarge
	}

	res := Result{Rev: d.rev, Op: cloneOp(xformed)}
	if !isNoOp(xformed) {
		d.doc = apply(d.doc, xformed)
		d.rev++
		d.lens = append(d.lens, newLen)
		d.history = append(d.history, cloneOp(xformed))
		res.Rev = d.rev
		res.Applied = true
	}

	// Accepted (applied or not): advance the site sequence and record the
	// result so a later duplicate can reproduce it exactly.
	if st == nil {
		st = &siteState{}
		d.sites[site] = st
	}
	st.last = seq
	st.result = &Result{Rev: res.Rev, Applied: res.Applied, Op: cloneOp(res.Op)}

	return res, nil
}

// Compact discards history below newFloor.
func (d *Doc) Compact(newFloor int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if newFloor < d.floor || newFloor > d.rev {
		return ErrBadFloor
	}
	drop := newFloor - d.floor
	d.history = append([]Op(nil), d.history[drop:]...)
	d.lens = append([]int(nil), d.lens[drop:]...)
	d.floor = newFloor
	return nil
}

// History returns the canonical operations transforming revisions
// [from, to); from must be >= floor and to <= rev.
func (d *Doc) History(from, to int) ([]Op, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if from < d.floor {
		return nil, ErrTooOld
	}
	if from > to || to > d.rev {
		return nil, ErrBadRange
	}
	out := make([]Op, 0, to-from)
	for k := from - d.floor; k < to-d.floor; k++ {
		out = append(out, cloneOp(d.history[k]))
	}
	return out, nil
}
