// Package bulkimport implements a chunked bulk-import subsystem for the
// ontology platform. Chunks of entries may arrive out of order, arrive
// more than once, or reference entries declared by chunks that have not
// arrived yet. The subsystem lands entries as early as their
// dependencies allow, records dangling references in an index whose cost
// is proportional to the number of dangling references (not to the
// number of processed entries), and resolves every entry to a final,
// queryable state.
package bulkimport

// Entry is a single importable object instance.
type Entry struct {
	// ID uniquely identifies the entry within its job.
	ID string
	// Fields is the opaque payload of the entry.
	Fields map[string]string
	// Refs lists IDs of other entries (possibly declared by chunks that
	// have not arrived yet) that this entry references.
	Refs []string
}

// Chunk is a numbered fragment of an import job.
type Chunk struct {
	JobID   string
	Seq     int
	Entries []Entry
}

// EntryStatus is the observable state of an entry. All values are
// pairwise distinguishable through QueryEntry.
type EntryStatus int

const (
	// EntryUnknown means the entry was never seen by the job.
	EntryUnknown EntryStatus = iota
	// EntryLanded means the entry was persisted successfully.
	EntryLanded
	// EntryFailed means the entry is deterministically failed
	// (validation failure or propagated reference failure).
	EntryFailed
	// EntryPending means the entry waits on dangling references.
	EntryPending
	// EntryTimedOut means the entry was still pending when the job
	// reached its declared chunk-count limit.
	EntryTimedOut
	// EntryConflicted means the entry only appeared in a chunk that was
	// rejected because its sequence number was already processed with
	// different content.
	EntryConflicted
)

func (s EntryStatus) String() string {
	switch s {
	case EntryUnknown:
		return "unknown"
	case EntryLanded:
		return "landed"
	case EntryFailed:
		return "failed"
	case EntryPending:
		return "pending"
	case EntryTimedOut:
		return "timed_out"
	case EntryConflicted:
		return "conflicted"
	default:
		return "invalid"
	}
}

// WaitReason explains why a single referenced target is still
// unresolved for a pending entry.
type WaitReason int

const (
	// WaitTargetNotArrived means the referenced entry has not been
	// declared by any arrived chunk yet.
	WaitTargetNotArrived WaitReason = iota + 1
	// WaitTargetPending means the referenced entry is declared but is
	// itself still waiting on its own references.
	WaitTargetPending
)

func (r WaitReason) String() string {
	switch r {
	case WaitTargetNotArrived:
		return "target_not_arrived"
	case WaitTargetPending:
		return "target_pending"
	default:
		return "invalid"
	}
}

// RefWait describes one unresolved reference of a pending entry.
type RefWait struct {
	TargetID string
	Reason   WaitReason
}

// EntryView is a read-only snapshot of an entry's state. Producing it
// never mutates job state.
type EntryView struct {
	ID     string
	Status EntryStatus
	// Err is set when Status is EntryFailed, EntryTimedOut or
	// EntryConflicted.
	Err *ImportError
	// WaitingOn lists unresolved references when Status is EntryPending.
	WaitingOn []RefWait
}

// ChunkOutcome is the deterministic verdict for an arrived chunk.
type ChunkOutcome int

const (
	// ChunkAccepted means the chunk was new and has been processed.
	ChunkAccepted ChunkOutcome = iota + 1
	// ChunkDuplicate means a chunk with the same sequence number and
	// identical content was already processed; entries are not
	// reprocessed.
	ChunkDuplicate
	// ChunkRejected means the chunk was refused; Err explains why.
	ChunkRejected
)

func (o ChunkOutcome) String() string {
	switch o {
	case ChunkAccepted:
		return "accepted"
	case ChunkDuplicate:
		return "duplicate"
	case ChunkRejected:
		return "rejected"
	default:
		return "invalid"
	}
}

// EntryResult reports the state of one submitted entry right after its
// chunk was processed. Pending entries may resolve later; use
// QueryEntry for the current state.
type EntryResult struct {
	ID     string
	Status EntryStatus
	Err    *ImportError
}

// ChunkResult is the deterministic result of submitting one chunk.
type ChunkResult struct {
	Outcome ChunkOutcome
	// Err is set when Outcome is ChunkRejected.
	Err *ImportError
	// Entries mirrors the submitted entries in order when Outcome is
	// ChunkAccepted.
	Entries []EntryResult
}

// Validator checks a single entry. A nil Validator accepts everything.
type Validator func(Entry) error

// Sink receives entries at the moment they land. It is called while
// the job lock is held, so implementations must be fast and must not
// call back into the Manager. A nil Sink only records state in memory.
type Sink interface {
	Put(jobID string, e Entry) error
}

// JobConfig configures a new import job.
type JobConfig struct {
	// ID uniquely identifies the job.
	ID string
	// MaxChunks is the declared upper bound of distinct chunks. Once
	// this many chunks have arrived, every still-pending entry is
	// finally failed with ErrKindDanglingTimeout. Zero means no limit.
	MaxChunks int
	// Validator optionally validates each entry.
	Validator Validator
	// Sink optionally receives landed entries.
	Sink Sink
}

// Stats exposes job counters. EntryTableScans is an invariant probe:
// the incremental engine never iterates the full entry table, so it is
// always zero; tests assert this to prove that dangling-reference
// bookkeeping does not grow with the number of processed entries.
type Stats struct {
	ArrivedChunks   int
	LandedEntries   int
	FailedEntries   int
	PendingEntries  int
	TimedOutEntries int
	// DanglingRefs is the current number of unresolved reference edges.
	DanglingRefs int
	// WaiterPops counts how many waiting entries were touched while
	// resolving references. It is bounded by the number of waiters, not
	// by the number of processed entries.
	WaiterPops      int64
	EntryTableScans int64
}
