// Package msrc validates a single CDC source's event stream.
package msrc

import "errors"

// Event is one change-data-capture record from an upstream source.
type Event struct {
	Src string
	Seq int64
	TS  int64
	Key string
	Val string
}

var (
	ErrSeqNotStrictlyIncreasing = errors.New("msrc: seq not strictly increasing")
	ErrTSDecreasing             = errors.New("msrc: ts decreased")
	ErrEmptyKey                 = errors.New("msrc: empty key")
	ErrDupKeyTS                 = errors.New("msrc: duplicate (key, ts) in source")
)

type keyTS struct {
	key string
	ts  int64
}

// Validate checks one source's stream: Seq strictly increasing, TS
// non-decreasing, Key non-empty, (Key, TS) unique within the source.
func Validate(evs []Event) error {
	seen := make(map[keyTS]struct{}, len(evs))
	for i, e := range evs {
		if e.Key == "" {
			return ErrEmptyKey
		}
		if i > 0 {
			if e.Seq <= evs[i-1].Seq {
				return ErrSeqNotStrictlyIncreasing
			}
			if e.TS < evs[i-1].TS {
				return ErrTSDecreasing
			}
		}
		kt := keyTS{e.Key, e.TS}
		if _, ok := seen[kt]; ok {
			return ErrDupKeyTS
		}
		seen[kt] = struct{}{}
	}
	return nil
}
