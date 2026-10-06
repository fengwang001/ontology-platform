package mirror

import (
	"sort"
	"sync"
)

// Config describes a mirrored volume at creation time.
type Config struct {
	// Members is the fixed number of replicas, 2 to 4, numbered from 0.
	Members int
	// Blocks is the fixed number of logical blocks per member.
	Blocks int
	// DirtyLimit is the dirty-region limit in blocks. A member whose
	// dirty-region record would hold strictly more than this many
	// distinct blocks discards the record and is marked as requiring a
	// full resync.
	DirtyLimit int
}

// WorkStats counts primitive work units performed by volume operations.
// It exists so tests can prove that write cost depends only on the
// member count and partial-resync block selection only on the dirty
// block count, never on the total number of blocks.
type WorkStats struct {
	// MemberBlockWrites counts single-block writes to member data.
	MemberBlockWrites uint64
	// DirtyOps counts insert/delete/lookup operations on dirty-region
	// and pending-block sets.
	DirtyOps uint64
	// BlockCopies counts blocks copied during resync advances.
	BlockCopies uint64
}

// Volume is a mirrored block volume. All methods are safe for
// concurrent use; the result is always equivalent to some serial
// execution order (operations are serialized by an internal mutex).
type Volume struct {
	mu      sync.Mutex
	blocks  int
	limit   int
	gen     uint64
	members []*member
	stats   WorkStats
}

// NewVolume creates a volume whose members are all online and whose
// blocks are all empty. The volume generation starts at 1.
func NewVolume(cfg Config) (*Volume, error) {
	if cfg.Members < 2 || cfg.Members > 4 {
		return nil, newError(ErrCodeInvalidParam, "members must be between 2 and 4, got %d", cfg.Members)
	}
	if cfg.Blocks <= 0 {
		return nil, newError(ErrCodeInvalidParam, "blocks must be positive, got %d", cfg.Blocks)
	}
	if cfg.DirtyLimit < 0 {
		return nil, newError(ErrCodeInvalidParam, "dirty limit must be non-negative, got %d", cfg.DirtyLimit)
	}
	v := &Volume{
		blocks:  cfg.Blocks,
		limit:   cfg.DirtyLimit,
		gen:     1,
		members: make([]*member, cfg.Members),
	}
	for i := range v.members {
		v.members[i] = newMember(cfg.Blocks)
	}
	return v, nil
}

// MemberSnapshot reports the externally visible state of one member.
type MemberSnapshot struct {
	State       MemberState
	FailGen     uint64
	DirtyCount  int
	RecordValid bool
}

// Snapshot reports the externally visible state of the volume.
type Snapshot struct {
	Generation uint64
	Members   []MemberSnapshot
}

// Snapshot returns a consistent point-in-time view of the volume
// generation and every member's state, failure generation and
// dirty-region status.
func (v *Volume) Snapshot() Snapshot {
	v.mu.Lock()
	defer v.mu.Unlock()
	snap := Snapshot{Generation: v.gen, Members: make([]MemberSnapshot, len(v.members))}
	for i, m := range v.members {
		snap.Members[i] = MemberSnapshot{
			State:       m.state,
			FailGen:     m.failGen,
			DirtyCount:  len(m.dirty),
			RecordValid: !m.forceFull,
		}
	}
	return snap
}

// MemberBlocks returns a copy of one member's raw block contents.
// It is intended for diagnostics and tests.
func (v *Volume) MemberBlocks(id int) ([]string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	m, err := v.member(id)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(m.data))
	copy(out, m.data)
	return out, nil
}

// WorkStats returns the cumulative primitive-work counters.
func (v *Volume) WorkStats() WorkStats {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.stats
}

// member resolves a member id or reports ErrCodeMemberNotFound.
func (v *Volume) member(id int) (*member, error) {
	if id < 0 || id >= len(v.members) {
		return nil, newError(ErrCodeMemberNotFound, "member %d does not exist", id)
	}
	return v.members[id], nil
}

