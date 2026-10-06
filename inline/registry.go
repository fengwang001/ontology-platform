package inline

import (
	"errors"
	"sort"
	"sync"
)

var (
	// ErrRegistryFrozen means a mutation was requested while at least one
	// decision task is running. The frozen-input policy rejects such requests;
	// the caller may retry after the task finishes.
	ErrRegistryFrozen   = errors.New("inline: registry frozen by a running decision task")
	ErrDuplicateFunc    = errors.New("inline: duplicate function name")
	ErrUnknownFunc      = errors.New("inline: unknown function")
	ErrConflictingFlags = errors.New("inline: function carries both noinline and alwaysinline")
)

// FlagBoth models the input error "noinline and alwaysinline on one function".
// It is rejected at registration time.
const FlagBoth Flag = 3

// Registry stores function definitions and serializes input mutation against
// running decision tasks. Policy (documented in DESIGN.md): inputs are frozen
// while a task runs; concurrent mutation requests fail immediately with
// ErrRegistryFrozen instead of affecting the running task.
type Registry struct {
	mu     sync.Mutex
	funcs  map[string]Func
	order  []string
	frozen int
}

func NewRegistry() *Registry {
	return &Registry{funcs: make(map[string]Func)}
}

// Add registers one function (a private copy is kept).
func (r *Registry) Add(f Func) error {
	if f.Flags == FlagBoth {
		return ErrConflictingFlags
	}
	if err := f.validate(); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen > 0 {
		return ErrRegistryFrozen
	}
	if _, ok := r.funcs[f.Name]; ok {
		return ErrDuplicateFunc
	}
	r.funcs[f.Name] = cloneFunc(f)
	r.order = append(r.order, f.Name)
	return nil
}

// Replace changes an existing function; it follows the same frozen policy.
func (r *Registry) Replace(f Func) error {
	if f.Flags == FlagBoth {
		return ErrConflictingFlags
	}
	if err := f.validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen > 0 {
		return ErrRegistryFrozen
	}
	if _, ok := r.funcs[f.Name]; !ok {
		return ErrUnknownFunc
	}
	r.funcs[f.Name] = cloneFunc(f)
	return nil
}

// Remove deletes a function; it follows the same frozen policy.
func (r *Registry) Remove(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen > 0 {
		return ErrRegistryFrozen
	}
	if _, ok := r.funcs[name]; !ok {
		return ErrUnknownFunc
	}
	delete(r.funcs, name)
	for i, n := range r.order {
		if n == name {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	return nil
}

// snapshot is an immutable deep copy used by one decision task. After it is
// taken, later registry mutations cannot reach the task.
type snapshot struct {
	cfg   Config
	funcs map[string]Func
	order []string
	base  map[string]float64
}

// beginTask freezes mutations and returns a private snapshot.
func (r *Registry) beginTask(cfg Config) *snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frozen++

	cfg = cfg.withDefaults()
	snap := &snapshot{
		cfg:   cfg,
		funcs: make(map[string]Func, len(r.funcs)),
		order: append([]string(nil), r.order...),
		base:  make(map[string]float64, len(r.funcs)),
	}
	sort.Strings(snap.order)
	for _, name := range snap.order {
		f := r.funcs[name]
		snap.funcs[name] = cloneFunc(f)
		snap.base[name] = deriveBaseHeat(f)
	}
	return snap
}

// endTask releases one freeze. The snapshot passed in is the caller's token.
func (r *Registry) endTask(_ *snapshot) {
	r.mu.Lock()
	r.frozen--
	if r.frozen < 0 {
		r.frozen = 0
	}
	r.mu.Unlock()
}

func cloneFunc(f Func) Func {
	f.Sites = append([]Site(nil), f.Sites...)
	return f
}

// deriveBaseHeat implements BaseHeat==0: max own-site heat, or 1 if none.
func deriveBaseHeat(f Func) float64 {
	if f.BaseHeat != 0 {
		return f.BaseHeat
	}
	var max float64 = 1
	for _, s := range f.Sites {
		if s.Heat > max {
			max = s.Heat
		}
	}
	return max
}
