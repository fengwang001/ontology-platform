// Command demo exercises the wildcard matcher end to end.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"ontology/engine"
	"ontology/set"
	"ontology/syntax"
)

var failures int

func check(name string, ok bool) {
	if ok {
		fmt.Printf("OK   %s\n", name)
	} else {
		fmt.Printf("FAIL %s\n", name)
		failures++
	}
}

func m(pat, path string) bool {
	p, err := syntax.Compile(pat, syntax.Limits{})
	if err != nil {
		return false
	}
	return engine.Match(p, path)
}

func syntaxErrs() bool {
	cases := []struct {
		pat  string
		kind error
	}{
		{"a[", syntax.ErrUnterminatedClass},
		{"[]", syntax.ErrEmptyClass},
		{"[z-a]", syntax.ErrReversedRange},
		{`a\`, syntax.ErrTrailingBackslash},
	}
	ok := true
	for _, c := range cases {
		_, err := syntax.Compile(c.pat, syntax.Limits{})
		var se *syntax.Error
		if !errors.As(err, &se) || !errors.Is(err, c.kind) {
			ok = false
			continue
		}
		fmt.Printf("     err %q -> %v\n", c.pat, se)
	}
	return ok
}

func concurrency() bool {
	s, _ := set.New([]string{"**"}, syntax.Limits{}, 0)
	type obs struct{ d set.Decision }
	ch := make(chan set.Decision, 1024)
	done := make(chan []set.Decision)
	go func() {
		var all []set.Decision
		for d := range ch {
			all = append(all, d)
		}
		done <- all
	}()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				ch <- s.Explain("a/b")
			}
		}()
	}
	for i := 0; i < 100; i++ {
		_ = s.Replace([]string{"!broken["}) // failing swap keeps old version
		if i%2 == 0 {
			_ = s.Replace([]string{"!**"})
		} else {
			_ = s.Replace([]string{"**"})
		}
	}
	wg.Wait()
	close(ch)
	for _, d := range <-done {
		if d.Verdict == set.Undecided || d.Raw == "" || d.Index != 0 {
			return false
		}
	}
	return true
}

func stepsOf(pat, path string) int64 {
	p, _ := syntax.Compile(pat, syntax.Limits{})
	engine.Match(p, path)
	return engine.LastSteps()
}

func main() {
	check("? matches one rune", m("a?c", "aéc") && !m("?", ""))
	check("* stays in segment", m("a*c", "axc") && !m("a*c", "a/c"))
	check("** whole-segment vs adjacent", m("a/**/b", "a/x/y/b") && m("a**b", "axb") && !m("a**b", "a/b"))
	check("x/** boundary: x, x/, x/y yes; /x no", m("x/**", "x") && m("x/**", "x/") && m("x/**", "x/y") && !m("x/**", "/x"))
	check("empty path: * and ** match, ? and x do not", m("*", "") && m("**", "") && m("", "") && !m("?", "") && !m("x", ""))
	check("classes: []a] [!]a] [a-] [é-ë] [\\]]", m("[]a]", "]") && !m("[!]a]", "]") && m("[a-]", "-") && m("[é-ë]", "ê") && m(`[\]]`, "]"))
	check("4 decidable syntax errors with offsets", syntaxErrs())
	p1, p2 := "a*a*a*a*a*a*a*a*b", "**/**/**/**/**/x"
	s1, s2 := stepsOf(p1, strings.Repeat("a", 10000)), stepsOf(p2, strings.Repeat("a/", 9999)+"a")
	check("pathological inputs return fast", s1 > 0 && s2 > 0)
	rs, _ := set.New([]string{"*.go", "!internal/**"}, syntax.Limits{}, 0)
	d := rs.Explain("internal/a.go")
	check("last hit wins; undecided distinct", d.Verdict == set.Exclude && rs.Decide("x.md") == set.Undecided)
	check("Explain raw text and index", d.Raw == "!internal/**" && d.Index == 1)
	v0 := rs.Version()
	check("failed replace keeps old version", rs.Replace([]string{"!broken["}) != nil && rs.Version() == v0 && rs.Decide("a.go") == set.Include)
	check("concurrent queries see one version", concurrency())
	a1, a2 := stepsOf(p1, strings.Repeat("a", 100)), s1
	b1, b2 := stepsOf(p2, strings.Repeat("a/", 99)+"a"), s2
	fmt.Printf("     steps: stars %d->%d, globstars %d->%d\n", a1, a2, b1, b2)
	check("steps scale linearly (<=200x for 100x input)", a2 <= 200*a1 && b2 <= 200*b1)
	fmt.Printf("TOTAL %d failure(s)\n", failures)
	if failures > 0 {
		os.Exit(1)
	}
}
