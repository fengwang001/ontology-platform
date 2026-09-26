package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"ontology/api"
	"ontology/match"
)

func main() {
	basic := []struct {
		s, p string
		want bool
	}{
		{"adceb", "*a*b", true}, {"aab", "*ab", true}, {"ab", "a?b", false},
		{"ab", "a*", true}, {"acdcb", "a*c?b", false},
	}
	for _, c := range basic {
		q, _ := api.Compile(c.p)
		got, _ := q.Match(c.s)
		report(fmt.Sprintf("(%q,%q)=%v", c.s, c.p, c.want), got == c.want)
	}
	dp := true
	for _, c := range basic {
		dp = dp && match.AgreesWithNaive(c.s, c.p)
	}
	report("agrees with naive DP", dp)

	_, e1 := api.Compile("a\x01b")
	_, e2 := api.Compile(strings.Repeat("a", 1<<15))
	_, e3 := api.Compile(strings.Repeat("*", 1<<10))
	distinct := errors.Is(e1, api.ErrUnsupportedChar) && errors.Is(e2, api.ErrInputTooLong) &&
		errors.Is(e3, api.ErrTooManyWildcards) && e1 != e2 && e2 != e3
	report("three distinct decidable errors", distinct)

	q, _ := api.Compile("a*b")
	before, _ := q.Match("azzb")
	_, _ = q.Match(strings.Repeat("a", 1<<15))
	after, _ := q.Match("azzb")
	report("state unchanged after rejection", before && after && before == after)
	report("advance count linear at large m",
		match.LinearAdvance("*a*a?b", []int{100, 1000, 10000}))

	texts := []string{"aaab", "axxb", "adceb", "acdcb", "", "zzzzb"}
	want := make([]bool, len(texts))
	for i, sx := range texts {
		want[i], _ = q.Match(sx)
	}
	got := make([]bool, len(texts))
	var wg sync.WaitGroup
	for i, sx := range texts {
		wg.Add(1)
		go func(i int, sx string) { defer wg.Done(); got[i], _ = q.Match(sx) }(i, sx)
	}
	wg.Wait()
	same := true
	for i := range want {
		same = same && got[i] == want[i]
	}
	report("concurrent results equal serial", same)
}

func report(label string, cond bool) {
	fmt.Printf("%s %s\n", ok(cond), label)
}

func ok(cond bool) string {
	if cond {
		return "OK"
	}
	return "FAIL"
}
