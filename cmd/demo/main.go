package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"ontology/class"
	"ontology/engine"
	"ontology/set"
	"ontology/syntax"
)

var failed int

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed++
	}
	fmt.Printf("%s %s\n", status, name)
}

func match(pat, path string) bool {
	p, err := engine.Compile(pat, syntax.Limits{})
	return err == nil && p.Match(path)
}

func errKind(pat string, kind class.Kind, off int) bool {
	_, err := syntax.Compile(pat, syntax.Limits{})
	var ce *class.Error
	return errors.As(err, &ce) && ce.Kind == kind && ce.Offset == off
}

func replaceKeepsOld() bool {
	s := set.New(set.Options{})
	if s.Replace([]string{"x"}) != nil {
		return false
	}
	v := s.Explain("x").Version
	if s.Replace([]string{"[bad"}) == nil {
		return false
	}
	d := s.Explain("x")
	return d.Version == v && d.Verdict == set.Included && s.Query("y") == set.Undecided
}

func concurrent() bool {
	s := set.New(set.Options{})
	if s.Replace([]string{"**"}) != nil {
		return false
	}
	stop := make(chan struct{})
	bad := make(chan struct{}, 1)
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				d := s.Explain("p")
				want, rule := set.Included, "**"
				if d.Version%2 == 0 {
					want, rule = set.Excluded, "!**"
				}
				if d.Verdict != want || d.Rule != rule || d.Index != 0 {
					select {
					case bad <- struct{}{}:
					default:
					}
					return
				}
			}
		}()
	}
	for i := 0; i < 500; i++ {
		_ = s.Replace([]string{"!**"})
		_ = s.Replace([]string{"[broken"})
		_ = s.Replace([]string{"**"})
	}
	close(stop)
	wg.Wait()
	select {
	case <-bad:
		return false
	default:
		return true
	}
}

func stepsScale() bool {
	p, err := engine.Compile("a*a*a*a*a*a*a*a*b", syntax.Limits{})
	if err != nil {
		return false
	}
	p.Match(strings.Repeat("a", 100))
	s100 := p.Steps()
	p.Match(strings.Repeat("a", 10000))
	sBig := p.Steps()
	return sBig <= 200*s100 && sBig <= 4*int64(p.Atoms())*10000
}

func main() {
	check("? matches one rune", match("a?c", "aéc") && !match("a?c", "ac") && !match("a?c", "a/b"))
	check("* stays in segment", match("a*b", "axb") && !match("a*b", "a/b"))
	check("** whole vs adjacent", match("a/**/b", "a/x/y/b") && match("a/**/b", "a/b") &&
		!match("a**b", "a/b") && match("**.go", "x.go") && !match("**.go", "a/b.go"))
	check("x/** boundary", match("x/**", "x") && match("x/**", "x/") && match("x/**", "x/y") && !match("x/**", "/x"))
	check("empty path", match("*", "") && match("**", "") && !match("?", "") && match("", "") && !match("", "x"))
	check("class edge cases", match("[]a]", "]") && match("[!]a]", "b") && !match("[!]a]", "/") &&
		match("[a-]", "-") && match("[é-ë]", "ê") && match(`[\]]`, "]"))
	check("syntax errors+offset", errKind("a[", class.KindUnclosed, 1) &&
		errKind("[z-a]", class.KindReversed, 2) && errKind(`ab\`, class.KindBackslash, 2) &&
		errKind("[]", class.KindEmpty, 0))
	check("pathological fast", !match("a*a*a*a*a*a*a*a*b", strings.Repeat("a", 5000)) &&
		!match("**/**/**/**/**/x", strings.Repeat("a/", 4999)+"a"))
	s := set.New(set.Options{})
	_ = s.Replace([]string{"a/*", "!a/secret"})
	check("last match wins+undecided", s.Query("a/x") == set.Included &&
		s.Query("a/secret") == set.Excluded && s.Query("b") == set.Undecided)
	d := s.Explain("a/secret")
	check("explain raw+index", d.Rule == "!a/secret" && d.Index == 1)
	check("failed replace keeps old", replaceKeepsOld())
	check("concurrent version consistent", concurrent())
	check("steps scale linear", stepsScale())
	fmt.Printf("total: %d failures\n", failed)
	if failed > 0 {
		panic("demo failed")
	}
}
