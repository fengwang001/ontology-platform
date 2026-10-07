// Package naive is an independent reference implementation of the
// bulk-import semantics. It buffers every accepted chunk and only
// resolves entries when Finalize is called, repeatedly scanning the
// whole entry table. It exists to differential-test the incremental
// engine in package bulkimport: both must reach identical final states
// for identical event sequences.
package naive

import (
	"fmt"
	"sort"
	"strings"

	"ontology/bulkimport"
)

// Model buffers chunks and resolves everything at the end.
type Model struct {
	maxChunks int
	validator bulkimport.Validator

	closed    bool
	chunks    map[int]string // seq -> canonical content
	arrived   int
	buffered  [][]bulkimport.Entry // accepted chunks, arrival order
	conflicts []map[string]struct{}

	finalized bool
	entries   map[string]bulkimport.Entry
	valErr    map[string]error
	status    map[string]bulkimport.EntryStatus
	errs      map[string]*bulkimport.ImportError

	// FullScans counts iterations over the whole entry table. It grows
	// with the number of processed entries, unlike the incremental
	// engine whose bookkeeping only tracks dangling references.
	FullScans int64
}

// New creates a Model with the same job-level semantics as bulkimport.
func New(maxChunks int, validator bulkimport.Validator) *Model {
	return &Model{
		maxChunks: maxChunks,
		validator: validator,
		chunks:    make(map[int]string),
		entries:   make(map[string]bulkimport.Entry),
		valErr:    make(map[string]error),
		status:    make(map[string]bulkimport.EntryStatus),
		errs:      make(map[string]*bulkimport.ImportError),
	}
}

// canonical is the model's own content fingerprint, independent of the
// hash used by the incremental engine.
func canonical(c bulkimport.Chunk) string {
	entries := make([]bulkimport.Entry, len(c.Entries))
	copy(entries, c.Entries)
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	var b strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&b, "id=%q;", e.ID)
		keys := make([]string, 0, len(e.Fields))
		for k := range e.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "f=%q:%q;", k, e.Fields[k])
		}
		refs := uniqueSorted(e.Refs)
		for _, r := range refs {
			fmt.Fprintf(&b, "r=%q;", r)
		}
		b.WriteString("|")
	}
	return b.String()
}

// SubmitChunk applies the same acceptance rules as the incremental
// engine but only buffers the chunk; entries are resolved by Finalize.
func (m *Model) SubmitChunk(c bulkimport.Chunk) bulkimport.ChunkResult {
	fp := canonical(c)
	if known, ok := m.chunks[c.Seq]; ok {
		if known == fp {
			return bulkimport.ChunkResult{Outcome: bulkimport.ChunkDuplicate}
		}
		ids := make(map[string]struct{}, len(c.Entries))
		for _, e := range c.Entries {
			ids[e.ID] = struct{}{}
		}
		m.conflicts = append(m.conflicts, ids)
		return bulkimport.ChunkResult{Outcome: bulkimport.ChunkRejected, Err: &bulkimport.ImportError{
			Kind:    bulkimport.ErrKindChunkConflict,
			Message: "sequence number already processed with different content",
		}}
	}
	if m.closed {
		return bulkimport.ChunkResult{Outcome: bulkimport.ChunkRejected, Err: &bulkimport.ImportError{
			Kind:    bulkimport.ErrKindJobClosed,
			Message: "job was closed before the chunk arrived",
		}}
	}
	if m.maxChunks > 0 && m.arrived >= m.maxChunks {
		return bulkimport.ChunkResult{Outcome: bulkimport.ChunkRejected, Err: &bulkimport.ImportError{
			Kind:    bulkimport.ErrKindChunkLimitExceeded,
			Message: "job already received its declared number of chunks",
		}}
	}
	m.chunks[c.Seq] = fp
	m.arrived++
	m.buffered = append(m.buffered, c.Entries)
	return bulkimport.ChunkResult{Outcome: bulkimport.ChunkAccepted}
}

// Close marks the job finished early; later chunks are rejected.
func (m *Model) Close() { m.closed = true }

