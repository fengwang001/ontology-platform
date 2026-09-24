package edit_test

import (
	"errors"
	"math/rand"
	"strings"
	"testing"

	"ontology/edit"
	"ontology/lines"
)

func split(s string) []lines.Line { return lines.Split([]byte(s)) }

func TestLinesRoundTrip(t *testing.T) {
	cases := []string{"", "a", "a\n", "a\nb", "a\r\nb\r\n", "a\n\rb\n", "\n\r\nx"}
	for _, in := range cases {
		got := string(lines.Join(lines.Split([]byte(in))))
		if got != in {
			t.Fatalf("round trip %q -> %q", in, got)
		}
	}
}

func lcsLen(a, b []string) int {
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] > dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	return dp[0][0]
}

func reconstruct(a, b []lines.Line, segs []edit.Segment) (string, string) {
	var ao, bo []byte
	for _, s := range segs {
		for _, l := range s.Lines {
			switch s.Op {
			case edit.Equal:
				ao, bo = append(ao, l.Full()...), append(bo, l.Full()...)
			case edit.Delete:
				ao = append(ao, l.Full()...)
			case edit.Insert:
				bo = append(bo, l.Full()...)
			}
		}
	}
	return string(ao), string(bo)
}

func TestShortestRandom(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for iter := 0; iter < 300; iter++ {
		alpha := strings.Split("a b c", " ")
		gen := func() ([]string, []lines.Line) {
			n := r.Intn(14)
			var ls []lines.Line
			var ss []string
			for k := 0; k < n; k++ {
				s := alpha[r.Intn(len(alpha))] + "\n"
				ss = append(ss, s)
				ls = append(ls, lines.Split([]byte(s))...)
			}
			return ss, ls
		}
		_, a := gen()
		bs, b := gen()
		as := make([]string, len(a))
		for i, l := range a {
			as[i] = string(l.Full())
		}
		d := edit.Differ{}
		segs, err := d.Diff(a, b)
		if err != nil {
			t.Fatal(err)
		}
		got := edit.Distance(segs)
		want := len(a) + len(b) - 2*lcsLen(as, bs)
		if got != want {
			t.Fatalf("iter %d distance %d want %d", iter, got, want)
		}
		ra, rb := reconstruct(a, b, segs)
		if ra != string(lines.Join(a)) || rb != string(lines.Join(b)) {
			t.Fatalf("iter %d reconstruction mismatch", iter)
		}
	}
}

func TestDeterminism(t *testing.T) {
	a, b := split("a\nb\n"), split("b\na\n")
	var first []byte
	for i := 0; i < 100; i++ {
		d := edit.Differ{}
		segs, err := d.Diff(a, b)
		if err != nil {
			t.Fatal(err)
		}
		var cur []byte
		for _, s := range segs {
			cur = append(cur, byte('0'+s.Op))
			cur = append(cur, byte(len(s.Lines)+'0'))
		}
		if i == 0 {
			first = cur
		} else if string(cur) != string(first) {
			t.Fatal("nondeterministic script")
		}
	}
}

func TestMaxEdits(t *testing.T) {
	d := edit.Differ{MaxEdits: 1}
	if _, err := d.Diff(split("a\nb\nc\n"), split("x\ny\nz\n")); !errors.Is(err, edit.ErrTooDifferent) {
		t.Fatalf("got %v", err)
	}
	if d.Steps > 4*(3+3)*(1+1) {
		t.Fatalf("steps %d exceed bounded budget", d.Steps)
	}
}

func mutateBase(n int) []lines.Line {
	var ls []lines.Line
	for i := 0; i < n; i++ {
		ls = append(ls, lines.Split([]byte("line"+itoa(i)+"\n"))...)
	}
	return ls
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestStepCounter(t *testing.T) {
	var counts [2]int64
	for ti, n := range []int{1000, 100000} {
		a := mutateBase(n)
		b := make([]lines.Line, len(a))
		copy(b, a)
		for _, idx := range []int{n / 4, n / 2, 3 * n / 4} {
			b[idx] = lines.Split([]byte("CHANGED\n"))[0]
		}
		d := edit.Differ{}
		if _, err := d.Diff(a, b); err != nil {
			t.Fatal(err)
		}
		bound := int64(4) * int64(n+len(b)) * int64(6+1)
		if d.Steps > bound {
			t.Fatalf("n=%d steps %d > %d", n, d.Steps, bound)
		}
		counts[ti] = d.Steps
	}
	if counts[1] > counts[0]*150 {
		t.Fatalf("steps scale %d vs %d", counts[0], counts[1])
	}
}
