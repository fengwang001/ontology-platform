package engine_test

import (
	"testing"

	"ontology/class"
	"ontology/engine"
	"ontology/syntax"
)

func match(t *testing.T, pat, path string) bool {
	t.Helper()
	p, err := syntax.Compile(pat, syntax.Limits{})
	if err != nil {
		t.Fatalf("Compile(%q): %v", pat, err)
	}
	return engine.Match(p, path)
}

func TestSemantics(t *testing.T) {
	cases := []struct {
		pat, path string
		want      bool
	}{
		{"a?c", "aéc", true},   // ? is one rune, not one byte
		{"a?c", "abc", true},   //
		{"?", "é", true},       //
		{"?", "", false},       //
		{"?", "ab", false},     //
		{"a*c", "aXYZc", true}, //
		{"a*c", "a/c", false},  // * does not cross /
		{"*", "", true},        //
		{"*", "a/b", false},    //
		{"**", "", true},       //
		{"**", "a/b/c", true},  //
		{"a/**/b", "a/b", true},
		{"a/**/b", "a/x/b", true},
		{"a/**/b", "a/x/y/b", true},
		{"a/**/b", "a/b/c", false},
		{"x/**", "x", true},
		{"x/**", "x/", true},
		{"x/**", "x/y", true},
		{"x/**", "x/y/z", true},
		{"x/**", "/x", false},
		{"x/**", "", false},
		{"**/x", "x", true},
		{"**/x", "/x", true},
		{"**/x", "x/", false},
		{"**/x", "", false},
		{"a**b", "axxb", true}, // adjacent ** collapses to *
		{"a**b", "a/b", false},
		{"**.go", "x.go", true},
		{"**.go", "x/y.go", false},
		{"", "", true},
		{"", "x", false},
		{"x/", "x/", true}, // trailing slash is significant
		{"x/", "x", false},
		{"a//b", "a//b", true}, // no normalization
		{"a/b", "a//b", false},
		{`a\?b`, "a?b", true},
		{`a\?b`, "axb", false},
		{`a\*b`, "a*b", true},
		{"é", "é", true},
	}
	for _, c := range cases {
		if got := match(t, c.pat, c.path); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.pat, c.path, got, c.want)
		}
	}
}

func TestClasses(t *testing.T) {
	cases := []struct {
		pat, path string
		want      bool
	}{
		{"[]a]", "]", true},
		{"[]a]", "a", true},
		{"[]a]", "b", false},
		{"[!]a]", "b", true},
		{"[!]a]", "]", false},
		{"[!]a]", "a", false},
		{"[a-]", "-", true},
		{"[a-]", "a", true},
		{"[a-]", "b", false},
		{"[é-ë]", "ê", true}, // rune range
		{"[é-ë]", "a", false},
		{`[\]]`, "]", true},
		{`[\]]`, `\`, false},
		{"[a-c]", "b", true},
		{"[!a-c]", "d", true},
		{"[!a-c]", "b", false},
		{"[^a]", "b", true},
		{"[^a]", "a", false},
		{"[-a]", "-", true},
	}
	for _, c := range cases {
		if got := match(t, c.pat, c.path); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.pat, c.path, got, c.want)
		}
	}
	cl, _, err := class.Parse([]byte("[!a]"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if cl.Match('/') {
		t.Error("negated class must not match '/'")
	}
}

func TestInvalidUTF8(t *testing.T) {
	cases := []struct {
		pat, path string
		want      bool
	}{
		{"?", "\xff", true}, // ? matches one invalid byte
		{"??", "\xff\xfe", true},
		{"a\xffc", "a\xffc", true}, // raw byte preserved for literals
		{"a\xffc", "axc", false},
		{"[é-ë]", "\xff", false}, // invalid byte not in rune range
		{"a*", "a\xff\xff", true},
	}
	for _, c := range cases {
		if got := match(t, c.pat, c.path); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.pat, c.path, got, c.want)
		}
	}
}
