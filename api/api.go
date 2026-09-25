// Package api is the public, concurrency-safe entry point to the reference-counting plus trial-deletion reclaimer.
package api

import (
	"errors"
	"fmt"
	"ontology/refc"
	"ontology/trial"
	"sync"
)

type Obj = refc.Obj // zero denotes nil
type Collector struct {
	mu sync.Mutex
	g  *trial.Graph
}

const defaultLimit = 1 << 30

func New() *Collector                   { return &Collector{g: trial.New(defaultLimit)} }
func (c *Collector) Root() (Obj, error) { c.mu.Lock(); defer c.mu.Unlock(); return c.g.Root() }
func (c *Collector) Unroot(o Obj) error { c.mu.Lock(); defer c.mu.Unlock(); return c.g.Unroot(o) }
func (c *Collector) Point(from, to Obj) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.g.Point(from, to)
}
func (c *Collector) RefCount(o Obj) int { c.mu.Lock(); defer c.mu.Unlock(); return c.g.Heap().RC(o) }
func (c *Collector) Collect() int       { c.mu.Lock(); defer c.mu.Unlock(); return c.g.Collect() }
func (c *Collector) Snapshot() []refc.Snap {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.g.Heap().Snapshot()
}

var (
	ErrUnknownObject = trial.ErrUnknownObject // unknown/freed object
	ErrNoRoot        = trial.ErrNoRoot        // Unroot would go negative
	ErrLimit         = trial.ErrLimit         // object limit reached
	errConserve      = errors.New("rc conservation violated")
	errDangling      = errors.New("dangling child reference")
	errReach         = errors.New("live set differs from naive reachability")
)

// check verifies invariant 1 (conservation) and 3 (no dangling), plus 2 (live == naive reachable) when reach is set.
func check(h *refc.Heap, reach bool) error {
	live := h.Snapshot()
	alive, indeg := map[Obj]bool{}, map[Obj]int{}
	for _, s := range live {
		alive[s.O] = true
		if s.Child != 0 {
			indeg[s.Child]++
		}
	}
	for _, s := range live {
		if s.Child != 0 && !alive[s.Child] {
			return errDangling
		}
		if s.Roots < 0 || s.Fields != indeg[s.O] {
			return errConserve
		}
	}
	if !reach {
		return nil
	}
	seen, st := map[Obj]bool{}, []Obj{}
	for _, s := range live {
		if s.Roots > 0 {
			seen[s.O], st = true, append(st, s.O)
		}
	}
	for len(st) > 0 {
		x := st[len(st)-1]
		st = st[:len(st)-1]
		if d := h.Child(x); d != 0 && alive[d] && !seen[d] {
			seen[d], st = true, append(st, d)
		}
	}
	if len(seen) != len(live) {
		return errReach
	}
	return nil
}
func rcOr(h *refc.Heap, o Obj) int {
	if h.Alive(o) {
		return h.RC(o)
	}
	return -1
}

// SelfCheck runs the built-in sequences on a fresh graph: eight-step table,
// rooted/rootless cycles, self cycle, and the three rejection kinds.
func (c *Collector) SelfCheck() error {
	g := trial.New(1 << 20)
	h := g.Heap()
	var A, B Obj
	want := [8][2]int{{1, -1}, {1, 1}, {1, 2}, {2, 2}, {1, 2}, {1, 1}, {-1, -1}, {-1, -1}}
	got := make([][2]int, 0, 8)
	step := func(fn func()) { fn(); got = append(got, [2]int{rcOr(h, A), rcOr(h, B)}) }
	step(func() { A, _ = g.Root() })
	step(func() { B, _ = g.Root() })
	step(func() { _ = g.Point(A, B) })
	step(func() { _ = g.Point(B, A) })
	step(func() { _ = g.Unroot(A) })
	step(func() { _ = g.Unroot(B) })
	if f := g.Collect(); f != 2 || h.Len() != 0 {
		return fmt.Errorf("eight-step Collect freed %d live %d", f, h.Len())
	}
	got = append(got, [2]int{-1, -1}, [2]int{-1, -1})
	for i := range want {
		if got[i] != want[i] {
			return fmt.Errorf("eight-step row %d: %v want %v", i+1, got[i], want[i])
		}
	}
	X, _ := g.Root() // (乙) rooted X<->Y must be rescued
	Y, _ := g.Root()
	_ = g.Point(X, Y)
	_ = g.Point(Y, X)
	if g.Collect() != 0 {
		return errors.New("rooted cycle wrongly collected")
	}
	if err := check(h, true); err != nil {
		return err
	}
	_ = g.Unroot(X) // (丙) rootless X<->Y plus self cycle Z
	_ = g.Unroot(Y)
	Z, _ := g.Root()
	_ = g.Point(Z, Z)
	_ = g.Unroot(Z)
	if g.Collect() != 3 {
		return errors.New("rootless/self cycles not collected")
	}
	n := h.Len() // invariant 4: distinct sentinels, no trace on rejection
	if err := g.Unroot(Obj(1 << 40)); !errors.Is(err, ErrUnknownObject) || h.Len() != n {
		return errors.New("unknown-object rejection left a trace")
	}
	P, _ := g.Root()
	Q, _ := g.Root()
	_ = g.Point(P, Q)
	_ = g.Unroot(Q) // Q: roots==0, alive only via P->Q
	if !errors.Is(g.Unroot(Q), ErrNoRoot) || h.RC(Q) != 1 {
		return errors.New("negative Unroot rejection left a trace")
	}
	gl := trial.New(1)
	_, _ = gl.Root()
	if _, err := gl.Root(); !errors.Is(err, ErrLimit) {
		return fmt.Errorf("limit error mismatch: %v", err)
	}
	return check(h, true) // P->Q is exactly the naive reachable set
}
