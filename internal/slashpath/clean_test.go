package slashpath

import (
	"strings"
	"testing"
)

// TestCleanSamples covers every row of the required sample table.
func TestCleanSamples(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/a/b/../c", "/a/c"},
		{"/a/./b", "/a/b"},
		{"a/b/../../c", "c"},
		{"/..", "/"},
		{"//a///b", "/a/b"},
		{"/a/b/", "/a/b"},
		{".", "."},
		{"", "."},
		{"/", "/"},
		{"a/./b/", "a/b"},
	}
	for _, c := range cases {
		if got := Clean(c.in); got != c.want {
			t.Errorf("Clean(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestCleanDerived covers behaviors derived from the invariants that
// the sample table does not spell out.
func TestCleanDerived(t *testing.T) {
	cases := []struct{ in, want string }{
		// Relative paths keep the leading ".." they cannot resolve.
		{"..", ".."},
		{"../..", "../.."},
		{"../../a", "../../a"},
		{"a/../../b", "../b"},
		{"../a/../b", "../b"},
		// Absolute paths clamp ".." at the root.
		{"/../..", "/"},
		{"/../../a", "/a"},
		{"/a/../../../b", "/b"},
		// Dot and empty segments vanish everywhere.
		{"./a", "a"},
		{"./", "."},
		{"a//./b//", "a/b"},
		{"//", "/"},
		{"///", "/"},
		// Multibyte segments pass through untouched.
		{"/中文/目录/../文件", "/中文/文件"},
		{"日本語/./パス/", "日本語/パス"},
	}
	for _, c := range cases {
		if got := Clean(c.in); got != c.want {
			t.Errorf("Clean(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestInvariantAbsPreserved checks invariant 2 on the sample inputs.
func TestInvariantAbsPreserved(t *testing.T) {
	inputs := []string{
		"/a/b/../c", "/a/./b", "a/b/../../c", "/..", "//a///b",
		"/a/b/", ".", "", "/", "a/./b/", "..", "../a", "/../a",
	}
	for _, in := range inputs {
		if IsAbs(in) != IsAbs(Clean(in)) {
			t.Errorf("IsAbs(%q)=%v but IsAbs(Clean)=%v",
				in, IsAbs(in), IsAbs(Clean(in)))
		}
	}
}

// TestInvariantAbsNoDotDot checks invariant 3: an absolute result
// never contains a ".." segment.
func TestInvariantAbsNoDotDot(t *testing.T) {
	inputs := []string{
		"/..", "/../..", "/a/../../b", "/a/b/../../../..", "///..//",
	}
	for _, in := range inputs {
		got := Clean(in)
		for _, seg := range strings.Split(got, "/") {
			if seg == ".." {
				t.Errorf("Clean(%q) = %q contains a .. segment", in, got)
			}
		}
	}
}

// TestResultShape checks the output-shape guarantees: no "//", no
// trailing "/" (except root), no "." segments (except whole result).
func TestResultShape(t *testing.T) {
	inputs := []string{
		"", ".", "/", "//", "///", "a", "/a", "a/", "/a/",
		"a//b", "/a//b/", "./a/./b/", "../a//../b", "/../a/..",
	}
	for _, in := range inputs {
		got := Clean(in)
		if strings.Contains(got, "//") {
			t.Errorf("Clean(%q) = %q contains //", in, got)
		}
		if got != "/" && strings.HasSuffix(got, "/") {
			t.Errorf("Clean(%q) = %q has trailing /", in, got)
		}
		if got != "." {
			for _, seg := range strings.Split(got, "/") {
				if seg == "." {
					t.Errorf("Clean(%q) = %q contains a . segment", in, got)
				}
			}
		}
	}
}
