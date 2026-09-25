package ontology_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"ontology/edit"
	"ontology/hunk"
	"ontology/lines"
	"ontology/patch"
	"ontology/udiff"
)

func render(t *testing.T, a, b string, c int) []byte {
	t.Helper()
	p, err := udiff.Build(lines.Split(a), lines.Split(b), c, 0)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	d, err := udiff.Render(p)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return d
}

func TestRoundTrip(t *testing.T) {
	cases := []struct {
		name, a, b string
		c          int
	}{
		{"identical", "a\nb\nc\n", "a\nb\nc\n", 3},
		{"crlf", "a\r\nb\r\n", "a\r\nB\r\n", 3},
		{"crlf-keep", "x\r\ny\n", "X\r\ny\n", 3},
		{"no-nl-both", "abc", "abd", 3},
		{"no-nl-old", "abc", "abc\n", 3},
		{"no-nl-new", "abc\n", "abc", 3},
		{"empty-a", "", "x\n", 3},
		{"empty-b", "a\n", "", 3},
		{"both-empty", "", "", 3},
		{"middle", "a\nb\nc\n", "a\nB\nc\n", 1},
		{"eol-only", "a\nb\n", "a\r\nb\r\n", 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := render(t, tc.a, tc.b, tc.c)
			got, err := patch.Apply([]byte(tc.a), d, patch.Options{Fuzz: 0})
			if err != nil {
				t.Fatalf("apply: %v\npatch:\n%s", err, d)
			}
			if string(got) != tc.b {
				t.Fatalf("roundtrip mismatch\n got=%q\nwant=%q\npatch:\n%s", got, tc.b, d)
			}
			p, _ := udiff.Parse(d, udiff.Limits{})
			rev, err := patch.ApplyParsed([]byte(tc.b), patch.Reverse(p), 0)
			if err != nil {
				t.Fatalf("reverse apply: %v", err)
			}
			if string(rev) != tc.a {
				t.Fatalf("reverse mismatch got=%q want=%q", rev, tc.a)
			}
		})
	}
}

func TestZeroHeaders(t *testing.T) {
	cases := []struct {
		a, b string
		want string
	}{
		{"x\n", "y\nx\n", "@@ -0,0 +1 @@\n"},
		{"a\nb\nc\n", "a\nb\nX\nc\n", "@@ -2,0 +3 @@\n"},
		{"a\n", "", "@@ -1 +0,0 @@\n"},
	}
	for _, tc := range cases {
		d := render(t, tc.a, tc.b, 0)
		if tc.a == "a\n" {
			d = render(t, tc.a, tc.b, 3)
		}
		if !strings.Contains(string(d), tc.want) {
			t.Fatalf("header %q missing in:\n%s", tc.want, d)
		}
	}
}

func TestMergeThreshold(t *testing.T) {
	// C=1: two edits separated by g unchanged lines.
	build := func(g int) int {
		a := "X\n" + strings.Repeat("m\n", g) + "Y\n"
		b := "P\n" + strings.Repeat("m\n", g) + "Q\n"
		p, _ := udiff.Build(lines.Split(a), lines.Split(b), 1, 0)
		return len(p.Hunks)
	}
	if got := build(2); got != 1 { // g == 2C -> merge
		t.Fatalf("g=2C must merge, got %d hunks", got)
	}
	if got := build(3); got != 2 { // g == 2C+1 -> split
		t.Fatalf("g=2C+1 must split, got %d hunks", got)
	}
}

func TestOnlyEOLChange(t *testing.T) {
	d := render(t, "a\n", "a", 3)
	if !strings.Contains(string(d), "-a\n") || !strings.Contains(string(d), `\ No newline`) {
		t.Fatalf("eol-only change must be non-empty:\n%s", d)
	}
	got, err := patch.Apply([]byte("a\n"), d, patch.Options{})
	if err != nil || string(got) != "a" {
		t.Fatalf("apply eol-only: got=%q err=%v", got, err)
	}
}

func TestOffset(t *testing.T) {
	// patch generated against a target missing 5 leading lines; nearest match wins.
	old := strings.Repeat("z\n", 5) + "keep\nOLD\nkeep\n"
	base := "keep\nOLD\nkeep\n"
	next := strings.Repeat("q\n", 2) + "keep\nOLD\nkeep\n" + strings.Repeat("t\n", 2)
	d := render(t, base, "keep\nNEW\nkeep\n", 3)
	got, err := patch.Apply([]byte(old), d, patch.Options{Fuzz: 10})
	if err != nil {
		t.Fatalf("offset apply: %v", err)
	}
	if !strings.Contains(string(got), "keep\nNEW\nkeep\n") {
		t.Fatalf("offset result=%q", got)
	}
	if _, err := patch.Apply([]byte(next), d, patch.Options{Fuzz: 0}); !(errors.Is(err, patch.ErrOutOfRange) || errors.Is(err, patch.ErrContext)) {
		t.Fatalf("Fuzz0 mismatch want out-of-range/context, got %v", err)
	}
}

