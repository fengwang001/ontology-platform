package edit

import (
	"fmt"
	"testing"

	"ontology/lines"
)

func lns(s string) []lines.Line { return lines.Split([]byte(s)) }

// lcs 用 O(NM) 动态规划求 LCS 长度。
func lcs(a, b []lines.Line) int {
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if string(a[i-1].Bytes()) == string(b[j-1].Bytes()) {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}
	return dp[n][m]
}

func TestShortest(t *testing.T) {
	pairs := []string{"", "a", "a\n", "abc\n", "a\nb\n", "kitten\nsitting\n",
		"x\ny\nz\n", "a\nb\nc\nd\n", "aaa\naaa\n"}
	for i, a := range pairs {
		for j, b := range pairs {
			la, lb := lns(a), lns(b)
			ops, err := Diff(la, lb, -1)
			if err != nil {
				t.Fatalf("(%d,%d) %v", i, j, err)
			}
			d := 0
			for _, op := range ops {
				d++
				if (op.Kind == Del) == (op.Kind == Ins) {
					t.Fatalf("bad kind")
				}
			}
			want := len(la) + len(lb) - 2*lcs(la, lb)
			if d != want {
				t.Fatalf("(%d,%d) dist=%d want %d", i, j, d, want)
			}
		}
	}
}

func TestTieDeleteFirst(t *testing.T) {
	ops, err := Diff(lns("a\nb\n"), lns("b\na\n"), -1)
	if err != nil {
		t.Fatal(err)
	}
	wantKinds := []int{Del, Ins}
	if len(ops) != 2 || ops[0].Kind != wantKinds[0] || ops[1].Kind != wantKinds[1] {
		t.Fatalf("want delete-first, got %+v", ops)
	}
}

func TestDeterminism(t *testing.T) {
	base := fmt.Sprintf("%s", rep("line\n", 50))
	a, b := lns(base), lns(rep("line\n", 48)+"changed\n"+"line\n")
	var first []Op
	for i := 0; i < 100; i++ {
		ops, err := Diff(a, b, -1)
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = ops
		} else if len(ops) != len(first) {
			t.Fatal("length differs")
		}
	}
}

func TestTooLarge(t *testing.T) {
	if _, err := Diff(lns("a\nb\n"), lns("c\nd\n"), 1); err != ErrTooLarge {
		t.Fatalf("got %v", err)
	}
	ops, err := Diff(lns("a\nb\n"), lns("c\nd\n"), 4)
	if err != nil || len(ops) != 4 {
		t.Fatalf("limit not exceeded: %v %d", err, len(ops))
	}
}

func rep(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}

func changed(n int) ([]lines.Line, []lines.Line) {
	a := lns(rep("row\n", n))
	b := make([]lines.Line, len(a))
	copy(b, a)
	for _, i := range []int{n / 4, n / 2, 3 * n / 4} {
		b[i] = lns("EDIT\n")[0]
	}
	return a, b
}

func TestCounterScale(t *testing.T) {
	var s1k, s100k int64
	for _, tc := range []struct {
		n     int
		store *int64
	}{{1000, &s1k}, {100000, &s100k}} {
		a, b := changed(tc.n)
		ops, err := Diff(a, b, -1)
		if err != nil {
			t.Fatal(err)
		}
		if len(ops) != 6 {
			t.Fatalf("dist=%d want 6", len(ops))
		}
		s := Steps()
		*tc.store = s
		bound := 4 * int64(tc.n+tc.n) * int64(len(ops)+1)
		if s > bound {
			t.Fatalf("n=%d steps=%d > bound %d", tc.n, s, bound)
		}
	}
	if s100k > s1k*150 {
		t.Fatalf("ratio %d/%d = %d > 150", s100k, s1k, s100k/s1k)
	}
	t.Logf("steps 1k=%d 100k=%d ratio=%d", s1k, s100k, s100k/s1k)
}
