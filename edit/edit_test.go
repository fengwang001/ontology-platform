package edit

import (
	"bytes"
	"errors"
	"math/rand"
	"strings"
	"testing"

	"ontology/lines"
)

func ls(s string) []lines.Line { return lines.Split([]byte(s)) }

// lcsDist is the O(NM) reference distance.
func lcsDist(a, b []lines.Line) int {
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			if bytes.Equal(a[i-1].Raw, b[j-1].Raw) {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}
	return len(a) + len(b) - 2*dp[len(a)][len(b)]
}

func TestShortestAndDeterministic(t *testing.T) {
	cases := []struct{ a, b string }{
		{"", ""}, {"a\n", "a\n"}, {"a\n", ""}, {"", "b\n"},
		{"a\nb\n", "b\na\n"}, {"abc\n", "abd\n"},
		{"x\ny\nz\n", "x\nY\nz\nw\n"},
		{"a\r\nb\r\n", "a\r\nc\r\nb\r\n"},
		{"noeol", "noeol2"},
	}
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 200; i++ {
		cases = append(cases, struct{ a, b string }{
			rndText(r, 8, 4), rndText(r, 8, 4)})
	}
	for _, c := range cases {
		a, b := ls(c.a), ls(c.b)
		ops, st, err := Diff(a, b, -1)
		if err != nil {
			t.Fatalf("Diff(%q,%q): %v", c.a, c.b, err)
		}
		if st.Distance != lcsDist(a, b) {
			t.Fatalf("distance %d != LCS %d for %q->%q", st.Distance, lcsDist(a, b), c.a, c.b)
		}
		var first []byte
		for rep := 0; rep < 100; rep++ {
			op2, _, err := Diff(a, b, -1)
			if err != nil {
				t.Fatal(err)
			}
			got := scriptBytes(op2)
			if rep == 0 {
				first = got
			} else if !bytes.Equal(first, got) {
				t.Fatalf("nondeterministic script for %q->%q", c.a, c.b)
			}
			ops = op2
		}
		_ = ops
	}
}

func TestTiePrefersDelete(t *testing.T) {
	ops, _, err := Diff(ls("a\nb\n"), ls("b\na\n"), -1)
	if err != nil {
		t.Fatal(err)
	}
	want := []Kind{Delete, Equal, Insert}
	if len(ops) != len(want) {
		t.Fatalf("got %d ops: %v", len(ops), ops)
	}
	for i, k := range want {
		if ops[i].Kind != k {
			t.Fatalf("op %d = %v, want %v (delete-first ordering)", i, ops[i].Kind, k)
		}
	}
}

func TestCounterBounds(t *testing.T) {
	cases := []struct {
		n, ratio int
	}{{1000, 1}, {100000, 1}}
	var prev int64
	for ci, c := range cases {
		a := make([]lines.Line, c.n)
		for i := range a {
			a[i] = lines.Make([]byte("line"+itoa(i)), []byte("\n"))
		}
		b := append([]lines.Line(nil), a...)
		b[100] = lines.Make([]byte("CHG1"), []byte("\n"))
		b[4000%len(b)] = lines.Make([]byte("CHG2"), []byte("\n"))
		b[c.n-50] = lines.Make([]byte("CHG3"), []byte("\n"))
		_, st, err := Diff(a, b, -1)
		if err != nil {
			t.Fatal(err)
		}
		bound := int64(4) * int64(len(a)+len(b)) * int64(st.Distance+1)
		if st.Steps > bound {
			t.Fatalf("n=%d steps=%d > bound=%d", c.n, st.Steps, bound)
		}
		if ci == 1 && st.Steps > 150*prev {
			t.Fatalf("steps ratio %d/%d = %d > 150", st.Steps, prev, st.Steps/prev)
		}
		prev = st.Steps
	}
}

func TestMaxDistance(t *testing.T) {
	cases := []struct {
		a, b string
		max  int
		ok   bool
	}{
		{"a\nb\n", "a\nb\n", 0, true},
		{"a\n", "b\n", 0, false},
		{"a\nb\n", "b\na\n", 1, false},
		{"a\nb\n", "b\na\n", 2, true},
	}
	for _, c := range cases {
		_, _, err := Diff(ls(c.a), ls(c.b), c.max)
		if c.ok && err != nil {
			t.Fatalf("a=%q b=%q max=%d: unexpected %v", c.a, c.b, c.max, err)
		}
		if !c.ok && !errors.Is(err, ErrTooLarge) {
			t.Fatalf("a=%q b=%q max=%d: want ErrTooLarge, got %v", c.a, c.b, c.max, err)
		}
	}
}

func rndText(r *rand.Rand, linesN, vocab int) string {
	var sb strings.Builder
	for i := 0; i < linesN; i++ {
		sb.WriteString("w")
		sb.WriteString(itoa(r.Intn(vocab)))
		sb.WriteByte('\n')
	}
	return sb.String()
}

func scriptBytes(ops []Op) []byte {
	var sb strings.Builder
	for _, op := range ops {
		switch op.Kind {
		case Equal:
			sb.WriteByte('=')
			sb.Write(op.A.Raw)
		case Delete:
			sb.WriteByte('-')
			sb.Write(op.A.Raw)
		case Insert:
			sb.WriteByte('+')
			sb.Write(op.B.Raw)
		}
	}
	return []byte(sb.String())
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}
