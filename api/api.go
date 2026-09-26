// Package api is the process-memory facade over the sites package.
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/sites"
)

// Distinct sentinel errors for the three rejected-operation classes.
var (
	ErrDuplicateSite        = sites.ErrDuplicate
	ErrCoordinateOutOfBound = sites.ErrOutOfBounds
	ErrNegativeRadius       = sites.ErrBadRadius
)

type service struct{ reg *sites.Registry }

var def = newService()

func newService() *service { return &service{reg: sites.New()} }

// New resets the process-wide service to an empty state.
func New() error { def = newService(); return nil }

// Add inserts a site and returns its index (insertion order, from 0).
func Add(x, y int) (int, error) { return def.reg.Add(x, y) }

// Nearest returns the nearest site index; ties go to the smallest index.
func Nearest(qx, qy int) (int, error) { return def.reg.Nearest(qx, qy) }

// Within counts sites strictly inside the given circle.
func Within(qx, qy, r int) (int, error) { return def.reg.Within(qx, qy, r) }

// naiveNearest is the independent O(n) reference: minimum d2, ties min index.
func naiveNearest(pts [][2]int, qx, qy int) int {
	best, bd := 0, int64(1<<62)
	for i, p := range pts {
		dx, dy := int64(qx-p[0]), int64(qy-p[1])
		if d2 := dx*dx + dy*dy; d2 < bd {
			bd, best = d2, i
		}
	}
	return best
}

// SelfCheck replays built-in operation sequences and verifies the invariants:
// the eight-step derivation, naive-scan agreement, tie stability, distinct
// reject errors with no state change, sublinear candidate counts, and
// identical results under concurrent readers.
func SelfCheck() error {
	r := sites.New()
	// Eight-step sequence from NOTES.md.
	for k, a := range [][2]int{{0, 0}, {1, 1}, {4, 0}} {
		if idx, err := r.Add(a[0], a[1]); err != nil || idx != k {
			return fmt.Errorf("api: step %d add: got %d,%v", k+1, idx, err)
		}
	}
	for _, c := range []struct {
		op                string
		qx, qy, rad, want int
	}{
		{"nearest", 0, 2, 0, 1}, {"nearest", 1, 0, 0, 0},
	} {
		if n, err := r.Nearest(c.qx, c.qy); err != nil || n != c.want {
			return fmt.Errorf("api: %s(%d,%d): got %d,%v want %d", c.op, c.qx, c.qy, n, err, c.want)
		}
	}
	if idx, err := r.Add(0, 2); err != nil || idx != 3 {
		return fmt.Errorf("api: step6 add: got %d,%v", idx, err)
	}
	if n, err := r.Nearest(0, 2); err != nil || n != 3 {
		return fmt.Errorf("api: step7: got %d,%v want 3", n, err)
	}
	if n, err := r.Within(1, 0, 1); err != nil || n != 0 {
		return fmt.Errorf("api: step8: got %d,%v want 0", n, err)
	}
	// Agreement with the naive scan over many accepted sites and queries.
	nr := sites.New()
	pts := [][2]int{}
	for i := 0; i < 300; i++ {
		x, y := (i*173)%201-100, (i*977)%201-100
		if _, err := nr.Add(x, y); err == nil {
			pts = append(pts, [2]int{x, y})
		}
	}
	for i := 0; i < 200; i++ {
		qx, qy := (i*53)%199-99, (i*211)%197-98
		got, err := nr.Nearest(qx, qy)
		if err != nil || got != naiveNearest(pts, qx, qy) {
			return fmt.Errorf("api: naive mismatch at (%d,%d): got %d,%v", qx, qy, got, err)
		}
	}
	// Tie stability: four equidistant sites, smallest index must win.
	t := sites.New()
	for _, p := range [][2]int{{1, 0}, {0, 1}, {-1, 0}, {0, -1}} {
		if _, err := t.Add(p[0], p[1]); err != nil {
			return err
		}
	}
	if n, err := t.Nearest(0, 0); err != nil || n != 0 {
		return fmt.Errorf("api: tie: got %d,%v want 0", n, err)
	}
	// Three distinct error classes; rejected ops leave no trace.
	before := r.Len()
	_, eDup := r.Add(0, 0)
	_, eOOB := r.Add(10001, 0)
	_, eNeg := r.Within(0, 0, -1)
	if !errors.Is(eDup, ErrDuplicateSite) || errors.Is(eDup, ErrCoordinateOutOfBound) || errors.Is(eDup, ErrNegativeRadius) ||
		!errors.Is(eOOB, ErrCoordinateOutOfBound) || errors.Is(eOOB, ErrDuplicateSite) || errors.Is(eOOB, ErrNegativeRadius) ||
		!errors.Is(eNeg, ErrNegativeRadius) || errors.Is(eNeg, ErrDuplicateSite) || errors.Is(eNeg, ErrCoordinateOutOfBound) {
		return fmt.Errorf("api: reject errors are not distinct: %v / %v / %v", eDup, eOOB, eNeg)
	}
	if r.Len() != before {
		return fmt.Errorf("api: rejected op changed state: %d -> %d", before, r.Len())
	}
	if _, err := r.Add(9999, 9999); err != nil {
		return fmt.Errorf("api: registry unusable after rejection: %w", err)
	}
	if err := sites.CheckSublinear(); err != nil {
		return err
	}
	// Concurrent readers must agree field by field; no sleeps needed.
	const N = 16
	var wg sync.WaitGroup
	res := make([]int, N)
	errs := make([]error, N)
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) { defer wg.Done(); res[g], errs[g] = r.Nearest(7, -3) }(g)
	}
	wg.Wait()
	for g := 1; g < N; g++ {
		if errs[g] != errs[0] || res[g] != res[0] {
			return fmt.Errorf("api: concurrent readers diverged: (%d,%v) vs (%d,%v)", res[0], errs[0], res[g], errs[g])
		}
	}
	return nil
}
