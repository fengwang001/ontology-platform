package main

import (
	"fmt"

	"ontology/snapshot"
	"ontology/store"
)

func readAll(s *store.Store, snaps []*snapshot.Snapshot, keys []string) []string {
	var out []string
	for _, snap := range snaps {
		for _, k := range keys {
			v, lk := s.ReadAt(snap, k)
			out = append(out, fmt.Sprintf("%d:%s", lk, v))
		}
	}
	return out
}

func checkReclaimStable() bool {
	s := newStore()
	keys := []string{"a", "b"}
	for r := 0; r < 3; r++ {
		for _, k := range keys {
			if !commitKV(s, k, fmt.Sprintf("%s-%d", k, r)) {
				return false
			}
		}
	}
	var snaps []*snapshot.Snapshot
	for i := 0; i < 3; i++ {
		snap, err := s.Begin()
		if err != nil {
			return false
		}
		snaps = append(snaps, snap)
		commitKV(s, keys[0], fmt.Sprintf("a-x%d", i))
	}
	defer func() {
		for _, snap := range snaps {
			s.Release(snap)
		}
	}()
	before := readAll(s, snaps, keys)
	s.Reclaim()
	after := readAll(s, snaps, keys)
	if len(before) != len(after) {
		return false
	}
	for i := range before {
		if before[i] != after[i] {
			return false
		}
	}
	return true
}

func examinedForN(n, k, m int) int64 {
	s := newStore()
	for r := 0; r < m; r++ {
		for i := 0; i < k; i++ {
			commitKV(s, fmt.Sprintf("hot-%d", i), "v")
		}
	}
	snap, _ := s.Begin()
	defer s.Release(snap)
	for r := 0; r < m; r++ {
		for i := 0; i < n-k; i++ {
			commitKV(s, fmt.Sprintf("cold-%d", i), "v")
		}
	}
	before := s.Stats().Examined
	s.Reclaim()
	return s.Stats().Examined - before
}

func checkIncremental() bool {
	e100 := examinedForN(100, 5, 10)
	e10000 := examinedForN(10000, 5, 10)
	fmt.Printf("     (N=100 考察 %d, N=10000 考察 %d)\n", e100, e10000)
	return e100 == e10000 && e100 == 45
}

func checkWaterMonotonic() bool {
	s := newStore()
	commitKV(s, "k", "v1")
	commitKV(s, "k", "v2")
	s.Reclaim()
	w1 := s.Stats().WaterLevel
	snap, _ := s.Begin()
	s.Reclaim()
	w2 := s.Stats().WaterLevel
	s.Release(snap)
	s.Reclaim()
	w3 := s.Stats().WaterLevel
	return w1 <= w2 && w2 <= w3
}
