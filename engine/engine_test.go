package engine_test

import (
	"strings"
	"testing"

	"ontology/engine"
	"ontology/syntax"
)

func TestBasicSemantics(t *testing.T) {
	cases := []struct {
		pat, path string
		want      bool
	}{
		{"", "", true}, {"", "x", false},
		{"?", "a", true}, {"?", "", false}, {"?", "é", true}, {"?", "/", true},
		{"*", "abc", true}, {"*", "", false}, {"*", "a/b", false},
		{"a?c", "abc", true}, {"a?c", "a/c", false},
		{"a*b", "ab", true}, {"a*b", "axxb", true}, {"a*b", "a/b", false},
		{"a/**/b", "a/b", true}, {"a/**/b", "a/x/b", true},
		{"a/**/b", "a/x/y/b", true}, {"a/**/b", "a/c", false},
		{"x/**", "x", true}, {"x/**", "x/", true}, {"x/**", "x/y", true},
		{"x/**", "x/y/z", true}, {"x/**", "", false}, {"x/**", "/x", false},
		{"**/x", "x", true}, {"**/x", "/x", true}, {"**/x", "a/x", true},
		{"**/x", "x/", false}, {"**/x", "", false},
		{"**", "", true}, {"**", "x", true}, {"**", "x/", true},
		{"**", "/x", true}, {"**", "a/b/c", true},
		{"**/**/**/**/**/x", "x", true},
		{"**/**/**/**/**/x", "a/b/c/d/e/x", true},
		{"a**b", "ab", true}, {"a**b", "axxb", true}, {"a**b", "a/b", false},
		{"**.go", "a.go", true}, {"**.go", "dir/a.go", false},
		{"x/", "x", false}, {"x/", "x/", true}, {"/x", "/x", true},
		{"/x", "x", false}, {"a//b", "a//b", true}, {"a//b", "a/b", false},
		{`a\*b`, "a*b", true}, {`a\*b`, "ab", false},
		{`a\/b`, "a/b", false},
	}
	for _, tc := range cases {
		m, err := engine.Compile(tc.pat, syntax.DefaultLimits)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.pat, err)
		}
		if got := m.Match(tc.path); got != tc.want {
			t.Errorf("Match(%q,%q)=%v want %v", tc.pat, tc.path, got, tc.want)
		}
	}
}

func TestClasses(t *testing.T) {
	cases := []struct {
		pat, path string
		want      bool
	}{
		{"[]a]", "]", true}, {"[]a]", "a", true}, {"[]a]", "b", false},
		{"[!]a]", "b", true}, {"[!]a]", "]", false}, {"[!]a]", "a", false},
		{"[!]a]", "/", false},
		{"[^a-c]", "d", true}, {"[^a-c]", "b", false}, {"[^a-c]", "/", false},
		{"[a-]", "a", true}, {"[a-]", "-", true}, {"[a-]", "b", false},
		{"[a-c]", "b", true}, {"[a-c]", "d", false},
		{"[é-ë]", "ê", true}, {"[é-ë]", "a", false},
		{`[\]]`, "]", true}, {`[\]]`, "[", false},
	}
	for _, tc := range cases {
		m, err := engine.Compile(tc.pat, syntax.DefaultLimits)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.pat, err)
		}
		if got := m.Match(tc.path); got != tc.want {
			t.Errorf("Match(%q,%q)=%v want %v", tc.pat, tc.path, got, tc.want)
		}
	}
}

func TestInvalidUTF8(t *testing.T) {
	invalid := "a\xffb"
	cases := []struct {
		pat, path string
		want      bool
	}{
		{"?", "\xff", true}, {"??", "\xff\xff", true},
		{"*", "\xff", true}, {"a?b", invalid, true},
		{"[é-ë]", "\xff", false}, {"[a-c]", "\xff", false},
		{"[^\x00-￿]", "\xff", false},
		{"\xff", "\xff", true}, {"\xff", "a", false},
		{"\xff", "\xef\xbf\xbd", false},
		{"\xef\xbf\xbd", "\xff", false},
	}
	for _, tc := range cases {
		m, err := engine.Compile(tc.pat, syntax.DefaultLimits)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.pat, err)
		}
		if got := m.Match(tc.path); got != tc.want {
			t.Errorf("Match(%q,%q)=%v want %v", tc.pat, tc.path, got, tc.want)
		}
	}
}

func TestComplexity(t *testing.T) {
	cases := []struct {
		pat      string
		path     func(n int) string
		atoms    func(n int) int
		codep    func(n int) int
	}{
		{"a*a*a*a*a*a*a*a*b",
			func(n int) string { return strings.Repeat("a", n) },
			func(n int) int { return 17 }, func(n int) int { return n }},
		{"**/**/**/**/**/x",
			func(n int) string { return strings.Repeat("s/", n) + "y" },
			func(n int) int { return 7 }, func(n int) int { return 2*n + 1 }},
	}
	var steps100 int64
	for ti, tc := range cases {
		var prev int64
		for _, n := range []int{100, 10000} {
			m, err := engine.Compile(tc.pat, syntax.DefaultLimits)
			if err != nil {
				t.Fatal(err)
			}
			if m.Match(tc.path(n)) {
				t.Errorf("case %d n=%d unexpectedly matched", ti, n)
			}
			bound := int64(4 * tc.atoms(n) * tc.codep(n))
			if m.Steps() > bound {
				t.Errorf("case %d n=%d steps %d > bound %d", ti, n, m.Steps(), bound)
			}
			if n == 100 {
				steps100 = m.Steps()
			}
			if n == 10000 {
				if m.Steps() > steps100*200 {
					t.Errorf("steps %d > 200x %d", m.Steps(), steps100)
				}
			}
			prev = m.Steps()
		}
		t.Logf("case %d steps100=%d steps10000=%d", ti, steps100, prev)
	}
}
