// Command demo prints OK/FAIL lines for the string-period package checks.
package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"ontology/api"
	"ontology/period"
	"ontology/pfx"
)

var fails int

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK " + name)
	} else {
		fails++
		fmt.Println("FAIL " + name)
	}
}

// naiveMinPeriod is the definition-level brute force used only for cross-check.
func naiveMinPeriod(s string) int {
	for p := 1; p <= len(s); p++ {
		if period.IsPeriodic(s, p) {
			return p
		}
	}
	return 0
}

func main() {
	check("ababab period=2 power=true",
		period.MinPeriod("ababab") == 2 && period.IsPower("ababab"))
	check("abababa period=2 not power",
		period.MinPeriod("abababa") == 2 && !period.IsPower("abababa"))
	check("abcabcabc period=3", period.MinPeriod("abcabcabc") == 3)
	check("abcde period=5 not power",
		period.MinPeriod("abcde") == 5 && !period.IsPower("abcde"))
	// Spec §10 asks IsPeriodic("ababab",4)=false, but the §1 definition (and
	// invariants 1/2) makes p=4 a period: s[4]=s[0], s[5]=s[1], n-p=2 is a
	// border. The formal rules win; p=3 is the genuinely-false example.
	check(`IsPeriodic("ababab",2)=true; (4)=true too; (3)=false`,
		period.IsPeriodic("ababab", 2) && period.IsPeriodic("ababab", 4) &&
			!period.IsPeriodic("ababab", 3))
	naiveOK := true
	for _, s := range []string{"ababab", "abababa", "abcabcabc", "abcde", "aaaa", "abcabcd"} {
		naiveOK = naiveOK && period.MinPeriod(s) == naiveMinPeriod(s)
	}
	check("MinPeriod matches naive definition", naiveOK)
	check("comparison count linear at large n", pfx.VerifyLinear())

	_, e0 := api.New("")
	_, e1 := api.New(strings.Repeat("a", 1<<20+1))
	x, _ := api.New("abababa")
	_, e2 := x.IsPeriodic(0)
	mp, _ := x.MinPeriod()
	check("three distinct sentinel errors; state intact after rejection",
		errors.Is(e0, api.ErrEmptyString) && errors.Is(e1, api.ErrStringTooLong) &&
			errors.Is(e2, api.ErrPeriodOutOfRange) && e0 != e1 && e1 != e2 && mp == 2)

	const N = 32
	var wg sync.WaitGroup
	res := make([][3]any, N)
	start := make(chan struct{})
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			m, _ := x.MinPeriod()
			q, _ := x.IsPeriodic(2)
			res[g] = [3]any{m, q, x.IsPower()}
		}(g)
	}
	close(start)
	wg.Wait()
	concOK := true
	for g := 1; g < N; g++ {
		if res[g] != res[0] {
			concOK = false
		}
	}
	check("concurrent readers agree; SelfCheck passes", concOK && x.SelfCheck())

	if fails > 0 {
		panic("demo checks failed")
	}
}
