// Package maglev implements a Maglev-style consistent hashing lookup table
// with per-backend weights and rate-limited migration.
//
// The table has M slots (M prime). Each backend contributes a permutation
// perm_i(j) = (offset_i + j*skip_i) mod M. The target table T* is built by
// weighted round-robin filling: backends in name order each occupy weight_i
// slots per round, advancing their own pointer j_i past already-taken slots.
//
// The current table cur does not jump to T* directly. After every successful
// AddBackend/RemoveBackend, all mandatory slots (empty or owned by a removed
// backend) are fixed first, then up to Lim optional slots (cur != T*) are
// migrated in ascending slot order. Step migrates up to Lim more optional
// slots without changing the backend set.
package maglev

import (
	"errors"
	"sort"
	"sync"
)

var (
	// ErrInvalidConfig reports a non-prime M outside [2, 65537] or a Lim
	// outside [1, M].
	ErrInvalidConfig = errors.New("maglev: invalid config: M must be a prime in [2, 65537] and Lim in [1, M]")
	// ErrEmptyName reports an empty backend name.
	ErrEmptyName = errors.New("maglev: backend name is empty")
	// ErrOutOfRange reports offset, skip or weight outside their valid ranges.
	ErrOutOfRange = errors.New("maglev: offset, skip or weight out of range")
	// ErrNameExists reports a duplicate backend name.
	ErrNameExists = errors.New("maglev: backend name already exists")
	// ErrTooManyBackends reports that the backend count has reached M.
	ErrTooManyBackends = errors.New("maglev: backend count reached M")
	// ErrBackendNotFound reports removal of an unknown backend.
	ErrBackendNotFound = errors.New("maglev: backend not found")
	// ErrNoBackends reports a lookup while the backend set is empty.
	ErrNoBackends = errors.New("maglev: no backends")
)

const (
	minM      = 2
	maxM      = 65537
	maxWeight = 16
)

type backend struct {
	offset int
	skip   int
	weight int
}

// Table is a Maglev-style lookup table with rate-limited migration.
// All methods are safe for concurrent use.
type Table struct {
	mu       sync.Mutex
	m        int
	lim      int
	backends map[string]backend
	cur      []string
}

// New builds an empty table of size m with per-operation migration limit
// lim. It returns ErrInvalidConfig unless m is a prime in [2, 65537] and
// lim is in [1, m].
func New(m, lim int) (*Table, error) {
	if m < minM || m > maxM || !isPrime(m) || lim < 1 || lim > m {
		return nil, ErrInvalidConfig
	}
	return &Table{
		m:        m,
		lim:      lim,
		backends: make(map[string]backend),
		cur:      make([]string, m),
	}, nil
}

func isPrime(n int) bool {
	if n < 2 {
		return false
	}
	for d := 2; d*d <= n; d++ {
		if n%d == 0 {
			return false
		}
	}
	return true
}

// M returns the table size.
func (t *Table) M() int {
	return t.m
}

// Lim returns the per-operation migration limit.
func (t *Table) Lim() int {
	return t.lim
}

// sortedNames returns the backend names in byte-wise ascending order.
func (t *Table) sortedNames() []string {
	names := make([]string, 0, len(t.backends))
	for name := range t.backends {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// target computes the target table T* from the current backend set.
// It depends only on the set of backends, not on registration order.
func (t *Table) target() []string {
	tstar := make([]string, t.m)
	names := t.sortedNames()
	if len(names) == 0 {
		return tstar
	}
	js := make([]int, len(names))
	filled := 0
	for filled < t.m {
		for i, name := range names {
			b := t.backends[name]
			for k := 0; k < b.weight; k++ {
				for {
					s := (b.offset + js[i]*b.skip) % t.m
					js[i]++
					if tstar[s] == "" {
						tstar[s] = name
						filled++
						break
					}
				}
				if filled == t.m {
					break
				}
			}
			if filled == t.m {
				break
			}
		}
	}
	return tstar
}

// migrate moves cur towards T*: first every mandatory slot (empty or owned
// by a backend no longer in the set), then up to lim optional slots in
// ascending slot order. It returns the number of slots actually changed.
// The caller must hold t.mu.
func (t *Table) migrate() int {
	tstar := t.target()
	changed := 0
	for s := 0; s < t.m; s++ {
		if t.cur[s] == "" {
			if tstar[s] != "" {
				t.cur[s] = tstar[s]
				changed++
			}
			continue
		}
		if _, ok := t.backends[t.cur[s]]; !ok {
			t.cur[s] = tstar[s]
			changed++
		}
	}
	changed += t.fillOptional(tstar, t.lim)
	return changed
}

// fillOptional migrates up to limit optional slots (cur != T*) in ascending
// slot order and returns how many were changed. The caller must hold t.mu.
func (t *Table) fillOptional(tstar []string, limit int) int {
	changed := 0
	for s := 0; s < t.m && changed < limit; s++ {
		if t.cur[s] != tstar[s] {
			t.cur[s] = tstar[s]
			changed++
		}
	}
	return changed
}

// AddBackend registers a backend and migrates cur towards the new T*.
// It returns the number of slots whose ownership changed. Rejection reasons
// are reported in this order, first match only: ErrEmptyName, ErrOutOfRange,
// ErrNameExists, ErrTooManyBackends. A rejected call changes nothing.
func (t *Table) AddBackend(name string, offset, skip, weight int) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if name == "" {
		return 0, ErrEmptyName
	}
	if offset < 0 || offset >= t.m || skip < 1 || skip >= t.m || weight < 1 || weight > maxWeight {
		return 0, ErrOutOfRange
	}
	if _, ok := t.backends[name]; ok {
		return 0, ErrNameExists
	}
	if len(t.backends) >= t.m {
		return 0, ErrTooManyBackends
	}
	t.backends[name] = backend{offset: offset, skip: skip, weight: weight}
	return t.migrate(), nil
}

// RemoveBackend deletes a backend and migrates cur towards the new T*.
// It returns the number of slots whose ownership changed. Unknown names are
// rejected with ErrBackendNotFound and change nothing.
func (t *Table) RemoveBackend(name string) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.backends[name]; !ok {
		return 0, ErrBackendNotFound
	}
	delete(t.backends, name)
	return t.migrate(), nil
}

// Step migrates up to Lim optional slots in ascending slot order without
// changing the backend set, and returns the number of slots changed. It is
// a no-op when the backend set is empty.
func (t *Table) Step() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.backends) == 0 {
		return 0
	}
	return t.fillOptional(t.target(), t.lim)
}

// Pending returns the number of slots where cur differs from T*.
func (t *Table) Pending() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	tstar := t.target()
	n := 0
	for s := 0; s < t.m; s++ {
		if t.cur[s] != tstar[s] {
			n++
		}
	}
	return n
}

// Lookup returns the backend name owning slot h mod M. It returns
// ErrNoBackends when the backend set is empty.
func (t *Table) Lookup(h uint64) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	name := t.cur[h%uint64(t.m)]
	if name == "" {
		return "", ErrNoBackends
	}
	return name, nil
}

// Table returns a copy of the current table; empty strings mark unassigned
// slots (only possible while the backend set is empty).
func (t *Table) Table() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, t.m)
	copy(out, t.cur)
	return out
}
