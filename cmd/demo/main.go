// Command demo exercises the scan/balance/longest packages.
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"

	"ontology/balance"
	"ontology/longest"
	"ontology/scan"
)

var failed bool

func check(name string, ok bool) {
	failed = failed || !ok
	fmt.Printf("%v %s\n", map[bool]string{true: "OK  ", false: "FAIL"}[ok], name)
}

func main() {
	check("scan classifies, locates first invalid", scan.Classify('(') == scan.Left &&
		scan.Classify(')') == scan.Right && scan.Classify('x') == scan.Invalid &&
		scan.FirstInvalid("(a)b") == 1 && scan.FirstInvalid("()") == -1)
	var pe *balance.PosError
	_, errR := balance.IsBalanced("())")
	_, errL := balance.IsBalanced("()(()")
	check("balance reports first error position", errors.As(errR, &pe) && pe.Pos == 2 &&
		errors.Is(errR, balance.ErrUnexpectedRight) && errors.As(errL, &pe) && pe.Pos == 2 &&
		errors.Is(errL, balance.ErrUnclosedLeft))
	_, errInv := balance.IsBalanced("(x)")
	_ = balance.SetMaxLength(3)
	_, errLong := balance.IsBalanced("((((")
	errBad := balance.SetMaxLength(0)
	_ = balance.SetMaxLength(1 << 20)
	check("three distinct rejection errors", errors.Is(errInv, balance.ErrInvalidChar) &&
		errors.Is(errLong, balance.ErrTooLong) && errors.Is(errBad, balance.ErrBadLimit) &&
		!errors.Is(errInv, errLong) && !errors.Is(errLong, errBad) && !errors.Is(errInv, errBad))
	rng := rand.New(rand.NewSource(1))
	same := true
	for t := 0; t < 300 && same; t++ {
		buf := make([]byte, rng.Intn(13))
		for i := range buf {
			buf[i] = "()"[rng.Intn(2)]
		}
		st, ln, err := longest.Longest(string(buf))
		ns, nl := naive(string(buf))
		same = err == nil && st == ns && ln == nl
	}
	check("longest matches naive on random strings", same)
	fixedIn := []string{")()())", "", "(((", ")))", "())()"}
	fixedWant := [][2]int{{1, 4}, {0, 0}, {0, 0}, {0, 0}, {0, 2}}
	pass := true
	for i, in := range fixedIn {
		st, ln, err := longest.Longest(in)
		pass = pass && err == nil && st == fixedWant[i][0] && ln == fixedWant[i][1]
	}
	check("fixed: )()())/empty/all-left/all-right/tie-leftmost", pass)
	big := true
	for _, n := range []int{1000, 100000} {
		_, ln, err := longest.Longest(strings.Repeat("()", n/2))
		big = big && err == nil && ln == n
	}
	check("sizes 1000/100000 (visits==n asserted in tests)", big)
	input := "())(())(()())"
	wantS, wantL, _ := longest.Longest(input)
	got := make([][2]int, 16)
	var wg sync.WaitGroup
	for g := range got {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			st, ln, _ := longest.Longest(input)
			got[g] = [2]int{st, ln}
		}(g)
	}
	wg.Wait()
	det := true
	for _, r := range got {
		det = det && r == [2]int{wantS, wantL}
	}
	check("concurrent results identical", det)
	if failed {
		panic("demo failed")
	}
}

func naive(s string) (int, int) {
	best, start := 0, 0
	for i := 0; i < len(s); i++ {
		for j := i; j < len(s); j++ {
			if ok, _ := balance.IsBalanced(s[i : j+1]); ok && j+1-i > best {
				best, start = j+1-i, i
			}
		}
	}
	return start, best
}
