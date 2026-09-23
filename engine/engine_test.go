package engine_test

import (
	"strings"
	"testing"

	"ontology/engine"
)

func TestMatchTable(t *testing.T) {
	cases := []struct {
		pat, path string
		want      bool
	}{
		{"", "", true}, {"", "x", false},
		{"?", "é", true}, {"?", "", false}, {"?", "ab", false}, {"?", "/", false},
		{"*", "", true}, {"*", "x", true}, {"*", "x/", false}, {"*", "a/b", false},
		{"a**b", "ab", true}, {"a**b", "axxb", true}, {"a**b", "a/b", false},
		{"**.go", "x.go", true}, {"**.go", "x/y.go", false},
		{"**", "", true}, {"**", "x", true}, {"**", "x/", false},
		{"**", "/x", true}, {"**", "x/y", true}, {"**", "a/b", true},
		{"**/x", "x", true}, {"**/x", "/x", false}, {"**/x", "a/x", true},
		{"x/**", "x", true}, {"x/**", "x/", false}, {"x/**", "x/y", true},
		{"x/**", "x/y/z", true}, {"x/**", "y", false},
		{"a/**/b", "a/b", true}, {"a/**/b", "a/x/b", true},
		{"a/**/b", "a/x/y/b", true}, {"a/**/b", "a/c", false},
		{"**/**/**/**/**/x", strings.Repeat("a/", 99)+"x", true},
		{"?", "\xff", true}, {"[é-ë]", "\xff", false},
	}
	for _, c := range cases {
		m, err := engine.New(c.pat)
		if err != nil {
			t.Fatalf("Compile(%q): %v", c.pat, err)
		}
		if got := m.Match(c.path); got != c.want {
			t.Fatalf("%q.Match(%q)=%v want %v", c.pat, c.path, got, c.want)
		}
	}
}

func TestStepsBound(t *testing.T) {
	pats := []string{"a*a*a*a*a*a*a*a*b", "**/**/**/**/**/x"}
	base := map[string]int{}
	for scale, n := range []int{100, 10000} {
		for _, pat := range pats {
			m, _ := engine.New(pat)
			p := strings.Repeat("a", n)
			if strings.Contains(pat, "/") {
				p = strings.Repeat("a/", n) + "y"
			}
			m.Match(p)
			codepoints := n
			if strings.Contains(pat, "/") {
				codepoints = 2*n + 1
			}
			bound := 4 * m.AtomCount() * codepoints
			if m.Steps() > bound {
				t.Fatalf("pat %q n=%d steps=%d > bound=%d", pat, n, m.Steps(), bound)
			}
			if scale == 0 {
				base[pat] = m.Steps()
			} else if m.Steps() > 200*base[pat] {
				t.Fatalf("steps grew too fast for %q: %d vs %d", pat, m.Steps(), base[pat])
			}
		}
	}
}

func TestPathologicalFast(t *testing.T) {
	m, _ := engine.New("a*a*a*a*a*a*a*a*b")
	if m.Match(strings.Repeat("a", 100)) {
		t.Fatal("must not match")
	}
	d, _ := engine.New("**/**/**/**/**/x")
	if d.Match(strings.Repeat("a/", 100) + "y") {
		t.Fatal("deep path must not match")
	}
}
