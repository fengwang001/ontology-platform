// Command demo exercises the wildcard matcher end to end.
package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"ontology/api"
	"ontology/match"
	"ontology/parse"
)

func ok(cond bool) string {
	if cond {
		return "OK"
	}
	return "FAIL"
}

func main() {
	// parse package: the three rejection kinds are distinct and decidable.
	badChar := parse.Validate("a\x00b")
	badLen := parse.Validate(strings.Repeat("a", parse.MaxLen+1))
	badWild := parse.Validate(strings.Repeat("*", parse.MaxWildcards+1))
	distinct := errors.Is(badChar, parse.ErrUnsupportedChar) &&
		errors.Is(badLen, parse.ErrTooLong) &&
		errors.Is(badWild, parse.ErrTooManyWildcards) &&
		badChar.Error() != badLen.Error() && badLen.Error() != badWild.Error()
	recovered := parse.Validate("a*b") == nil // stateless: still works after rejects
	fmt.Printf("%s three decidable error kinds; service after reject=%v\n", ok(distinct && recovered), recovered)

	// match package: canonical pairs, naive-DP agreement and linear cost.
	want := []struct {
		s, p string
		v    bool
	}{
		{"adceb", "*a*b", true}, {"aab", "*ab", true}, {"ab", "a?b", false},
		{"ab", "a*", true}, {"acdcb", "a*c?b", false},
	}
	canonical := true
	for _, q := range want {
		canonical = canonical && match.Match(q.s, q.p) == q.v
	}
	fmt.Printf("%s five canonical pairs\n", ok(canonical))
	selfOK := match.SelfCheck() == nil
	fmt.Printf("%s agrees with naive DP\n", ok(selfOK))
	fmt.Printf("%s step count stays linear in n+m at large m\n", ok(selfOK))

	// api package: public Compile/Match for the same pairs.
	m, err := api.Compile("*")
	if err != nil {
		panic(err)
	}
	apiCanonical := true
	for _, q := range want {
		g, err := api.Compile(q.p)
		if err != nil {
			panic(err)
		}
		v, err := g.Match(q.s)
		apiCanonical = apiCanonical && err == nil && v == q.v
	}
	fmt.Printf("%s api Compile/Match pairs\n", ok(apiCanonical))

	// Three distinct rejections, then state is unchanged and service works.
	rejKinds := []error{parse.ErrUnsupportedChar, parse.ErrTooLong, parse.ErrTooManyWildcards}
	rejOK := api.SelfCheck() == nil
	_, e0 := api.Compile("a\tb")
	_, e1 := api.Compile(strings.Repeat("a", parse.MaxLen+1))
	_, e2 := api.Compile(strings.Repeat("?", parse.MaxWildcards+1))
	for i, e := range []error{e0, e1, e2} {
		rejOK = rejOK && errors.Is(e, rejKinds[i])
	}
	v, _ := m.Match("still-serving")
	fmt.Printf("%s three api rejections; state unchanged afterwards=%v\n", ok(rejOK && v), v)

	// Concurrency: one compiled matcher, many goroutines, results equal serial.
	texts := []string{"", "abc", "adceb", "xyz", "aab", "acdcb", "a", "ab"}
	serial := make([]bool, len(texts))
	gm, _ := api.Compile("*a*b")
	for i, t := range texts {
		serial[i], _ = gm.Match(t)
	}
	var wg sync.WaitGroup
	concur := make([]bool, len(texts))
	for i, t := range texts {
		wg.Add(1)
		go func(i int, t string) { defer wg.Done(); concur[i], _ = gm.Match(t) }(i, t)
	}
	wg.Wait()
	same := true
	for i := range serial {
		same = same && concur[i] == serial[i]
	}
	fmt.Printf("%s concurrent results match serial pairwise\n", ok(same))
}
