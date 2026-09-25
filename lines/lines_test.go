package lines_test

import (
	"bytes"
	"testing"

	"ontology/edit"
	"ontology/lines"
)

func TestSplitJoinRoundTrip(t *testing.T) {
	cases := [][]byte{
		nil,
		[]byte(""),
		[]byte("\n"),
		[]byte("a"),
		[]byte("a\n"),
		[]byte("a\nb"),
		[]byte("a\nb\n"),
		[]byte("a\r\nb\r\n"),
		[]byte("a\r\n"),
		[]byte("\r\n\r\n"),
		[]byte("a\n\rb\n"),
		[]byte("没有\n换行\r结尾"),
	}
	for _, in := range cases {
		if got := lines.Join(lines.Split(in)); !bytes.Equal(got, in) {
			t.Errorf("round trip %q: got %q", in, got)
		}
	}
}

func TestSplitKeepsLineEndings(t *testing.T) {
	ls := lines.SplitString("a\r\nb\nc")
	want := []lines.Line{
		{Text: "a", NL: "\r\n"},
		{Text: "b", NL: "\n"},
		{Text: "c", NL: ""},
	}
	if len(ls) != len(want) {
		t.Fatalf("len = %d, want %d", len(ls), len(want))
	}
	for i := range want {
		if ls[i] != want[i] {
			t.Errorf("line %d = %+v, want %+v", i, ls[i], want[i])
		}
	}
}

// lcs 用 O(NM) 动态规划计算最长公共子序列长度。
func lcs(a, b []lines.Line) int {
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			if a[i-1].Content() == b[j-1].Content() {
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

func TestDiffShortestRandom(t *testing.T) {
	pool := []string{"a\n", "b\n", "c\n", "d\n"}
	rng := newRNG(12345)
	for iter := 0; iter < 300; iter++ {
		la := rng.n() % 12
		lb := rng.n() % 12
		a := make([]lines.Line, la)
		b := make([]lines.Line, lb)
		for i := range a {
			a[i] = lines.SplitString(pool[rng.n()%len(pool)])[0]
		}
		for i := range b {
			b[i] = lines.SplitString(pool[rng.n()%len(pool)])[0]
		}
		ops, err := edit.Diff(a, b, len(a)+len(b))
		if err != nil {
			t.Fatal(err)
		}
		want := len(a) + len(b) - 2*lcs(a, b)
		if got := edit.Distance(ops); got != want {
			t.Fatalf("iter %d distance = %d, want %d", iter, got, want)
		}
	}
}

func TestDiffDeleteFirstTieBreak(t *testing.T) {
	a := lines.SplitString("a\nb\n")
	b := lines.SplitString("b\na\n")
	ops, err := edit.Diff(a, b, 4)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []edit.Kind
	for _, op := range ops {
		kinds = append(kinds, op.Kind)
	}
	want := []edit.Kind{edit.Delete, edit.Equal, edit.Insert}
	if len(kinds) != len(want) {
		t.Fatalf("ops = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("ops = %v, want %v", kinds, want)
		}
	}
}

func TestDiffDeterministic(t *testing.T) {
	a := lines.SplitString("x\na\nb\nx\n")
	b := lines.SplitString("x\nb\na\nx\n")
	first, err := edit.Diff(a, b, 8)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		got, err := edit.Diff(a, b, 8)
		if err != nil {
			t.Fatal(err)
		}
		if opsString(got) != opsString(first) {
			t.Fatalf("diff not deterministic on iteration %d", i)
		}
	}
}

func TestDiffDistanceLimit(t *testing.T) {
	a := lines.SplitString("a\nb\nc\n")
	b := lines.SplitString("x\ny\nz\n")
	if _, err := edit.Diff(a, b, 1); err != edit.ErrTooDifferent {
		t.Fatalf("err = %v, want ErrTooDifferent", err)
	}
	if _, err := edit.Diff(a, b, 6); err != nil {
		t.Fatalf("unexpected error with sufficient limit: %v", err)
	}
}

func TestDiffStepCounterLinear(t *testing.T) {
	var counts []int
	for _, n := range []int{1000, 100000} {
		base := make([]lines.Line, n)
		for i := range base {
			base[i] = lines.Line{Text: "row", NL: "\n"}
		}
		b := append([]lines.Line(nil), base...)
		for _, at := range []int{n / 4, n / 2, 3 * n / 4} {
			b[at] = lines.Line{Text: "ZZZ", NL: "\n"}
		}
		ops, err := edit.Diff(base, b, 100)
		if err != nil {
			t.Fatal(err)
		}
		c := edit.Steps()
		counts = append(counts, c)
		if bound := 4 * (len(base) + len(b)) * (edit.Distance(ops) + 1); c > bound {
			t.Fatalf("n=%d steps=%d > bound %d", n, c, bound)
		}
	}
	if float64(counts[1]) > 150*float64(counts[0]) {
		t.Fatalf("large steps %d > 150 x small %d", counts[1], counts[0])
	}
}

func opsString(ops []edit.Op) string {
	buf := make([]byte, 0, len(ops))
	for _, op := range ops {
		buf = append(buf, byte('0'+op.Kind))
	}
	return string(buf)
}

// rng 是简单确定性 LCG，避免引入 math/rand 之外的依赖。
type rng struct{ state uint64 }

func newRNG(seed uint64) *rng { return &rng{state: seed} }

func (r *rng) n() int {
	r.state = r.state*6364136223846793005 + 1442695040888963407
	return int(r.state >> 33)
}
