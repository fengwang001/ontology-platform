package udiff_test

import (
	"bytes"
	"errors"
	"math/rand"
	"strings"
	"testing"

	"ontology/patch"
	"ontology/udiff"
)

func roundtrip(t *testing.T, a, b string) {
	t.Helper()
	p, err := udiff.Diff("a", "b", []byte(a), []byte(b), 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	q, err := udiff.Parse(udiff.Render(p), nil)
	if err != nil {
		t.Fatalf("parse own render: %v", err)
	}
	got, err := patch.Apply([]byte(a), q, 0)
	if err != nil || string(got) != b {
		t.Fatalf("apply(%q) = %q, %v; want %q", a, got, err, b)
	}
	back, err := patch.Reverse([]byte(b), q, 0)
	if err != nil || string(back) != a {
		t.Fatalf("reverse(%q) = %q, %v; want %q", b, back, err, a)
	}
}

func TestRoundTrip(t *testing.T) {
	cases := [][2]string{
		{"a\r\nb\r\n", "a\r\nc\r\n"},
		{"a\nb", "a\nc"},
		{"a\nb", "a\nb\n"},
		{"a\nb\n", "a\nb"},
		{"", "x\n"},
		{"x\n", ""},
		{"", ""},
		{"a\nb\nc\n", "a\nb\nX\nc\n"},
		{"a\n\nb\n", "a\n\nc\n"},
		{"x\r\ny", "y\r\nx"},
	}
	for _, c := range cases {
		roundtrip(t, c[0], c[1])
	}
	r := rand.New(rand.NewSource(7))
	for i := 0; i < 200; i++ {
		roundtrip(t, randText(r), randText(r))
	}
}

func randText(r *rand.Rand) string {
	var sb strings.Builder
	for i, n := 0, r.Intn(8); i < n; i++ {
		sb.WriteByte(byte('a' + r.Intn(3)))
		switch r.Intn(3) {
		case 0:
			sb.WriteString("\n")
		case 1:
			sb.WriteString("\r\n")
		}
	}
	return sb.String()
}

func TestDeterminism(t *testing.T) {
	a, b := []byte("a\nb\nc\nd\n"), []byte("b\nX\nd\ne\n")
	first, err := udiff.Diff("a", "b", a, b, 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := udiff.Render(first)
	for i := 0; i < 100; i++ {
		p, _ := udiff.Diff("a", "b", a, b, 3, 0)
		if !bytes.Equal(udiff.Render(p), want) {
			t.Fatal("render not deterministic")
		}
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
		p, err := udiff.Diff("a", "b", []byte(c.a), []byte(c.b), c.ctx, 0)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(udiff.Render(p)), c.want+"\n") {
			t.Fatalf("header %q not in %q", c.want, udiff.Render(p))
		}
	}
}

func TestMergeThreshold(t *testing.T) {
	cases := []struct {
		ctx, gap, want int
	}{
		{1, 2, 1}, {1, 3, 2}, {3, 6, 1}, {3, 7, 2}, {0, 1, 2}, {2, 4, 1}, {2, 5, 2},
	}
	for _, c := range cases {
		pad := strings.Repeat("p\n", c.ctx+1)
		a := pad + "A\n" + strings.Repeat("m\n", c.gap) + "B\n" + pad
		b := pad + "A2\n" + strings.Repeat("m\n", c.gap) + "B2\n" + pad
		p, err := udiff.Diff("a", "b", []byte(a), []byte(b), c.ctx, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Hunks) != c.want {
			t.Fatalf("ctx=%d gap=%d: got %d hunks, want %d", c.ctx, c.gap, len(p.Hunks), c.want)
		}
	}
}

func TestParseStrict(t *testing.T) {
	cases := []struct {
		name, text, want string
	}{
		{"no-header", "@@ -1 +1 @@\n", "line 1"},
		{"bad-header", "--- a\n+++ b\n@@ -x @@\n", "line 3"},
		{"bad-first-char", "--- a\n+++ b\n@@ -1 +1 @@\n?x\n", "line 4"},
		{"too-many", "--- a\n+++ b\n@@ -1 +1 @@\n-a\n+b\n+c\n", "line 6"},
		{"too-few", "--- a\n+++ b\n@@ -2 +2 @@\n-a\n", "line 5"},
		{"lone-backslash", "--- a\n+++ b\n\\ x\n", "line 3"},
		{"empty-hunk", "--- a\n+++ b\n@@ -0,0 +0,0 @@\n", "line 3"},
	}
	for _, c := range cases {
		_, err := udiff.Parse([]byte(c.text), nil)
		if !errors.Is(err, udiff.ErrMalformed) {
			t.Fatalf("%s: want ErrMalformed, got %v", c.name, err)
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: error %q lacks %q", c.name, err, c.want)
		}
	}
	// A context line that is a single space (empty line in file) is valid.
	ok := "--- a\n+++ b\n@@ -1,2 +1,2 @@\n x\n \n"
	if _, err := udiff.Parse([]byte(ok), nil); err != nil {
		t.Fatalf("single-space context line rejected: %v", err)
	}
}

func TestLimits(t *testing.T) {
	p, err := udiff.Diff("a", "b", []byte("a\nb\nc\nd\ne\n"), []byte("A\nb\nC\nd\nE\n"), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	text := udiff.Render(p)
	if len(p.Hunks) < 2 {
		t.Fatal("need >=2 hunks for this test")
	}
	if _, err := udiff.Parse(text, &udiff.Limits{MaxBytes: len(text) - 1}); err == nil {
		t.Fatal("byte limit not enforced")
	}
	if _, err := udiff.Parse(text, &udiff.Limits{MaxHunks: len(p.Hunks) - 1}); err == nil {
		t.Fatal("hunk limit not enforced")
	}
	if _, err := udiff.Parse(text, &udiff.Limits{MaxBytes: len(text), MaxHunks: len(p.Hunks)}); err != nil {
		t.Fatalf("limits at exact size rejected: %v", err)
	}
}
