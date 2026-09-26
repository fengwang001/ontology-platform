package cuckoo

import (
	"errors"
	"fmt"
	"slices"
)

type snapshot struct {
	t1, t2 []int
	o1, o2 []bool
}

func (t *Table) snap() snapshot {
	return snapshot{slices.Clone(t.t1), slices.Clone(t.t2), slices.Clone(t.o1), slices.Clone(t.o2)}
}

func equalSnap(a, b snapshot) bool {
	return slices.Equal(a.t1, b.t1) && slices.Equal(a.t2, b.t2) &&
		slices.Equal(a.o1, b.o1) && slices.Equal(a.o2, b.o2)
}

// Dump4 returns T1/T2 as length-4 arrays (-1 marks empty slots); for demos
// and diagnostics on an n=4 table. It exposes no probe counters.
func (t *Table) Dump4() [2][4]int {
	var g [2][4]int
	for j := range g[0] {
		g[0][j], g[1][j] = -1, -1
		if t.o1[j] {
			g[0][j] = t.t1[j]
		}
		if t.o2[j] {
			g[1][j] = t.t2[j]
		}
	}
	return g
}

// SelfCheck verifies the four invariants on built-in scratch sequences and
// the O(1) probe bound at multiple table sizes. It never mutates the receiver.
func (t *Table) SelfCheck() error {
	if _, err := New(0, 1); !errors.Is(err, ErrInvalidN) {
		return fmt.Errorf("selfcheck invalid n: %v", err)
	}
	s, err := New(4, 8)
	if err != nil {
		return err
	}
	for _, x := range []int{0, 1, 2, 3, 4, 5, 6, 7} {
		if err := s.Insert(x); err != nil {
			return fmt.Errorf("selfcheck insert %d: %w", x, err)
		}
	}
	for x := 0; x <= 7; x++ {
		if ok, err := s.Lookup(x); !ok || err != nil {
			return fmt.Errorf("selfcheck lookup %d = %v, %v", x, ok, err)
		}
	}
	before := s.snap()
	if err := s.Insert(8); !errors.Is(err, ErrTableFull) {
		return fmt.Errorf("selfcheck insert 8: %v", err)
	}
	if after := s.snap(); !equalSnap(before, after) {
		return errors.New("selfcheck: ErrTableFull left a trace")
	}
	if err := s.Insert(4); !errors.Is(err, ErrDuplicate) {
		return fmt.Errorf("selfcheck duplicate: %v", err)
	}
	if _, err := s.Lookup(8); !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("selfcheck missing lookup: %v", err)
	}
	if err := s.Delete(8); !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("selfcheck missing delete: %v", err)
	}
	if err := s.Delete(0); err != nil {
		return err
	}
	if ok, _ := s.Lookup(0); ok {
		return errors.New("selfcheck: key found after delete")
	}
	for _, m := range []int{100, 1000, 10000} {
		b, err := New(4*m, 500) // ~12% load: at most one key per residue class
		if err != nil {
			return err
		}
		for i := 0; i < m; i++ {
			if err := b.Insert(i); err != nil {
				return fmt.Errorf("selfcheck tier %d insert %d: %w", m, i, err)
			}
		}
		if _, err := b.Lookup(0); err != nil {
			return err
		}
		if p := b.lastProbes.Load(); p != 2 {
			return fmt.Errorf("selfcheck tier %d probes = %d, want 2", m, p)
		}
	}
	return nil
}
