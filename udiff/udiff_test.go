package udiff_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"ontology/patch"
	"ontology/udiff"
)

func render(a, b string, ctx int) []byte {
	f, err := udiff.Diff("a", "b", []byte(a), []byte(b), ctx, -1)
	if err != nil {
		panic(err)
	}
	return udiff.Render(f)
}

func countHunks(p []byte) int {
	n := 0
	for _, l := range strings.Split(string(p), "\n") {
		if strings.HasPrefix(l, "@@ ") {
			n++
		}
	}
	return n
}

func gap(g int) (string, string) {
	var o, n strings.Builder
	o.WriteString("H\n")
	n.WriteString("h\n")
	for i := 0; i < g; i++ {
		fmt.Fprintf(&o, "c%d\n", i)
		fmt.Fprintf(&n, "c%d\n", i)
	}
	o.WriteString("T\n")
	n.WriteString("t\n")
	return o.String(), n.String()
}

func TestHeadersAndMerge(t *testing.T) {
	hdr := []struct {
		a, b string
		ctx  int
		want string
	}{
		{"x\n", "y\nx\n", 0, "@@ -0,0 +1 @@"},
		{"a\nb\nc\n", "a\nb\nX\nc\n", 0, "@@ -2,0 +3 @@"},
		{"a\n", "", 3, "@@ -1 +0,0 @@"},
	}
	for _, c := range hdr {
		if got := render(c.a, c.b, c.ctx); !strings.Contains(string(got), c.want) {
			t.Fatalf("want header %q in:\n%s", c.want, got)
		}
	}
	merge := []struct {
		ctx, g, hunks int
	}{{3, 6, 1}, {3, 7, 2}, {0, 0, 1}, {0, 1, 2}}
	for _, c := range merge {
		o, n := gap(c.g)
		if got := countHunks(render(o, n, c.ctx)); got != c.hunks {
			t.Fatalf("ctx=%d g=%d want %d hunks, got %d", c.ctx, c.g, c.hunks, got)
		}
	}
}

func TestNoNewlineMarker(t *testing.T) {
	cases := []struct {
		a, b string
		mark int
	}{
		{"a\nb", "a\nc", 2},
		{"a\nb", "a\nb\n", 1},
		{"a\nb\r\n", "a\nc\r\n", 2},
	}
	for _, c := range cases {
		p := render(c.a, c.b, 3)
		if got := strings.Count(string(p), "No newline at end of file"); got != c.mark {
			t.Fatalf("%q->%q want %d markers, got %d:\n%s", c.a, c.b, c.mark, got, p)
		}
		if len(p) <= len("--- a\n+++ b\n") {
			t.Fatalf("expected a non-empty patch")
		}
		f, err := udiff.Parse(p, udiff.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		out, err := patch.Apply([]byte(c.a), f, 0)
		if err != nil || string(out) != c.b {
			t.Fatalf("apply: %v got=%q", err, out)
		}
	}
}

func TestStrictParse(t *testing.T) {
	head := "--- a\n+++ b\n"
	cases := []struct {
		name string
		body string
		bad  bool
	}{
		{"normal", "@@ -1 +1 @@\n x\n", false},
		{"empty context row", "@@ -1 +1 @@\n \n", false},
		{"over declared", "@@ -2,2 +1 @@\n x\n", true},
		{"extra row", "@@ -1 +1 @@\n x\n y\n", true},
		{"bad prefix", "@@ -1 +1 @@\n?x\n", true},
		{"stray marker", "@@ -1 +1 @@\nx\n" + "\\ No newline at end of file\n", true},
		{"truncated row", "@@ -1 +1 @@\n-x", true},
		{"bad header", "@@ @ @@\n", true},
	}
	for _, c := range cases {
		_, err := udiff.Parse([]byte(head+c.body), udiff.Limits{})
		if c.bad && !errors.Is(err, udiff.ErrFormat) {
			t.Fatalf("%s: want ErrFormat, got %v", c.name, err)
		}
		var fe *udiff.FormatError
		if c.bad && (!errors.As(err, &fe) || fe.Line < 1) {
			t.Fatalf("%s: error must carry patch line number", c.name)
		}
		if !c.bad && err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
	}
}

func TestDeterministicRender(t *testing.T) {
	ref := render("x\na\nb\nc\nd\ne\nf\nY\n", "x\nA\nb\nc\nd\ne\nf\nZ\n", 3)
	for i := 0; i < 100; i++ {
		if got := render("x\na\nb\nc\nd\ne\nf\nY\n", "x\nA\nb\nc\nd\ne\nf\nZ\n", 3); !equalBytes(got, ref) {
			t.Fatalf("iteration %d differs", i)
		}
	}
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestLimits(t *testing.T) {
	o, n := gap(7)
	p := render(o, n, 3)
	cases := []struct {
		name string
		lim  udiff.Limits
	}{
		{"bytes", udiff.Limits{MaxBytes: 10}},
		{"hunks", udiff.Limits{MaxHunks: 1}},
	}
	for _, c := range cases {
		if _, err := udiff.Parse(p, c.lim); !errors.Is(err, udiff.ErrLimit) {
			t.Fatalf("%s: want ErrLimit, got %v", c.name, err)
		}
	}
}

func TestEveryTruncation(t *testing.T) {
	o, n := gap(7)
	p := render(o, n, 3)
	for i := 0; i <= len(p); i++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("truncation %d panicked: %v", i, r)
				}
			}()
			f, err := udiff.Parse(p[:i], udiff.Limits{})
			if err != nil {
				return
			}
			_, _ = patch.Apply([]byte(o), f, 3)
		}()
	}
}
