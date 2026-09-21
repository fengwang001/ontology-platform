package slashpath

import "testing"

// TestJoinSamples covers every row of the required Join sample table.
func TestJoinSamples(t *testing.T) {
	cases := []struct {
		elems []string
		want  string
	}{
		{[]string{"a", "b"}, "a/b"},
		{[]string{"a", "", "b"}, "a/b"},
		{[]string{"/a", "b"}, "/a/b"},
		{[]string{"a", "../b"}, "b"},
		{nil, "."},
		{[]string{"", ""}, "."},
	}
	for _, c := range cases {
		if got := Join(c.elems...); got != c.want {
			t.Errorf("Join(%q) = %q, want %q", c.elems, got, c.want)
		}
	}
}

// TestJoinDerived checks Join behaviors derived from the invariants.
func TestJoinDerived(t *testing.T) {
	cases := []struct {
		elems []string
		want  string
	}{
		// Empty elements anywhere are ignored.
		{[]string{"", "a", ""}, "a"},
		{[]string{""}, "."},
		// Elements may themselves contain slashes and dots.
		{[]string{"a/b", "c"}, "a/b/c"},
		{[]string{"a//b", "./c"}, "a/b/c"},
		// Later ".." can resolve earlier elements.
		{[]string{"a", "b", "../../c"}, "c"},
		{[]string{"/a", "..", "b"}, "/b"},
		// Absoluteness comes from the joined string.
		{[]string{"/", "a"}, "/a"},
		{[]string{"..", "a"}, "../a"},
		// Multibyte elements.
		{[]string{"中文", "目录"}, "中文/目录"},
	}
	for _, c := range cases {
		if got := Join(c.elems...); got != c.want {
			t.Errorf("Join(%q) = %q, want %q", c.elems, got, c.want)
		}
	}
}

// TestJoinConsistentWithClean verifies Join equals Clean of the
// naive '/'-concatenation of its non-empty elements.
func TestJoinConsistentWithClean(t *testing.T) {
	if got, want := Join("a", "b"), Clean("a/b"); got != want {
		t.Errorf("Join = %q, Clean of concat = %q", got, want)
	}
	if got, want := Join("/a/", "/b/"), Clean("/a//b/"); got != want {
		t.Errorf("Join = %q, Clean of concat = %q", got, want)
	}
}
