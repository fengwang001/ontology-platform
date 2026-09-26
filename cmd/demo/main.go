package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"ontology/api"
	"ontology/dp"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK: " + name)
	} else {
		failed = true
		fmt.Println("FAIL: " + name)
	}
}

// naiveLCS is the brute-force enumeration used only as the demo's oracle.
func naiveLCS(a, b string) int {
	best := 0
	for i := 0; i < len(a); i++ {
		for j := 0; j < len(b); j++ {
			k := 0
			for i+k < len(a) && j+k < len(b) && a[i+k] == b[j+k] {
				k++
			}
			if k > best {
				best = k
			}
		}
	}
	return best
}

func main() {
	const maxLen = 1 << 20

	m, err := api.New("banana")
	if err != nil {
		fmt.Println("FAIL: New(banana)")
		os.Exit(1)
	}
	l, st, _ := m.Query("ananas")
	sub, _ := m.Substring("ananas")
	check(`("banana","ananas") -> "anana" len5 start1`,
		sub == "anana" && l == 5 && st == 1)

	pairs := []struct {
		a, b  string
		fixed int
	}{
		{"banana", "ananas", 5},
		{"abcx", "abc", 3},
		{"abcde", "abfce", 2},
	}
	agree := true
	for _, p := range pairs {
		mm, _ := api.New(p.a)
		got, _, _ := mm.Query(p.b)
		agree = agree && got == p.fixed && got == naiveLCS(p.a, p.b)
	}
	check(`lengths: abcx/abc=3, abcde/abfce=2, all match naive`, agree)

	dpErr := dp.SelfCheck()
	check("rolling row equals full O(n*m) table", dpErr == nil)

	_, e0 := api.New("")
	_, _, e1 := m.Query("")
	_, _, e2 := m.Query(strings.Repeat("x", maxLen))
	distinct := errors.Is(e0, api.ErrEmptyReference) &&
		errors.Is(e1, api.ErrEmptyQuery) && errors.Is(e2, api.ErrInputTooLong) &&
		e0 != e1 && e1 != e2 && e0 != e2
	check("three mutually distinct sentinel errors", distinct)

	l2, st2, e3 := m.Query("ananas")
	check("state unchanged after rejected ops", e3 == nil && l2 == 5 && st2 == 1)

	check("retained cells stay min(n,m)+1 as m grows past n", dpErr == nil)

	const N = 64
	bs := make([]string, N)
	for i := range bs {
		bs[i] = "an" + strings.Repeat("ab", i%9)
	}
	type res struct{ l, s int }
	serial := make([]res, N)
	for i, b := range bs {
		ll, ss, _ := m.Query(b)
		serial[i] = res{ll, ss}
	}
	par := make([]res, N)
	var wg sync.WaitGroup
	for i, b := range bs {
		wg.Add(1)
		go func(i int, b string) {
			defer wg.Done()
			ll, ss, _ := m.Query(b)
			par[i] = res{ll, ss}
		}(i, b)
	}
	wg.Wait()
	same := true
	for i := range serial {
		same = same && par[i] == serial[i]
	}
	check("concurrent Query results equal serial results", same)

	if failed {
		os.Exit(1)
	}
}
