package slashpath

import (
	"strings"
	"testing"
)

// cleanCases is the mandatory sample table from the specification, plus
// derived cases that follow from the three invariants.
var cleanCases = []struct {
	in, want string
}{
	// Mandatory samples.
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
	// Derived: leading ".." of relative paths is preserved.
	{"..", ".."},
	{"../..", "../.."},
	{"../a", "../a"},
	{"../../a/b", "../../a/b"},
	{"a/../../b", "../b"},
	{"./a", "a"},
	{"./", "."},
	// Derived: absolute paths never escape the root.
	{"/../..", "/"},
	{"/../../a", "/a"},
	{"/a/../../../b", "/b"},
	// Derived: redundant slashes and dots.
	{"///", "/"},
	{"//", "/"},
	{"a//b", "a/b"},
	{"a/././b", "a/b"},
	{"/././.", "/"},
	// Multi-byte segments pass through untouched.
	{"/目录/子/../文件", "/目录/文件"},
	{"目录/./文件", "目录/文件"},
	{"../目录", "../目录"},
}

func TestCleanSamples(t *testing.T) {
	for _, c := range cleanCases {
		if got := Clean(c.in); got != c.want {
			t.Errorf("Clean(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsAbs(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"/", true},
		{"/a", true},
		{"//a", true},
		{"", false},
		{".", false},
		{"a/b", false},
		{"../a", false},
	} {
		if got := IsAbs(c.in); got != c.want {
			t.Errorf("IsAbs(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// checkInvariants verifies the three invariants plus the output shape
// guarantees for one input. It is shared by the table test and the
// randomized test.
func checkInvariants(t *testing.T, p string) {
	t.Helper()
	c := Clean(p)

	// Invariant 1: idempotence.
	if cc := Clean(c); cc != c {
		t.Errorf("Clean(Clean(%q)) = %q, Clean(%q) = %q", p, cc, p, c)
	}

	// Invariant 2: absoluteness preservation.
	if IsAbs(p) != IsAbs(c) {
		t.Errorf("IsAbs(%q) = %v, IsAbs(Clean(%q)=%q) = %v",
			p, IsAbs(p), p, c, IsAbs(c))
	}

	// Invariant 3: absolute results contain no ".." segment.
	if IsAbs(c) {
		for _, seg := range strings.Split(c, "/") {
			if seg == ".." {
				t.Errorf("Clean(%q) = %q contains \"..\" segment", p, c)
			}
		}
	}

	// Shape: no consecutive slashes.
	if strings.Contains(c, "//") {
		t.Errorf("Clean(%q) = %q contains \"//\"", p, c)
	}

	// Shape: no trailing slash except the root itself.
	if c != "/" && strings.HasSuffix(c, "/") {
		t.Errorf("Clean(%q) = %q has trailing \"/\"", p, c)
	}

	// Shape: no "." segment unless the whole result is ".".
	if c != "." {
		for _, seg := range strings.Split(c, "/") {
			if seg == "." {
				t.Errorf("Clean(%q) = %q contains \".\" segment", p, c)
			}
		}
	}

	// Shape: never empty.
	if c == "" {
		t.Errorf("Clean(%q) returned empty string", p)
	}
}

func TestInvariantsOnSamples(t *testing.T) {
	for _, c := range cleanCases {
		checkInvariants(t, c.in)
	}
}
