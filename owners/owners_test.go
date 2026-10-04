package owners

import (
	"reflect"
	"testing"
)

func TestOwnersMatchPrecedence(t *testing.T) {
	rules := []Rule{
		{Pattern: "*", Owners: []string{"alice"}},
		{Pattern: "docs/", Owners: nil},
		{Pattern: "*.go", Owners: []string{"bob", "carol"}},
		{Pattern: "api/", Owners: []string{"dave"}},
	}
	r := New(rules)

	cases := []struct {
		path string
		want []string
	}{
		{"docs/a.md", nil},                        // 最后匹配 docs/，空属主
		{"docs/gen.go", []string{"bob", "carol"}}, // 后者优先于 docs/
		{"api/x.go", []string{"dave"}},            // api/ 最后，压过 *.go
		{"README", []string{"alice"}},             // 仅 * 命中
		{"src/main.go", []string{"bob", "carol"}}, // 任意目录后缀
		{".go", []string{"alice"}},                // 不满足 *.go（需至少一个前缀字符），回落到 *
		{"x.go.bak", []string{"alice"}},           // 后缀必须结尾，回落到 *
		{"docs", []string{"alice"}},               // 前缀要求保留斜杠
		{"docs/", nil},                            // 前缀匹配 docs/ 自身
		{"api/deep/x", []string{"dave"}},
		{"nope", []string{"alice"}},
	}
	for _, tc := range cases {
		got := r.Owners(tc.path)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Owners(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestOwnersNoMatchAndDefensiveCopy(t *testing.T) {
	r := New([]Rule{{Pattern: "a", Owners: []string{"x", "y"}}})
	if got := r.Owners("b"); got != nil {
		t.Errorf("no match = %v, want nil", got)
	}
	got := r.Owners("a")
	got[0] = "mutated"
	again := r.Owners("a")
	if again[0] != "x" {
		t.Fatalf("internal owners leaked/mutated: %v", again)
	}
}

func TestOwnersEmptyRuleSet(t *testing.T) {
	if got := New(nil).Owners("anything"); got != nil {
		t.Errorf("empty ruleset = %v, want nil", got)
	}
}
