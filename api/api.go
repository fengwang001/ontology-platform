// Package api 是对外接口：构造、十进制字符串往返、四则运算与自检。
package api

import (
	"errors"
	"fmt"
	"strings"

	"ontology/arith"
	"ontology/limb"
)

// 三类可判定哨兵错误，互不相同。
var (
	ErrDivByZero    = errors.New("division by zero")
	ErrInvalidDigit = errors.New("invalid digit")
	ErrBadSign      = errors.New("misplaced or dangling sign")
)

// Int 是带符号任意精度整数，构造后不可变，只读方法可并发调用。
type Int struct{ v arith.Int }

// New 返回 0。
func New() *Int { return &Int{arith.Zero()} }

// FromString 解析十进制串（可带前导 +/- 与前导零）；任何非法输入整体失败。
func FromString(s string) (*Int, error) {
	sign := 1
	if len(s) > 0 && (s[0] == '-' || s[0] == '+') {
		if s[0] == '-' {
			sign = -1
		}
		s = s[1:]
		if len(s) == 0 {
			return nil, ErrBadSign // +/- 后无数字
		}
	}
	if len(s) == 0 {
		return nil, ErrInvalidDigit // 空串
	}
	if strings.IndexAny(s, "+-") >= 0 {
		return nil, ErrBadSign // 符号出现在非首位
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return nil, ErrInvalidDigit
		}
	}
	var m limb.Mag // 从右往左每 9 位一个 limb
	for hi := len(s); hi > 0; hi -= 9 {
		lo := hi - 9
		if lo < 0 {
			lo = 0
		}
		var v uint32
		for _, c := range []byte(s[lo:hi]) {
			v = v*10 + uint32(c-'0')
		}
		m = append(m, v)
	}
	return &Int{arith.Make(sign, m)}, nil
}

// String 返回规范十进制串：无前导零，0 输出 "0"。
func (x *Int) String() string {
	m := x.v.Mag()
	if len(m) == 0 {
		return "0"
	}
	var b strings.Builder
	if x.v.Sign() < 0 {
		b.WriteByte('-')
	}
	fmt.Fprintf(&b, "%d", m[len(m)-1])
	for i := len(m) - 2; i >= 0; i-- {
		fmt.Fprintf(&b, "%09d", m[i])
	}
	return b.String()
}

// Sign 返回 -1/0/+1。
func (x *Int) Sign() int { return x.v.Sign() }

// Add 返回 x+y。
func (x *Int) Add(y *Int) *Int { return &Int{arith.Add(x.v, y.v)} }

// Sub 返回 x-y。
func (x *Int) Sub(y *Int) *Int { return &Int{arith.Sub(x.v, y.v)} }

// Mul 返回 x*y。
func (x *Int) Mul(y *Int) *Int { return &Int{arith.Mul(x.v, y.v)} }

// DivMod 返回向零截断的商与余数；y 为零时报 ErrDivByZero，不产生任何结果。
func (x *Int) DivMod(y *Int) (*Int, *Int, error) {
	q, r, ok := arith.DivMod(x.v, y.v)
	if !ok {
		return nil, nil, ErrDivByZero
	}
	return &Int{q}, &Int{r}, nil
}

// SelfCheck 对一组内置运算序列核验四条不变量，全部通过返回 nil。
func SelfCheck() error {
	cases := [][4]string{{"17", "5", "3", "2"}, {"-17", "5", "-3", "-2"},
		{"17", "-5", "-3", "2"}, {"-17", "-5", "3", "-2"},
		{"1000000000", "7", "142857142", "6"}, {"-1", "1000000000", "0", "-1"},
		{"0", "-5", "0", "0"}, {"7", "-10", "0", "7"}}
	for _, c := range cases {
		a, _ := FromString(c[0])
		b, _ := FromString(c[1])
		q, r, err := a.DivMod(b)
		if err != nil || q.String() != c[2] || r.String() != c[3] { // 不变量1
			return fmt.Errorf("divmod %s %s", c[0], c[1])
		}
		if q.Mul(b).Add(r).String() != a.String() { // a==q*b+r
			return fmt.Errorf("identity %s %s", c[0], c[1])
		}
	}
	for _, s := range []string{"0", "-0", "000123", "-0001230", "999999999999999999"} {
		v, err := FromString(s)
		w, err2 := FromString(v.String())
		if err != nil || err2 != nil || w.String() != v.String() { // 不变量2/3
			return fmt.Errorf("roundtrip %q", s)
		}
	}
	if _, err := FromString(""); !errors.Is(err, ErrInvalidDigit) { // 不变量4
		return errors.New("empty input not rejected")
	}
	if _, _, err := New().DivMod(New()); !errors.Is(err, ErrDivByZero) {
		return errors.New("div by zero not rejected")
	}
	return nil
}
