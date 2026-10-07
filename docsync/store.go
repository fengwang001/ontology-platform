package docsync

import (
	"fmt"
	"io"
	"sort"
	"sync"
)

// Store is a thread-safe versioned document with migrating diagnostics.
// Accepted operations are serialized under mu; reads take the read lock and
// observe one atomic snapshot, so concurrent stale changes have exactly one
// winner and queries never tear.
type Store struct {
	mu      sync.RWMutex
	version int64
	text    *rope
	diags   *diagSet
	gone    *invalidList
	regSeq  int64

	log      io.Writer
	counters counters
}

// Option configures a Store.
type Option func(*Store)

// WithLogger records every input, output and decision rationale to w.
func WithLogger(w io.Writer) Option {
	return func(st *Store) { st.log = w }
}

// NewStore creates a store holding text at version 0.
func NewStore(text string, opts ...Option) *Store {
	st := &Store{version: 0}
	for _, o := range opts {
		o(st)
	}
	st.text = newRope(text, st.counters.line)
	st.diags = newDiagSet(st.counters.diag)
	st.gone = newInvalidList()
	st.logf("NewStore", "initialText=%q version=0", text)
	return st
}

func (st *Store) logf(op, format string, args ...any) {
	if st.log != nil {
		fmt.Fprintf(st.log, "[%s] %s\n", op, fmt.Sprintf(format, args...))
	}
}

// Version returns the current version.
func (st *Store) Version() int64 {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.version
}

// Text returns the current text.
func (st *Store) Text() string {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.text.string()
}

type parsedEdit struct {
	idx       int
	s, e      int
	text      string
	textLen16 int
}

// validateRange converts a range to UTF-16 offsets with protocol precedence:
// stale is handled by callers; reversed > out-of-bounds > surrogate split.
func (st *Store) rangeOffsets(r Range, idx int) (s, e int, err error) {
	if posLess(r.End, r.Start) {
		return 0, 0, reject(RejectReversedRange, idx, r.Start,
			fmt.Sprintf("range start %v is after end %v", r.Start, r.End))
	}
	so, ss, sok := st.text.offsetOf(r.Start)
	eo, es, eok := st.text.offsetOf(r.End)
	if !sok {
		if ss {
			return 0, 0, reject(RejectSurrogateSplit, idx, r.Start,
				fmt.Sprintf("start %v splits an astral character", r.Start))
		}
		return 0, 0, reject(RejectOutOfBounds, idx, r.Start,
			fmt.Sprintf("start %v is out of bounds", r.Start))
	}
	if !eok {
		if es {
			return 0, 0, reject(RejectSurrogateSplit, idx, r.End,
				fmt.Sprintf("end %v splits an astral character", r.End))
		}
		return 0, 0, reject(RejectOutOfBounds, idx, r.End,
			fmt.Sprintf("end %v is out of bounds", r.End))
	}
	return so, eo, nil
}

func posLess(a, b Position) bool {
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Character < b.Character
}

// Apply validates and commits a batch of simultaneous-coordinate edits.
func (st *Store) Apply(baseVersion int64, edits []Edit) (int64, error) {
	st.mu.Lock()
	defer st.mu.Unlock()

	if baseVersion != st.version {
		err := reject(RejectStale, -1, Position{},
			fmt.Sprintf("base version %d != current %d", baseVersion, st.version))
		st.logf("Apply", "REJECT %s edits=%d", err, len(edits))
		return st.version, err
	}

	parsed := make([]parsedEdit, 0, len(edits))
	for i, ed := range edits {
		s, e, err := st.rangeOffsets(ed.Range, i)
		if err != nil {
			st.logf("Apply", "REJECT edit#%d %s: %+v -> %q", i, err, ed.Range, ed.Text)
			return st.version, err
		}
		parsed = append(parsed, parsedEdit{
			idx: i, s: s, e: e, text: ed.Text, textLen16: utf16Len(ed.Text),
		})
	}

	if err := checkOverlap(parsed); err != nil {
		st.logf("Apply", "REJECT %s", err)
		return st.version, err
	}

	ordered := make([]parsedEdit, len(parsed))
	copy(ordered, parsed)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].s != ordered[j].s {
			return ordered[i].s < ordered[j].s
		}
		return ordered[i].idx < ordered[j].idx
	})

	// Apply at shifted coordinates: edits are non-overlapping and ordered, so
	// the live offset of boundary i is its old offset plus the net length
	// change produced by edits before it.
	// Text edits apply at shifted live coordinates; diagnostics migrate once
	// against original simultaneous coordinates (independent endpoint mapping).
	cumShift := 0
	for _, pe := range ordered {
		ms, me := pe.s+cumShift, pe.e+cumShift
		st.text.replace(ms, me, pe.text)
		cumShift += pe.textLen16 - (pe.e - pe.s)
	}
	batch := make([]batchEdit, 0, len(ordered))
	for _, pe := range ordered {
		batch = append(batch, batchEdit{s: pe.s, e: pe.e, L: pe.textLen16})
		if false {
			println("BATCH", pe.s, pe.e, pe.textLen16)
		}
	}
	invalidated := st.diags.applyBatch(batch)
	st.version++
	newVersion := st.version
	for _, n := range invalidated {
		st.gone.add(InvalidatedEntry{
			Diagnostic:         n.diag,
			RegisteredAt:       int64(n.id),
			InvalidatedVersion: newVersion,
		})
	}
	if st.log != nil {
		st.logf("Apply", "ACCEPT base=%d version=%d edits=%d invalidated=%d text=%q",
			baseVersion, newVersion, len(edits), len(invalidated), st.text.string())
	}
	return newVersion, nil
}

