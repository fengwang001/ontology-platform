// Package rec holds single-partition exactly-once state: the high-water
// offset, the retained-window value table, edge eviction and the fixed
// five-way decision (negative / new high water / equal duplicate /
// conflicting value / rewound). It depends on no other package.
package rec

import "errors"

// Sentinel errors. All three are mutually distinguishable via errors.Is.
var (
	ErrNegativeOffset = errors.New("rec: negative offset")
	ErrConflict       = errors.New("rec: offset retained with a different value")
	ErrRewound        = errors.New("rec: offset older than the retention window")
)

// Event is one partition-local change message.
type Event struct {
	Offset int64
	Value  string
}

// Pair is one retained offset/value entry.
type Pair struct {
	Offset int64
	Value  string
}

// State is one partition's bookkeeping. Construct with New.
type State struct {
	window int
	max    int64
	// order holds retained offsets ascending; the alive prefix is [head:].
	head  int
	order []int64
	vals  map[int64]string

	// checked counts retained entries inspected by the latest Apply while
	// deciding and evicting. Unexported diagnostic: it is never returned
	// through any exported API.
	checked int
}

// New returns an empty partition state; window must be >= 1 (validated by
// the caller one layer up).
func New(window int) *State {
	return &State{window: window, vals: map[int64]string{}}
}

// Max reports the largest effective offset so far (0 while empty).
func (s *State) Max() int64 { return s.max }

// Clone returns an independent deep copy; checked is reset.
func (s *State) Clone() *State {
	c := &State{window: s.window, max: s.max, vals: make(map[int64]string, len(s.vals))}
	c.order = append(c.order, s.order[s.head:]...)
	for off, v := range s.vals {
		c.vals[off] = v
	}
	return c
}

// Snapshot returns retained pairs in ascending offset order.
func (s *State) Snapshot() []Pair {
	out := make([]Pair, 0, len(s.order)-s.head)
	for _, off := range s.order[s.head:] {
		out = append(out, Pair{Offset: off, Value: s.vals[off]})
	}
	return out
}

// Apply runs the fixed decision order. It reports applied=true only for
// branch ②; branch ③ returns (false, nil); rejected branches return the
// matching sentinel and mutate nothing.
func (s *State) Apply(e Event) (applied bool, err error) {
	s.checked = 0
	if e.Offset < 0 { // ① illegal offset
		return false, ErrNegativeOffset
	}
	if e.Offset > s.max { // ② new high water: take effect, then evict
		s.vals[e.Offset] = e.Value
		s.order = append(s.order, e.Offset)
		s.max = e.Offset
		s.evict()
		return true, nil
	}
	v, ok := s.vals[e.Offset]
	s.checked++ // decision inspects exactly one retained-table entry (this probe)
	if !ok {    // ⑤ not retained: too old (or an unfillable hole), unverifiable
		return false, ErrRewound
	}
	if v == e.Value { // ③ equal-value duplicate: idempotent success
		return false, nil
	}
	return false, ErrConflict // ④ same offset, different value
}

// evict drops offsets below the window's lower edge max-window+1. Only the
// queue's lower edge is inspected, never the whole table. Afterwards the
// dead prefix is compacted away (a copy of at most window entries, not a
// decision inspection, so it does not touch checked).
func (s *State) evict() {
	cut := s.max - int64(s.window) + 1
	for s.head < len(s.order) && s.order[s.head] < cut {
		s.checked++
		delete(s.vals, s.order[s.head])
		s.head++
	}
	if s.head > 0 {
		alive := s.order[s.head:]
		s.order = append(make([]int64, 0, len(alive)), alive...)
		s.head = 0
	}
}

// SelfCheck replays built-in workloads on fresh single-partition states and
// verifies the bounded-work eviction: the entries inspected by an Apply at
// a large high water must not grow with the number of offsets ever seen.
// It reports pass/fail only; the inspection count never leaves the package.
func SelfCheck() error {
	for _, m := range []int{100, 1000, 10000} {
		st := New(8)
		for i := int64(1); i <= int64(m); i++ {
			if _, err := st.Apply(Event{Offset: i, Value: "v"}); err != nil {
				return err
			}
		}
		if _, err := st.Apply(Event{Offset: int64(m) + 1, Value: "v"}); err != nil {
			return err
		}
		if st.checked > 3 { // one entry truly evicted plus an m-independent slack
			return errors.New("rec: eviction inspected more than the window edge")
		}
	}
	return nil
}
