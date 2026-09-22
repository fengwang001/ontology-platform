package ontology

import (
	"fmt"
	"sync"
	"testing"
)

func fillSequence(t *testing.T, s *Sequence, n int) []string {
	t.Helper()
	want := make([]string, 0, n)
	for i := 0; i < n; i++ {
		v := fmt.Sprintf("item-%03d", i)
		if _, err := s.Insert("", "", v); err != nil {
			t.Fatalf("seed insert %s: %v", v, err)
		}
		want = append(want, v)
	}
	return want
}

func TestRebalancePreservesOrder(t *testing.T) {
	s := NewSequence(nil, 64)
	want := fillSequence(t, s, 100)
	before := values(s.Snapshot())
	if fmt.Sprint(before) != fmt.Sprint(want) {
		t.Fatalf("setup order = %v", before)
	}
	s.Rebalance()
	after := s.Snapshot()
	if got := values(after); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("order after rebalance = %v, want %v", got, want)
	}
	for i := 1; i < len(after); i++ {
		if after[i-1].Key >= after[i].Key {
			t.Fatalf("keys not increasing after rebalance: %q >= %q",
				after[i-1].Key, after[i].Key)
		}
	}
	if length, _ := s.LongestKey(); length > 2 {
		t.Fatalf("longest key after rebalance = %d, want <= 2", length)
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck after rebalance: %v", err)
	}
}

func TestRebalanceAtomicUnderConcurrentReads(t *testing.T) {
	const n = 200
	s := NewSequence(nil, 64)
	fillSequence(t, s, n)

	oldKeys := make(map[string]bool, n)
	for _, e := range s.Snapshot() {
		oldKeys[e.Key] = true
	}
	newKeys := make(map[string]bool, n)
	for _, k := range spreadKeys(n) {
		newKeys[k] = true
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := s.Snapshot()
				old, new_ := 0, 0
				for _, e := range snap {
					if oldKeys[e.Key] {
						old++
					} else if newKeys[e.Key] {
						new_++
					} else {
						t.Errorf("snapshot contains unknown key %q", e.Key)
						return
					}
				}
				if old > 0 && new_ > 0 {
					t.Errorf("mixed key generations: %d old, %d new", old, new_)
					return
				}
			}
		}()
	}
	for i := 0; i < 50; i++ {
		s.Rebalance()
	}
	close(stop)
	wg.Wait()
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestRebalanceEmpty(t *testing.T) {
	s := NewSequence(nil, 0)
	s.Rebalance() // must not panic
	if s.Len() != 0 {
		t.Fatal("empty sequence changed by rebalance")
	}
}