func TestAtomicReject(t *testing.T) {
	a := "a\nb\nc\nd\ne\n"
	p, _ := udiff.Build(lines.Split(a), lines.Split("A\nb\nc\nd\nE\n"), 0, 0)
	// Corrupt the second hunk's context in target: build manually via two hunks.
	d := render(t, a, "A\nb\nc\nd\nE\n", 0)
	original := "a\nb\nc\nd\nX\n" // hunk 1 applies, hunk 2 cannot match
	target := []byte(original)
	got, err := patch.Apply(target, d, patch.Options{Fuzz: 0})
	if got != nil {
		t.Fatalf("failed patch must return nil, got %q", got)
	}
	var ae *patch.ApplyError
	if !errors.As(err, &ae) || ae.Hunk != 2 || !errors.Is(err, patch.ErrContext) {
		t.Fatalf("want hunk 2 context error, got %v", err)
	}
	if string(target) != original {
		t.Fatalf("target mutated: %q", target)
	}
	_ = p
}

func TestStrictParse(t *testing.T) {
	good := render(t, "a\nb\n", "a\nB\n", 3)
	cases := []struct {
		name string
		text string
		want error
	}{
		{"missing-line", truncateLine(good, 5), patch.ErrFormat},
		{"bad-prefix", strings.Replace(string(good), "-b\n", "xb\n", 1), patch.ErrFormat},
		{"extra-line", string(good) + "garbage\n", patch.ErrFormat},
		{"count-mismatch", strings.Replace(string(good), "@@ -1,2 +1,2 @@", "@@ -1,1 +1,2 @@", 1), patch.ErrFormat},
		{"empty-ctx-ok", "--- \n+++ \n@@ -0,0 +1 @@\n+x\n", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := udiff.Parse([]byte(tc.text), udiff.Limits{})
			if tc.want == nil {
				if err != nil {
					t.Fatalf("want ok, got %v", err)
				}
				return
			}
			var fe *udiff.FormatError
			if !errors.As(err, &fe) || !errors.Is(err, tc.want) {
				t.Fatalf("want FormatError, got %v (patch=%+v)", err, p)
			}
			if fe.Line < 1 {
				t.Fatalf("format error must carry line number, got %d", fe.Line)
			}
		})
	}
}

func truncateLine(d []byte, n int) string {
	ls := strings.Split(string(d), "\n")
	if n >= len(ls) {
		n = len(ls) - 1
	}
	return strings.Join(ls[:n], "\n")
}

func TestLimits(t *testing.T) {
	d := render(t, "a\n", "b\n", 0)
	if _, err := patch.Apply([]byte("a\n"), d, patch.Options{MaxBytes: 4}); !errors.Is(err, patch.ErrFormat) {
		t.Fatalf("byte limit: got %v", err)
	}
	if _, err := patch.Apply([]byte("a\n"), d, patch.Options{MaxHunks: 0}); err != nil {
		t.Fatalf("MaxHunks=0 means unlimited, got %v", err)
	}
	d2 := render(t, "a\nz\n", "b\nz\n", 0)
	_ = d2
}

func TestErrorClassesDistinct(t *testing.T) {
	classes := []error{patch.ErrFormat, patch.ErrContext, patch.ErrOutOfRange, edit.ErrTooDifferent}
	for i := range classes {
		for j := i + 1; j < len(classes); j++ {
			if errors.Is(classes[i], classes[j]) {
				t.Fatalf("error classes %d,%d must be distinct", i, j)
			}
		}
	}
	d := render(t, "a\n", "b\n", 0)
	if _, err := patch.Apply([]byte("z\n"), d, patch.Options{Fuzz: 0}); !errors.Is(err, patch.ErrContext) {
		t.Fatalf("want context, got %v", err)
	}
	if _, err := patch.Apply([]byte(strings.Repeat("x\n", 50)+"a\n"), d, patch.Options{Fuzz: 2}); !errors.Is(err, patch.ErrOutOfRange) {
		t.Fatalf("want out-of-range, got %v", err)
	}
	if _, err := udiff.Diff([]byte("a\n"), []byte("b\n"), 3, 0); err != nil {
		t.Fatalf("distance 1 within unlimited: %v", err)
	}
	if _, err := udiff.Diff([]byte("a\nb\n"), []byte("c\nd\n"), 3, 1); !errors.Is(err, edit.ErrTooDifferent) {
		t.Fatalf("want too different, got %v", err)
	}
	_ = bytes.Equal
	_ = hunk.Build
}
