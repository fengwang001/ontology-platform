// Package api 是 GF(2^8) 域运算的对外封装，并提供 SelfCheck 自检。
package api

import (
	"errors"
	"fmt"

	"ontology/field"
)

// ErrSelfCheck 表示自检发现某条不变量不成立。
var ErrSelfCheck = errors.New("api: self-check failed")

// 对外暴露的可判定哨兵错误（与 field 同源，可用 errors.Is 判定）。
var (
	ErrZeroInverse      = field.ErrZeroInverse
	ErrExponentTooLarge = field.ErrExponentTooLarge
)

func Add(a, b uint8) uint8 { return field.Add(a, b) }

func Mul(a, b uint8) uint8 { return field.Mul(a, b) }

func Inv(a uint8) (uint8, error) { return field.Inv(a) }

func Pow(a uint8, e uint64) (uint8, error) { return field.Pow(a, e) }

// naive 是逐位多项式乘再手工对 0x11B 归约的朴素参照实现。
func naive(a, b uint8) uint8 {
	r, aa := 0, int(a)
	for b != 0 {
		if b&1 != 0 {
			r ^= aa
		}
		b >>= 1
		aa <<= 1
		if aa&0x100 != 0 {
			aa ^= 0x11B
		}
	}
	return uint8(r)
}

// samples 是自检用的内置元素集。
var samples = [...]uint8{0x00, 0x01, 0x02, 0x03, 0x0E, 0x53, 0x57, 0x80, 0x83, 0x9A, 0xCA, 0xFF}

// SelfCheck 对内置元素核验四条不变量，全部通过返回 nil，否则返回包裹 ErrSelfCheck 的错误。
func SelfCheck() error {
	for _, a := range samples {
		for _, b := range samples {
			if Mul(a, b) != naive(a, b) || Mul(a, b) != Mul(b, a) { // 不变量 1、2（交换）
				return fmt.Errorf("%w: mul mismatch at %02x,%02x", ErrSelfCheck, a, b)
			}
			for _, c := range samples {
				if Mul(a, Add(b, c)) != Add(Mul(a, b), Mul(a, c)) { // 不变量 2（分配）
					return fmt.Errorf("%w: distributivity at %02x,%02x,%02x", ErrSelfCheck, a, b, c)
				}
			}
		}
		if a == 0 {
			if _, err := Inv(0); !errors.Is(err, ErrZeroInverse) { // 不变量 4
				return fmt.Errorf("%w: Inv(0) must fail decidable", ErrSelfCheck)
			}
			continue
		}
		inv, err := Inv(a)
		if err != nil || Mul(a, inv) != 0x01 { // 不变量 1（逆元）
			return fmt.Errorf("%w: inverse at %02x", ErrSelfCheck, a)
		}
		p254, _ := Pow(a, 254)
		p255, _ := Pow(a, 255)
		p256, _ := Pow(a, 256)
		if inv != p254 || p255 != 0x01 || p256 != a { // 不变量 3
			return fmt.Errorf("%w: fermat at %02x", ErrSelfCheck, a)
		}
	}
	return nil
}
