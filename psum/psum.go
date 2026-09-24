// Package psum maintains an incremental prefix-sum view over ordered
// keys [0, n) via two Fenwick trees (values + key counts).
package psum

import (
	"errors"
	"sync"
	"sync/atomic"

	"ontology/fen"
)

// Sentinel errors, checked in this exact order.
var (
	ErrBadParam   = errors.New("psum: invalid parameter")
	ErrOutOfRange = errors.New("psum: key out of range")
	ErrNotFound   = errors.New("psum: key not found")
	ErrTooMany    = errors.New("psum: too many keys")
)

// View is an incremental prefix-sum view. Safe for concurrent use.
type View struct {
	mu         sync.RWMutex
	n, maxKeys int
	vals       map[int64]int64 // present keys -> current values
	valFen     *fen.Tree       // prefix sums over values
	cntFen     *fen.Tree       // prefix counts over present keys
	last       atomic.Int64    // nodes visited by the latest Put/Del/Prefix
}

// New builds an empty view over [0, n) holding at most maxKeys keys.
func New(n, maxKeys int) (*View, error) {
	if n < 1 || n > 1<<20 || maxKeys < 1 {
		return nil, ErrBadParam
	}
	return &View{n: n, maxKeys: maxKeys, vals: make(map[int64]int64), valFen: fen.New(n), cntFen: fen.New(n)}, nil
}

func (v *View) countLE(k int64, visits *int) int {
	le, c := v.cntFen.Sum(int(k))
	*visits += c
	return int(le)
}

// Put inserts or rewrites k, returning the affected-key count (a fresh key counts as 1).
func (v *View) Put(k, val int64) (int, error) {
	if val < -1_000_000_000 || val > 1_000_000_000 {
		return 0, ErrBadParam
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if k < 0 || k >= int64(v.n) {
		return 0, ErrOutOfRange
	}
	old, ok := v.vals[k]
	var visits, affected int
	if ok {
		if d := val - old; d != 0 {
			visits += v.valFen.Add(int(k), d)
			affected = len(v.vals) - v.countLE(k-1, &visits) // keys >= k shift
		}
		v.vals[k] = val
	} else {
		if len(v.vals) >= v.maxKeys {
			return 0, ErrTooMany
		}
		v.vals[k] = val
		visits += v.valFen.Add(int(k), val) + v.cntFen.Add(int(k), 1)
		affected = 1
		if val != 0 {
			affected += len(v.vals) - v.countLE(k, &visits) // keys > k shift
		}
	}
	v.last.Store(int64(visits))
	return affected, nil
}

// Del removes an existing key; the deleted key never counts.
func (v *View) Del(k int64) (int, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if k < 0 || k >= int64(v.n) {
		return 0, ErrOutOfRange
	}
	old, ok := v.vals[k]
	if !ok {
		return 0, ErrNotFound
	}
	delete(v.vals, k)
	visits := v.valFen.Add(int(k), -old) + v.cntFen.Add(int(k), -1)
	affected := 0
	if old != 0 {
		affected = len(v.vals) - v.countLE(k, &visits) // keys > k shift
	}
	v.last.Store(int64(visits))
	return affected, nil
}

// Prefix returns the prefix sum at k; a missing key is an error.
func (v *View) Prefix(k int64) (int64, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if k < 0 || k >= int64(v.n) {
		return 0, ErrOutOfRange
	}
	if _, ok := v.vals[k]; !ok {
		return 0, ErrNotFound
	}
	s, c := v.valFen.Sum(int(k))
	v.last.Store(int64(c))
	return s, nil
}

// View returns key -> prefix sum for every present key.
func (v *View) View() map[int64]int64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := make(map[int64]int64, len(v.vals))
	for k := range v.vals {
		s, _ := v.valFen.Sum(int(k))
		out[k] = s
	}
	return out
}

// CheckLogarithmic verifies node visits stay within 8*(log2 N + 2).
func CheckLogarithmic() error {
	const n = 1 << 20 // max allowed size; log2(n) = 20
	for _, m := range []int{100, 1000, 5000, 10000} {
		v, _ := New(n, m+1) // arguments are valid by construction
		for i := 0; i < m; i++ {
			_, _ = v.Put(int64(i*(n/m)), 1) // cannot fail: valid args
		}
		ops := []func() (int, error){
			func() (int, error) { return v.Put(7, 3) },  // insert
			func() (int, error) { return v.Put(7, -5) }, // modify
			func() (int, error) { return v.Del(7) },     // delete
			func() (int, error) { _, e := v.Prefix(int64((m - 1) * (n / m))); return 0, e },
		}
		for _, op := range ops {
			if _, err := op(); err != nil {
				return err
			}
			if v.last.Load() > 8*(20+2) {
				return errors.New("psum: node visits exceed logarithmic bound")
			}
		}
	}
	return nil
}
