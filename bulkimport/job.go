package bulkimport

import (
	"log/slog"
	"sort"
	"sync"
)

// entryState is the mutable record of one declared entry.
type entryState struct {
	entry  Entry
	status EntryStatus
	err    *ImportError
	// valErr is the entry's own validation error, if any.
	valErr error
	// pending holds reference targets that are not landed yet. A target
	// is either undeclared (its chunk has not arrived) or itself
	// pending; failed targets fail this entry immediately instead.
	pending map[string]struct{}
	seq     int
}

// chunkRecord remembers a processed chunk for duplicate detection.
type chunkRecord struct {
	hash string
}

// conflictRecord remembers a rejected conflicting duplicate so that
// entries that only ever appeared in it can be reported as
// EntryConflicted.
type conflictRecord struct {
	seq      int
	hash     string
	entryIDs map[string]struct{}
}

// job is the per-import-task state machine. All mutable state is
// guarded by mu; entry validation runs outside the lock so independent
// chunks validate in parallel.
type job struct {
	mu        sync.Mutex
	id        string
	maxChunks int
	closed    bool
	validator Validator
	sink      Sink
	logger    *slog.Logger

	chunks    map[int]chunkRecord
	conflicts []conflictRecord
	arrived   int
	// inflight counts chunks that are registered but whose commit has
	// not finished yet. The timeout sweep must wait for them, because
	// they may still declare the targets of dangling references.
	inflight int

	entries map[string]*entryState
	// dangling maps an unresolved reference target to the set of
	// entries waiting on it. It only ever contains edges for currently
	// pending entries, so its size is proportional to the number of
	// dangling references, not to the number of processed entries.
	dangling map[string]map[string]struct{}
	// waiters is the set of currently pending entries.
	waiters map[string]*entryState

	landedN    int
	failedN    int
	timedOutN  int
	waiterPops int64
}

func newJob(cfg JobConfig, logger *slog.Logger) *job {
	return &job{
		id:        cfg.ID,
		maxChunks: cfg.MaxChunks,
		validator: cfg.Validator,
		sink:      cfg.Sink,
		logger:    logger,
		chunks:    make(map[int]chunkRecord),
		entries:   make(map[string]*entryState),
		dangling:  make(map[string]map[string]struct{}),
		waiters:   make(map[string]*entryState),
	}
}

func (j *job) submit(c Chunk) ChunkResult {
	hash := chunkHash(c)
	entryIDs := make([]string, 0, len(c.Entries))
	for _, e := range c.Entries {
		entryIDs = append(entryIDs, e.ID)
	}
	j.logger.Debug("chunk arrived", "seq", c.Seq, "hash", hash, "entryIDs", entryIDs)

	// Registration phase: duplicate detection, close and limit checks
	// are serialized under the job lock. This is the linearization
	// point against close(): a chunk either wins registration (and
	// will be fully processed) or is rejected.
	j.mu.Lock()
	if rec, ok := j.chunks[c.Seq]; ok {
		if rec.hash == hash {
			j.mu.Unlock()
			j.logger.Info("chunk duplicate, entries not reprocessed",
				"seq", c.Seq, "hash", hash, "entries", len(c.Entries))
			return ChunkResult{Outcome: ChunkDuplicate}
		}
		ids := make(map[string]struct{}, len(c.Entries))
		for _, e := range c.Entries {
			ids[e.ID] = struct{}{}
		}
		j.conflicts = append(j.conflicts, conflictRecord{seq: c.Seq, hash: hash, entryIDs: ids})
		j.mu.Unlock()
		j.logger.Warn("chunk conflict: sequence already processed with different content",
			"seq", c.Seq, "hash", hash, "knownHash", rec.hash)
		return ChunkResult{Outcome: ChunkRejected, Err: &ImportError{
			Kind:    ErrKindChunkConflict,
			Message: "sequence number already processed with different content",
		}}
	}
	if j.closed {
		j.mu.Unlock()
		j.logger.Warn("chunk rejected: job closed", "seq", c.Seq)
		return ChunkResult{Outcome: ChunkRejected, Err: &ImportError{
			Kind:    ErrKindJobClosed,
			Message: "job was closed before the chunk arrived",
		}}
	}
	if j.maxChunks > 0 && j.arrived >= j.maxChunks {
		j.mu.Unlock()
		j.logger.Warn("chunk rejected: chunk limit exceeded", "seq", c.Seq, "maxChunks", j.maxChunks)
		return ChunkResult{Outcome: ChunkRejected, Err: &ImportError{
			Kind:    ErrKindChunkLimitExceeded,
			Message: "job already received its declared number of chunks",
		}}
	}
	j.chunks[c.Seq] = chunkRecord{hash: hash}
	j.arrived++
	j.inflight++
	j.mu.Unlock()

	// Validation phase: runs outside the lock, so independent chunks
	// validate in parallel.
	valErrs := make(map[string]error, len(c.Entries))
	if j.validator != nil {
		for _, e := range c.Entries {
			if err := j.validator(e); err != nil {
				valErrs[e.ID] = err
			}
		}
	}

	// Commit phase: declare entries and settle all consequences.
	j.mu.Lock()
	defer j.mu.Unlock()
	results := j.commitLocked(c, valErrs)
	j.logger.Info("chunk processed",
		"seq", c.Seq, "hash", hash, "entries", len(c.Entries),
		"arrived", j.arrived, "maxChunks", j.maxChunks)
	return ChunkResult{Outcome: ChunkAccepted, Entries: results}
}