// checkOverlap enforces positive-length disjoint intervals. Zero-length edits
// never overlap; touching intervals are allowed.
func checkOverlap(edits []parsedEdit) error {
	type span struct{ s, e, idx int }
	nonEmpty := make([]span, 0, len(edits))
	for _, pe := range edits {
		if pe.e > pe.s {
			nonEmpty = append(nonEmpty, span{pe.s, pe.e, pe.idx})
		}
	}
	sort.SliceStable(nonEmpty, func(i, j int) bool {
		if nonEmpty[i].s != nonEmpty[j].s {
			return nonEmpty[i].s < nonEmpty[j].s
		}
		return nonEmpty[i].idx < nonEmpty[j].idx
	})
	for i := 1; i < len(nonEmpty); i++ {
		if nonEmpty[i].s < nonEmpty[i-1].e {
			return reject(RejectOverlappingEdits, nonEmpty[i].idx, Position{},
				fmt.Sprintf("edit %d overlaps edit %d at [%d,%d) vs [%d,%d)",
					nonEmpty[i].idx, nonEmpty[i-1].idx,
					nonEmpty[i-1].s, nonEmpty[i-1].e,
					nonEmpty[i].s, nonEmpty[i].e))
		}
	}
	return nil
}

// Register adds a diagnostic based at baseVersion.
func (st *Store) Register(baseVersion int64, d Diagnostic) (int, error) {
	st.mu.Lock()
	defer st.mu.Unlock()

	if baseVersion != st.version {
		err := reject(RejectStale, -1, Position{},
			fmt.Sprintf("base version %d != current %d", baseVersion, st.version))
		st.logf("Register", "REJECT %s diag=%q", err, d.Message)
		return 0, err
	}
	s, e, err := st.rangeOffsets(d.Range, 0)
	if err != nil {
		st.logf("Register", "REJECT %s diag=%q", err, d.Message)
		return 0, err
	}
	st.regSeq++
	n := st.diags.insert(s, e, d)
	st.logf("Register", "ACCEPT id=%d version=%d [%d,%d) sev=%d msg=%q",
		n.id, st.version, s, e, d.Severity, d.Message)
	return n.id, nil
}

// Snapshot returns a consistent view of text, version and diagnostics.
func (st *Store) Snapshot() Snapshot {
	st.mu.RLock()
	defer st.mu.RUnlock()

	order := st.diags.activeSorted()
	sort.SliceStable(order, func(i, j int) bool {
		if order[i].a != order[j].a {
			return order[i].a < order[j].a
		}
		if order[i].b != order[j].b {
			return order[i].b < order[j].b
		}
		return order[i].id < order[j].id
	})
	entries := make([]DiagnosticEntry, 0, len(order))
	for _, n := range order {
		startPos, _, _ := st.text.positionOf(n.a)
		endPos, _, _ := st.text.positionOf(n.b)
		entries = append(entries, DiagnosticEntry{
			Diagnostic: Diagnostic{
				Range:    Range{Start: startPos, End: endPos},
				Severity: n.diag.Severity,
				Message:  n.diag.Message,
			},
			RegisteredAt: int64(n.id),
		})
	}
	snap := Snapshot{
		Version:     st.version,
		Text:        st.text.string(),
		Diagnostics: entries,
		Invalidated: st.gone.sorted(),
	}
	st.logf("Snapshot", "version=%d active=%d invalidated=%d",
		snap.Version, len(snap.Diagnostics), len(snap.Invalidated))
	return snap
}

// PositionOf converts a UTF-16 offset to a position.
func (st *Store) PositionOf(offset int) (Position, error) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	p, split, ok := st.text.positionOf(offset)
	if !ok {
		if split {
			err := reject(RejectSurrogateSplit, -1, p,
				fmt.Sprintf("offset %d splits an astral character", offset))
			st.logf("PositionOf", "REJECT %s", err)
			return Position{}, err
		}
		err := reject(RejectOutOfBounds, -1, Position{},
			fmt.Sprintf("offset %d out of bounds [0,%d]", offset, st.text.length()))
		st.logf("PositionOf", "REJECT %s", err)
		return Position{}, err
	}
	st.logf("PositionOf", "offset=%d -> %v", offset, p)
	return p, nil
}

// OffsetOf converts a position to a UTF-16 offset.
func (st *Store) OffsetOf(p Position) (int, error) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	off, split, ok := st.text.offsetOf(p)
	if !ok {
		if split {
			err := reject(RejectSurrogateSplit, -1, p,
				fmt.Sprintf("position %v splits an astral character", p))
			st.logf("OffsetOf", "REJECT %s", err)
			return 0, err
		}
		err := reject(RejectOutOfBounds, -1, p, fmt.Sprintf("position %v out of bounds", p))
		st.logf("OffsetOf", "REJECT %s", err)
		return 0, err
	}
	st.logf("OffsetOf", "%v -> %d", p, off)
	return off, nil
}
