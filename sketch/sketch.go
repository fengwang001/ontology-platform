// Package sketch implements a Count-Min Sketch (d rows, w columns): Add hits
// one column per row; Query is the per-row minimum (exact, or an overestimate).
package sketch

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"

	"ontology/ch"
)

// The three argument-failure sentinels are mutually distinct.
var ErrBadDimensions = errors.New("sketch: width and depth must be positive")
var ErrBadCount = errors.New("sketch: count must be positive")
var ErrBadKey = errors.New("sketch: key must be non-negative")
var ErrSelfCheck = errors.New("sketch: self-check failed")

// Sketch is a d*w table of counters kept in process memory.
type Sketch struct {
	mu    sync.RWMutex
	f     *ch.Family
	d     int
	table [][]int64
	// lastQueryProbes: cells touched by the most recent Query; unexported,
	// and no method ever returns its value.
	lastQueryProbes atomic.Int64
}

// New builds a sketch with w columns and d rows.
func New(w, d int) (*Sketch, error) {
	if w <= 0 || d <= 0 {
		return nil, ErrBadDimensions
	}
	f, err := ch.NewFamily(w)
	if err != nil {
		return nil, err
	}
	t := make([][]int64, d)
	for i := range t {
		t[i] = make([]int64, w)
	}
	return &Sketch{f: f, d: d, table: t}, nil
}

// Add validates first (rejected call leaves no trace), then hits every row.
func (s *Sketch) Add(key, count int64) error {
	if key < 0 {
		return ErrBadKey
	}
	if count <= 0 {
		return ErrBadCount
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for j := 0; j < s.d; j++ {
		s.table[j][s.f.Column(j+1, key)] += count
	}
	return nil
}

// Query returns the per-row minimum and records that it read exactly d cells.
func (s *Sketch) Query(key int64) (int64, error) {
	if key < 0 {
		return 0, ErrBadKey
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	est, probes := int64(-1), int64(0)
	for j := 0; j < s.d; j++ {
		v := s.table[j][s.f.Column(j+1, key)]
		probes++
		if est < 0 || v < est {
			est = v
		}
	}
	s.lastQueryProbes.Store(probes)
	return est, nil
}

// Cell returns one counter; row is 1-based. For demos and diagnostics.
func (s *Sketch) Cell(row, col int) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.table[row-1][col]
}

// SelfCheck: worked w=6,d=3 example (cells; Query(2)=4, Query(5)=
// Query(11)=3), failure leaves no trace, Query >= exact map, probes == d.
func (s *Sketch) SelfCheck() error {
	ck, _ := New(6, 3)
	for _, e := range [][2]int64{{2, 4}, {5, 2}, {11, 1}} {
		if err := ck.Add(e[0], e[1]); err != nil {
			return err
		}
	}
	want := map[[2]int]int64{{1, 2}: 4, {1, 5}: 3, {2, 5}: 7, {3, 1}: 4, {3, 4}: 3}
	checkCells := func(stage string) error {
		for c, v := range want {
			if g := ck.Cell(c[0], c[1]); g != v {
				return fmt.Errorf("%w: %s r%dc%d=%d want %d", ErrSelfCheck, stage, c[0], c[1], g, v)
			}
		}
		return nil
	}
	if err := checkCells("example"); err != nil {
		return err
	}
	for _, q := range [][2]int64{{2, 4}, {5, 3}, {11, 3}} {
		if g, err := ck.Query(q[0]); err != nil || g != q[1] {
			return fmt.Errorf("%w: Query(%d)=%d want %d", ErrSelfCheck, q[0], g, q[1])
		}
	}
	for _, e := range [][2]int64{{-1, 1}, {1, 0}, {1, -3}} {
		if err := ck.Add(e[0], e[1]); err == nil {
			return fmt.Errorf("%w: Add(%d,%d) accepted", ErrSelfCheck, e[0], e[1])
		}
	}
	if err := checkCells("after-reject"); err != nil {
		return err
	}
	rs, _ := New(256, 5)
	exact := map[int64]int64{}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 2000; i++ {
		k, c := rng.Int63n(1000), rng.Int63n(9)+1
		_ = rs.Add(k, c)
		exact[k] += c
	}
	for k, c := range exact {
		if g, _ := rs.Query(k); g < c {
			return fmt.Errorf("%w: Query(%d)=%d<%d", ErrSelfCheck, k, g, c)
		}
	}
	for _, m := range []int{100, 1000, 10000} {
		ps, _ := New(256, 5)
		for k := 0; k < m; k++ {
			_ = ps.Add(int64(k), 1)
		}
		if _, err := ps.Query(0); err != nil {
			return err
		}
		if n := ps.lastQueryProbes.Load(); n != int64(ps.d) {
			return fmt.Errorf("%w: m=%d probes=%d want %d", ErrSelfCheck, m, n, ps.d)
		}
	}
	return nil
}
