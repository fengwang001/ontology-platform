package edit

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// minEdits 用 O(NM) 动态规划求最小编辑距离（仅删除+插入）。
func minEdits(a, b []string) int {
	d := make([][]int, len(a)+1)
	for i := range d {
		d[i] = make([]int, len(b)+1)
	}
	for i := 0; i <= len(a); i++ {
		d[i][0] = i
	}
	for j := 1; j <= len(b); j++ {
		d[0][j] = j
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			d[i][j] = 1 + min(d[i-1][j], d[i][j-1])
			if a[i-1] == b[j-1] {
				d[i][j] = min(d[i][j], d[i-1][j-1])
			}
		}
	}
	return d[len(a)][len(b)]
}

func toLines(s []string) [][]byte {
	out := make([][]byte, len(s))
	for i, v := range s {
		out[i] = []byte(v)
	}
	return out
}

// replay 把脚本应用到 a 上并统计编辑距离。
func replay(a [][]byte, s Script) (out [][]byte, dist int) {
	ai := 0
	for _, op := range s {
		switch op.Kind {
		case ' ':
			out, ai = append(out, a[ai]), ai+1
		case '-':
			ai, dist = ai+1, dist+1
		case '+':
			out, dist = append(out, op.Text), dist+1
		}
	}
	return out, dist
}

func equal(x, y [][]byte) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if string(x[i]) != string(y[i]) {
			return false
		}
	}
	return true
}

func TestShortest(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	var cases [][2][]string
	for i := 0; i < 400; i++ {
		var a, b []string
		for j := 0; j < rng.Intn(9); j++ {
			a = append(a, fmt.Sprint(rng.Intn(4)))
		}
		for j := 0; j < rng.Intn(9); j++ {
			b = append(b, fmt.Sprint(rng.Intn(4)))
		}
		cases = append(cases, [2][]string{a, b})
	}
	for _, tc := range cases {
		la, lb := toLines(tc[0]), toLines(tc[1])
		s, err := Diff(la, lb, -1)
		if err != nil {
			t.Fatalf("%v -> %v: %v", tc[0], tc[1], err)
		}
		out, dist := replay(la, s)
		if want := minEdits(tc[0], tc[1]); dist != want {
			t.Fatalf("%v -> %v: dist %d, want %d", tc[0], tc[1], dist, want)
		}
		if !equal(out, lb) {
			t.Fatalf("%v -> %v: script replays to %v", tc[0], tc[1], out)
		}
	}
}

func TestCounter(t *testing.T) {
	prev := 0
	for _, n := range []int{1000, 100000} {
		a := make([][]byte, n)
		for i := range a {
			a[i] = []byte(fmt.Sprintf("line%07d\n", i))
		}
		b := append([][]byte(nil), a...)
		for _, i := range []int{n / 4, n / 2, 3 * n / 4} {
			b[i] = []byte("changed\n")
		}
		s, err := Diff(a, b, -1)
		if err != nil {
			t.Fatal(err)
		}
		_, dist := replay(a, s)
		steps := Steps()
		if bound := 4 * (n + n) * (dist + 1); steps > bound {
			t.Fatalf("n=%d: steps %d > bound %d", n, steps, bound)
		}
		if prev > 0 && steps > 150*prev {
			t.Fatalf("n=%d: steps %d > 150 * %d", n, steps, prev)
		}
		t.Logf("n=%d dist=%d steps=%d", n, dist, steps)
		prev = steps
	}
}

func TestMaxDist(t *testing.T) {
	a := toLines([]string{"a", "b", "c", "d", "e"})
	b := toLines([]string{"1", "2", "3", "4", "5"})
	if _, err := Diff(a, b, 3); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
	if steps, bound := Steps(), 4*(len(a)+len(b))*(3+1); steps > bound {
		t.Fatalf("steps %d > bound %d", steps, bound)
	}
	if s, err := Diff(a, b, -1); err != nil || len(s) == 0 {
		t.Fatalf("unlimited: %v", err)
	}
}