func (j *job) close() {
	j.mu.Lock()
	j.closed = true
	j.logger.Info("job closed: new chunks will be rejected")
	j.mu.Unlock()
}

func (j *job) queryEntry(id string) EntryView {
	j.mu.Lock()
	defer j.mu.Unlock()
	if st, ok := j.entries[id]; ok {
		v := EntryView{ID: id, Status: st.status, Err: st.err}
		if st.status == EntryPending {
			refs := make([]string, 0, len(st.pending))
			for r := range st.pending {
				refs = append(refs, r)
			}
			sort.Strings(refs)
			for _, r := range refs {
				reason := WaitTargetNotArrived
				if _, ok := j.entries[r]; ok {
					reason = WaitTargetPending
				}
				v.WaitingOn = append(v.WaitingOn, RefWait{TargetID: r, Reason: reason})
			}
		}
		return v
	}
	for _, cf := range j.conflicts {
		if _, ok := cf.entryIDs[id]; ok {
			return EntryView{ID: id, Status: EntryConflicted, Err: &ImportError{
				Kind:    ErrKindChunkConflict,
				EntryID: id,
				Message: "entry only appeared in a chunk rejected for conflicting content",
			}}
		}
	}
	return EntryView{ID: id, Status: EntryUnknown}
}

func (j *job) statsSnapshot() Stats {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.statsLocked()
}

func (j *job) statsLocked() Stats {
	danglingRefs := 0
	for _, s := range j.dangling {
		danglingRefs += len(s)
	}
	return Stats{
		ArrivedChunks:   j.arrived,
		LandedEntries:   j.landedN,
		FailedEntries:   j.failedN,
		PendingEntries:  len(j.waiters),
		TimedOutEntries: j.timedOutN,
		DanglingRefs:    danglingRefs,
		WaiterPops:      j.waiterPops,
		EntryTableScans: 0,
	}
}

// commitLocked declares the chunk's entries and settles every
// consequence before returning. It returns per-entry results aligned
// with c.Entries. Callers must hold j.mu.
func (j *job) commitLocked(c Chunk, valErrs map[string]error) []EntryResult {
	results := make([]EntryResult, len(c.Entries))
	seen := make(map[string]struct{}, len(c.Entries))
	type declaredAt struct {
		st  *entryState
		idx int
	}
	var declared []declaredAt

	for i, e := range c.Entries {
		if _, ok := seen[e.ID]; ok {
			err := &ImportError{Kind: ErrKindValidation, EntryID: e.ID,
				Message: "duplicate entry id within the same chunk"}
			results[i] = EntryResult{ID: e.ID, Status: EntryFailed, Err: err}
			j.logger.Warn("entry failed", "entry", e.ID, "chunk", c.Seq,
				"kind", err.Kind, "reason", err.Message)
			continue
		}
		seen[e.ID] = struct{}{}
		if _, exists := j.entries[e.ID]; exists {
			err := &ImportError{Kind: ErrKindValidation, EntryID: e.ID,
				Message: "entry id already declared by an earlier chunk"}
			results[i] = EntryResult{ID: e.ID, Status: EntryFailed, Err: err}
			j.logger.Warn("entry failed", "entry", e.ID, "chunk", c.Seq,
				"kind", err.Kind, "reason", err.Message)
			continue
		}
		st := &entryState{
			entry:   e,
			status:  EntryPending,
			pending: make(map[string]struct{}, len(e.Refs)),
			seq:     c.Seq,
		}
		if ve, ok := valErrs[e.ID]; ok {
			st.valErr = ve
		}
		for _, r := range e.Refs {
			t, ok := j.entries[r]
			switch {
			case !ok:
				// Target chunk has not arrived yet.
				st.pending[r] = struct{}{}
			case t.status == EntryLanded:
				// Already satisfied.
			default:
				// Declared but pending, or already failed; classify
				// distinguishes the two.
				st.pending[r] = struct{}{}
			}
		}
		j.entries[e.ID] = st
		declared = append(declared, declaredAt{st: st, idx: i})
	}

	var queue []string
	for _, d := range declared {
		j.classifyLocked(d.st, &queue)
	}
	j.drainLocked(queue)
	j.breakCyclesLocked()
	j.inflight--
	j.sweepTimeoutLocked()

	for _, d := range declared {
		results[d.idx] = EntryResult{ID: d.st.entry.ID, Status: d.st.status, Err: d.st.err}
	}
	return results
}

