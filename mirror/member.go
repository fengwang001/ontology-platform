package mirror

// MemberState is the lifecycle state of a single volume member.
type MemberState int

const (
	// StateOnline means the member holds a complete, current mirror of
	// the volume and serves reads and writes.
	StateOnline MemberState = iota
	// StateFailed means the member is out of the volume. Its missed
	// writes are tracked in its dirty-region record while possible.
	StateFailed
	// StateResyncing means the member rejoined and is copying missing
	// blocks. It receives writes but never serves reads.
	StateResyncing
)

func (s MemberState) String() string {
	switch s {
	case StateOnline:
		return "online"
	case StateFailed:
		return "failed"
	case StateResyncing:
		return "resyncing"
	}
	return "unknown"
}

// member holds all per-replica runtime state.
type member struct {
	state MemberState
	data  []string

	// failGen is the volume generation at which the member last
	// transitioned into StateFailed (the generation before the
	// failure-triggered increment).
	failGen uint64

	// Dirty-region record, meaningful while the member is not online.
	// dirty is the set of blocks the member is known to miss; it is
	// valid only while forceFull is false. Once the number of distinct
	// dirty blocks grows strictly beyond the configured limit the
	// record is discarded and forceFull is set permanently, until the
	// next completed resync.
	dirty     map[int]struct{}
	forceFull bool

	// Resync progress, meaningful only in StateResyncing.
	partial  bool             // true: partial (dirty-region) resync
	pending  map[int]struct{} // partial: blocks still to copy
	order    []int            // partial: ascending snapshot of pending
	orderIdx int              // partial: scan position inside order
	fullNext int              // full: next block candidate to copy
	fullSkip map[int]struct{} // full: blocks already written directly
}

func newMember(blocks int) *member {
	return &member{
		state: StateOnline,
		data:  make([]string, blocks),
		dirty: make(map[int]struct{}),
	}
}

// resetResyncState clears all resync-progress fields.
func (m *member) resetResyncState() {
	m.partial = false
	m.pending = nil
	m.order = nil
	m.orderIdx = 0
	m.fullNext = 0
	m.fullSkip = nil
}

// markOnline completes a resync: the member becomes online, its dirty
// record and the permanent full-resync mark are cleared.
func (m *member) markOnline() {
	m.state = StateOnline
	m.dirty = make(map[int]struct{})
	m.forceFull = false
	m.resetResyncState()
}

// markFailed moves the member into StateFailed at the given generation,
// preserving whatever dirty-region knowledge still exists.
func (m *member) markFailed(gen uint64) {
	if m.state == StateResyncing {
		// Fold resync progress back into the dirty-region record.
		if m.partial {
			m.dirty = m.pending
		} else {
			m.dirty = nil
			m.forceFull = true
		}
	}
	m.state = StateFailed
	m.failGen = gen
	m.resetResyncState()
}
