package engine_test

import (
	"testing"

	"ontology/engine"
	"ontology/syntax"
)

func match(t *testing.T, pat, path string) bool {
	t.Helper()
	p, err := syntax.Compile(pat, syntax.Limits{})
	if err != nil {
		t.Fatalf("compile %q: %v", pat, err)
	}
	return engine.New(p).Match(path)
}

// 语义：原子、**、转义、字符类刁钻样例、无规范化。
func TestSemantics(t *testing.T) {
	cases := []struct {
		pat, path string
		want      bool
	}{
		// ? 按码点而非字节
		{"?", "é", true}, {"?", "ee", false}, {"?", "", false},
		// * 段内任意、不跨段
		{"a*b", "ab", true}, {"a*b", "axyzb", true}, {"a*b", "a/b", false},
		{"*", "", true}, {"*", "abc", true}, {"*", "a/b", false},
		// ** 整段：零个或多个整段
		{"a/**/b", "a/b", true}, {"a/**/b", "a/x/b", true},
		{"a/**/b", "a/x/y/b", true}, {"a/**/b", "a/c", false},
		{"**", "", true}, {"**", "a/b/c", true},
		// ** 与其他字符相邻 = 单个 *
		{"a**b", "axxb", true}, {"a**b", "a/xxb", false}, {"**.go", "x.go", true},
		// 转义按字面
		{`\*`, "*", true}, {`\*`, "x", false}, {`\?`, "?", true}, {`\[`, "[", true},
		// 字符类六个刁钻样例
		{"[]a]", "]", true}, {"[]a]", "a", true}, {"[]a]", "b", false},
		{"[!]a]", "b", true}, {"[!]a]", "]", false}, {"[!]a]", "/", false},
		{"[^a]", "b", true}, {"[^a]", "a", false},
		{"[a-]", "-", true}, {"[a-]", "a", true}, {"[a-]", "b", false},
		{"[é-ë]", "ê", true}, {"[é-ë]", "a", false},
		{`[\]]`, "]", true}, {`[\]]`, "x", false},
		// 取反类不跨段
		{"x/[!a]", "x/b", true}, {"x/[!a]", "x//", false},
		// 无规范化：x 与 x/ 不同，// 不折叠
		{"x", "x/", false}, {"x/", "x", false},
		{"a//b", "a//b", true}, {"a/b", "a//b", false},
		// 空模式只匹配空串
		{"", "", true}, {"", "a", false}, {"", "/", false},
	}
	for _, c := range cases {
		if got := match(t, c.pat, c.path); got != c.want {
			t.Errorf("match(%q, %q) = %v, want %v", c.pat, c.path, got, c.want)
		}
	}
}

// DESIGN.md 推导表：x/**、**/x、*、**、a/**/b 对七个路径的结果。
func TestBoundaryTable(t *testing.T) {
	pats := []string{"x/**", "**/x", "*", "**", "a/**/b"}
	paths := []string{"", "x", "x/", "/x", "x/y", "a/b", "a/x/y/b"}
	want := [][]bool{
		{false, true, true, false, true, false, false},
		{false, true, false, true, true, false, false},
		{true, true, false, false, false, false, false},
		{true, true, true, true, true, true, true},
		{false, false, false, false, false, true, true},
	}
	for i, pat := range pats {
		for j, path := range paths {
			if got := match(t, pat, path); got != want[i][j] {
				t.Errorf("match(%q, %q) = %v, want %v", pat, path, got, want[i][j])
			}
		}
	}
}