// classifyLocked decides the initial fate of a freshly declared entry.
//
// Error priority is enforced by decision order: a reference that is
// already failed fails the entry immediately (ErrKindRefFailed); the
// entry's own validation error is only materialized once every
// reference has resolved, so a later reference failure or a dangling
// timeout (both higher priority) still wins. This keeps final states
// independent of chunk arrival order. Callers must hold j.mu.
func (j *job) classifyLocked(st *entryState, queue *[]string) {
	id := st.entry.ID

	// A reference to an already-failed entry fails this entry
	// immediately and outranks the entry's own validation error.
	for _, r := range sortedKeys(st.pending) {
		if t, ok := j.entries[r]; ok && (t.status == EntryFailed || t.status == EntryTimedOut) {
			j.failLocked(st, &ImportError{Kind: ErrKindRefFailed, EntryID: id, RefID: r,
				Message: "referenced entry deterministically failed"})
			*queue = append(*queue, id)
			return
		}
	}

	// References may have landed while this entry waited for
	// classification inside the same commit.
	for r := range st.pending {
		if t, ok := j.entries[r]; ok && t.status == EntryLanded {
			delete(st.pending, r)
		}
	}
	if len(st.pending) == 0 {
		j.resolveLocked(st)
		*queue = append(*queue, id)
		return
	}

	for r := range st.pending {
		setAdd(j.dangling, r, id)
	}
	j.waiters[id] = st
	j.logger.Debug("entry pending on dangling references",
		"entry", id, "chunk", st.seq, "waitingOn", sortedKeys(st.pending))
}

// resolveLocked decides an entry whose references have all landed: its
// own validation error (if any) is materialized now, otherwise the
// entry lands. Callers must hold j.mu.
func (j *job) resolveLocked(st *entryState) {
	if st.valErr != nil {
		j.failLocked(st, &ImportError{Kind: ErrKindValidation, EntryID: st.entry.ID,
			Message: st.valErr.Error()})
		return
	}
	j.landLocked(st)
}

// drainLocked propagates landed/failed outcomes to waiting entries
// until quiescence. Callers must hold j.mu.
func (j *job) drainLocked(queue []string) {
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		st := j.entries[id]
		if st == nil {
			continue
		}
		ws := j.dangling[id]
		delete(j.dangling, id)
		for _, wid := range sortedKeys(ws) {
			w, ok := j.waiters[wid]
			if !ok || w.status != EntryPending {
				continue
			}
			j.waiterPops++
			if st.status == EntryLanded {
				delete(w.pending, id)
				if len(w.pending) == 0 {
					delete(j.waiters, wid)
					j.resolveLocked(w)
					j.logger.Debug("dangling reference resolved, entry retried",
						"entry", wid, "resolvedBy", id)
					queue = append(queue, wid)
				}
				continue
			}
			// The reference target failed (or timed out): propagate the
			// failure immediately instead of waiting forever.
			j.removeWaiterEdgesLocked(w)
			j.failLocked(w, &ImportError{Kind: ErrKindRefFailed, EntryID: wid, RefID: id,
				Message: "referenced entry deterministically failed"})
			queue = append(queue, wid)
		}
	}
}

