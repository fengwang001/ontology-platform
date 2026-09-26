package main

import (
	"errors"
	"fmt"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/nfa"
	"ontology/reast"
)

func ok(b bool) string {
	if b {
		return "OK"
	}
	return "FAIL"
}

func main() {
	re, _ := reast.Parse("a(b|c)*")
	n := nfa.Compile(re)
	wantChar := []nfa.CharTr{
		{From: 0, To: 1, Char: 'a'},
		{From: 2, To: 3, Char: 'b'},
		{From: 4, To: 5, Char: 'c'},
	}
	wantEps := map[int][]int{1: {8}, 6: {2, 4}, 3: {7}, 5: {7}, 8: {6, 9}, 7: {6, 9}}
	six := n.N == 10 && n.Start == 0 && n.Accept == 9 &&
		reflect.DeepEqual(n.Char, wantChar) && reflect.DeepEqual(n.Eps, map2slice(wantEps))
	fmt.Println(ok(six), "steps: 1(0,1)0-a>1 | 2(2,3)2-b>3 | 3(4,5)4-c>5 |",
		"4alt(6,7)6e{2,4},3e7,5e7 | 5star(8,9)8e{6,9},7e{6,9} | 6cat 1e8,s0,a9")

	pass := true
	for s, want := range map[string]bool{"a": true, "ab": true, "ac": true, "abb": true, "b": false} {
		pass = pass && n.Match(s) == want
	}
	fmt.Println(ok(pass), "match a(b|c)*: a/ab/ac/abb=T, b=F")
	v := func(p, s string) bool { r, e := api.Match(p, s); return e == nil && r }
	fmt.Println(ok(v("ab|cd", "ab") && v("ab*", "a") && v("(a|b)*", "")),
		"precedence: ab|cd~ab T; ab*~a T; (a|b)*~empty T")

	four := []error{api.ErrEmptyPattern, api.ErrIllegalChar, api.ErrUnbalancedParen, api.ErrDanglingPostfix}
	bad := []string{"", "9", "(a", "*a"}
	distinct := true
	for i, p := range bad {
		_, e := api.Match(p, "x")
		distinct = distinct && errors.Is(e, four[i])
	}
	after, errA := api.Match("a", "a")
	fmt.Println(ok(distinct && len(unique(four)) == 4 && after && errA == nil),
		"errors: 4 distinct sentinels; rejected call leaves no trace")
	fmt.Println(ok(api.SelfCheck() == nil), "SelfCheck: all four invariants")

	const m = 5000
	big, _ := api.Compile(repeat('a', m))
	before := make([]int, big.N)
	for i := range before {
		before[i] = len(big.Eps[i])
	}
	oldAcc := big.Accept
	big.AppendChar('b')
	constTouched := big.N == 2*m+2 && len(big.Eps[oldAcc]) == before[oldAcc]+1
	for i := 0; i < 2*m && constTouched; i++ {
		if i != oldAcc && len(big.Eps[i]) != before[i] {
			constTouched = false
		}
	}
	fmt.Println(ok(constTouched), "append m=5000: only old accept gains one epsilon edge")

	strs := []string{"", "a", "ab", "ac", "abb", "b", "abcc", "abcbc"}
	base := make([]bool, len(strs))
	for i, s := range strs {
		base[i], _ = api.Match("a(b|c)*", s)
	}
	const G = 32
	var wg sync.WaitGroup
	same := true
	var mu sync.Mutex
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i, s := range strs {
				r, e := api.Match("a(b|c)*", s)
				mu.Lock()
				if e != nil || r != base[i] {
					same = false
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	fmt.Println(ok(same), "concurrency: 32 goroutines agree value-by-value")
}

func map2slice(m map[int][]int) [][]int {
	out := make([][]int, 10)
	for k, val := range m {
		out[k] = val
	}
	return out
}

func repeat(c byte, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = c
	}
	return string(b)
}

func unique(errs []error) map[error]bool {
	m := map[error]bool{}
	for _, e := range errs {
		m[e] = true
	}
	return m
}
