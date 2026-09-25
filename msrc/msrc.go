// Package msrc validates a single upstream CDC source's event stream.
// It must not depend on merge or api.
package msrc

import "errors"

// Sentinel errors: callers judge failures with errors.Is.
var (
	// ErrSeqNotStrict: a later event's Seq is not greater than the previous.
	ErrSeqNotStrict = errors.New("msrc: seq not strictly increasing")
	// ErrTSDecreased: a later event's TS is smaller than the previous.
	ErrTSDecreased = errors.New("msrc: timestamp decreased")
	// ErrEmptyKey: an event carries an empty Key.
	ErrEmptyKey = errors.New("msrc: empty key")
	// ErrKeyTSDup: the same (Key,TS) appears twice inside one source.
	ErrKeyTSDup = errors.New("msrc: duplicate (key,ts) within source")
)

// Event is one CDC change from source Src. Within one source Seq is strictly
// increasing and TS is non-decreasing.
type Event struct {
	Src string
	Seq int64
	TS  int64
	Key string
	Val string
}

type keyTS struct {
	key string
	ts  int64
}

// Validate checks evs as the stream of one source:
//   - every Key is non-empty,
//   - Seq is strictly increasing,
//   - TS is non-decreasing,
//   - (Key,TS) is unique within the source.
//
// The first violation encountered decides the returned sentinel error.
// Validate is pure: it takes no state and cannot leave any trace.
func Validate(name string, evs []Event) error {
	_ = name
	seen := make(map[keyTS]struct{}, len(evs))
	var prev Event
	for i, e := range evs {
		if e.Key == "" {
			return ErrEmptyKey
		}
		if i > 0 {
			if e.Seq <= prev.Seq {
				return ErrSeqNotStrict
			}
			if e.TS < prev.TS {
				return ErrTSDecreased
			}
		}
		kt := keyTS{e.Key, e.TS}
		if _, ok := seen[kt]; ok {
			return ErrKeyTSDup
		}
		seen[kt] = struct{}{}
		prev = e
	}
	return nil
}
