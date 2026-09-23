package engine_test

import (
	"strings"
	"testing"

	"ontology/engine"
	"ontology/runes"
	"ontology/syntax"
)

func TestMatch(t *testing.T) {
	cases := []struct {
		pat, path string
		want      bool
	}{
		{"a?c", "aéc", true}, {"a?c", "ac", false}, {"a?c", "a/b", false},
		{"a*b", "axb", true}, {"a*b", "ab", true}, {"a*b", "a/b", false},
		{`a\*b`, "a*b", true}, {`a\*b`, "axb", false},
		{"a/**/b", "a/b", true}, {"a/**/b", "a/x/b", true},
		{"a/**/b", "a/x/y/b", true}, {"a/**/b", "a/b/c", false},
		{"a**b", "a/b", false}, {"a**b", "axxb", true},
		{"**.go", "x.go", true}, {"**.go", "a/b.go", false},
		{"x/**", "", false}, {"x/**", "x", true}, {"x/**", "x/", true},
		{"x/**", "/x", false}, {"x/**", "x/y", true}, {"x/**", "a/b", false},
		{"x/**", "a/x/y/b", false},
		{"**/x", "", false}, {"**/x", "x", true}, {"**/x", "x/", false},
		{"**/x", "/x", true}, {"**/x", "x/y", false}, {"**/x", "a/b", false},
		{"**/x", "a/x/y/b", false},
		{"*", "", true}, {"*", "x", true}, {"*", "x/", false}, {"*", "/x", false},
		{"*", "x/y", false}, {"*", "a/b", false}, {"*", "a/x/y/b", false},
		{"**", "", true}, {"**", "x", true}, {"**", "x/", true}, {"**", "/x", true},
		{"**", "x/y", true}, {"**", "a/b", true}, {"**", "a/x/y/b", true},
		{"a/**/b", "", false}, {"a/**/b", "x", false}, {"a/**/b", "x/", false},
		{"a/**/b", "/x", false}, {"a/**/b", "x/y", false},
		{"", "", true}, {"", "x", false}, {"?", "", false},
		{"[]a]", "]", true}, {"[]a]", "a", true}, {"[]a]", "b", false},
		{"[!]a]", "b", true}, {"[!]a]", "]", false}, {"[!]a]", "a", false},
		{"[!]a]", "/", false},
		{"[^a]", "b", true}, {"[^a]", "a", false},
		{"[a-]", "-", true}, {"[a-]", "a", true}, {"[a-]", "b", false},
		{"[é-ë]", "ê", true}, {"[é-ë]", "a", false},
		{`[\]]`, "]", true},
		{"?", "\xff", true}, {"[é-ë]", "\xff", false}, {"\xff", "\xff", true},
		{"*", "\xff\xfe", true}, {"a?c", "a\xffc", true},
	}
	for _, c := range cases {
		p, err := engine.Compile(c.pat, syntax.Limits{})
		if err != nil {
			t.Fatalf("Compile(%q): %v", c.pat, err)
		}
		if got := p.Match(c.path); got != c.want {
			t.Errorf("match(%q, %q) = %v, want %v", c.pat, c.path, got, c.want)
		}
	}
}

func deep(n int) string { return strings.Repeat("a/", n-1) + "a" }

func TestStepsBound(t *testing.T) {
	cases := []struct {
		pat        string
		small, big string
	}{
		{"a*a*a*a*a*a*a*a*b", strings.Repeat("a", 100), strings.Repeat("a", 10000)},
		{"**/**/**/**/**/x", deep(100), deep(10000)},
	}
	for _, c := range cases {
		p, err := engine.Compile(c.pat, syntax.Limits{})
		if err != nil {
			t.Fatalf("Compile(%q): %v", c.pat, err)
		}
		if p.Match(c.small) || p.Match(c.big) {
			t.Errorf("pattern %q should not match pathological inputs", c.pat)
		}
		sBig := p.Steps()
		p.Match(c.small)
		sSmall := p.Steps()
		atoms := int64(p.Atoms())
		if bound := 4 * atoms * int64(runes.Count(c.small)); sSmall > bound {
			t.Errorf("%q small: %d steps > bound %d", c.pat, sSmall, bound)
		}
		if bound := 4 * atoms * int64(runes.Count(c.big)); sBig > bound {
			t.Errorf("%q big: %d steps > bound %d", c.pat, sBig, bound)
		}
		if sBig > 200*sSmall {
			t.Errorf("%q: big %d steps > 200x small %d", c.pat, sBig, sSmall)
		}
		t.Logf("%q: small=%d big=%d steps", c.pat, sSmall, sBig)
	}
}
