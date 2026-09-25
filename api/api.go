// Package api 是十进制字符串 → 精确有理数解析器的对外接口。
// 依赖 conv（进而 lex），依赖方向单向：lex ← conv ← api。
package api

import (
	"errors"
	"fmt"
	"math/big"

	"ontology/conv"
	"ontology/lex"
)

// 四类可判定哨兵错误，互不相同：空输入、非法字符、语法非法、数值溢出。
var (
	ErrEmpty       = lex.ErrEmpty
	ErrIllegalChar = lex.ErrIllegalChar
	ErrSyntax      = lex.ErrSyntax
	ErrOverflow    = conv.ErrOverflow
)

// Frac 是既约分数：D > 0，符号在 N，gcd(|N|,D)==1，值 0 一律 0/1。
type Frac = conv.Frac

// Parser 无状态：Parse、String、SelfCheck 均可被多 goroutine 并发调用。
type Parser struct{}

func New() *Parser { return &Parser{} }

// Parse 把十进制字面量解析为精确既约分数。任何被拒绝的输入都整体
// 失败并返回可判定哨兵错误，不改变任何状态（Parser 本就无状态）。
func (p *Parser) Parse(s string) (*Frac, error) {
	parts, err := lex.Scan(s)
	if err != nil {
		return nil, err
	}
	f, err := conv.Convert(parts)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// checkCases 内置核验集：有限小数、循环小数、负号、零归一、整数。
var checkCases = []string{
	"3.14", "0.1", "-2.5", "0.(3)", "0.1(6)", "123", "0.(142857)", "-0.0",
	"1.(285714)", "0.(0)", "1.0", "-0.(9)",
}

// badCases 内置非法输入集：四类可判定错误各一条。
var badCases = []struct {
	s   string
	err error
}{
	{"", ErrEmpty},
	{"12x", ErrIllegalChar},
	{"1..2", ErrSyntax},
	{"0.9999999999999999999", ErrOverflow},
}

// SelfCheck 对内置字符串核验四条不变量：与 big 朴素参照一致、既约且
// 规范、有限与循环都精确、失败不留痕。全部通过返回 nil。
func SelfCheck() error {
	p := New()
	for _, s := range checkCases {
		f, err := p.Parse(s)
		if err != nil {
			return fmt.Errorf("selfcheck: parse %q: %w", s, err)
		}
		want, err := refRat(s) // 不变量1：与朴素参照逐条一致
		if err != nil {
			return fmt.Errorf("selfcheck: ref %q: %w", s, err)
		}
		if f.N != want.Num().Int64() || f.D != want.Denom().Int64() {
			return fmt.Errorf("selfcheck: %q = %s, want %s", s, f, want.RatString())
		}
		if f.D <= 0 || gcd(abs(f.N), f.D) != 1 { // 不变量2：既约且规范
			return fmt.Errorf("selfcheck: %q = %s not reduced", s, f)
		}
		if f.N == 0 && f.D != 1 { // 零一律 0/1
			return fmt.Errorf("selfcheck: %q = %s, zero must be 0/1", s, f)
		}
	}
	for _, b := range badCases { // 不变量4：失败可判定且不留痕
		if _, err := p.Parse(b.s); !errors.Is(err, b.err) {
			return fmt.Errorf("selfcheck: %q: got %v, want %v", b.s, err, b.err)
		}
	}
	if f, err := p.Parse("3.14"); err != nil || f.String() != "157/50" {
		return fmt.Errorf("selfcheck: parser unusable after rejects: %v", err)
	}
	return nil
}

// refRat 用 big.Rat 按 I.F(R) = I + F/10^j + R/(10^j·(10^k−1)) 精确
// 计算参照值并约分（big.Rat 自动既约），是不变量1 的朴素参照。
func refRat(s string) (*big.Rat, error) {
	p, err := lex.Scan(s)
	if err != nil {
		return nil, err
	}
	atol := func(t string) *big.Int {
		v, _ := new(big.Int).SetString(t, 10)
		if v == nil {
			return big.NewInt(0)
		}
		return v
	}
	pow10 := func(n int) *big.Int {
		return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
	}
	pj := pow10(len(p.Frac))
	rat := new(big.Rat).SetInt(atol(p.Int))
	rat.Add(rat, new(big.Rat).SetFrac(atol(p.Frac), pj))
	if len(p.Rep) > 0 {
		den := new(big.Int).Mul(pj, new(big.Int).Sub(pow10(len(p.Rep)), big.NewInt(1)))
		rat.Add(rat, new(big.Rat).SetFrac(atol(p.Rep), den))
	}
	if p.Neg {
		rat.Neg(rat)
	}
	return rat, nil
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

func gcd(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
