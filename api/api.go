// Package api is the public face of the two-level perfect hash:
// Build / Lookup / Size / SelfCheck. It depends only on second.
package api

import (
	"errors"
	"fmt"

	"ontology/second"
)

// Sentinel errors: the four rejection categories, mutually distinguishable.
var (
	ErrDuplicateKey = errors.New("ontology: duplicate key")
	ErrEmptyKeys    = errors.New("ontology: empty key set")
	ErrNotFound     = errors.New("ontology: key not found")
	ErrInvalidParam = errors.New("ontology: invalid parameter")
)

// Table is an immutable perfect-hash lookup structure.
type Table struct {
	t *second.Table
	n int
}

// Build validates everything before constructing anything, so a rejected
// build leaves no trace. m must be >= 1, keys non-empty and distinct,
// p prime and greater than every key.
func Build(keys []int, m, p int) (*Table, error) {
	if m < 1 {
		return nil, ErrInvalidParam
	}
	if len(keys) == 0 {
		return nil, ErrEmptyKeys
	}
	seen := make(map[int]struct{}, len(keys))
	maxK := keys[0]
	for _, k := range keys {
		if _, dup := seen[k]; dup {
			return nil, ErrDuplicateKey
		}
		seen[k] = struct{}{}
		if k > maxK {
			maxK = k
		}
	}
	if p <= maxK || !isPrime(p) {
		return nil, ErrInvalidParam
	}
	return &Table{t: second.Build(keys, m, p), n: len(keys)}, nil
}

func isPrime(p int) bool {
	for d := 2; d*d <= p; d++ {
		if p%d == 0 {
			return false
		}
	}
	return p > 1
}

// Lookup reports whether x was built into the table; anything else is
// ErrNotFound. It never mutates the structure.
func (t *Table) Lookup(x int) (bool, error) {
	if t.t.Lookup(x) {
		return true, nil
	}
	return false, ErrNotFound
}

// Size returns the number of keys.
func (t *Table) Size() int { return t.n }

// Second exposes the underlying structure for read-only inspection.
func (t *Table) Second() *second.Table { return t.t }

// SelfCheck verifies the four invariants on the receiver; nil means healthy.
func (t *Table) SelfCheck() error {
	keys := t.t.Keys()
	if len(keys) == 0 || len(keys) != t.n {
		return fmt.Errorf("invariant1: %d slots hold %d keys", len(keys), t.n)
	}
	naive := make(map[int]bool, len(keys))
	maxK := keys[0]
	for _, k := range keys { // invariant 1: every built key found
		if ok, err := t.Lookup(k); !ok || err != nil {
			return fmt.Errorf("invariant1: built key %d not found", k)
		}
		naive[k] = true
		if k > maxK {
			maxK = k
		}
	}
	for _, k := range keys { // invariant 2: neighbours agree with naive map
		for _, x := range []int{k - 1, k + 1} {
			if ok, _ := t.Lookup(x); ok != naive[x] {
				return fmt.Errorf("invariant2: Lookup(%d) disagrees with naive", x)
			}
		}
	}
	if ok, _ := t.Lookup(maxK + 1); ok {
		return fmt.Errorf("invariant2: Lookup(%d) should miss", maxK+1)
	}
	if got, want := t.t.Space(); got != want { // invariant 3
		return fmt.Errorf("invariant3: space %d != sum n_j^2 %d", got, want)
	}
	// invariant 4: rejections are decidable and leave the receiver intact
	for _, err := range []error{buildErr([]int{1, 1}, 4, 5), buildErr(nil, 4, 5), buildErr([]int{1}, 0, 5)} {
		if err == nil {
			return errors.New("invariant4: invalid build accepted")
		}
	}
	if _, err := t.Lookup(maxK + 1); !errors.Is(err, ErrNotFound) {
		return errors.New("invariant4: absent key not ErrNotFound")
	}
	if t.Size() != t.n {
		return errors.New("invariant4: size changed")
	}
	for _, k := range keys {
		if ok, _ := t.Lookup(k); !ok {
			return fmt.Errorf("invariant4: key %d lost", k)
		}
	}
	return nil
}

func buildErr(keys []int, m, p int) error {
	_, err := Build(keys, m, p)
	return err
}
