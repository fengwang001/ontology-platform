package otdoc

import "sync"

// Result is the outcome of an accepted Submit.
type Result struct {
	Rev     int       // revision after the submit was processed
	Applied bool      // false if the transformed operation was a no-op
	Op      Operation // transformed operation in canonical form
}

type siteState struct {
	lastSeq int
	res     Result
	hasRes  bool
}

// Document is a server-side OT document with revision history and per-site
// sequence deduplication. All methods are safe for concurrent use.
type Document struct {
	mu      sync.Mutex
	maxLen  int
	doc     []rune
	rev     int
	floor   int
	history []Operation // history[i] transforms revision floor+i into floor+i+1
	lens    []int       // lens[i] is the document length in runes at revision floor+i
	sites   map[string]*siteState
}

// New creates an empty document (rev=0, floor=0) with the given maximum
// document length in runes. maxLen must be in [1, 1e6].
func New(maxLen int) (*Document, error) {
	if maxLen < 1 || maxLen > 1_000_000 {
		return nil, ErrInvalid
	}
	return &Document{
		maxLen: maxLen,
		lens:   []int{0},
		sites:  make(map[string]*siteState),
	}, nil
}

// Submit transforms op (based on baseRev) against history[baseRev..rev) and
// applies it. It returns the recorded Result for duplicate submissions
// (seq == last accepted seq for the site) without changing any state.
//
// Rejections are reported with a fixed priority, first match wins:
// invalid arguments (ErrInvalid) > sequence class (duplicate result,
// ErrStaleSeq, ErrSeqGap) > ErrFuture > ErrTooOld > ErrLength > ErrTooLarge.
// A rejected submit changes neither rev, history, seq nor the document.
func (d *Document) Submit(site string, seq int, baseRev int, op Operation) (Result, error) {
	if site == "" || seq < 1 || baseRev < 0 {
		return Result{}, ErrInvalid
	}
	if err := validate(op); err != nil {
		return Result{}, err
	}
	norm := Normalize(op)

	d.mu.Lock()
	defer d.mu.Unlock()

	st := d.sites[site]
	if st == nil {
		st = &siteState{}
		d.sites[site] = st
	}
	switch {
	case seq == st.lastSeq && st.hasRes:
		return cloneResult(st.res), nil
	case seq < st.lastSeq:
		return Result{}, ErrStaleSeq
	case seq > st.lastSeq+1:
		return Result{}, ErrSeqGap
	}

	if baseRev > d.rev {
		return Result{}, ErrFuture
	}
	if baseRev < d.floor {
		return Result{}, ErrTooOld
	}
	if baseLen(norm) != d.lens[baseRev-d.floor] {
		return Result{}, ErrLength
	}

	transformed := norm
	for k := baseRev; k < d.rev; k++ {
		transformed, _ = transform(transformed, d.history[k-d.floor])
	}
	transformed = Normalize(transformed)

	newLen := d.lens[d.rev-d.floor] + insertRunes(transformed) - deleteRunes(transformed)
	if newLen > d.maxLen {
		return Result{}, ErrTooLarge
	}

	res := Result{Rev: d.rev, Applied: false, Op: cloneOp(transformed)}
	if !isNoop(transformed) {
		d.doc = apply(d.doc, transformed)
		d.rev++
		d.history = append(d.history, cloneOp(transformed))
		d.lens = append(d.lens, len(d.doc))
		res.Rev = d.rev
		res.Applied = true
	}
	st.lastSeq = seq
	st.res = cloneResult(res)
	st.hasRes = true
	return res, nil
}

// Doc returns the current document content.
func (d *Document) Doc() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return string(d.doc)
}

// Rev returns the current revision.
func (d *Document) Rev() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.rev
}

// Floor returns the current history floor.
func (d *Document) Floor() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.floor
}

// Len returns the current document length in runes.
func (d *Document) Len() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.doc)
}

// Compact drops history for revisions below newFloor. It succeeds only when
// floor <= newFloor <= rev. Submissions with baseRev == floor remain valid,
// and per-site dedup results survive compaction.
func (d *Document) Compact(newFloor int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if newFloor < d.floor || newFloor > d.rev {
		return ErrBadFloor
	}
	drop := newFloor - d.floor
	d.history = append([]Operation(nil), d.history[drop:]...)
	d.lens = append([]int(nil), d.lens[drop:]...)
	d.floor = newFloor
	return nil
}

// History returns the stored operations transforming revisions [from, to).
// from < floor yields ErrTooOld; from > to or to > rev yields ErrBadRange
// (from > to is checked first).
func (d *Document) History(from, to int) ([]Operation, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if from < d.floor {
		return nil, ErrTooOld
	}
	if from > to || to > d.rev {
		return nil, ErrBadRange
	}
	out := make([]Operation, to-from)
	for i := range out {
		out[i] = cloneOp(d.history[from-d.floor+i])
	}
	return out, nil
}

func cloneResult(r Result) Result {
	r.Op = cloneOp(r.Op)
	return r
}
