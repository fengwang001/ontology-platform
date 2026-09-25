// Package api 对外暴露模幂能力，依赖 pow 包。
package api

import (
	"errors"

	"ontology/mod"
	"ontology/pow"
)

// errSelfCheck 是自检失败的可判定哨兵错误。
var errSelfCheck = errors.New("api: self check failed")

// Checker 是模幂服务的对外句柄，无状态，可并发使用。
type Checker struct{}

// New 构造一个 Checker。
func New() *Checker { return &Checker{} }

// PowMod 计算 base^exp mod mod，结果非负且严格小于 mod。
func (c *Checker) PowMod(base, exp, modulus int64) (int64, error) {
	return pow.PowMod(base, exp, modulus)
}

// SelfCheck 对一组内置三元组核验四条不变量，全部通过返回 nil。
func (c *Checker) SelfCheck() error {
	triples := [][3]int64{
		{-2, 3, 5}, {-3, 2, 7}, {-3, 3, 7}, {7, 100, 13},
		{-5, 3, 9}, {2, 0, 5}, {5, 0, 1}, {2, 10, 1},
		{123456789, 987, 1000000007}, {-987654321, 12, 97},
	}
	for _, t := range triples {
		b, e, m := t[0], t[1], t[2]
		got, err := pow.PowMod(b, e, m)
		if err != nil {
			return err
		}
		// 不变量 2：结果恒在 [0, mod)，负 base 等价于归一化后再幂。
		if got < 0 || got >= m {
			return errSelfCheck
		}
		ref, err := pow.PowMod(mod.Normalize(b, m), e, m)
		if err != nil || ref != got {
			return errSelfCheck
		}
		// 不变量 1：与朴素参照一致（e 较小，逐次 MulMod 自乘）。
		naive := int64(1) % m
		for i := int64(0); i < e; i++ {
			naive = mod.MulMod(naive, mod.Normalize(b, m), m)
		}
		if naive != got {
			return errSelfCheck
		}
		// 不变量 3：指数拆分 e = e1 + e2。
		e1, e2 := e/2, e-e/2
		p1, err1 := pow.PowMod(b, e1, m)
		p2, err2 := pow.PowMod(b, e2, m)
		if err1 != nil || err2 != nil || mod.MulMod(p1, p2, m) != got {
			return errSelfCheck
		}
	}
	// 不变量 4：三类故障注入分别报互不相同的可判定错误。
	if _, err := pow.PowMod(1, 1, 0); err != pow.ErrZeroModulus {
		return errSelfCheck
	}
	if _, err := pow.PowMod(1, 1, -1); err != pow.ErrNegativeModulus {
		return errSelfCheck
	}
	if _, err := pow.PowMod(1, -1, 2); err != pow.ErrNegativeExponent {
		return errSelfCheck
	}
	return nil
}
