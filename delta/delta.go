// Package delta defines the change types and the pure function that applies a
// single Change to a map. It has no dependencies on the other packages.
package delta

import "errors"

// ErrEmptyKey is returned when a Change carries an empty key.
var ErrEmptyKey = errors.New("delta: change key must not be empty")

// Change is a single mutation: either Set{Key, Val} or Del{Key}.
// Del==true means delete; Val is ignored in that case.
type Change struct {
	Key string
	Val int
	Del bool
}

// Set builds a change that writes (or overwrites) Key with Val.
func Set(key string, val int) Change { return Change{Key: key, Val: val} }

// Del builds a change that deletes Key (a no-op if it is absent).
func Del(key string) Change { return Change{Key: key, Del: true} }

// Delta is an ordered batch of changes moving a replica From one version To a
// later version.
type Delta struct {
	From    int
	To      int
	Changes []Change
}

// ApplyChange applies one change to m, in place: a Set writes/overwrites, a Del
// deletes (no-op when the key is absent). It is a pure function of (m, c).
func ApplyChange(m map[string]int, c Change) error {
	if c.Key == "" {
		return ErrEmptyKey
	}
	if c.Del {
		delete(m, c.Key)
		return nil
	}
	m[c.Key] = c.Val
	return nil
}
