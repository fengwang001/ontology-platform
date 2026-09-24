package main

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"

	"ontology/find"
	"ontology/scan"
	"ontology/table"
)

func report(name string, ok bool) {
	if ok {
		fmt.Println("OK", name)
		return
	}
	fmt.Println("FAIL", name)
}

func naive(p, t string) []int {
	var out []int
	for i := 0; i+len(p) <= len(t); i++ {
		if t[i:i+len(p)] == p {
			out = append(out, i)
		}
	}
	return out
}

func equal(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func repeat(n int, b byte) string { return string(make([]byte, n)) + string(b) }

func main() {
	rng := rand.New(rand.NewSource(1))
	okNaive, okOverlap := true, false
	for c := 0; c < 100; c++ {
		text := fmt.Sprintf("%016x%016x", rng.Uint64(), rng.Uint64())
		m, err := find.Compile("ab", find.Config{})
		got, err := m.FindAll(text)
		okNaive = okNaive && err == nil && equal(got, naive("ab", text))
	}
	m, _ := find.Compile("aa", find.Config{})
	overlap, _ := m.FindAll("aaaa")
	okOverlap = equal(overlap, []int{0, 1, 2})
	report("naive and overlap", okNaive && okOverlap)

	built, _ := table.Build("ababaca")
	want := []int{0, 0, 1, 2, 3, 0, 1}
	okTable := built.SelfCheck()
	for i, v := range want {
		okTable = okTable && built.At(i+1) == v
	}
	report("table and walk", okTable && equal(scan.New(built).Scan("abababaca"), []int{2}))

	sentinel := []error{find.ErrEmptyPattern, find.ErrPatternTooLong, find.ErrLimitExceeded}
	calls := []func() error{
		func() error { _, e := find.Compile("", find.Config{}); return e },
		func() error { _, e := m.FindAll("a"); return e },
		func() error { _, e := find.Compile("abcd", find.Config{MaxPatternLen: 3}); return e },
	}
	okErrors := true
	for i, call := range calls {
		okErrors = okErrors && errors.Is(call(), sentinel[i])
	}
	report("distinct errors", okErrors)

	after, _ := m.FindAll("aaaa")
	report("rejection leaves no trace", equal(after, overlap))

	okScale := true
	for _, size := range [][2]int{{1000, 10}, {100000, 100}} {
		text := repeat(size[0]-1, 'a') + "b"
		prefix, _ := table.Build(repeat(size[1]-1, 'a') + "b")
		scanner := scan.New(prefix)
		scanner.Scan(text)
		okScale = okScale && scanner.TextAdvances() == len(text) && scanner.CharacterComparisons() <= 2*len(text)
	}
	report("linear counters", okScale)

	const workers = 8
	var wg sync.WaitGroup
	results := make([][]int, workers)
	start := make(chan struct{})
	for i := range workers {
		wg.Add(1)
		go func(id int) { defer wg.Done(); <-start; results[id], _ = m.FindAll("aaaa") }(i)
	}
	close(start)
	wg.Wait()
	okConcurrent := true
	for _, result := range results {
		okConcurrent = okConcurrent && equal(result, overlap)
	}
	report("concurrent matches", okConcurrent)
	report("matcher self-check", m.SelfCheck("aaaa"))
}
