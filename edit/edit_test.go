package edit

import (
	"errors"
	"math/rand"
	"strings"
	"testing"

	"ontology/lines"
)

func lc(s string) []lines.Line { return lines.Split(s) }

func TestShortestAgainstDP(t *testing.T) {
	cases := []struct{ a, b string }{
		{"a\nb\n", "b\na\n"}, {"abc\n", "abc\n"}, {"", "x\n"},
		{"a\nb\nc\n", "a\nX\nc\n"}, {"x\n", ""},
		{"a\nb\n", "a\nb\nc\n"}, {"r1\r\nr2\r\n", "r1\r\nR2\r\n"},
	}
	rng := rand.New(rand.NewSource(1))
	words := []string{"a\n", "b\n", "c\n", "a\r\n", "z\n", "\n"}
	for i := 0; i < 300; i++ {
		na, nb := rng.Intn(8), rng.Intn(8)
		sa, sb := "", ""
		for j := 0; j < na; j++ {
			sa += words[rng.Intn(len(words))]
		}
		for j := 0; j < nb; j++ {
			sb += words[rng.Intn(len(words))]
		}
		cases = append(cases, struct{ a, b string }{sa, sb})
	}
	for _, tc := range cases {
		scr, err := Diff(lc(tc.a), lc(tc.b), Options{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := lcsDistance(lc(tc.a), lc(tc.b))
		if scr.Distance != want {
			t.Fatalf("distance(%q,%q)=%d want %d", tc.a, tc.b, scr.Distance, want)
		}
	}
}

func TestDeterministic(t *testing.T) {
	a, b := lc("a\nb\nc\n"), lc("c\na\nb\n")
	var first Script
	for i := 0; i < 100; i++ {
		s, _ := Diff(a, b, Options{})
		if i == 0 {
			first = s
			continue
		}
		if len(s.Ops) != len(first.Ops) {
			t.Fatal("length differs")
		}
		for j := range s.Ops {
			if s.Ops[j] != first.Ops[j] {
				t.Fatalf("op %d differs on run %d", j, i)
			}
		}
	}
}

func TestDeleteFirstTie(t *testing.T) {
	s, _ := Diff(lc("a\nb\n"), lc("b\na\n"), Options{})
	got := []Kind{}
	for _, op := range s.Ops {
		if op.Kind != Equal {
			got = append(got, op.Kind)
		}
	}
	if len(got) != 2 || got[0] != Delete || got[1] != Insert {
		t.Fatalf("a b -> b a wants -a,+a (delete first), got %v", got)
	}
	if s.Ops[0].Kind != Delete || s.Ops[2].Kind != Insert {
		t.Fatalf("delete must precede insert in body: %v", s.Ops)
	}
}

func TestMaxDistance(t *testing.T) {
	if _, err := Diff(lc("a\nb\nc\n"), lc("x\ny\nz\n"), Options{MaxDistance: 2}); !errors.Is(err, ErrTooDifferent) {
		t.Fatalf("want ErrTooDifferent, got %v", err)
	}
	if _, err := Diff(lc("a\nb\nc\n"), lc("a\nB\nc\n"), Options{MaxDistance: 2}); err != nil {
		t.Fatalf("within cap must succeed, got %v", err)
	}
}

func TestStepCounterLinear(t *testing.T) {
	makeInput := func(n int) ([]lines.Line, []lines.Line) {
		a := make([]string, n)
		for i := range a {
			a[i] = "line" + itoa(i) + "\n"
		}
		b := append([]string(nil), a...)
		b[n/4] = "CHANGED1\n"
		b[n/2] = "CHANGED2\n"
		b[3*n/4] = "CHANGED3\n"
		return lc(join(a)), lc(join(b))
	}
	var ratio, smallSteps int
	for gi, n := range []int{1000, 100000} {
		a, b := makeInput(n)
		s, err := Diff(a, b, Options{})
		if err != nil {
			t.Fatal(err)
		}
		bound := 4 * (n + n) * (s.Distance + 1)
		if s.Steps > bound {
			t.Fatalf("n=%d steps=%d > bound=%d", n, s.Steps, bound)
		}
		t.Logf("MEASURED n=%d steps=%d bound=%d", n, s.Steps, bound)
		if gi == 0 {
			smallSteps = s.Steps
		} else {
			ratio = s.Steps
		}
	}
	if ratio > 150*smallSteps {
		t.Fatalf("large/small steps %d/%d exceeds 150x", ratio, smallSteps)
	}
}

func lcsDistance(a, b []lines.Line) int {
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	return len(a) + len(b) - 2*dp[0][0]
}

func join(ls []string) string {
	var sb strings.Builder
	for _, l := range ls {
		sb.WriteString(l)
	}
	return sb.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