// Finalize resolves every buffered entry by repeatedly scanning the
// whole table until a fixpoint is reached.
func (m *Model) Finalize() {
	if m.finalized {
		return
	}
	m.finalized = true

	// Declare entries in arrival order; the first declaration wins.
	for _, chunkEntries := range m.buffered {
		for _, e := range chunkEntries {
			if _, ok := m.entries[e.ID]; ok {
				continue
			}
			m.entries[e.ID] = e
			if m.validator != nil {
				if err := m.validator(e); err != nil {
					m.valErr[e.ID] = err
				}
			}
		}
	}

	for {
		for m.fixpointPass() {
		}
		doomed := m.doomedCycles()
		if len(doomed) == 0 {
			break
		}
		for _, id := range doomed {
			m.fail(id, &bulkimport.ImportError{Kind: bulkimport.ErrKindValidation, EntryID: id,
				Message: "circular reference can never resolve"})
		}
	}

	if m.maxChunks > 0 && m.arrived >= m.maxChunks {
		for _, id := range m.undecided() {
			m.status[id] = bulkimport.EntryTimedOut
			m.errs[id] = &bulkimport.ImportError{Kind: bulkimport.ErrKindDanglingTimeout, EntryID: id,
				Message: "chunk limit reached while references were still dangling"}
		}
	}
}

// fixpointPass scans the whole table once, landing entries whose
// references all landed and failing entries whose references failed.
// It reports whether anything changed.
func (m *Model) fixpointPass() bool {
	m.FullScans++
	progress := false
	for _, id := range m.undecided() {
		e := m.entries[id]
		var refFailed *bulkimport.ImportError
		allLanded := true
		for _, r := range uniqueSorted(e.Refs) {
			st, declared := m.status[r]
			switch {
			case declared && (st == bulkimport.EntryFailed || st == bulkimport.EntryTimedOut):
				refFailed = &bulkimport.ImportError{Kind: bulkimport.ErrKindRefFailed,
					EntryID: id, RefID: r, Message: "referenced entry deterministically failed"}
			case !declared || st != bulkimport.EntryLanded:
				allLanded = false
			}
			if refFailed != nil {
				break
			}
		}
		// A failed reference outranks the entry's own validation error;
		// the validation error only materializes once every reference
		// has landed, mirroring the incremental engine.
		if refFailed != nil {
			m.fail(id, refFailed)
			progress = true
			continue
		}
		if allLanded {
			if ve := m.validationErr(id); ve != nil {
				m.fail(id, ve)
			} else {
				m.status[id] = bulkimport.EntryLanded
			}
			progress = true
		}
	}
	return progress
}

// doomedCycles finds SCCs of undecided entries that mutually wait on
// each other with every reference target already declared.
func (m *Model) doomedCycles() []string {
	undecided := m.undecided()
	inflight := make(map[string]bool, len(undecided))
	for _, id := range undecided {
		inflight[id] = true
	}
	neighbors := func(id string) []string {
		var out []string
		for _, r := range uniqueSorted(m.entries[id].Refs) {
			if inflight[r] {
				out = append(out, r)
			}
		}
		return out
	}

	// Iterative Tarjan SCC.
	indexOf := make(map[string]int, len(undecided))
	lowlink := make(map[string]int, len(undecided))
	onStack := make(map[string]bool, len(undecided))
	var stack []string
	index := 0
	var doomed []string

	type frame struct {
		v    string
		kids []string
		ki   int
	}
	for _, root := range undecided {
		if _, seen := indexOf[root]; seen {
			continue
		}
		indexOf[root] = index
		lowlink[root] = index
		index++
		stack = append(stack, root)
		onStack[root] = true
		callStack := []frame{{v: root, kids: neighbors(root)}}
		for len(callStack) > 0 {
			f := &callStack[len(callStack)-1]
			if f.ki < len(f.kids) {
				w := f.kids[f.ki]
				f.ki++
				if _, seen := indexOf[w]; !seen {
					indexOf[w] = index
					lowlink[w] = index
					index++
					stack = append(stack, w)
					onStack[w] = true
					callStack = append(callStack, frame{v: w, kids: neighbors(w)})
				} else if onStack[w] && indexOf[w] < lowlink[f.v] {
					lowlink[f.v] = indexOf[w]
				}
				continue
			}
			callStack = callStack[:len(callStack)-1]
			if lowlink[f.v] == indexOf[f.v] {
				var scc []string
				for {
					w := stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					onStack[w] = false
					scc = append(scc, w)
					if w == f.v {
						break
					}
				}
				if m.sccDoomed(scc, inflight) {
					doomed = append(doomed, scc...)
				}
			}
			if len(callStack) > 0 {
				p := &callStack[len(callStack)-1]
				if lowlink[f.v] < lowlink[p.v] {
					lowlink[p.v] = lowlink[f.v]
				}
			}
		}
	}
	sort.Strings(doomed)
	return doomed
}

