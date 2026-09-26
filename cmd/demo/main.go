package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"ontology/api"
	"ontology/match"
	"ontology/parse"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK  " + name)
	} else {
		fmt.Println("FAIL " + name)
		failed = true
	}
}

func main() {
	pairs := []struct {
		s, p string
		want bool
	}{
		{"aab", "c*a*b", true},
		{"a", "a.", false},
		{"mississippi", "mis*is*p*.", false},
		{"ab", ".*", true},
	}
	ok := true
	for _, c := range pairs {
		ok = ok && match.Match(c.s, c.p) == c.want && match.Match(c.s, c.p) == match.Naive(c.s, c.p)
	}
	check("four required pairs + naive agree", ok)

	m, err := api.Compile("mis*is*p*.")
	check("compile good pattern", err == nil)
	_, e1 := api.Compile("*a")
	_, e2 := api.Compile("a b")
	_, e3 := m.Match(strings.Repeat("a", parse.MaxLen+1))
	distinct := errors.Is(e1, api.ErrSyntax) && errors.Is(e2, api.ErrUnsupported) &&
		errors.Is(e3, api.ErrTooLong) && !errors.Is(e1, e2) && !errors.Is(e2, e3) && !errors.Is(e1, e3)
	check("three distinguishable errors", distinct)

	before, _ := m.Match("mississippi")
	after, _ := m.Match("mississippi")
	check("rejections leave no trace", before == after && !after)

	big := match.Match(strings.Repeat("a", 10000)+"b", "a*b")
	check("m=10000 finishes (poly states)", big)

	texts := []string{"mississippi", "mis", "mississipp", "misisip", "mississippix"}
	want := make([]bool, len(texts))
	for i, t := range texts {
		want[i], _ = m.Match(t)
	}
	got := make([]bool, len(texts))
	var wg sync.WaitGroup
	for i, t := range texts {
		wg.Add(1)
		go func() { defer wg.Done(); got[i], _ = m.Match(t) }()
	}
	wg.Wait()
	same := true
	for i := range want {
		same = same && got[i] == want[i]
	}
	check("concurrent == serial", same)

	check("SelfCheck", api.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
