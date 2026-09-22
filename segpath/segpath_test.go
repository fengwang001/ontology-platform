package segpath

import (
	"reflect"
	"testing"
)

func TestNormalizeRules(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"/", "/"},
		{"/a/b/c", "/a/b/c"},
		{"/a/./b", "/a/b"},     // "." removed
		{"/a/b/../c", "/a/c"},  // ".." pops one level
		{"/../../x", "/x"},     // ".." above root swallowed
		{"/..", "/"},           // root ".." swallowed, trailing kept
		{"/a/..", "/"},         // trailing ".." leaves "/"
		{"/a/.", "/a/"},        // trailing "." leaves trailing slash
		{"/a", "/a"},           // no trailing slash
		{"/a/", "/a/"},         // trailing slash is significant
		{"//a//b", "//a//b"},   // empty segments preserved
		{"/a//../b", "/a/b"},   // ".." pops the empty segment
		{"/%2e%2e/x", "/x"},    // encoded ".." resolved after decode
		{"/a%2Fb", "/a%2Fb"},   // reserved escape kept, one segment
		{"/%41/%62", "/A/b"},   // unreserved escapes folded
		{"a/b", "a/b"},         // relative path
		{"../a", "a"},          // relative ".." at start swallowed
		{"/a/b/../../..", "/"}, // cascade cannot go above root
	}
	for _, c := range cases {
		got, _, _, _, err := Normalize(c.in)
		if err != nil {
			t.Fatalf("Normalize(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("Normalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSegmentList(t *testing.T) {
	_, segs, _, _, err := Normalize("/a//b/")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"", "a", "", "b", ""}
	if !reflect.DeepEqual(segs, want) {
		t.Errorf("segs = %v, want %v", segs, want)
	}
}

func TestIdempotent(t *testing.T) {
	inputs := []string{"/../../x", "/a/./b/../c/", "//a//.././b", "/%2e/%2E%2E/a%2Fb"}
	for _, in := range inputs {
		once, _, _, _, err := Normalize(in)
		if err != nil {
			t.Fatal(err)
		}
		twice, _, _, _, err := Normalize(once)
		if err != nil {
			t.Fatal(err)
		}
		if once != twice {
			t.Errorf("not idempotent: %q -> %q -> %q", in, once, twice)
		}
	}
}

func TestBadEscapePropagates(t *testing.T) {
	if _, _, _, _, err := Normalize("/a/%zz"); err == nil {
		t.Error("expected pct error to propagate")
	}
}
