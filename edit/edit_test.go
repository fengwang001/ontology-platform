package edit

import (
	"bytes"
	"errors"
	"math/rand"
	"reflect"
	"testing"

	"ontology/lines"
)

func TestSplitJoin(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", []string{}},
		{"a", []string{"a"}},
		{"a\n", []string{"a\n"}},
		{"a\r\nb\r\n", []string{"a\r\n", "b\r\n"}},
		{"a\nb", []string{"a\n", "b"}},
		{"\n\n", []string{"\n", "\n"}},
	}
	for _, c := range cases {
		got := lines.Split([]byte(c.in))
		gs := make([]string, len(got))
		for i, l := range got {
			gs[i] = string(l)
		}
		if !reflect.DeepEqual(gs, c.want) || string(lines.Join(got)) != c.in {
			t.Fatalf("Split(%q) = %q, want %q", c.in, gs, c.want)
		}
	}
}

func randLines(r *rand.Rand, n int) [][]byte {
	out := make([][]byte, n)
	for i := range out {
		out[i] = []byte{byte('a' + r.Intn(3)), byte('0' + r.Intn(3)), '\n'}
	}
	if n > 0 && r.Intn(4) == 0 {
		out[n-1] = out[n-1][:2]
	}
	return out
}

func minDist(a, b [][]byte) int { // O(NM) DP 对照最短性
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if bytes.Equal(a[i], b[j]) {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}
	return n + m - 2*dp[0][0]
}

func applyOps(a [][]byte, ops []Op) [][]byte {
	var out [][]byte
	ai := 0
	for _, op := range ops {
		if op.Kind != '+' {
			if !bytes.Equal(op.Line, a[ai]) {
				panic("脚本行与原文不符")
			}
			ai++
		}
		if op.Kind != '-' {
			out = append(out, op.Line)
		}
	}
	if ai != len(a) {
		panic("脚本未消费全部原文")
	}
	return out
}

func TestShortestAndValid(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 300; i++ {
		a, b := randLines(r, r.Intn(13)), randLines(r, r.Intn(13))
		ops, err := Diff(a, b, -1)
		if err != nil {
			t.Fatal(err)
		}
		if d, want := Distance(ops), minDist(a, b); d != want {
			t.Fatalf("距离 %d != 最短 %d", d, want)
		}
		if got := applyOps(a, ops); !bytes.Equal(lines.Join(got), lines.Join(b)) {
			t.Fatalf("脚本应用结果不等于目标")
		}
	}
}

func TestDeterministic(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	a, b := randLines(r, 30), randLines(r, 30)
	base, _ := Diff(a, b, -1)
	for i := 0; i < 100; i++ {
		if ops, _ := Diff(a, b, -1); !reflect.DeepEqual(ops, base) {
			t.Fatal("同一输入重复差分结果不同")
		}
	}
}

func changed(n int) (a, b [][]byte) {
	a = make([][]byte, n)
	for i := range a {
		a[i] = []byte("l" + string(rune(i+1000)) + "\n")
	}
	b = append([][]byte(nil), a...)
	for _, i := range []int{n / 4, n / 2, 3 * n / 4} {
		b[i] = []byte("X\n")
	}
	return a, b
}

func TestStepBound(t *testing.T) {
	var prev int64
	for _, n := range []int{1000, 100000} {
		a, b := changed(n)
		ops, _ := Diff(a, b, -1)
		d := int64(Distance(ops))
		bound := 4 * int64(len(a)+len(b)) * (d + 1)
		if s := Steps(); s > bound {
			t.Fatalf("n=%d: 步数 %d 超过上界 %d", n, s, bound)
		}
		if prev > 0 && Steps() > 150*prev {
			t.Fatalf("n=%d: 步数 %d 超过上一档 %d 的 150 倍", n, Steps(), prev)
		}
		prev = Steps()
	}
}

func TestMaxDist(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	a, b := randLines(r, 20), randLines(r, 20)
	_, err := Diff(a, b, 2)
	if !errors.Is(err, ErrTooBig) || Steps() > 4*int64(len(a)+len(b))*3 {
		t.Fatalf("err=%v 超限返回时步数 %d 仍应满足上界", err, Steps())
	}
}