// onlineLocked returns the ids of all online members in ascending order.
func (v *Volume) onlineLocked() []int {
	var ids []int
	for i, m := range v.members {
		if m.state == StateOnline {
			ids = append(ids, i)
		}
	}
	return ids
}

// Write stores value at block on every online and resyncing member,
// except members listed in failed, which transition to StateFailed.
//
// The write succeeds when at least one online member accepts it; if all
// online members fail, the write is rejected with
// ErrCodeVolumeUnavailable and no state, generation or dirty-region
// change is applied. A successful write bumps the generation at most
// once regardless of how many members fail, and records the block in
// the dirty-region record of every non-online member that missed it.
func (v *Volume) Write(block int, value string, failed []int) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if block < 0 || block >= v.blocks {
		return newError(ErrCodeInvalidParam, "block %d out of range [0,%d)", block, v.blocks)
	}
	failedSet := make(map[int]struct{}, len(failed))
	for _, id := range failed {
		if _, err := v.member(id); err != nil {
			return err
		}
		failedSet[id] = struct{}{}
	}

	online := v.onlineLocked()
	if len(online) == 0 {
		return newError(ErrCodeVolumeUnavailable, "no online member")
	}
	survivors := 0
	for _, id := range online {
		if _, bad := failedSet[id]; !bad {
			survivors++
		}
	}
	if survivors == 0 {
		// Rejected writes leave no trace.
		return newError(ErrCodeVolumeUnavailable, "all online members failed the write")
	}

	newlyFailed := false
	for id, m := range v.members {
		_, bad := failedSet[id]
		switch m.state {
		case StateOnline, StateResyncing:
			if bad {
				m.markFailed(v.gen)
				newlyFailed = true
			} else {
				m.data[block] = value
				v.stats.MemberBlockWrites++
				if m.state == StateResyncing {
					v.clearPending(m, block)
				}
			}
		}
	}
	if newlyFailed {
		v.gen++
	}

	// Register the block in the dirty record of every member that did
	// not receive this write (all failed members, including the ones
	// that just failed).
	for _, m := range v.members {
		if m.state == StateFailed {
			v.addDirty(m, block)
		}
	}
	return nil
}

// Read returns the value stored at block, served by the lowest-numbered
// online member. Blocks never written read back as the empty string.
func (v *Volume) Read(block int) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if block < 0 || block >= v.blocks {
		return "", newError(ErrCodeInvalidParam, "block %d out of range [0,%d)", block, v.blocks)
	}
	for _, m := range v.members {
		if m.state == StateOnline {
			return m.data[block], nil
		}
	}
	return "", newError(ErrCodeVolumeUnavailable, "no online member")
}

// ReportFailed declares member id failed. The member must be online or
// resyncing. The generation grows by exactly one even when the report
// takes the last online member down, in which case every resyncing
// member fails as well, sharing the same failure generation.
func (v *Volume) ReportFailed(id int) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	m, err := v.member(id)
	if err != nil {
		return err
	}
	if m.state == StateFailed {
		return newError(ErrCodeStateMismatch, "member %d is already failed", id)
	}

	lastOnline := m.state == StateOnline && len(v.onlineLocked()) == 1
	m.markFailed(v.gen)
	if lastOnline {
		for _, other := range v.members {
			if other.state == StateResyncing {
				other.markFailed(v.gen)
			}
		}
	}
	v.gen++
	return nil
}

