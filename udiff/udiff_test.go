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

func roundTrip(t *testing.T, a, b string) {
	t.Helper()
	hs, err := udiff.Parse(udiff.Diff([]byte(a), []byte(b), 3))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, err1 := patch.Apply([]byte(a), hs, 0)
	back, err2 := patch.Reverse([]byte(b), hs, 0)
	if err1 != nil || err2 != nil || string(got) != b || string(back) != a {
		t.Fatalf("roundtrip %q<->%q: got %q back %q err %v %v", a, b, got, back, err1, err2)
	}
}

func TestRoundTrip(t *testing.T) {
	cases := [][2]string{
		{"", ""}, {"", "x\n"}, {"x\n", ""}, {"x", "y"}, {"a\nb\nc\n", "a\nX\nc\n"},
		{"a\r\nb\r\n", "a\r\nc\r\n"}, {"a\nb", "a\nc"}, {"a\nb", "a\nb\n"},
		{"a\nb\n", "a\nb"}, {"one\r\ntwo\nthree", "one\r\nTWO\nthree\n"},
	}
	for _, c := range cases {
		roundTrip(t, c[0], c[1])
	}
	rng := rand.New(rand.NewSource(7))
	gen := func() []byte {
		var b []byte
		for n := rng.Intn(12); n >= 0; n-- {
			b = append(b, 'l', byte('a'+rng.Intn(4)), '\r', '\n')
		}
		return b
	}
	for i := 0; i < 100; i++ {
		roundTrip(t, string(gen()), string(gen()))
	}
}

func TestDeterministic(t *testing.T) {
	want := udiff.Diff([]byte("a\nb\nc\nd\n"), []byte("b\na\nd\nc\n"), 3)
	for i := 0; i < 100; i++ {
		if got := udiff.Diff([]byte("a\nb\nc\nd\n"), []byte("b\na\nd\nc\n"), 3); !bytes.Equal(got, want) {
			t.Fatalf("run %d differs", i)
		}
	}
}

func TestZeroCountHeaders(t *testing.T) {
	cases := []struct{ old, new string; ctx int; want string }{
		{"x\n", "y\nx\n", 0, "@@ -0,0 +1 @@"},
		{"a\nb\nc\n", "a\nb\nX\nc\n", 0, "@@ -2,0 +3 @@"},
		{"a\n", "", 3, "@@ -1 +0,0 @@"},
	}
	for _, c := range cases {
		p := udiff.Diff([]byte(c.old), []byte(c.new), c.ctx)
		if !strings.Contains(string(p), c.want) {
			t.Fatalf("%q -> %q: missing %q in\n%s", c.old, c.new, c.want, p)
		}
		roundTrip(t, c.old, c.new)
	}
}

func TestMergeThreshold(t *testing.T) {
	for _, c := range []int{0, 1, 3} {
		for _, d := range []struct{ g, want int }{{2 * c, 1}, {2*c + 1, 2}} {
			a := "p\n" + strings.Repeat("k\n", d.g) + "q\n"
			b := "P\n" + strings.Repeat("k\n", d.g) + "Q\n"
			hs, err := udiff.Parse(udiff.Diff([]byte(a), []byte(b), c))
			if err != nil || len(hs) != d.want {
				t.Fatalf("C=%d g=%d: %d hunks (err %v), want %d", c, d.g, len(hs), err, d.want)
			}
		}
	}
}

func TestParseStrict(t *testing.T) {
	head := "--- a\n+++ b\n"
	cases := []struct{ p string; ok bool }{
		{head, true},
		{head + "@@ -1 +1 @@\n-x\n+y\n", true},
		{head + "@@ -1,2 +1,2 @@\n \n-x\n+y\n", true},
		{head + "@@ -1 +1 @@\n-x\n\\ No newline at end of file\n+y\n\\ No newline at end of file\n", true},
		{"", false},
		{"--- a\n", false},
		{head + "@@ -1 +1\n-x\n+y\n", false},
		{head + "@@ -1 +1 @@\n-x\n+y\n+z\n", false},
		{head + "@@ -1,2 +1 @@\n-x\n", false},
		{head + "@@ -1 +1 @@\n?x\n", false},
		{head + "@@ -1 +1 @@\n\n", false},
		{head + "@@ -1 +1 @@\n\\ No newline at end of file\n-x\n+y\n", false},
	}
	for _, c := range cases {
		if _, err := udiff.Parse([]byte(c.p)); c.ok != (err == nil) || (!c.ok && !errors.Is(err, udiff.ErrFormat)) {
			t.Fatalf("%q: ok=%v err=%v", c.p, c.ok, err)
		}
	}
}

func TestTruncation(t *testing.T) {
	src := "1\n2\n3\n4\n5\n6\n7\n8\n"
	p := udiff.Diff([]byte(src), []byte("1\nA\n3\n4\n5\n6\nB\n8\n"), 1)
	for i := 0; i <= len(p); i++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("truncate %d: panic %v", i, r)
				}
			}()
			if hs, err := udiff.Parse(p[:i]); err == nil {
				if got, err := patch.Apply([]byte(src), hs, 1); err != nil && got != nil {
					t.Fatalf("truncate %d: partial apply", i)
				}
			}
		}()
	}
}

func TestFlip(t *testing.T) {
	src := "alpha\nbeta\ngamma\ndelta\n"
	lines := strings.Split(string(udiff.Diff([]byte(src), []byte("alpha\nBETA\ngamma\nDELTA\n"), 0)), "\n")
	for i, l := range lines {
		var flips []string
		if j := strings.IndexByte(l, '-'); j >= 0 {
			flips = append(flips, l[:j]+"+"+l[j+1:])
		}
		if j := strings.IndexAny(l, "0123456789"); j >= 0 {
			flips = append(flips, l[:j]+string(l[j]+1)+l[j+1:])
		}
		for _, f := range flips {
			cp := append([]string{}, lines...)
			cp[i] = f
			if hs, err := udiff.Parse([]byte(strings.Join(cp, "\n"))); err == nil {
				if _, err := patch.Apply([]byte(src), hs, 0); err == nil {
					t.Fatalf("flip line %d to %q: applied cleanly", i, f)
				}
			}
		}
	}
}
