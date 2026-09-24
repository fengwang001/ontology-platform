package udiff_test

import (
	"bytes"
	"strings"
	"testing"

	"ontology/patch"
	"ontology/udiff"
)

func opt(fuzz int) patch.Options {
	return patch.Options{Fuzz: fuzz}
}

func diff(a, b string, C int) []byte {
	p, err := udiff.Diff("f", "f", []byte(a), []byte(b), C, 0)
	if err != nil {
		panic(err)
	}
	return p.Render()
}

func TestRoundTrip(t *testing.T) {
	cases := []struct{ a, b string }{
		{"", ""},
		{"a\n", "a\n"},
		{"a\nb\nc\n", "a\nX\nc\n"},
		{"x\n", "y\nx\n"},
		{"a\nb\n", "b\na\n"},
		{"a\r\nb\r\n", "a\r\nX\r\n"},
		{"abc", "abc\n"},
		{"abc\n", "abc"},
		{"abc", "abd"},
		{"", "x\n"},
		{"a\n", ""},
		{"one\ntwo\nthree\nfour\nfive\nsix\n", "one\n2\nthree\nfour\nfive\n6\n"},
	}
	for _, tc := range cases {
		text := diff(tc.a, tc.b, 3)
		got, err := patch.Apply([]byte(tc.a), text, opt(0))
		if err != nil {
			t.Fatalf("apply %q->%q: %v\n%s", tc.a, tc.b, err, text)
		}
		if !bytes.Equal(got, []byte(tc.b)) {
			t.Fatalf("%q->%q got %q", tc.a, tc.b, got)
		}
		rev, err := patch.Reverse(text, udiff.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		back, err := patch.Apply(got, rev, opt(0))
		if err != nil || !bytes.Equal(back, []byte(tc.a)) {
			t.Fatalf("reverse %q: %v / %q", tc.a, err, back)
		}
	}
}

func TestZeroCountHeaders(t *testing.T) {
	cases := []struct {
		a, b, want string
	}{
		{"x\n", "y\nx\n", "@@ -0,0 +1 @@"},
		{"a\nb\nc\n", "a\nb\nX\nc\n", "@@ -2,0 +3 @@"},
		{"a\n", "", "@@ -1 +0,0 @@"},
	}
	for _, tc := range cases {
		text := string(diff(tc.a, tc.b, 0))
		if !strings.Contains(text, tc.want) {
			t.Fatalf("want header %q in:\n%s", tc.want, text)
		}
		if _, err := patch.Apply([]byte(tc.a), []byte(text), opt(0)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMergeThreshold(t *testing.T) {
	// C=1: two single-line changes separated by g equal lines.
	mk := func(g int) string {
		var b strings.Builder
		b.WriteString("a\n")
		for i := 0; i < g; i++ {
			b.WriteString("m\n")
		}
		b.WriteString("z\n")
		return b.String()
	}
	mk2 := func(g int) string {
		var b strings.Builder
		b.WriteString("A\n")
		for i := 0; i < g; i++ {
			b.WriteString("m\n")
		}
		b.WriteString("Z\n")
		return b.String()
	}
	hunks := func(text []byte) int {
		p, err := udiff.Parse(text, udiff.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		return len(p.Hunks)
	}
	if h := hunks(diff(mk(2), mk2(2), 1)); h != 1 {
		t.Fatalf("g=2C must merge, got %d hunks", h)
	}
	if h := hunks(diff(mk(3), mk2(3), 1)); h != 2 {
		t.Fatalf("g=2C+1 must split, got %d hunks", h)
	}
}

func TestOnlyNewlineChange(t *testing.T) {
	cases := [][2]string{{"a", "a\n"}, {"a\n", "a"}}
	for _, tc := range cases {
		text := diff(tc[0], tc[1], 3)
		if len(text) == 0 {
			t.Fatal("empty patch for newline-only change")
		}
		got, err := patch.Apply([]byte(tc[0]), text, opt(0))
		if err != nil || !bytes.Equal(got, []byte(tc[1])) {
			t.Fatalf("%q->%q: %v %q", tc[0], tc[1], err, got)
		}
	}
}

func TestRenderDeterminism(t *testing.T) {
	first := diff("a\nb\nc\n", "x\nb\ny\n", 3)
	for i := 0; i < 100; i++ {
		if !bytes.Equal(first, diff("a\nb\nc\n", "x\nb\ny\n", 3)) {
			t.Fatal("render not deterministic")
		}
	}
}
