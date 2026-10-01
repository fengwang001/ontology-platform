package maglev

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrEmptyName       = errors.New("maglev: backend name must not be empty")
	ErrInvalidBackend  = errors.New("maglev: offset/skip/weight out of range")
	ErrBackendExists   = errors.New("maglev: backend already exists")
	ErrTableFull       = errors.New("maglev: backend count already equals M")
	ErrBackendNotFound = errors.New("maglev: backend not found")
	ErrNoBackends      = errors.New("maglev: no backends registered")
)

type backend struct {
	name   string
	offset int
	skip   int
	weight int
}

type Table struct {
	mu       sync.Mutex
	m        int
	lim      int
	backends map[string]*backend
	cur      []string
}

// New creates a Maglev-style lookup table with M slots.
// M must be a prime in [2, 65537] and lim must be in [1, M].
func New(m, lim int) (*Table, error) {
	if m < 2 || m > 65537 || !isPrime(m) || lim < 1 || lim > m {
		return nil, errors.New("maglev: invalid configuration")
	}
	return &Table{
		m:        m,
		lim:      lim,
		backends: make(map[string]*backend),
		cur:      make([]string, m),
	}, nil
}

// AddBackend registers a backend and immediately migrates the current table
// toward the freshly computed target table. It returns the number of slots
// whose owner actually changed.
func (t *Table) AddBackend(name string, offset, skip, weight int) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if name == "" {
		return 0, ErrEmptyName
	}
	if offset < 0 || offset >= t.m || skip < 1 || skip >= t.m || weight < 1 || weight > 16 {
		return 0, ErrInvalidBackend
	}
	if _, ok := t.backends[name]; ok {
		return 0, ErrBackendExists
	}
	if len(t.backends) >= t.m {
		return 0, ErrTableFull
	}

	t.backends[name] = &backend{name: name, offset: offset, skip: skip, weight: weight}
	return t.migrate(), nil
}

// RemoveBackend removes a backend and migrates the current table toward the
// freshly computed target table. It returns the number of changed slots.
func (t *Table) RemoveBackend(name string) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, ok := t.backends[name]; !ok {
		return 0, ErrBackendNotFound
	}
	delete(t.backends, name)
	return t.migrate(), nil
}

// Step migrates up to lim optional slots (ascending slot order) without
// changing the backend set. With no backends it does nothing.
func (t *Table) Step() int {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.backends) == 0 {
		return 0
	}
	return t.applyOptional(t.buildTarget(), t.lim)
}

// Pending returns the number of slots where cur differs from the target table.
func (t *Table) Pending() int {
	t.mu.Lock()
	defer t.mu.Unlock()

	target := t.buildTarget()
	pending := 0
	for s := 0; s < t.m; s++ {
		if t.cur[s] != target[s] {
			pending++
		}
	}
	return pending
}

// Lookup returns the backend owning slot h mod M.
func (t *Table) Lookup(h uint64) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.backends) == 0 {
		return "", ErrNoBackends
	}
	return t.cur[int(h%uint64(t.m))], nil
}

// Table returns a copy of the current slot ownership table.
func (t *Table) Table() []string {
	t.mu.Lock()
	defer t.mu.Unlock()

	out := make([]string, t.m)
	copy(out, t.cur)
	return out
}

// migrate first overwrites every mandatory slot (empty, or owned by a backend
// no longer in the set), then at most lim optional slots in ascending order.
func (t *Table) migrate() int {
	target := t.buildTarget()
	changed := 0
	for s := 0; s < t.m; s++ {
		owner := t.cur[s]
		if owner == "" {
			if target[s] != "" {
				t.cur[s] = target[s]
				changed++
			}
			continue
		}
		if _, ok := t.backends[owner]; !ok {
			t.cur[s] = target[s]
			changed++
		}
	}
	changed += t.applyOptional(target, t.lim)
	return changed
}

// applyOptional changes up to budget slots whose current owner differs from
// the target (and is still a live backend), in ascending slot order.
func (t *Table) applyOptional(target []string, budget int) int {
	changed := 0
	for s := 0; s < t.m && changed < budget; s++ {
		if t.cur[s] != target[s] {
			t.cur[s] = target[s]
			changed++
		}
	}
	return changed
}

// buildTarget fills the target table from scratch following the weighted
// round-robin rule. Backends take turns in ascending name (byte) order; in its
// turn backend i claims up to weight_i slots using perm_i(j) =
// (offset_i + j*skip_i) mod M, advancing pointer j_i past occupied slots.
// Filling stops the instant the table is full.
func (t *Table) buildTarget() []string {
	target := make([]string, t.m)
	n := len(t.backends)
	if n == 0 {
		return target
	}

	names := make([]string, 0, n)
	for name := range t.backends {
		names = append(names, name)
	}
	sort.Strings(names)

	bs := make([]*backend, n)
	ptrs := make([]int, n)
	for i, name := range names {
		bs[i] = t.backends[name]
	}

	filled := 0
	for filled < t.m {
		for i := 0; i < n && filled < t.m; i++ {
			b := bs[i]
			for k := 0; k < b.weight && filled < t.m; k++ {
				for {
					j := ptrs[i]
					slot := int((int64(b.offset) + int64(j)*int64(b.skip)) % int64(t.m))
					ptrs[i] = j + 1
					if target[slot] == "" {
						target[slot] = b.name
						filled++
						break
					}
				}
			}
		}
	}
	return target
}

func isPrime(n int) bool {
	if n < 2 {
		return false
	}
	if n%2 == 0 {
		return n == 2
	}
	for d := 3; int64(d)*int64(d) <= int64(n); d += 2 {
		if n%d == 0 {
			return false
		}
	}
	return true
}
