// Package api is the public facade over the materialized-view DAG.
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/dag"
	"ontology/view"
)

// The four required failure kinds are mutually distinguishable.
var (
	ErrEmptyName  = errors.New("api: empty view name")
	ErrExists     = errors.New("api: view name already exists")
	ErrCycle      = errors.New("api: dependency cycle detected")
	ErrUnresolved = errors.New("api: unresolved view name")
	errMap        = map[error]error{dag.ErrEmptyName: ErrEmptyName, dag.ErrExists: ErrExists,
		dag.ErrCycle: ErrCycle, view.ErrUnresolved: ErrUnresolved, view.ErrNotBase: view.ErrNotBase}
)

// Registry is the concurrency-safe entry point.
type Registry struct {
	mu sync.RWMutex
	s  *view.Store
}

func New() *Registry { return &Registry{s: view.New()} }

func mapErr(e error) error {
	if x, ok := errMap[e]; ok {
		return x
	}
	return e
}

func (r *Registry) write(f func() error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return mapErr(f())
}

// AddView registers a view; forward references are allowed.
func (r *Registry) AddView(n string, d []string, f func(...int64) int64) error {
	return r.write(func() error { return r.s.AddView(n, d, f) })
}
func (r *Registry) Set(n string, v int64) error {
	return r.write(func() error { return r.s.Set(n, v) })
}
func (r *Registry) Recompute() error { return r.write(r.s.Recompute) }

// Get returns the current value of a view.
func (r *Registry) Get(n string) (int64, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok, e := r.s.Get(n)
	return v, ok, mapErr(e)
}

type vspec struct {
	n string
	d []string
	f func(...int64) int64
}

// SelfCheck runs the built-in sequence of §3 on a fresh registry and
// verifies the four invariants; it never mutates receiver state.
func (r *Registry) SelfCheck() (err error) {
	defer func() {
		if x := recover(); x != nil {
			err = fmt.Errorf("api: selfcheck failed: %v", x)
		}
	}()
	must := func(e error) {
		if e != nil {
			panic(e)
		}
	}
	sum := func(x ...int64) int64 { return x[0] + x[1] }
	dbl := func(x ...int64) int64 { return x[0] * 2 }
	dec := func(x ...int64) int64 { return x[0] - 1 }
	// Invariants 1&2: §3 sequence (E,F before C,D); values equal a naive
	// dependency-first batch recomputation.
	c := New()
	for _, v := range []vspec{{"A", nil, nil}, {"B", nil, nil}, {"E", []string{"C", "D"}, sum},
		{"F", []string{"D"}, dec}, {"C", []string{"A", "B"}, sum}, {"D", []string{"A"}, dbl}} {
		must(c.AddView(v.n, v.d, v.f))
	}
	exp := [][6]int64{{1, 2, 3, 2, 5, 1}, {1, 20, 21, 2, 23, 1}, {10, 20, 30, 20, 50, 19}}
	for i, ab := range [][2]int64{{1, 2}, {1, 20}, {10, 20}} {
		must(c.Set("A", ab[0]))
		must(c.Set("B", ab[1]))
		must(c.Recompute())
		for j, n := range []string{"A", "B", "C", "D", "E", "F"} {
			g, ok, e := c.Get(n)
			if e != nil || !ok || g != exp[i][j] {
				panic(fmt.Sprintf("phase %d %s=%d want %d", i, n, g, exp[i][j]))
			}
		}
	}
	// Invariant 3: boolean dirty marks; diamond-invalidated views are
	// evaluated once, clean views zero times.
	d := New()
	calls := map[string]int{}
	cf := func(n string, f func(...int64) int64) func(...int64) int64 {
		return func(x ...int64) int64 {
			calls[n]++
			return f(x...)
		}
	}
	for _, v := range []vspec{{"a", nil, nil}, {"b", []string{"a"}, cf("b", dbl)},
		{"c", []string{"a"}, cf("c", dbl)}, {"v", []string{"b", "c"}, cf("v", sum)},
		{"w", nil, nil}, {"u", []string{"w"}, cf("u", dbl)}} {
		must(d.AddView(v.n, v.d, v.f))
	}
	must(d.Set("a", 1))
	must(d.Recompute())
	for n, w := range map[string]int{"b": 1, "c": 1, "v": 1, "u": 0} {
		if calls[n] != w {
			panic(fmt.Sprintf("dedup %s=%d want %d", n, calls[n], w))
		}
	}
	// Invariant 4: four distinct errors; rejected ops leave no trace and
	// the registry stays usable.
	must(d.AddView("m", []string{"n"}, nil))
	bad := []struct {
		op func() error
		er error
	}{
		{func() error { return d.AddView("", nil, nil) }, ErrEmptyName},
		{func() error { return d.AddView("a", nil, nil) }, ErrExists},
		{func() error { return d.AddView("n", []string{"m"}, nil) }, ErrCycle},
		{func() error { return d.Set("z", 1) }, ErrUnresolved},
	}
	for _, b := range bad {
		if !errors.Is(b.op(), b.er) {
			panic(fmt.Sprintf("want %v", b.er))
		}
	}
	if _, _, e := d.Get("n"); !errors.Is(e, ErrUnresolved) {
		panic("rejected view left a trace")
	}
	must(d.Recompute())
	if v, _, e := d.Get("v"); e != nil || v != 4 {
		panic("state changed by rejected op")
	}
	return nil
}
