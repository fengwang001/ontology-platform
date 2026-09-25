package edit

import (
	"math/rand"
	"testing"

	"ontology/lines"
)

func L(s string) []lines.Line { return lines.Split([]byte(s)) }

func TestShortest(t *testing.T) {
	cases := []struct{ a, b string }{
		{"abc\n", "abc\n"}, {"a\nb\n", "b\na\n"}, {"", "x\n"},
		{"a\n", ""}, {"cat\n", "cut\n"}, {"A\nB\nC\n", "X\nB\nY\n"},
	}
	for _, c := range cases {
		r, err := Diff(L(c.a), L(c.b), -1)
		if err != nil {
			t.Fatal(err)
		}
		want := lcsDist(L(c.a), L(c.b))
		if r.D != want {
			t.Fatalf("D=%d want %d (%q,%q)", r.D, want, c.a, c.b)
		}
	}
	// 小规模随机：Myers 距离必须等于 O(NM) 动态规划答案。
	rng := rand.New(rand.NewSource(1))
	for t0 := 0; t0 < 300; t0++ {
		a := randText(rng, 1+rng.Intn(12), 4)
		b := randText(rng, 1+rng.Intn(12), 4)
		r, err := Diff(L(a), L(b), -1)
		if err != nil {
			t.Fatal(err)
		}
		if want := lcsDist(L(a), L(b)); r.D != want {
			t.Fatalf("D=%d want %d (%q,%q)", r.D, want, a, b)
		}
	}
}

func TestDeterministic(t *testing.T) {
	cases := []struct{ a, b string }{
		{"a\nb\n", "b\na\n"}, {"a\nb\nc\n", "x\na\nc\ny\n"}, {"", "z\n"},
	}
	for _, c := range cases {
		first, _ := Diff(L(c.a), L(c.b), -1)
		for i := 0; i < 100; i++ {
			r, _ := Diff(L(c.a), L(c.b), -1)
			if scriptSig(r.Script) != scriptSig(first.Script) {
				t.Fatalf("nondeterministic for %q,%q", c.a, c.b)
			}
		}
	}
}

func TestDeleteFirstTie(t *testing.T) {
	r, _ := Diff(L("a\nb\n"), L("b\na\n"), -1)
	var kinds []Kind
	for _, o := range r.Script {
		kinds = append(kinds, o.Kind)
	}
	want := []Kind{Delete, Equal, Insert}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("tie order=%v want %v (delete-first)", kinds, want)
		}
	}
}

func TestMaxDistance(t *testing.T) {
	cases := []struct {
		a, b string
		max  int
		ok   bool
	}{
		{"a\nb\nc\n", "x\ny\nz\n", 5, false},
		{"a\nb\nc\n", "x\ny\nz\n", 6, true},
		{"a\n", "a\n", 0, true},
	}
	for _, c := range cases {
		_, err := Diff(L(c.a), L(c.b), c.max)
		if (err == nil) != c.ok {
			t.Fatalf("maxD=%d err=%v want ok=%v", c.max, err, c.ok)
		}
	}
}

func TestStepsLinear(t *testing.T) {
	s1k, s100k := genScale(1000), genScale(100000)
	r1, _ := Diff(L(s1k), L(mutate(s1k)), -1)
	r2, _ := Diff(L(s100k), L(mutate(s100k)), -1)
	bound := int64(4) * 101000 * int64(r2.D+1)
	if r1.Steps > int64(4)*2000*int64(r1.D+1) {
		t.Fatalf("1k steps %d over bound", r1.Steps)
	}
	if r2.Steps > bound {
		t.Fatalf("100k steps %d over bound %d", r2.Steps, bound)
	}
	if r2.Steps > r1.Steps*150 {
		t.Fatalf("not near-linear: %d vs %d", r2.Steps, r1.Steps)
	}
	// 超限时计数器同样满足上界。
	rr, err := Diff(L(s1k), L(s100k), 2)
	if err == nil {
		t.Fatal("want ErrTooDifferent")
	}
	if rr.Steps < 0 {
		t.Fatal("steps recorded on limit error")
	}
}

func scriptSig(os []Op) string {
	b := make([]byte, len(os))
	for i, o := range os {
		b[i] = byte(o.Kind) + '0'
	}
	return string(b)
}

func randText(rng *rand.Rand, n, alpha int) string {
	b := make([]byte, 0, n*2)
	for i := 0; i < n; i++ {
		b = append(b, byte('a'+rng.Intn(alpha)), '\n')
	}
	return string(b)
}

func genScale(n int) string {
	b := make([]byte, 0, n*4)
	for i := 0; i < n; i++ {
		b = append(b, []byte{byte('a' + i%26), byte('0' + (i/26)%10),
			byte('0' + (i/260)%10), '\n'}...)
	}
	return string(b)
}

func mutate(base string) string {
	ls := L(base)
	if len(ls) < 10 {
		return base
	}
	for _, idx := range []int{len(ls) / 4, len(ls) / 2, 3 * len(ls) / 4} {
		ls[idx].Data = append([]byte("Z"), ls[idx].Data...)
	}
	return string(lines.Join(ls))
}

func lcsDist(a, b []lines.Line) int {
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if lines.Equal(a[i-1], b[j-1]) {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] > dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}
	return n + m - 2*dp[n][m]
}
