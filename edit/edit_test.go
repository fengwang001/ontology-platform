package edit

import (
	"errors"
	"math/rand"
	"testing"

	"ontology/lines"
)

func mkLines(ss ...string) []lines.Line {
	out := make([]lines.Line, len(ss))
	for i, s := range ss {
		nl := []byte{'\n'}
		txt := []byte(s)
		if len(s) > 0 && s[0] == 'z' { // "z..." marks a no-newline final line
			txt, nl = txt[1:], nil
		}
		out[i] = lines.Line{Text: txt, NL: nl}
	}
	return out
}

func replay(s []Step) (del, ins, eq []string) {
	for _, st := range s {
		switch st.Op {
		case Delete:
			del = append(del, string(st.A.Text))
		case Insert:
			ins = append(ins, string(st.B.Text))
		case Equal:
			eq = append(eq, string(st.A.Text))
		}
	}
	return
}

func lcsDP(a, b []lines.Line) int {
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			if eq(a[i-1], b[j-1]) {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}
	return dp[len(a)][len(b)]
}

func TestShortest(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for iter := 0; iter < 300; iter++ {
		a := randLines(r, 1+r.Intn(14), 4)
		b := randLines(r, 1+r.Intn(14), 4)
		s, err := Diff(a, b, Options{})
		if err != nil {
			t.Fatal(err)
		}
		want := len(a) + len(b) - 2*lcsDP(a, b)
		if got := Distance(s); got != want {
			t.Fatalf("iter %d: distance %d want %d", iter, got, want)
		}
	}
}

func TestTieAndDeterminism(t *testing.T) {
	cases := []struct {
		a, b     []string
		wantDel  []string
		wantIns  []string
	}{
		{[]string{"a", "b"}, []string{"b", "a"}, []string{"a"}, []string{"a"}},
	}
	for _, c := range cases {
		var first []Step
		for i := 0; i < 100; i++ {
			s, err := Diff(mkLines(c.a...), mkLines(c.b...), Options{})
			if err != nil {
				t.Fatal(err)
			}
			if i == 0 {
				first = s
				del, ins, _ := replay(s)
				if !eqStr(del, c.wantDel) || !eqStr(ins, c.wantIns) {
					t.Fatalf("tie order del=%v ins=%v", del, ins)
				}
				continue
			}
			if scriptSig(s) != scriptSig(first) {
				t.Fatal("non-deterministic script")
			}
		}
	}
}

func TestMaxDistance(t *testing.T) {
	_, err := Diff(mkLines("a"), mkLines("b"), Options{MaxDistance: 2})
	if err != nil {
		t.Fatalf("distance 2 <= cap 2 should pass, got %v", err)
	}
	if _, err := Diff(mkLines("a"), mkLines("b"), Options{MaxDistance: 1}); !errors.Is(err, ErrTooDifferent) {
		t.Fatalf("got %v want ErrTooDifferent", err)
	}
}

func TestSnakeCounter(t *testing.T) {
	var c1k, c100k int64
	for _, n := range []int{1000, 100000} {
		a := bigLines(n)
		b := bigLines(n)
		b[n/4].Text[0] = 'Q'
		b[n/2].Text[0] = 'Q'
		b[3*n/4].Text[0] = 'Q'
		if _, err := Diff(a, b, Options{}); err != nil {
			t.Fatal(err)
		}
		c := SnakeSteps()
		d := 6
		if c > int64(4*(n+n)*(d+1)) {
			t.Fatalf("n=%d counter %d exceeds bound", n, c)
		}
		if n == 1000 {
			c1k = c
		} else {
			c100k = c
		}
	}
	if c100k > c1k*150 {
		t.Fatalf("counter not near-linear: %d vs %d", c100k, c1k)
	}
}

func bigLines(n int) []lines.Line {
	out := make([]lines.Line, n)
	for i := range out {
		out[i] = lines.Line{Text: []byte("x"), NL: []byte{'\n'}}
	}
	return out
}

func randLines(r *rand.Rand, n, vocab int) []lines.Line {
	return mkLines(randStrs(r, n, vocab)...)
}

func randStrs(r *rand.Rand, n, vocab int) []string {
	ss := make([]string, n)
	for i := range ss {
		ss[i] = string(rune('a' + r.Intn(vocab)))
	}
	return ss
}

func eqStr(a, b []string) bool {
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

func scriptSig(s []Step) string {
	out := make([]byte, len(s))
	for i, st := range s {
		out[i] = byte('0' + st.Op)
	}
	return string(out)
}
