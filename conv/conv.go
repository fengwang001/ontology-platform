// Package conv 把 lex 切好的四段按 I.F(R) 公式转成既约分数，全程整数运算。
package conv

import (
	"errors"
	"math"
	"strconv"

	"ontology/lex"
)

// ErrOverflow：分子/分母任一步累加超出 int64 即报出。
var ErrOverflow = errors.New("conv: int64 overflow")

// Frac 是既约分数：D > 0，符号在 N，gcd(|N|,D)==1，值 0 一律 0/1。
type Frac struct{ N, D int64 }

func (f Frac) String() string {
	return strconv.FormatInt(f.N, 10) + "/" + strconv.FormatInt(f.D, 10)
}

// converter 是单次转换的局部状态。mul10 为非导出「乘以 10」计数器，
// 仅供同包测试证明 O(m)，不经任何导出接口暴露数值。
type converter struct{ mul10 int }

// Convert 为纯函数：失败返回零值与哨兵错误，不留痕。
func Convert(p lex.Parts) (Frac, error) { return new(converter).convert(p) }

// scan 单趟累加 num=num*10+d；前导零不计数。检出溢出后仍扫完本段（计数须完整），最后报 ErrOverflow。
func (c *converter) scan(s string) (int64, error) {
	var num int64
	started, overflow := false, false
	for i := 0; i < len(s); i++ {
		d := int64(s[i] - '0')
		if !started && d == 0 {
			continue
		}
		started = true
		c.mul10++
		if overflow {
			continue
		}
		if num > (math.MaxInt64-d)/10 {
			overflow = true
			continue
		}
		num = num*10 + d
	}
	if overflow {
		return 0, ErrOverflow
	}
	return num, nil
}

func (c *converter) convert(p lex.Parts) (Frac, error) {
	var err error
	step := func(f func() (int64, error)) int64 { // 短路：首个错误后不再计算
		if err != nil {
			return 0
		}
		var v int64
		v, err = f()
		return v
	}
	i := step(func() (int64, error) { return c.scan(p.Int) })
	f := step(func() (int64, error) { return c.scan(p.Frac) })
	r := step(func() (int64, error) { return c.scan(p.Rep) })
	pj := step(func() (int64, error) { return pow10(len(p.Frac)) })
	num0 := step(func() (int64, error) { return mulAdd(i, pj, f) })
	if err != nil {
		return Frac{}, err
	}
	if len(p.Rep) == 0 {
		return finish(p.Neg, num0, pj), nil
	}
	pk := step(func() (int64, error) { return pow10(len(p.Rep)) })
	num := step(func() (int64, error) { return mulAdd(num0, pk-1, r) })
	den := step(func() (int64, error) { return mul(pj, pk-1) })
	if err != nil {
		return Frac{}, err
	}
	return finish(p.Neg, num, den), nil
}

// finish：赋符号、零归一 0/1、gcd 约分（保证 d>0、既约）。
func finish(neg bool, num, den int64) Frac {
	if neg {
		num = -num
	}
	if num == 0 {
		return Frac{0, 1}
	}
	g := gcd(abs(num), den)
	return Frac{num / g, den / g}
}

// pow10 快速幂算 10^n（n≥0），超 int64 报 ErrOverflow。
func pow10(n int) (int64, error) {
	base, acc := int64(10), int64(1)
	for n > 0 {
		if n&1 == 1 {
			v, err := mul(acc, base)
			if err != nil {
				return 0, err
			}
			acc = v
		}
		n >>= 1
		if n > 0 {
			v, err := mul(base, base)
			if err != nil {
				return 0, err
			}
			base = v
		}
	}
	return acc, nil
}

// mul / mulAdd：非负 a·b 与 a·b+c，溢出即报 ErrOverflow。
func mul(a, b int64) (int64, error) {
	if a != 0 && b > math.MaxInt64/a {
		return 0, ErrOverflow
	}
	return a * b, nil
}
func mulAdd(a, b, c int64) (int64, error) {
	v, err := mul(a, b)
	if err != nil {
		return 0, err
	}
	if v > math.MaxInt64-c {
		return 0, ErrOverflow
	}
	return v + c, nil
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
