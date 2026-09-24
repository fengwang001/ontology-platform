// Package parse 把十进制文本精确解回 float64，全程有理数算术。
package parse

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
)

// 错误分类，均可用 errors.Is 判定。
var (
	ErrEmpty         = errors.New("parse: 空串")
	ErrSyntax        = errors.New("parse: 非法字符")
	ErrRange         = errors.New("parse: 指数溢出")
	ErrTooManyDigits = errors.New("parse: 有效数字过多，无法保证往返")
)

const maxSigDigits = 17

var pow10 = func() []*big.Int {
	t := make([]*big.Int, 500)
	t[0] = big.NewInt(1)
	for i := 1; i < len(t); i++ {
		t[i] = new(big.Int).Mul(t[i-1], big.NewInt(10))
	}
	return t
}()

// Parse 解析 [-]ddd[.ddd][(e|E)[+-]ddd] 形式的文本，返回最近的 float64。
func Parse(s string) (float64, error) {
	if s == "" {
		return 0, ErrEmpty
	}
	i := 0
	neg := false
	if s[i] == '+' || s[i] == '-' {
		neg = s[i] == '-'
		i++
	}
	start := i
	frac := -1
	for i < len(s) {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			i++
		case c == '.' && frac < 0:
			frac = i
			i++
		default:
			return 0, syntaxAt(i)
		}
		if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
			break
		}
	}
	mant := s[start:i]
	digits := mant
	fracDigits := 0
	if frac >= 0 {
		digits = mant[:frac-start] + mant[frac-start+1:]
		fracDigits = len(mant) - (frac - start) - 1
	}
	if digits == "" {
		return 0, syntaxAt(start)
	}
	exp := 0
	if i < len(s) {
		i++
		var err error
		if exp, err = expAt(s, &i); err != nil {
			return 0, err
		}
	}
	if i != len(s) {
		return 0, syntaxAt(i)
	}
	return build(neg, digits, fracDigits, exp)
}

func syntaxAt(pos int) error {
	return fmt.Errorf("%w（字节位置 %d）", ErrSyntax, pos)
}

func expAt(s string, i *int) (int, error) {
	neg := false
	if *i < len(s) && (s[*i] == '+' || s[*i] == '-') {
		neg = s[*i] == '-'
		*i++
	}
	start := *i
	v := 0
	for *i < len(s) && s[*i] >= '0' && s[*i] <= '9' {
		v = v*10 + int(s[*i]-'0')
		if v > math.MaxInt32 {
			return 0, ErrRange
		}
		*i++
	}
	if *i == start {
		return 0, syntaxAt(start)
	}
	if neg {
		v = -v
	}
	return v, nil
}

// build 由有效数字串与小数、指数偏移精确构造 float64。
func build(neg bool, digits string, fracDigits, exp int) (float64, error) {
	trim := strings.TrimLeft(digits, "0")
	sig := strings.TrimRight(trim, "0")
	if len(sig) > maxSigDigits {
		return 0, ErrTooManyDigits
	}
	e10 := exp - fracDigits
	lead := e10 + len(trim) - 1 // 最高有效数字的十进制指数
	if len(trim) > 0 && (lead > 400 || lead < -400) {
		return 0, ErrRange
	}
	m, _ := new(big.Int).SetString(trim, 10)
	if m == nil {
		m = new(big.Int)
	}
	return toFloat(neg, m, e10)
}

// toFloat 计算 (-1)^neg * m * 10^e10 的最近 float64（平局取偶）。
func toFloat(neg bool, m *big.Int, e10 int) (float64, error) {
	sign := uint64(0)
	if neg {
		sign = 1 << 63
	}
	if m.Sign() == 0 {
		return math.Float64frombits(sign), nil
	}
	num := new(big.Int).Set(m)
	den := big.NewInt(1)
	if e10 >= 0 {
		num.Mul(num, pow10[e10])
	} else {
		den = pow10[-e10]
	}
	e := num.BitLen() - den.BitLen()
	if shiftCmp(num, den, e) < 0 {
		e--
	}
	var expField, mant uint64
	if e >= -1022 {
		s := roundDiv(num, den, e-52)
		if s.BitLen() > 53 {
			s.Rsh(s, 1)
			e++
		}
		if e > 1023 {
			return 0, ErrRange
		}
		expField = uint64(e + 1023)
		mant = new(big.Int).And(s, big.NewInt(1<<52-1)).Uint64()
	} else {
		s := roundDiv(num, den, -1074)
		if s.Sign() == 0 {
			return math.Float64frombits(sign), nil
		}
		if s.BitLen() > 52 {
			expField = 1
			mant = new(big.Int).Sub(s, big.NewInt(1<<52)).Uint64()
		} else {
			mant = s.Uint64()
		}
	}
	return math.Float64frombits(sign | expField<<52 | mant), nil
}

// shiftCmp 比较 num 与 den * 2^e。
func shiftCmp(num, den *big.Int, e int) int {
	if e >= 0 {
		return num.Cmp(new(big.Int).Lsh(den, uint(e)))
	}
	return new(big.Int).Lsh(num, uint(-e)).Cmp(den)
}

// roundDiv 返回 num / (den * 2^shift) 舍入到最近整数（平局取偶）。
func roundDiv(num, den *big.Int, shift int) *big.Int {
	n := new(big.Int).Set(num)
	d := new(big.Int).Set(den)
	if shift >= 0 {
		d.Lsh(d, uint(shift))
	} else {
		n.Lsh(n, uint(-shift))
	}
	q, r := new(big.Int).QuoRem(n, d, new(big.Int))
	c := new(big.Int).Lsh(r, 1).Cmp(d)
	if c > 0 || c == 0 && q.Bit(0) == 1 {
		q.Add(q, big.NewInt(1))
	}
	return q
}
