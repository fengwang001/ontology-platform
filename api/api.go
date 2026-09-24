// Package api is the public face of the incremental prefix-sum view.
package api

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"slices"

	"ontology/psum"
)

var ( // sentinel errors re-exported from psum
	ErrBadParam   = psum.ErrBadParam
	ErrOutOfRange = psum.ErrOutOfRange
	ErrNotFound   = psum.ErrNotFound
	ErrTooMany    = psum.ErrTooMany
)

// View is an incremental prefix-sum view over keys [0, n).
type View struct{ v *psum.View }

// New builds an empty view over [0, n) holding at most maxKeys keys.
func New(n, maxKeys int) (*View, error) {
	v, err := psum.New(n, maxKeys)
	if err != nil {
		return nil, err
	}
	return &View{v: v}, nil
}

// Put, Del, Prefix and View forward to the underlying psum.View.
func (a *View) Put(k, val int64) (int, error) { return a.v.Put(k, val) }
func (a *View) Del(k int64) (int, error)      { return a.v.Del(k) }
func (a *View) Prefix(k int64) (int64, error) { return a.v.Prefix(k) }
func (a *View) View() map[int64]int64         { return a.v.View() }

// naive is a reference model: sort present keys, fold from the head.
type naive struct{ vals map[int64]int64 }

func (n *naive) view() map[int64]int64 {
	ks := make([]int64, 0, len(n.vals))
	for k := range n.vals {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	out := make(map[int64]int64, len(ks))
	var run int64
	for _, k := range ks {
		run += n.vals[k]
		out[k] = run
	}
	return out
}

// checkRandom compares random Put/Del sequences with the naive model
// after every step: view and affected-key count (invariants 1, 2).
func checkRandom(n, maxKeys, ops int, seed int64) error {
	a, err := New(n, maxKeys)
	if err != nil {
		return err
	}
	m := &naive{vals: map[int64]int64{}}
	r := rand.New(rand.NewSource(seed))
	for i := 0; i < ops; i++ {
		k := int64(r.Intn(n))
		before := m.view()
		got, want, inserted := 0, 0, false
		if r.Intn(2) == 0 {
			val := int64(r.Intn(21) - 10)
			_, exists := m.vals[k]
			if !exists && len(m.vals) >= maxKeys {
				if _, err := a.Put(k, val); !errors.Is(err, ErrTooMany) {
					return fmt.Errorf("op %d: put err=%v, want ErrTooMany", i, err)
				}
				continue
			}
			if got, err = a.Put(k, val); err != nil {
				return fmt.Errorf("op %d: %w", i, err)
			}
			m.vals[k], inserted = val, !exists
		} else {
			if _, exists := m.vals[k]; !exists {
				if _, err := a.Del(k); !errors.Is(err, ErrNotFound) {
					return fmt.Errorf("op %d: del err=%v, want ErrNotFound", i, err)
				}
				continue
			}
			if got, err = a.Del(k); err != nil {
				return fmt.Errorf("op %d: %w", i, err)
			}
			delete(m.vals, k)
		}
		after := m.view()
		for key, s := range after { // naive affected count
			if inserted && key == k || before[key] != s {
				want++
			}
		}
		if got != want {
			return fmt.Errorf("op %d: affected=%d, want %d", i, got, want)
		}
		if !reflect.DeepEqual(a.View(), after) {
			return fmt.Errorf("op %d: view diverges from naive", i)
		}
	}
	return nil
}

// SelfCheck replays built-in sequences verifying NOTES.md's invariants.
func SelfCheck() error {
	for _, tc := range []struct {
		n, max, ops int
		seed        int64
	}{
		{16, 16, 300, 1}, {100, 40, 600, 2}, {1 << 20, 5000, 800, 3},
	} {
		if err := checkRandom(tc.n, tc.max, tc.ops, tc.seed); err != nil {
			return err
		}
	}
	a, _ := New(64, 64) // valid by construction
	_, _ = a.Put(7, 9)
	base := a.View()
	_, _ = a.Put(7, -3) // modify, then revert (invariant 3)
	_, _ = a.Put(7, 9)
	_, _ = a.Put(20, 4) // insert, then delete
	_, _ = a.Del(20)
	if !reflect.DeepEqual(a.View(), base) {
		return errors.New("api: invariant 3 (reversibility) violated")
	}
	small, _ := New(8, 2)
	_, _ = small.Put(1, 1)
	_, _ = small.Put(2, 2)
	snap := small.View()
	rejects := []func() error{ // invariant 4: all-or-nothing
		func() error { _, e := small.Put(3, 1); return e },    // too many
		func() error { _, e := small.Put(8, 1); return e },    // out of range
		func() error { _, e := small.Put(1, 1e10); return e }, // bad param
		func() error { _, e := small.Del(5); return e },       // not found
		func() error { _, e := small.Prefix(5); return e },    // not found
	}
	for i, rej := range rejects {
		if rej() == nil || !reflect.DeepEqual(small.View(), snap) {
			return fmt.Errorf("api: reject %d succeeded or left a trace", i)
		}
	}
	return nil
}
