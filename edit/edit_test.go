package edit

import (
	"errors"
	"testing"

	"ontology/lines"
)

func ls(s ...string) []lines.Line {
	out := make([]lines.Line, len(s))
	for i, t := range s {
		out[i] = lines.Line{Text: []byte(t), EOL: []byte("\n")}
	}
	return out
}

// lcsDist is the O(NM) reference edit distance.
func lcsDist(a, b []lines.Line) int {
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
	return n + m - 2*dp[n][m]
}

func dist(ops []Op) int {
	d := 0
	for _, o := range ops {
		if o.Kind != Equal {
			d++
		}
	}
	return d
}

func TestShortestAndDeterministic(t *testing.T) {
	cases := []struct{ a, b []string }{
		{nil, nil},
		{[]string{"a", "b"}, []string{"a", "b"}},
		{[]string{"a", "b"}, []string{"b", "a"}},
		{[]string{"a", "b", "c"}, []string{"a", "b", "X", "c"}},
		{[]string{"x"}, []string{"y", "x"}},
		{[]string{"a"}, nil},
		{[]string{"k1", "k2", "k3", "k4"}, []string{"k2", "k5", "k4"}},
	}
	for _, tc := range cases {
		a, b := ls(tc.a...), ls(tc.b...)
		ops, err := Diff(a, b, Options{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got, want := dist(ops), lcsDist(a, b); got != want {
			t.Errorf("distance %v->%v = %d, want %d", tc.a, tc.b, got, want)
		}
		first := scriptString(ops)
		for i := 0; i < 20; i++ {
			again, _ := Diff(a, b, Options{})
			if scriptString(again) != first {
				t.Fatalf("non-deterministic output for %v->%v", tc.a, tc.b)
			}
		}
	}
}

func TestDeleteFirstTie(t *testing.T) {
	ops, _ := Diff(ls("a", "b"), ls("b", "a"), Options{})
	want := []Kind{Delete, Equal, Insert}
	if len(ops) != len(want) {
		t.Fatalf("ops=%v", ops)
	}
	for i := range want {
		if ops[i].Kind != want[i] {
			t.Fatalf("at %d got %v want %v (full=%v)", i, ops[i].Kind, want[i], ops)
		}
	}
}

func TestMaxDistance(t *testing.T) {
	var c Counter
	_, err := Diff(ls("a", "b"), ls("c", "d"), Options{MaxDistance: 1, Counter: &c})
	if !errors.Is(err, ErrTooDifferent) {
		t.Fatalf("want ErrTooDifferent, got %v", err)
	}
	if c.Steps <= 0 {
		t.Fatalf("counter not recorded: %d", c.Steps)
	}
}

func scriptString(ops []Op) string {
	b := make([]byte, 0, len(ops))
	for _, o := range ops {
		switch o.Kind {
		case Equal:
			b = append(b, ' ')
		case Delete:
			b = append(b, '-')
		case Insert:
			b = append(b, '+')
		}
		b = append(b, o.L.Bytes()...)
	}
	return string(b)
}
