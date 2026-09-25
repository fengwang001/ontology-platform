package udiff_test

import (
	"errors"
	"math/rand"
	"strings"
	"testing"

	"ontology/patch"
	"ontology/udiff"
)

func roundtrip(t *testing.T, a, b []byte, ctx int) {
	t.Helper()
	p, err := udiff.Diff(a, b, ctx, -1)
	if err != nil {
		t.Fatal(err)
	}
	q, err := udiff.Parse(udiff.Render(p), udiff.Limits{})
	if err != nil {
		t.Fatalf("parse own render: %v", err)
	}
	out, err := patch.Apply(a, q, 0)
	if err != nil || string(out) != string(b) {
		t.Fatalf("apply(%q->%q) = %q, %v", a, b, out, err)
	}
	back, err := patch.Reverse(b, q, 0)
	if err != nil || string(back) != string(a) {
		t.Fatalf("reverse(%q->%q) = %q, %v", a, b, back, err)
	}
}

func TestRoundtrip(t *testing.T) {
	cases := [][2]string{
		{"", ""}, {"", "x\n"}, {"x\n", ""}, {"a\nb\nc\n", "a\nX\nc\n"},
		{"a\r\nb\r\n", "a\r\nc\r\n"}, {"x\n", "x"}, {"x", "x\n"}, {"x", "y"},
		{"a\nb\nc\nd\ne\nf\ng\nh\n", "A\nb\nc\nd\ne\nf\ng\nH\n"},
		{"same\n", "same\n"}, {"noeol", "noeol"}, {"\n\n\n", "\nX\n\n"},
	}
	for _, c := range cases {
		roundtrip(t, []byte(c[0]), []byte(c[1]), 3)
	}
	r := rand.New(rand.NewSource(2))
	for i := 0; i < 100; i++ {
		var gen func() []byte
		gen = func() []byte {
			var sb strings.Builder
			for n := r.Intn(12); n > 0; n-- {
				sb.WriteByte(byte('a' + r.Intn(3)))
				switch r.Intn(3) {
				case 0:
					sb.WriteString("\r\n")
				case 1:
					sb.WriteString("\n")
				}
			}
			return []byte(sb.String())
		}
		a, b := gen(), gen()
		if r.Intn(2) == 0 && len(a) > 0 {
			a = a[:len(a)-1] // drop an ending to mix no-EOL cases
		}
		roundtrip(t, a, b, r.Intn(4))
	}
}

func TestZeroCountHeaders(t *testing.T) {
	cases := []struct {
		a, b string
		ctx  int
		want string
	}{
		{"x\n", "y\nx\n", 0, "@@ -0,0 +1 @@"},
		{"a\nb\nc\n", "a\nb\nX\nc\n", 0, "@@ -2,0 +3 @@"},
		{"a\n", "", 3, "@@ -1 +0,0 @@"},
	}
	for _, c := range cases {
		p, _ := udiff.Diff([]byte(c.a), []byte(c.b), c.ctx, -1)
		if !strings.Contains(string(udiff.Render(p)), c.want) {
			t.Fatalf("%q->%q: want header %s in\n%s", c.a, c.b, c.want, udiff.Render(p))
		}
	}
}

func TestMergeThreshold(t *testing.T) {
	cases := []struct {
		g, ctx, hunks int
	}{
		{2, 1, 1}, {3, 1, 2}, {6, 3, 1}, {7, 3, 2}, {0, 0, 1}, {1, 0, 2},
	}
	for _, c := range cases {
		mk := func(av, bv string) []byte {
			var sb strings.Builder
			sb.WriteString(av + "\n")
			for i := 0; i < c.g; i++ {
				sb.WriteString("x\n")
			}
			sb.WriteString(bv + "\n")
			return []byte(sb.String())
		}
		p, _ := udiff.Diff(mk("A", "B"), mk("A1", "B1"), c.ctx, -1)
		if len(p.Hunks) != c.hunks {
			t.Fatalf("g=%d ctx=%d: got %d hunks, want %d", c.g, c.ctx, len(p.Hunks), c.hunks)
		}
	}
}

func TestParseStrict(t *testing.T) {
	bad := []string{
		"not a patch\n",
		"--- a\n+++ b\n@@ -1 +1 @@\n-a\n-b\n",                           // one line too many
		"--- a\n+++ b\n@@ -1,2 +1 @@\n-a\n",                             // one line too few
		"--- a\n+++ b\n@@ -1 +1 @@\n?a\n",                               // bad prefix
		"--- a\n+++ b\n@@ -1 +1 @@\n-a\n\\ bogus\n",                     // bad marker text
		"--- a\n+++ b\n@@ -1 +1 @@\n\\ No newline at end of file\n-a\n", // stray marker
		"--- a\n+++ b\n@@ 1 1 @@\n-a\n",                                 // bad header
		"--- a\n+++ b\n@@ -0,1 +1 @@\n-a\n",                             // start 0 with count 1
	}
	for _, s := range bad {
		if _, err := udiff.Parse([]byte(s), udiff.Limits{}); !errors.Is(err, udiff.ErrFormat) {
			t.Fatalf("want ErrFormat for %q, got %v", s, err)
		}
	}
	good := "--- a\n+++ b\n@@ -1,2 +1,2 @@\n \n-x\n+y\n\\ No newline at end of file\n"
	if _, err := udiff.Parse([]byte(good), udiff.Limits{}); err != nil {
		t.Fatalf("empty context line must parse: %v", err)
	}
}

func TestNoNewlineOnly(t *testing.T) {
	p, _ := udiff.Diff([]byte("x\n"), []byte("x"), 3, -1)
	data := udiff.Render(p)
	if !strings.Contains(string(data), "\\ No newline at end of file") {
		t.Fatalf("EOL-only change must produce a marked patch:\n%s", data)
	}
	roundtrip(t, []byte("x\n"), []byte("x"), 3)
}
