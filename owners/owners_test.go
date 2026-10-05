package owners

import (
	"reflect"
	"testing"
)

func exampleResolver() *Resolver {
	return NewResolver([]Rule{
		{Pattern: "*", Owners: []string{"alice"}},
		{Pattern: "docs/", Owners: nil},
		{Pattern: "*.go", Owners: []string{"bob", "carol"}},
		{Pattern: "api/", Owners: []string{"dave"}},
	})
}

func TestOwnersOf(t *testing.T) {
	cases := []struct {
		name string
		path string
		want []string
	}{
		{"empty list rule wins over wildcard", "docs/a.md", nil},
		{"suffix rule wins over prefix rule", "docs/gen.go", []string{"bob", "carol"}},
		{"prefix rule wins over suffix rule", "api/x.go", []string{"dave"}},
		{"wildcard fallback", "README", []string{"alice"}},
		{"suffix in any directory", "deep/nested/dir/z.go", []string{"bob", "carol"}},
		{"prefix at root only", "xdocs/a.md", []string{"alice"}},
	}
	r := exampleResolver()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := r.OwnersOf(tc.path); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("OwnersOf(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestMatch(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"*", "anything/at/all", true},
		{"docs/", "docs/a.md", true},
		{"docs/", "docs", false},
		{"docs/", "adocs/a.md", false},
		{"*.go", "main.go", true},
		{"*.go", "a/b/c.go", true},
		{"*.go", "main.gox", false},
		{"*.go", "go", false},
		{"README", "README", true},
		{"README", "README.md", false},
		{"README", "x/README", false},
	}
	for _, tc := range cases {
		if got := Match(tc.pattern, tc.path); got != tc.want {
			t.Errorf("Match(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}

func TestLastRuleWinsAndNoMatch(t *testing.T) {
	r := NewResolver([]Rule{
		{Pattern: "a.txt", Owners: []string{"x"}},
		{Pattern: "a.txt", Owners: []string{"y", "z"}},
	})
	if got := r.OwnersOf("a.txt"); !reflect.DeepEqual(got, []string{"y", "z"}) {
		t.Errorf("last rule should win, got %v", got)
	}
	if got := r.OwnersOf("b.txt"); got != nil {
		t.Errorf("no rule should match, got %v", got)
	}
	empty := NewResolver(nil)
	if got := empty.OwnersOf("a.txt"); got != nil {
		t.Errorf("no rules at all, got %v", got)
	}
}
