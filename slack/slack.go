// Package slack decides, for a single key, whether an incoming event is
// accepted within the closed K-slack tolerance window [high-K, high] and
// whether the per-key high watermark advances.
//
// It depends on no other package in this module.
package slack

// State is the per-key high-watermark state. The zero value is valid and
// means "no event seen yet" (High is not 0-as-watermark until the first
// event arrives).
type State struct {
	High int64
	seen bool
}

// Seen reports whether at least one event has been processed.
func (s *State) Seen() bool { return s.seen }

// Decision classifies one event.
type Decision int8

const (
	// First is the unconditional accept of a key's first event; High is set.
	First Decision = iota + 1
	// Accept means the event counts; High advances only when Seq > old High.
	Accept
	// Drop means the event is older than the window floor high-K.
	Drop
)

// Decide applies the rules for one event and mutates st in place.
//   - first event: always accepted, high becomes seq (high was "absent");
//   - seq > high: accepted, high advances to seq (high never retreats);
//   - seq <= high: accepted iff seq >= high-K (closed left boundary),
//     without advancing high; otherwise dropped, high untouched.
//
// K must be non-negative (validated by the constructor of the caller).
func Decide(st *State, seq, k int64) Decision {
	if !st.seen {
		st.High, st.seen = seq, true
		return First
	}
	if seq > st.High {
		st.High = seq
		return Accept
	}
	// seq <= high: distance high-seq is non-negative here. Drop strictly
	// beyond the floor; equality (seq == high-K) is accepted (left closed).
	if st.High-seq > k {
		return Drop
	}
	return Accept
}
