// Package off holds one partition's commit position, checkpoint and the
// recovery rule over a surviving arc archive.
package off

import (
	"errors"
	"math"

	"ontology/arc"
)

// NoCheckpoint marks the -inf recovery position for a partition that has
// never checkpointed and whose archive is empty.
const NoCheckpoint = int64(math.MinInt64)

var (
	// ErrCommitNotMonotonic is returned when off does not strictly advance
	// or ts moves backwards.
	ErrCommitNotMonotonic = errors.New("off: commit not monotonic")
	// ErrNoCommit is returned when Checkpoint is called before any commit.
	ErrNoCommit = errors.New("off: no committed offset to checkpoint")
	// ErrNowRewound is returned when Evict's now precedes the previous one.
	ErrNowRewound = errors.New("off: evict time moved backwards")
)

// State is one partition's position state. The checkpoint lives outside
// the archive list, so evicting the archive can never erase a checkpoint.
type State struct {
	arc       *arc.List
	committed int64
	hasCommit bool
	cp        int64
	hasCP     bool
	lastTS    int64
	lastNow   int64
	hasNow    bool
}

func New() *State {
	return &State{arc: arc.New()}
}

// Commit advances the committed offset and appends an archive entry. Every
// validation runs before any state is touched, so a rejected call leaves
// the partition unchanged.
func (s *State) Commit(off, ts int64) error {
	if s.hasCommit {
		if off <= s.committed {
			return ErrCommitNotMonotonic
		}
		if ts < s.lastTS {
			return ErrCommitNotMonotonic
		}
	}
	s.arc.Append(off, ts)
	s.committed = off
	s.hasCommit = true
	s.lastTS = ts
	return nil
}

// Checkpoint pins the current committed offset. It requires a prior commit.
func (s *State) Checkpoint() error {
	if !s.hasCommit {
		return ErrNoCommit
	}
	s.cp = s.committed
	s.hasCP = true
	return nil
}

// Evict archives entries with ts < now-retention. now must not move
// backwards. The checkpoint is untouched.
func (s *State) Evict(now, retention int64) error {
	if s.hasNow && now < s.lastNow {
		return ErrNowRewound
	}
	s.lastNow = now
	s.hasNow = true
	s.arc.Evict(now - retention)
	return nil
}

func (s *State) Committed() (int64, bool) { return s.committed, s.hasCommit }

// Recover returns max(checkpoint, largest surviving archive offset),
// treating a missing checkpoint and empty archive as -inf.
func (s *State) Recover() int64 {
	r := NoCheckpoint
	if s.hasCP {
		r = s.cp
	}
	if m, ok := s.arc.Max(); ok && m > r {
		r = m
	}
	return r
}