// Rejoin brings a failed member back using the generation label found
// on its disk. The member becomes resyncing: a partial resync over the
// dirty-region record when label equals the member's recorded failure
// generation and the record is still valid, a full resync otherwise.
//
// When every member is failed the volume has no authoritative data;
// only the member with the highest failure generation (lowest id on
// ties) may rejoin first, becoming online immediately, and every other
// member is from then on forced into a full resync.
func (v *Volume) Rejoin(id int, label uint64) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	m, err := v.member(id)
	if err != nil {
		return err
	}
	if m.state != StateFailed {
		return newError(ErrCodeStateMismatch, "member %d is %s, not failed", id, m.state)
	}
	if label > v.gen {
		return newError(ErrCodeGenerationAhead, "label %d exceeds volume generation %d", label, v.gen)
	}

	if v.allFailedLocked() {
		if id != v.authoritativeLocked() {
			return newError(ErrCodeNonAuthoritative, "member %d is not the authoritative member", id)
		}
		// The authoritative member's data defines the volume; all
		// other members must resync fully from now on.
		for _, other := range v.members {
			if other != m {
				other.dirty = nil
				other.forceFull = true
			}
		}
		m.markOnline()
		return nil
	}

	if label == m.failGen && !m.forceFull {
		m.partial = true
		m.pending = make(map[int]struct{}, len(m.dirty))
		m.order = make([]int, 0, len(m.dirty))
		for b := range m.dirty {
			m.pending[b] = struct{}{}
			m.order = append(m.order, b)
		}
		sort.Ints(m.order)
		v.stats.DirtyOps += uint64(len(m.dirty))
	} else {
		m.partial = false
		m.fullNext = 0
		m.fullSkip = make(map[int]struct{})
	}
	m.dirty = make(map[int]struct{})
	m.forceFull = false
	m.state = StateResyncing
	return nil
}

// ResyncAdvance copies up to limit pending blocks, in ascending block
// order, from the authoritative online data to the resyncing member,
// and returns the block numbers actually copied. The advance that
// finishes the last pending block turns the member online.
func (v *Volume) ResyncAdvance(id int, limit int) ([]int, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if limit <= 0 {
		return nil, newError(ErrCodeInvalidParam, "limit must be positive, got %d", limit)
	}
	m, err := v.member(id)
	if err != nil {
		return nil, err
	}
	if m.state != StateResyncing {
		return nil, newError(ErrCodeStateMismatch, "member %d is %s, not resyncing", id, m.state)
	}

	src := v.members[v.onlineLocked()[0]]
	var copied []int
	if m.partial {
		for m.orderIdx < len(m.order) && len(copied) < limit {
			b := m.order[m.orderIdx]
			m.orderIdx++
			if _, ok := m.pending[b]; !ok {
				continue // already written directly while resyncing
			}
			delete(m.pending, b)
			v.stats.DirtyOps++
			m.data[b] = src.data[b]
			v.stats.BlockCopies++
			copied = append(copied, b)
		}
		if m.orderIdx == len(m.order) {
			m.markOnline()
		}
	} else {
		for m.fullNext < v.blocks && len(copied) < limit {
			b := m.fullNext
			m.fullNext++
			if _, ok := m.fullSkip[b]; ok {
				delete(m.fullSkip, b)
				v.stats.DirtyOps++
				continue // written directly while resyncing
			}
			m.data[b] = src.data[b]
			v.stats.BlockCopies++
			copied = append(copied, b)
		}
		if m.fullNext == v.blocks {
			m.markOnline()
		}
	}
	return copied, nil
}

// addDirty registers block in a failed member's dirty-region record,
// discarding the record and setting the permanent full-resync mark
// when the distinct block count grows strictly beyond the limit.
func (v *Volume) addDirty(m *member, block int) {
	if m.forceFull {
		return
	}
	m.dirty[block] = struct{}{}
	v.stats.DirtyOps++
	if len(m.dirty) > v.limit {
		m.dirty = nil
		m.forceFull = true
	}
}

// clearPending removes block from a resyncing member's pending set
// after the block was written directly to that member.
func (v *Volume) clearPending(m *member, block int) {
	v.stats.DirtyOps++
	if m.partial {
		delete(m.pending, block)
		return
	}
	m.fullSkip[block] = struct{}{}
}

func (v *Volume) allFailedLocked() bool {
	for _, m := range v.members {
		if m.state != StateFailed {
			return false
		}
	}
	return true
}

// authoritativeLocked returns the id of the member with the highest
// failure generation, breaking ties by lowest id.
func (v *Volume) authoritativeLocked() int {
	best := 0
	for i, m := range v.members {
		if m.failGen > v.members[best].failGen {
			best = i
		}
	}
	return best
}
