// Command demo exercises the sort-key generator end to end: the three
// insert positions, determinism, length-limit detection, rebalancing,
// concurrent inserts, and illegal input handling. It takes no arguments
// and exits 0 when every check passes.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"ontology"
)

var failures int

func report(ok bool, format string, args ...any) {
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
		failures++
	}
	fmt.Printf(verdict+" "+format+"\n", args...)
}

func main() {
	gen := ontology.NewLexGenerator()

	front, _ := gen.Between("", "m", 0)
	report(front < "m", "front insert:  %q < %q < %q", "", front, "m")
	middle, _ := gen.Between("a", "c", 0)
	report("a" < middle && middle < "c", "middle insert: %q < %q < %q", "a", middle, "c")
	back, _ := gen.Between("s", "", 0)
	report("s" < back, "back insert:   %q < %q", "s", back)

	k1, _ := gen.Between("a1", "a2", 0)
	k2, _ := gen.Between("a1", "a2", 0)
	report(k1 == k2, "deterministic:  Between(%q,%q) always = %q", "a1", "a2", k1)

	const maxLen = 8
	s := ontology.NewSequence(nil, maxLen)
	n := 0
	for {
		_, err := s.Insert("", firstKey(s), fmt.Sprintf("v%03d", n))
		if errors.Is(err, ontology.ErrNeedsRebalance) {
			break
		}
		if err != nil {
			report(false, "insert %d: unexpected error %v", n, err)
			break
		}
		n++
	}
	longest, remaining := s.LongestKey()
	report(remaining == 0, "length limit:  %d inserts, longest key %d/%d bytes", n, longest, maxLen)

	before := values(s.Snapshot())
	s.Rebalance()
	after := s.Snapshot()
	longest, _ = s.LongestKey()
	report(fmt.Sprint(before) == fmt.Sprint(values(after)),
		"rebalance:     order preserved, new keys %q..%q (longest %d)",
		after[0].Key, after[len(after)-1].Key, longest)

	report(runConcurrent(), "concurrent:    16 goroutines, one gap, deterministic order, self-check passed")

	_, errOrder := gen.Between("b", "a", 0)
	report(errors.Is(errOrder, ontology.ErrInvalidOrder), "illegal order: %v", errOrder)
	_, errCharL := gen.Between("A", "", 0)
	report(errors.Is(errCharL, ontology.ErrInvalidChar), "illegal char (left):  %v", errCharL)
	_, errCharR := gen.Between("", "~", 0)
	report(errors.Is(errCharR, ontology.ErrInvalidChar), "illegal char (right): %v", errCharR)

	runStress()

	if failures > 0 {
		fmt.Printf("FAIL total: %d check(s) failed\n", failures)
		os.Exit(1)
	}
	fmt.Println("OK   total: all checks passed")
}

// runStress exercises the fixed rebalance/insert race: while goroutines
// hammer front inserts, the main goroutine rebalances. Every round must
// end with a clean self-check — no mixed key generations, no
// non-increasing keys.
func runStress() {
	const maxLen = 64
	s := ontology.NewSequence(nil, maxLen)
	last := ""
	seeded := 0
	for seeded < 5000 {
		key, err := s.Insert(last, "", fmt.Sprintf("s%05d", seeded))
		if errors.Is(err, ontology.ErrNeedsRebalance) {
			s.Rebalance()
			snap := s.Snapshot()
			last = snap[len(snap)-1].Key
			continue
		}
		if err != nil {
			report(false, "stress seed: %v", err)
			return
		}
		last = key
		seeded++
	}
	var inserted int64
	const rounds = 8
	for round := 1; round <= rounds; round++ {
		stop := make(chan struct{})
		var wg sync.WaitGroup
		for g := 0; g < 4; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				for i := 0; ; i++ {
					select {
					case <-stop:
						return
					default:
					}
					snap := s.Snapshot()
					if len(snap) == 0 {
						return
					}
					v := fmt.Sprintf("r%d-g%d-%04d", round, g, i)
					if _, err := s.Insert("", snap[0].Key, v); err != nil {
						return // front gap exhausted; next round rebalances
					}
					atomic.AddInt64(&inserted, 1)
				}
			}(g)
		}
		s.Rebalance()
		close(stop)
		wg.Wait()
		check := "clean"
		if err := s.SelfCheck(); err != nil {
			check = err.Error()
		}
		report(check == "clean", "stress round %d: 4 inserters vs rebalance, self-check %s", round, check)
	}
	longest, _ := s.LongestKey()
	report(true, "stress: %d rounds, %d concurrent inserts, len=%d, longest key=%d/%d",
		rounds, atomic.LoadInt64(&inserted), s.Len(), longest, maxLen)
}

// runConcurrent inserts 16 values into one gap from 16 goroutines and
// verifies the resulting order is sorted by value and self-consistent.
func runConcurrent() bool {
	s := ontology.NewSequence(nil, 0)
	if _, err := s.Insert("", "", "first"); err != nil {
		return false
	}
	if _, err := s.Insert("", "", "last"); err != nil {
		return false
	}
	snap := s.Snapshot()
	left, right := snap[0].Key, snap[1].Key
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = s.Insert(left, right, fmt.Sprintf("c%02d", i))
		}(i)
	}
	wg.Wait()
	got := values(s.Snapshot())
	want := append([]string{"first"}, sortedValues(16)...)
	want = append(want, "last")
	return fmt.Sprint(got) == fmt.Sprint(want) && s.SelfCheck() == nil
}

func sortedValues(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("c%02d", i)
	}
	return out
}

func firstKey(s *ontology.Sequence) string {
	snap := s.Snapshot()
	if len(snap) == 0 {
		return ""
	}
	return snap[0].Key
}

func values(entries []ontology.Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Value
	}
	return out
}
