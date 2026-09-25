package conv

import (
	"strings"
	"testing"

	"ontology/lex"
)

// 证明单趟线性 O(m)：m 位有限小数的「乘以 10」次数恰好等于 m。
// m≥19 时数值必然溢出（ErrOverflow），但扫描计数仍须恰好为 m。
func TestMulBy10Linear(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		mulBy10.Store(0)
		p := lex.Parts{Int: "0", Frac: strings.Repeat("1", m)}
		if _, _, err := Convert(p); err != ErrOverflow {
			t.Fatalf("m=%d: got %v, want ErrOverflow", m, err)
		}
		if got := mulBy10.Load(); got != int64(m) {
			t.Errorf("m=%d: mul-by-10 = %d, want exactly %d", m, got, m)
		}
	}
}

// 各溢出点都必须报 ErrOverflow。
func TestOverflow(t *testing.T) {
	cases := map[string]lex.Parts{
		"int part overflow":  {Int: "9999999999999999999"},
		"19 nines frac":      {Int: "0", Frac: "9999999999999999999"},
		"denominator 10^19":  {Int: "0", Frac: strings.Repeat("1", 19)},
		"rep 10^k overflow":  {Int: "0", Rep: strings.Repeat("1", 19)},
		"cross sum overflow": {Int: "9", Frac: strings.Repeat("9", 17), Rep: strings.Repeat("9", 18)},
		"huge int plus rep":  {Int: "999999999999999999", Rep: "99"},
	}
	for name, p := range cases {
		if _, _, err := Convert(p); err != ErrOverflow {
			t.Errorf("%s: got %v, want ErrOverflow", name, err)
		}
	}
}

// 边界值不溢出：MaxInt64 相关的合法输入应成功且结果正确。
func TestBoundaryOK(t *testing.T) {
	cases := []struct {
		name string
		p    lex.Parts
		n, d int64
	}{
		{"max int64", lex.Parts{Int: "9223372036854775807"}, 9223372036854775807, 1},
		{"18 nines frac", lex.Parts{Int: "0", Frac: "999999999999999999"}, 999999999999999999, 1000000000000000000},
		{"zero rep", lex.Parts{Int: "0", Rep: "0"}, 0, 1},
		{"neg zero", lex.Parts{Neg: true, Int: "0", Frac: "0"}, 0, 1},
	}
	for _, c := range cases {
		n, d, err := Convert(c.p)
		if err != nil || n != c.n || d != c.d {
			t.Errorf("%s: got %d/%d, %v; want %d/%d", c.name, n, d, err, c.n, c.d)
		}
	}
}
