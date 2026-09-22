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

	if failures > 0 {
		fmt.Printf("FAIL total: %d check(s) failed\n", failures)
		os.Exit(1)
	}
	fmt.Println("OK   total: all checks passed")
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
