package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/pfx"
)

var failed bool

func check(name string, ok bool, detail any) {
	if !ok {
		failed = true
		fmt.Printf("FAIL %s: %v\n", name, detail)
		return
	}
	fmt.Printf("OK   %s: %v\n", name, detail)
}

func run(pat, text string) []int {
	m, err := api.New([]byte(pat))
	if err != nil {
		failed = true
		fmt.Println("FAIL new:", err)
		return nil
	}
	if _, err := m.Feed([]byte(text)); err != nil {
		failed = true
		fmt.Println("FAIL feed:", err)
	}
	return m.Matches()
}

func naive(p, t string) []int {
	var out []int
	for s := 0; s+len(p) <= len(t); s++ {
		if t[s:s+len(p)] == p {
			out = append(out, s+len(p)-1)
		}
	}
	return out
}

// prand builds a deterministic pseudo-random text over "ab".
func prand(n int, seed uint64) []byte {
	b := make([]byte, n)
	for i := range b {
		seed = seed*6364136223846793005 + 1442695040888963407
		b[i] = "ab"[seed>>63]
	}
	return b
}

func main() {
	pat, text := "abba", string(prand(5000, 42))
	pi := pfx.Compute([]byte("abaaba"))
	check("pi(abaaba)", reflect.DeepEqual(pi, []int{0, 0, 1, 1, 2, 3}), pi)
	check("aa@aaa", reflect.DeepEqual(run("aa", "aaa"), []int{1, 2}), run("aa", "aaa"))
	check("aa@aaaa", reflect.DeepEqual(run("aa", "aaaa"), []int{1, 2, 3}), run("aa", "aaaa"))
	check("abaaba@aabaabaab", reflect.DeepEqual(run("abaaba", "aabaabaab"), []int{6}), run("abaaba", "aabaabaab"))

	// Naive consistency on chunked pseudo-random feeds.
	m, _ := api.New([]byte(pat))
	for k := 0; k < len(text); k += 7 {
		m.Feed([]byte(text[k:min(k+7, len(text))]))
	}
	check("naive-consistent", reflect.DeepEqual(m.Matches(), naive(pat, text)), len(m.Matches()))

	// Three distinguishable sentinel errors.
	_, e1 := api.New(nil)
	_, e2 := api.New(make([]byte, api.MaxPatLen+1))
	lim, _ := api.New([]byte("a"))
	lim.Feed([]byte(strings.Repeat("a", api.MaxMatches)))
	_, e3 := lim.Feed([]byte("a"))
	distinct := errors.Is(e1, api.ErrEmptyPattern) && errors.Is(e2, api.ErrPatternTooLong) &&
		errors.Is(e3, api.ErrTooManyMatches) && e1 != e2 && e2 != e3 && e1 != e3
	check("sentinel-errors", distinct, fmt.Sprintf("%v | %v | %v", e1, e2, e3))

	// Rejected feed leaves state untouched; matcher keeps working.
	before := len(lim.Matches())
	_, err := lim.Feed([]byte("b"))
	check("state-after-reject", err == nil && before == api.MaxMatches && len(lim.Matches()) == api.MaxMatches, before)

	// Comparison count stays within 2*(m+n) for large m (own counter).
	p, t := []byte("aabaaaba"), prand(200000, 7)
	pi, j, cmps := pfx.Compute(p), 0, 0
	for _, c := range t {
		j = pfx.Step(p, pi, j, c, &cmps)
		if j == len(p) {
			j = pi[len(pi)-1]
		}
	}
	check("cmps<=2(m+n)", cmps <= 2*(len(t)+len(p)), cmps)

	// Concurrent readers see identical results; SelfCheck passes.
	full, _ := api.New([]byte(pat))
	full.Feed([]byte(text))
	want := full.Matches()
	var wg sync.WaitGroup
	var bad atomic.Bool
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !reflect.DeepEqual(full.Matches(), want) || full.SelfCheck() != nil {
				bad.Store(true)
			}
		}()
	}
	wg.Wait()
	check("concurrent-readers", !bad.Load(), "8 goroutines identical")

	if failed {
		os.Exit(1)
	}
}
