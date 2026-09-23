package engine_test

import (
	"strings"
	"testing"

	"ontology/engine"
	"ontology/runes"
	"ontology/syntax"
)

func mustComp(t *testing.T, pat string) *syntax.Pattern {
	t.Helper()
	p, err := syntax.Compile(pat, nil)
	if err != nil {
		t.Fatalf("Compile(%q): %v", pat, err)
	}
	return p
}

func hit(t *testing.T, pat, path string, want bool) {
	t.Helper()
	if got := engine.Match(mustComp(t, pat), path); got != want {
		t.Errorf("Match(%q,%q)=%v want %v", pat, path, got, want)
	}
}

func TestSemantics(t *testing.T) {
	// ? 按码点；* 不跨段；转义逐字；不规范化；尾斜杠有别。
	cases := []struct {
		pat, path string
		want      bool
	}{
		{"?", "é", true}, {"?", "é", true}, {"?", "", false}, {"?", "ab", false},
		{"?", "/", false}, {"*", "", true}, {"*", "abc", true}, {"*", "a/b", false},
		{"a*b", "axxb", true}, {"a*b", "a/b", false},
		{`a\*b`, "a*b", true}, {`a\*b`, "axb", false}, {`a\\b`, `a\b`, true},
		{"**", "", true}, {"**", "a/b/c", true}, {"**/x", "x", true}, {"**/x", "/x", true},
		{"a/**/b", "a/b", true}, {"a/**/b", "a/x/b", true}, {"a/**/b", "a/x/y/b", true},
		{"x/**", "x", true}, {"x/**", "x/", true}, {"x/**", "x/y", true}, {"x/**", "", false},
		{"**/x", "x/y", false}, {"x/", "x", false}, {"x", "x/", false},
		{"a//b", "a/b", false}, {"a/./b", "a/b", false},
		{"a**b", "axxb", true}, {"**.go", "main.go", true}, {"**.go", "dir/x.go", false},
	}
	for _, c := range cases {
		hit(t, c.pat, c.path, c.want)
	}
}

func TestDesignTable(t *testing.T) {
	paths := []string{"", "x", "x/", "/x", "x/y", "a/b", "a/x/y/b"}
	rows := map[string][]bool{
		"x/**":   {false, true, true, false, true, false, false},
		"**/x":   {false, true, false, true, false, false, false},
		"*":      {true, true, false, false, false, false, false},
		"**":     {true, true, true, true, true, true, true},
		"a/**/b": {false, false, false, false, false, true, true},
	}
	for pat, want := range rows {
		for i, path := range paths {
			hit(t, pat, path, want[i])
		}
	}
}

func TestClasses(t *testing.T) {
	cases := []struct {
		pat, path string
		want      bool
	}{
		{"[]a]", "]", true}, {"[]a]", "a", true}, {"[]a]", "x", false},
		{"[!]a]", "x", true}, {"[!]a]", "]", false}, {"[!]a]", "a", false},
		{"[!]a]", "/", false},
		{"[a-]", "a", true}, {"[a-]", "-", true}, {"[a-]", "b", false},
		{"[\\]]", "]", true}, {"[\\]]", "[", false},
		{"[é-ë]", "ê", true}, {"[é-ë]", "é", true}, {"[é-ë]", "e", false},
		{"[^a-c]", "d", true}, {"[^a-c]", "b", false},
	}
	for _, c := range cases {
		hit(t, c.pat, c.path, c.want)
	}
}

func TestInvalidUTF8(t *testing.T) {
	bad := string([]byte{0xC3, 0x2F}) // 非法字节 + '/'
	cases := []struct {
		pat, path string
		want      bool
	}{
		{"?", string([]byte{0xC3}), true},        // ? 匹配一个非法字节
		{"??", string([]byte{0xFF, 0xFE}), true}, // 两个非法字节各一个码点
		{"?", bad, false},                        // 非法字节 + '/' 是两段
		{"[é-ë]", string([]byte{0xC3}), false},
		{string([]byte{0xC3}), string([]byte{0xC3}), true}, // 逐字节相等
		{string([]byte{0xC3}), "�", false},                 // 合法 U+FFFD 不等
	}
	_ = runes.Count // 包被引用
	for _, c := range cases {
		hit(t, c.pat, c.path, c.want)
	}
}

func TestComplexity(t *testing.T) {
	pats := []string{"a*a*a*a*a*a*a*a*b", "**/**/**/**/**/x"}
	lens := []int{100, 10000}
	var prev int64
	for ci, pat := range pats {
		for li, n := range lens {
			path := strings.Repeat("a", n)
			if ci == 1 {
				path = strings.Repeat("a/", n) + "a"
			}
			p := mustComp(t, pat)
			if engine.Match(p, path) {
				t.Errorf("pathology #%d unexpectedly matched", ci)
			}
			s := engine.LastSteps()
			bound := int64(4) * int64(p.Atoms()) * int64(runes.Count(path))
			if s > bound {
				t.Errorf("pattern %q n=%d steps=%d > bound %d", pat, n, s, bound)
			}
			if li == 1 && s > 200*prev {
				t.Errorf("pattern %q ratio steps(%d)=%d vs %d", pat, n, s, prev)
			}
			prev = s
		}
	}
}
