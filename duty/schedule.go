package duty

import (
	"errors"
	"sync"
)

// Distinguishable rejection reasons. Every operation that is rejected returns
// one of these wrapped (or identical) errors so callers can use errors.Is.
var (
	// ErrEmptyRoster is returned by New when members is empty.
	ErrEmptyRoster = errors.New("duty: roster must contain at least one member")
	// ErrDuplicateMember is returned by New when members are not pairwise distinct.
	ErrDuplicateMember = errors.New("duty: roster contains duplicate members")
	// ErrInvalidLength is returned by New when L is not strictly positive.
	ErrInvalidLength = errors.New("duty: shift length L must be positive")

	// ErrEmptyInterval is returned when s == e.
	ErrEmptyInterval = errors.New("duty: override interval is empty")
	// ErrInvertedInterval is returned when s > e (also used for Timeline a > b).
	ErrInvertedInterval = errors.New("duty: interval endpoints are inverted")
	// ErrUnknownMember is returned when an override names a member not in the roster.
	ErrUnknownMember = errors.New("duty: override member is not in the roster")
	// ErrDuplicateOverride is returned when adding an id that is currently active.
	ErrDuplicateOverride = errors.New("duty: override id already exists")
	// ErrOverrideNotFound is returned when removing an id that is not active.
	ErrOverrideNotFound = errors.New("duty: override id does not exist")
	// ErrTimelineTooLarge is returned when a Timeline result exceeds 10000 segments.
	ErrTimelineTooLarge = errors.New("duty: timeline exceeds 10000 segments")
)

// maxSegments bounds the number of maximal contiguous segments Timeline emits.
const maxSegments = 10000

// Override is a temporary assignment [Start, End) to Member identified by Id.
// Overrides may overlap each other and the rotation; the last added, not yet
// deleted override wins at every instant.
type Override struct {
	ID     string
	Member string
	Start  int64
	End    int64
}

// Segment is one maximal contiguous piece of a Timeline.
type Segment struct {
	Start  int64
	End    int64
	Member string
	Source string // "rotation" or the winning override's id
}

// entry is an active override plus its insertion order (higher = newer).
type entry struct {
	ov  Override
	seq uint64
}

// Schedule is a concurrency-safe duty rota with overrides.
type Schedule struct {
	mu      sync.RWMutex
	members []string
	t0      int64
	length  int64
	index   map[string]int
	over    map[string]entry
	seq     uint64
}

// New validates the roster and shift length and returns an empty schedule.
func New(members []string, t0, length int64) (*Schedule, error) {
	if len(members) == 0 {
		return nil, ErrEmptyRoster
	}
	seen := make(map[string]struct{}, len(members))
	for _, m := range members {
		if _, dup := seen[m]; dup {
			return nil, ErrDuplicateMember
		}
		seen[m] = struct{}{}
	}
	if length <= 0 {
		return nil, ErrInvalidLength
	}
	roster := make([]string, len(members))
	copy(roster, members)
	idx := make(map[string]int, len(members))
	for i, m := range roster {
		idx[m] = i
	}
	return &Schedule{
		members: roster,
		t0:      t0,
		length:  length,
		index:   idx,
		over:    make(map[string]entry),
	}, nil
}

// Add validates and stores an override. A rejected add leaves the set unchanged.
// Check order is fixed: interval (empty, then inverted), member, duplicate id.
func (s *Schedule) Add(o Override) error {
	if err := validateInterval(o.Start, o.End); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.index[o.Member]; !ok {
		return ErrUnknownMember
	}
	if _, exists := s.over[o.ID]; exists {
		return ErrDuplicateOverride
	}
	s.seq++
	s.over[o.ID] = entry{ov: o, seq: s.seq}
	return nil
}

// Remove deletes an override; the times it covered fall back to the next
// lower-priority active override or the rotation. A previously removed id may
// be added again and is then treated as the newest override.
func (s *Schedule) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.over[id]; !ok {
		return ErrOverrideNotFound
	}
	delete(s.over, id)
	return nil
}

// validateInterval enforces empty before inverted, the required check order.
func validateInterval(start, end int64) error {
	if start == end {
		return ErrEmptyInterval
	}
	if start > end {
		return ErrInvertedInterval
	}
	return nil
}

// rotationMember returns members[floorMod(floorDiv(t-T0, L), n)].
// L is positive, so floorDiv is a truncating quotient plus one correction when
// the numerator is negative and not exactly divisible; floorMod is corrected to
// stay non-negative, giving the same rule before and after T0.
func (s *Schedule) rotationMember(t int64) string {
	delta := t - s.t0
	q := delta / s.length
	r := delta % s.length
	if r < 0 {
		q--
		r += s.length
	}
	idx := q % int64(len(s.members))
	if idx < 0 {
		idx += int64(len(s.members))
	}
	return s.members[idx]
}

// snapshot copies the active overrides under the read lock so a concurrent
// Add/Remove cannot tear a Who or Timeline computation.
func (s *Schedule) snapshot() []entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]entry, 0, len(s.over))
	for _, e := range s.over {
		out = append(out, e)
	}
	return out
}

// winnerAt scans the snapshot for the highest-seq override containing t.
func winnerAt(entries []entry, t int64) (entry, bool) {
	best, ok := entry{}, false
	for _, e := range entries {
		if e.ov.Start <= t && t < e.ov.End && (!ok || e.seq > best.seq) {
			best, ok = e, true
		}
	}
	return best, ok
}

// Who reports the on-duty member and source at instant t.
func (s *Schedule) Who(t int64) (member, source string) {
	if e, ok := winnerAt(s.snapshot(), t); ok {
		return e.ov.Member, e.ov.ID
	}
	return s.rotationMember(t), "rotation"
}
