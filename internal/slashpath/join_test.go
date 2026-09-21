package slashpath

import "testing"

func TestJoinSamples(t *testing.T) {
	cases := []struct {
		elems []string
		want  string
	}{
		// Mandatory samples.
		{[]string{"a", "b"}, "a/b"},
		{[]string{"a", "", "b"}, "a/b"},
		{[]string{"/a", "b"}, "/a/b"},
		{[]string{"a", "../b"}, "b"},
		{nil, "."},
		{[]string{"", ""}, "."},
		// Derived cases.
		{[]string{"/a", "/b"}, "/a/b"},
		{[]string{"a/", "/b"}, "a/b"},
		{[]string{"..", "a"}, "../a"},
		{[]string{"/", "a"}, "/a"},
		{[]string{"a", "..", ".."}, ".."},
		{[]string{"目录", "文件"}, "目录/文件"},
	}
	for _, c := range cases {
		if got := Join(c.elems...); got != c.want {
			t.Errorf("Join(%q) = %q, want %q", c.elems, got, c.want)
		}
	}
}

// Join of already-joined parts must agree with Clean of the manual
// concatenation, and the result must itself be idempotent.
func TestJoinConsistency(t *testing.T) {
	parts := []string{"a", ".", "..", "b", "", "/", "目录"}
	for _, x := range parts {
		for _, y := range parts {
			got := Join(x, y)
			if got != Clean(got) {
				t.Errorf("Join(%q, %q) = %q is not clean", x, y, got)
			}
		}
	}
}
