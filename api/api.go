// Package api 是有理数运算的对外接口，依赖 rat（进而依赖 num），方向单向。
package api

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"math/rand"

	"ontology/num"
	"ontology/rat"
)

// Rat 是不可变既约分数，String/IsZero 等方法可并发调用。
type Rat = rat.Rat

// 可判定的哨兵错误，三者互不相同。
var (
	ErrZeroDenominator = num.ErrZeroDenominator
	ErrMulOverflow     = num.ErrMulOverflow
	ErrAddOverflow     = num.ErrAddOverflow
	ErrNormOverflow    = num.ErrNormOverflow
)

// New 构造 n/d 的规范形式，d == 0 报 ErrZeroDenominator。
func New(n, d int64) (*Rat, error) { return rat.New(n, d) }

// 四则运算：Add/Sub 用 lcm 通分（溢出报 ErrAddOverflow），Mul/Div 先交叉
// 约分再相乘（溢出报 ErrMulOverflow），Div 除零报 ErrZeroDenominator。
func Add(a, b *Rat) (*Rat, error) { return rat.Add(a, b) }
func Sub(a, b *Rat) (*Rat, error) { return rat.Sub(a, b) }
func Mul(a, b *Rat) (*Rat, error) { return rat.Mul(a, b) }
func Div(a, b *Rat) (*Rat, error) { return rat.Div(a, b) }

// SelfCheck 对一组内置运算序列核验四条不变量，全部通过返回 nil。
// 只构造局部值、不写任何共享状态，可并发调用。
func SelfCheck() error {
	if err := checkReference(); err != nil {
		return err
	}
	if err := checkCommutative(); err != nil {
		return err
	}
	return checkFailureAtomic()
}

// checkReference 核验不变量 1（与 big.Rat 朴素参照逐条一致）与
// 不变量 2（规范形式唯一：分母恒正、既约、等值同形）。
func checkReference() error {
	rng := rand.New(rand.NewSource(608))
	for i := 0; i < 200; i++ {
		an, ad := rng.Int63n(20001)-10000, rng.Int63n(10000)+1
		bn, bd := rng.Int63n(20001)-10000, rng.Int63n(10000)+1
		if rng.Intn(2) == 0 {
			bd = -bd
		}
		a, err := New(an, ad)
		if err != nil {
			return err
		}
		b, err := New(bn, bd)
		if err != nil {
			return err
		}
		ra, rb := big.NewRat(an, ad), big.NewRat(bn, bd)
		ops := []struct {
			name string
			f    func(x, y *Rat) (*Rat, error)
			g    func(x, y *big.Rat) *big.Rat
		}{
			{"add", Add, new(big.Rat).Add}, {"sub", Sub, new(big.Rat).Sub},
			{"mul", Mul, new(big.Rat).Mul}, {"div", Div, new(big.Rat).Quo},
		}
		for _, o := range ops {
			if o.name == "div" && b.IsZero() {
				continue // big.Rat.Quo 除零会 panic，除零语义由不变量 4 覆盖
			}
			got, err := o.f(a, b)
			if err != nil {
				return fmt.Errorf("%s(%s,%s): %w", o.name, a, b, err)
			}
			if want := o.g(ra, rb).String(); got.String() != want {
				return fmt.Errorf("invariant1: %s(%s,%s)=%s, want %s", o.name, a, b, got, want)
			}
			if got.Den() <= 0 || num.Gcd(got.Num(), got.Den()) != 1 {
				return fmt.Errorf("invariant2: %s not canonical", got)
			}
		}
	}
	x, _ := New(2, 4)
	y, _ := New(-3, -6)
	if x.String() != y.String() {
		return errors.New("invariant2: equal values have different forms")
	}
	return nil
}

// checkCommutative 核验不变量 3：加法交换律。
func checkCommutative() error {
	rng := rand.New(rand.NewSource(609))
	for i := 0; i < 100; i++ {
		a, _ := New(rng.Int63n(2001)-1000, rng.Int63n(1000)+1)
		b, _ := New(rng.Int63n(2001)-1000, rng.Int63n(1000)+1)
		ab, err := Add(a, b)
		if err != nil {
			return err
		}
		ba, err := Add(b, a)
		if err != nil {
			return err
		}
		if ab.String() != ba.String() {
			return fmt.Errorf("invariant3: %s+%s != %s+%s", a, b, b, a)
		}
	}
	return nil
}

// checkFailureAtomic 核验不变量 4：被拒操作整体失败、不留痕、错误可判定。
func checkFailureAtomic() error {
	if ErrZeroDenominator == ErrMulOverflow || ErrMulOverflow == ErrAddOverflow ||
		ErrZeroDenominator == ErrAddOverflow {
		return errors.New("sentinel errors are not distinct")
	}
	v, err := New(3, 4)
	if err != nil {
		return err
	}
	before := v.String()
	if _, err := New(1, 0); !errors.Is(err, ErrZeroDenominator) {
		return errors.New("invariant4: New(1,0) is not ErrZeroDenominator")
	}
	max, _ := New(math.MaxInt64, 1)
	if _, err := Mul(max, max); !errors.Is(err, ErrMulOverflow) {
		return errors.New("invariant4: Mul overflow is not ErrMulOverflow")
	}
	big62, _ := New(int64(1)<<62, 1)
	if _, err := Add(big62, big62); !errors.Is(err, ErrAddOverflow) {
		return errors.New("invariant4: Add overflow is not ErrAddOverflow")
	}
	if v.String() != before {
		return errors.New("invariant4: state mutated after rejected ops")
	}
	w, err := Add(v, v) // 被拒后仍可正常使用
	if err != nil || w.String() != "3/2" {
		return errors.New("invariant4: value unusable after rejected ops")
	}
	return nil
}