// breakCyclesLocked finds pending entries that wait on each other in a
// cycle with every reference target already declared. Such cycles can
// never resolve, so their members fail deterministically (classified as
// validation failures) instead of dangling forever. Callers must hold
// j.mu.
func (j *job) breakCyclesLocked() {
	if len(j.waiters) == 0 {
		return
	}
	nodes := make([]string, 0, len(j.waiters))
	for id := range j.waiters {
		nodes = append(nodes, id)
	}
	sort.Strings(nodes)

	neighbors := func(id string) []string {
		w := j.waiters[id]
		var out []string
		for r := range w.pending {
			if _, ok := j.waiters[r]; ok {
				out = append(out, r)
			}
		}
		sort.Strings(out)
		return out
	}

	// Iterative Tarjan SCC over the waiter graph.
	indexOf := make(map[string]int, len(nodes))
	lowlink := make(map[string]int, len(nodes))
	onStack := make(map[string]bool, len(nodes))
	var stack []string
	index := 0
	var doomed []string

	type frame struct {
		v    string
		kids []string
		ki   int
	}
	for _, root := range nodes {
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
				if j.sccDoomedLocked(scc) {
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

	if len(doomed) == 0 {
		return
	}
	sort.Strings(doomed)
	var queue []string
	for _, id := range doomed {
		w, ok := j.waiters[id]
		if !ok || w.status != EntryPending {
			continue
		}
		j.removeWaiterEdgesLocked(w)
		j.failLocked(w, &ImportError{Kind: ErrKindValidation, EntryID: id,
			Message: "circular reference can never resolve"})
		queue = append(queue, id)
	}
	j.drainLocked(queue)
}

// sccDoomedLocked reports whether an SCC of pending entries can never
// resolve: it must be a real cycle (size > 1 or a self-loop) and no
// member may wait on anything outside the SCC (an undeclared chunk that
// could still arrive, or another pending entry). Callers must hold j.mu.
func (j *job) sccDoomedLocked(scc []string) bool {
	member := make(map[string]bool, len(scc))
	for _, id := range scc {
		member[id] = true
	}
	if len(scc) == 1 {
		if !hasKey(j.waiters[scc[0]].pending, scc[0]) {
			return false // single node without a self-loop is not a cycle
		}
	}
	for _, id := range scc {
		for r := range j.waiters[id].pending {
			if _, declared := j.entries[r]; !declared {
				return false
			}
			if !member[r] {
				return false
			}
		}
	}
	return true
}

// sweepTimeoutLocked finally fails every still-pending entry once the
// job has received its declared number of chunks and every registered
// chunk has finished committing: the references they wait on can never
// arrive. Landed entries are unaffected. Callers must hold j.mu.
func (j *job) sweepTimeoutLocked() {
	if j.maxChunks <= 0 || j.arrived < j.maxChunks || j.inflight > 0 || len(j.waiters) == 0 {
		return
	}
	ids := make([]string, 0, len(j.waiters))
	for id := range j.waiters {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		w := j.waiters[id]
		w.status = EntryTimedOut
		w.err = &ImportError{Kind: ErrKindDanglingTimeout, EntryID: id,
			Message: "chunk limit reached while references were still dangling"}
		j.timedOutN++
		j.logger.Warn("entry timed out on dangling references",
			"entry", id, "chunk", w.seq, "waitingOn", sortedKeys(w.pending),
			"arrived", j.arrived, "maxChunks", j.maxChunks)
	}
	j.waiters = make(map[string]*entryState)
	j.dangling = make(map[string]map[string]struct{})
}

// landLocked marks an entry as landed, delivering it to the sink if
// one is configured. A sink error fails the entry as a validation
// failure. Callers must hold j.mu.
func (j *job) landLocked(st *entryState) {
	if j.sink != nil {
		if err := j.sink.Put(j.id, st.entry); err != nil {
			j.failLocked(st, &ImportError{Kind: ErrKindValidation, EntryID: st.entry.ID,
				Message: "sink rejected entry: " + err.Error()})
			return
		}
	}
	st.status = EntryLanded
	j.landedN++
	j.logger.Info("entry landed", "entry", st.entry.ID, "chunk", st.seq)
}

// failLocked marks an entry as deterministically failed. Callers must
// hold j.mu.
func (j *job) failLocked(st *entryState, err *ImportError) {
	st.status = EntryFailed
	st.err = err
	j.failedN++
	j.logger.Warn("entry failed", "entry", st.entry.ID, "chunk", st.seq,
		"kind", err.Kind, "reason", err.Message, "ref", err.RefID)
}

// removeWaiterEdgesLocked drops all dangling edges owned by a waiter
// that is leaving the pending state. Callers must hold j.mu.
func (j *job) removeWaiterEdgesLocked(w *entryState) {
	for r := range w.pending {
		if s, ok := j.dangling[r]; ok {
			delete(s, w.entry.ID)
			if len(s) == 0 {
				delete(j.dangling, r)
			}
		}
	}
	delete(j.waiters, w.entry.ID)
}

func setAdd(m map[string]map[string]struct{}, key, val string) {
	s, ok := m[key]
	if !ok {
		s = make(map[string]struct{})
		m[key] = s
	}
	s[val] = struct{}{}
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func hasKey(m map[string]struct{}, k string) bool {
	_, ok := m[k]
	return ok
}
