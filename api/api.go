// Package api is the public facade over prune: New, AddPartition, Query and
// SelfCheck. It depends only on prune (and, transitively, part).
package api

import (
	"errors"
	"slices"
	"sync"

	"ontology/part"
	"ontology/prune"
)

// The three fault classes are pairwise-distinct decidable sentinels.
var (
	ErrInvalidPartition = part.ErrInvalidPartition  // lo >= hi or min_v > max_v
	ErrInvalidPredicate = prune.ErrInvalidPredicate // PLo >= PHi or VLo >= VHi
	ErrUnknownPartition = prune.ErrUnknownPartition // result cites an unknown id
)

// DB is an in-process, in-memory partitioned table safe for concurrent use.
type DB struct {
	mu sync.RWMutex
	t  *prune.Table
}

func New() *DB { return &DB{t: prune.NewTable()} }

// AddPartition registers one range partition; every check precedes mutation,
// so a rejection leaves existing state untouched.
func (d *DB) AddPartition(id string, lo, hi, minv, maxv int64) error {
	p, err := part.New(id, lo, hi, minv, maxv)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.t.Add(p)
}

// Query returns the scan set and pruned set for p in [Plo,Phi), v in [Vlo,Vhi).
func (d *DB) Query(Plo, Phi, Vlo, Vhi int64) ([]string, []string, error) {
	d.mu.RLock()
	ids := d.t.IDs()
	d.mu.RUnlock()
	r, err := d.t.Prune(ids, prune.Predicate{PLo: Plo, PHi: Phi, VLo: Vlo, VHi: Vhi})
	if err != nil {
		return nil, nil, err
	}
	return r.Scan, r.Pruned, nil
}

type spec struct {
	id               string
	lo, hi, vlo, vhi int64
}

var fixture = []spec{
	{"P0", 0, 10, 10, 20}, {"P1", 10, 20, 30, 40}, {"P2", 20, 30, 50, 60},
	{"P3", 30, 40, 15, 25}, {"P4", 40, 50, 60, 80},
}

// boxes reports p-axis and v-axis intersection (brute truth) of s with a box.
func boxes(s spec, plo, phi, vlo, vhi int64) (bool, bool) {
	return s.hi > plo && s.lo < phi, s.vhi >= vlo && s.vlo < vhi
}

func newFixtureDB() *DB {
	d := New()
	for _, f := range fixture {
		if err := d.AddPartition(f.id, f.lo, f.hi, f.vlo, f.vhi); err != nil {
			panic(err)
		}
	}
	return d
}

// SelfCheck verifies the four invariants over the built-in fixture and a grid
// of predicates covering every equality boundary; nil means all held.
func SelfCheck() error {
	d := newFixtureDB()
	if scan, _, err := d.Query(20, 50, 30, 60); err != nil || !slices.Equal(scan, []string{"P2"}) {
		return errors.New("api: self-check fixture scan set mismatch")
	}
	pv := []int64{-5, 0, 10, 20, 25, 30, 40, 50, 60}
	vv := []int64{5, 15, 20, 25, 30, 40, 55, 60, 80, 90}
	for _, plo := range pv {
		for _, phi := range pv {
			if plo >= phi {
				continue
			}
			for _, vlo := range vv {
				for _, vhi := range vv {
					if vlo >= vhi {
						continue
					}
					scan, pruned, err := d.Query(plo, phi, vlo, vhi)
					if err != nil {
						return err
					}
					var wScan, wPruned, wRev []string
					for _, f := range fixture {
						pi, vi := boxes(f, plo, phi, vlo, vhi)
						if pi && vi { // invariants 1 (sound) and 2 (exact)
							wScan = append(wScan, f.id)
						} else {
							wPruned = append(wPruned, f.id)
						}
						if vi && pi { // invariant 3: conjunction commutes
							wRev = append(wRev, f.id)
						}
					}
					if !slices.Equal(scan, wScan) || !slices.Equal(pruned, wPruned) || !slices.Equal(wScan, wRev) {
						return errors.New("api: exactness/soundness/order mismatch")
					}
				}
			}
		}
	}
	// Invariant 4: rejected operations leave no trace; sentinels distinct.
	before, _, _ := d.Query(20, 50, 30, 60)
	if err := d.AddPartition("BAD", 10, 5, 1, 2); !errors.Is(err, ErrInvalidPartition) {
		return errors.New("api: expected ErrInvalidPartition")
	}
	if err := d.AddPartition("INV", 0, 5, 9, 8); !errors.Is(err, ErrInvalidPartition) {
		return errors.New("api: expected ErrInvalidPartition (v)")
	}
	if _, _, err := d.Query(5, 5, 0, 1); !errors.Is(err, ErrInvalidPredicate) {
		return errors.New("api: expected ErrInvalidPredicate")
	}
	after, _, _ := d.Query(20, 50, 30, 60)
	if !slices.Equal(before, after) {
		return errors.New("api: state changed after rejection")
	}
	// Unknown-id fault lives where ids are referenced (the prune layer).
	tt := prune.NewTable()
	x, _ := part.New("X", 0, 10, 0, 1)
	if err := tt.Add(x); err != nil {
		return err
	}
	q := prune.Predicate{PLo: 0, PHi: 1, VLo: 0, VHi: 1}
	if _, err := tt.Prune([]string{"GHOST"}, q); !errors.Is(err, ErrUnknownPartition) {
		return errors.New("api: expected ErrUnknownPartition")
	}
	if r, err := tt.Prune([]string{"X"}, q); err != nil || len(r.Scan) != 1 {
		return errors.New("api: table unusable after rejection")
	}
	return nil
}
