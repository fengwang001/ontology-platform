// Package api compiles patterns to Thompson NFAs (public entry point).
package api

import (
	"errors"
	"fmt"
	"ontology/nfa"
	"ontology/reast"
)

var ErrEmptyPattern, ErrIllegalChar, ErrUnbalancedParen, ErrDanglingPostfix = reast.ErrEmpty, reast.ErrIllegalChar, reast.ErrParens, reast.ErrPostfix

func Compile(p string) (*nfa.NFA, error) {
	re, err := reast.Parse(p)
	if err != nil {
		return nil, err
	}
	return nfa.Compile(re), nil
}
func Match(p, s string) (bool, error) {
	n, err := Compile(p)
	if err != nil {
		return false, err
	}
	return n.Match(s), nil
}

var checkPats = []string{"a", "ab", "a|b", "ab|cd", "a*", "a+", "a?", "ab*", "a(b|c)*", "(a|b)*", "(ab)+", "a?b+c*", "((a))", "a|b|c"}
var badPats = []string{"", "A", "(a", "a)", "*a", "(|a)*", "a|"}
var badWant = []error{ErrEmptyPattern, ErrIllegalChar, ErrUnbalancedParen, ErrUnbalancedParen, ErrDanglingPostfix, ErrDanglingPostfix, ErrDanglingPostfix}

// SelfCheck verifies the four invariants on a built-in pattern set.
func SelfCheck() error {
	for _, p := range checkPats {
		n, err := Compile(p)
		if err != nil {
			return fmt.Errorf("SelfCheck %q: %w", p, err)
		}
		re, _ := reast.Parse(p)
		for _, s := range enumStrings() { // invariant (1): NFA == naive backtracking
			if n.Match(s) != naiveAccept(re.Root, s) {
				return fmt.Errorf("SelfCheck %q vs %q: reference mismatch", p, s)
			}
		}
		if !closureFixed(n) { // invariant (2): epsilon closure is a fixed point
			return fmt.Errorf("SelfCheck %q: closure not transitive", p)
		}
		if re.Root.Kind == reast.KStar && !n.Match("") { // invariant (3): R* takes epsilon
			return fmt.Errorf("SelfCheck %q: star must accept empty", p)
		}
	}
	for _, q := range []string{"a+", "(ab|c)+"} { // invariant (3): R+ == R.R*
		b := q[:len(q)-1]
		n1, _ := Compile(q)
		n2, _ := Compile(b + b + "*")
		for _, s := range enumStrings() {
			if n1.Match(s) != n2.Match(s) {
				return fmt.Errorf("SelfCheck: R+ mismatch on %q", q)
			}
		}
	}
	for _, q := range []string{"a?", "(ab|c)?"} { // invariant (3): R? == {eps} U L(R)
		in, _ := reast.Parse(q[:len(q)-1])
		n, _ := Compile(q)
		for _, s := range enumStrings() {
			want := s == "" || naiveAccept(in.Root, s)
			if n.Match(s) != want {
				return fmt.Errorf("SelfCheck: R? mismatch on %q", q)
			}
		}
	}
	for i, p := range badPats { // invariant (4): distinct sentinels, nil NFA
		got, e := Compile(p)
		if got != nil || !errors.Is(e, badWant[i]) {
			return fmt.Errorf("SelfCheck: bad-pattern %q: %v", p, e)
		}
	}
	if ok, err := Match("a", "a"); !ok || err != nil { // a rejected call leaves no trace
		return fmt.Errorf("SelfCheck: later call broken after rejected calls")
	}
	return nil
}

// closureFixed: every state's closure stays closed under one more step.
func closureFixed(n *nfa.NFA) bool {
	for st := 0; st < n.N; st++ {
		c, q := map[int]bool{st: true}, []int{st}
		for len(q) > 0 {
			x := q[len(q)-1]
			q = q[:len(q)-1]
			for _, y := range n.Eps[x] {
				if !c[y] {
					c[y], q = true, append(q, y)
				}
			}
		}
		for y := range c {
			for _, z := range n.Eps[y] {
				if !c[z] {
					return false
				}
			}
		}
	}
	return true
}

// matchHere is naive backtracking on the AST (no NFA): nd consumes a
// prefix of s, continuation k decides whether the rest works.
func matchHere(nd *reast.Node, s string, k func(string) bool) bool {
	switch nd.Kind {
	case reast.KChar:
		return len(s) > 0 && s[0] == nd.Char && k(s[1:])
	case reast.KConcat: // chain continuations right to left
		for i := len(nd.Subs) - 1; i >= 0; i-- {
			sub, k2 := nd.Subs[i], k
			k = func(t string) bool { return matchHere(sub, t, k2) }
		}
		return k(s)
	case reast.KAlt:
		for _, sub := range nd.Subs {
			if matchHere(sub, s, k) {
				return true
			}
		}
	case reast.KStar: // take k, or consume one body copy and repeat; t!=s avoids a zero-width loop
		return k(s) || matchHere(nd.Subs[0], s, func(t string) bool { return t != s && matchHere(nd, t, k) })
	case reast.KPlus: // R+ == R.R*
		return matchHere(nd.Subs[0], s, func(t string) bool {
			return matchHere(&reast.Node{Kind: reast.KStar, Subs: nd.Subs}, t, k)
		})
	case reast.KQuest: // R? == R|epsilon
		return k(s) || matchHere(nd.Subs[0], s, k)
	}
	return false
}
func naiveAccept(nd *reast.Node, s string) bool {
	return matchHere(nd, s, func(t string) bool { return t == "" })
}

// enumStrings enumerates all strings of length 0..2 over {a,b,c}.
func enumStrings() []string {
	out := []string{""}
	for i := 0; i < len(out) && len(out[i]) < 2; i++ {
		for _, c := range "abc" {
			out = append(out, out[i]+string(c))
		}
	}
	return out
}