// sccDoomed reports whether an SCC is an unresolvable cycle: it must be
// a real cycle and no member may reference anything outside the SCC
// that is not already landed.
func (m *Model) sccDoomed(scc []string, inflight map[string]bool) bool {
	member := make(map[string]bool, len(scc))
	for _, id := range scc {
		member[id] = true
	}
	if len(scc) == 1 {
		self := false
		for _, r := range m.entries[scc[0]].Refs {
			if r == scc[0] {
				self = true
			}
		}
		if !self {
			return false
		}
	}
	for _, id := range scc {
		for _, r := range m.entries[id].Refs {
			if _, declared := m.entries[r]; !declared {
				return false
			}
			if inflight[r] && !member[r] {
				return false
			}
		}
	}
	return true
}

// QueryEntry returns the final view of an entry. Finalize must have
// been called.
func (m *Model) QueryEntry(id string) bulkimport.EntryView {
	if st, ok := m.status[id]; ok && st != bulkimport.EntryUnknown {
		v := bulkimport.EntryView{ID: id, Status: st, Err: m.errs[id]}
		if st == bulkimport.EntryPending {
			v.WaitingOn = m.waitingOn(id)
		}
		return v
	}
	if _, declared := m.entries[id]; declared {
		// Declared but still undecided: pending.
		return bulkimport.EntryView{ID: id, Status: bulkimport.EntryPending, WaitingOn: m.waitingOn(id)}
	}
	for _, ids := range m.conflicts {
		if _, ok := ids[id]; ok {
			return bulkimport.EntryView{ID: id, Status: bulkimport.EntryConflicted,
				Err: &bulkimport.ImportError{Kind: bulkimport.ErrKindChunkConflict, EntryID: id,
					Message: "entry only appeared in a chunk rejected for conflicting content"}}
		}
	}
	return bulkimport.EntryView{ID: id, Status: bulkimport.EntryUnknown}
}

// waitingOn lists the references of an entry that are still
// unresolved, mirroring the pending-set semantics of the incremental
// engine: landed references are already satisfied and excluded.
func (m *Model) waitingOn(id string) []bulkimport.RefWait {
	var out []bulkimport.RefWait
	for _, r := range uniqueSorted(m.entries[id].Refs) {
		if st, declared := m.status[r]; declared && st == bulkimport.EntryLanded {
			continue
		}
		reason := bulkimport.WaitTargetNotArrived
		if _, declared := m.entries[r]; declared {
			reason = bulkimport.WaitTargetPending
		}
		out = append(out, bulkimport.RefWait{TargetID: r, Reason: reason})
	}
	return out
}

func (m *Model) fail(id string, err *bulkimport.ImportError) {
	m.status[id] = bulkimport.EntryFailed
	m.errs[id] = err
}

func (m *Model) validationErr(id string) *bulkimport.ImportError {
	if ve, ok := m.valErr[id]; ok {
		return &bulkimport.ImportError{Kind: bulkimport.ErrKindValidation, EntryID: id, Message: ve.Error()}
	}
	return nil
}

func (m *Model) undecided() []string {
	var out []string
	for id := range m.entries {
		if _, decided := m.status[id]; !decided {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func uniqueSorted(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	var out []string
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
