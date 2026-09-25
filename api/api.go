// Package api 对外提供十进制字符串 → 精确既约分数的解析入口。依赖 conv。
package api

import (
	"fmt"
	"math/big"

	"ontology/conv"
	"ontology/lex"
)

// 四类可判定哨兵错误，互不相同。
var (
	ErrEmpty    = lex.ErrEmpty
	ErrBadChar  = lex.ErrBadChar
	ErrSyntax   = lex.ErrSyntax
	ErrOverflow = conv.ErrOverflow
)

// Frac 是既约分数 N/D：D>0，符号在分子，gcd(|N|,D)==1，零为 0/1。
type Frac struct {
	N, D int64
}

// String 输出 "n/d"，零输出 "0/1"（不会出现 "-0/1"）。
func (f *Frac) String() string { return fmt.Sprintf("%d/%d", f.N, f.D) }

// Parser 无状态，可并发使用。
type Parser struct{}

// New 返回一个可并发使用的 Parser。
func New() *Parser { return &Parser{} }

// Parse 把十进制字面量解析为精确既约分数。任何错误整体失败、不留痕。
func (p *Parser) Parse(s string) (*Frac, error) {
	parts, err := lex.Split(s)
	if err != nil {
		return nil, err
	}
	n, d, err := conv.Convert(parts)
	if err != nil {
		return nil, err
	}
	return &Frac{N: n, D: d}, nil
}

// Parse 是包级便捷入口，与 New().Parse 等价。
func Parse(s string) (*Frac, error) { return New().Parse(s) }

// SelfCheck 对一组内置字符串核验四条不变量，全部通过返回 nil。
func (p *Parser) SelfCheck() error {
	cases := []string{
		"3.14", "0.1", "-2.5", "0.(3)", "0.1(6)", "123",
		"0.(142857)", "-0.0", "0.(0)", "1.(285714)", "1.0", "0",
	}
	for _, s := range cases {
		f, err := p.Parse(s)
		if err != nil {
			return fmt.Errorf("selfcheck parse %q: %w", s, err)
		}
		// 不变量 1：与 big 朴素参照一致
		if ref := bigRef(s); ref == nil || f.N != ref.N || f.D != ref.D {
			return fmt.Errorf("selfcheck %q: got %s, want %v", s, f, ref)
		}
		// 不变量 2：既约且规范
		if f.D <= 0 || gcd(abs(f.N), f.D) != 1 || (f.N == 0 && f.D != 1) {
			return fmt.Errorf("selfcheck %q: not normalized: %s", s, f)
		}
	}
	// 不变量 3：循环小数精确
	for s, want := range map[string]string{"0.(3)": "1/3", "0.1(6)": "1/6"} {
		f, _ := p.Parse(s)
		if f.String() != want {
			return fmt.Errorf("selfcheck %q: got %s, want %s", s, f, want)
		}
	}
	// 不变量 4：被拒输入整体失败且不留痕
	bad := []string{"", "1a", "1..2", "1(2)(3)", "1-2", ".5", "9999999999999999999"}
	for _, s := range bad {
		if _, err := p.Parse(s); err == nil {
			return fmt.Errorf("selfcheck: %q should be rejected", s)
		}
	}
	if _, err := p.Parse("3.14"); err != nil { // 拒绝后仍可正常使用
		return fmt.Errorf("selfcheck: state corrupted after rejection")
	}
	return nil
}

// bigRef 用 math/big 按 I.F(R) 公式精确计算再约分，作为朴素参照。
func bigRef(s string) *Frac {
	parts, err := lex.Split(s)
	if err != nil {
		return nil
	}
	n := big.NewInt(0)
	n.SetString(parts.Int+parts.Frac, 10)
	d := big.NewInt(1)
	pj := pow10big(len(parts.Frac))
	d.Mul(d, pj)
	if parts.Rep != "" {
		r, _ := new(big.Int).SetString(parts.Rep, 10)
		pk := pow10big(len(parts.Rep))
		pk.Sub(pk, big.NewInt(1)) // 10^k − 1
		n.Mul(n, pk)
		n.Add(n, r)
		d.Mul(d, pk)
	}
	if parts.Neg {
		n.Neg(n)
	}
	r := new(big.Rat).SetFrac(n, d) // SetFrac 自动约分
	return &Frac{N: r.Num().Int64(), D: r.Denom().Int64()}
}

func pow10big(k int) *big.Int {
	r := big.NewInt(1)
	for i := 0; i < k; i++ {
		r.Mul(r, big.NewInt(10))
	}
	return r
}

func gcd(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
