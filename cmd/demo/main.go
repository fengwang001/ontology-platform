package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"ontology/api"
	"ontology/bm"
	"ontology/shift"
)

var failed bool

func report(name string, ok bool) {
	if ok {
		fmt.Println("OK  " + name)
	} else {
		fmt.Println("FAIL " + name)
		failed = true
	}
}

func naive(text, pat string) []int {
	var hits []int
	for s := 0; s+len(pat) <= len(text); s++ {
		ok := true
		for k := 0; k < len(pat); k++ {
			if text[s+k] != pat[k] {
				ok = false
				break
			}
		}
		if ok {
			hits = append(hits, s)
		}
	}
	return hits
}

func eq(a, b []int) bool {
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

func main() {
	t := shift.Build("abab")
	report("shift tables for abab",
		t.Last['a'] == 2 && t.Last['b'] == 3 && t.Last['c'] == -1 &&
			t.Good[0] == 2 && t.Good[2] == 2 && t.Good[4] == 1)

	report(`("abababcab","abab") -> [0,2]`, eq(bm.Search("abababcab", "abab"), []int{0, 2}))
	report(`("aaaaab","aaaab") -> [1]`, eq(bm.Search("aaaaab", "aaaab"), []int{1}))
	report(`("aaaa","aa") -> [0,1,2]`, eq(bm.Search("aaaa", "aa"), []int{0, 1, 2}))

	cases := []struct{ text, pat string }{
		{"abababcab", "abab"}, {"aaaaab", "aaaab"}, {"aaaa", "aa"},
		{"ababab", "abab"}, {"mississippi", "issi"},
	}
	naiveOK := true
	for _, c := range cases {
		naiveOK = naiveOK && eq(bm.Search(c.text, c.pat), naive(c.text, c.pat))
	}
	report("bm equals naive", naiveOK)
	report("comparisons sublinear in n", bm.SublinearCheck())

	_, e1 := api.New(nil)
	_, e2 := api.New(make([]byte, 1<<21))
	rpt, err := api.New([]byte("a"))
	if err != nil {
		panic(err)
	}
	_, _ = rpt.Search([]byte("aa"))
	_, e3 := rpt.Search([]byte(strings.Repeat("a", 1<<11)))
	distinct := errors.Is(e1, api.ErrEmptyPattern) && errors.Is(e2, api.ErrPatternTooLong) &&
		errors.Is(e3, api.ErrTooManyMatches) && e1 != e2 && e2 != e3 && e1 != e3
	report("three distinguishable sentinel errors", distinct)
	report("rejected call leaves no trace, still usable",
		eq(rpt.Matches(), []int{0, 1}))

	const N = 16
	var wg sync.WaitGroup
	ok := make([]bool, N)
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			base := rpt.Matches()
			same := true
			for k := 0; k < 50; k++ {
				same = same && eq(rpt.Matches(), base) && rpt.SelfCheck()
			}
			ok[i] = same
		}(g)
	}
	wg.Wait()
	concurOK := true
	for _, v := range ok {
		concurOK = concurOK && v
	}
	report("concurrent readers agree element-wise", concurOK)

	if failed {
		os.Exit(1)
	}
}
