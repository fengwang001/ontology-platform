// Package api 对外封装 GF(2^8)（模 0x11B）域运算：Add/Mul/Inv/Pow 与 SelfCheck。
package api

import (
	"errors"
	"fmt"

	"ontology/field"
	"ontology/poly"
)

// 三类可判定哨兵错误（互不相同），由下层包定义、在此再导出。
var (
	ErrZeroInverse    = field.ErrZeroInverse
	ErrExponentRange  = field.ErrExponentRange
	ErrNotIrreducible = poly.ErrNotIrreducible

	// ErrSelfCheck：SelfCheck 发现不变量被破坏。
	ErrSelfCheck = errors.New("api: self-check failed")
)

// Add 域加法：特征 2，即按位异或（加即减）。
func Add(a, b uint8) uint8 { return a ^ b }

// Mul 域乘法：多项式相乘后对 0x11B 归约。
func Mul(a, b uint8) uint8 { return field.Mul(a, b) }

// Inv 乘法逆元；Inv(0x00) 报 ErrZeroInverse。
func Inv(a uint8) (uint8, error) { return field.Inv(a) }

// Pow 域内平方乘快速幂；e==0 恒返回 1；e > 1<<63 报 ErrExponentRange。
func Pow(a uint8, e uint64) (uint8, error) { return field.Pow(a, e) }

// selfCheckElems 是 SelfCheck 内置的核验元素集。
var selfCheckElems = []uint8{0x00, 0x01, 0x02, 0x03, 0x53, 0x57, 0x83, 0x9A, 0xCA, 0xFF}

// SelfCheck 对内置元素核验四条不变量：与朴素参照一致、交换律/分配律、
// 费马（群阶 255）、Inv(0) 失败不留痕。全部通过返回 nil。
func SelfCheck() error {
	ref, err := poly.NewReducer(0x1B)
	if err != nil {
		return err
	}
	for _, a := range selfCheckElems {
		// 不变量 1：Mul 与朴素「移位+归约」参照一致；Inv 回乘为 1
		for _, b := range selfCheckElems {
			if Mul(a, b) != ref.Mul(a, b) {
				return fmt.Errorf("%w: Mul(%02X,%02X) != naive", ErrSelfCheck, a, b)
			}
		}
		if a != 0 {
			iv, err := Inv(a)
			if err != nil || Mul(a, iv) != 0x01 {
				return fmt.Errorf("%w: Inv(%02X) round-trip", ErrSelfCheck, a)
			}
		}
		// 不变量 3：群阶 255（a^255==1，故 a^256==a）；Inv(a)==Pow(a,254)
		if a != 0 {
			p255, _ := Pow(a, 255)
			p256, _ := Pow(a, 256)
			iv, _ := Inv(a)
			p254, _ := Pow(a, 254)
			if p255 != 1 || p256 != a || iv != p254 {
				return fmt.Errorf("%w: Fermat on %02X", ErrSelfCheck, a)
			}
		}
		// 不变量 2：交换律与分配律（域公理）
		for _, b := range selfCheckElems {
			if Mul(a, b) != Mul(b, a) {
				return fmt.Errorf("%w: commutativity", ErrSelfCheck)
			}
			for _, c := range selfCheckElems {
				if Mul(a, Add(b, c)) != Add(Mul(a, b), Mul(a, c)) {
					return fmt.Errorf("%w: distributivity", ErrSelfCheck)
				}
			}
		}
	}
	// 不变量 4：Inv(0) 必须报可判定错误，不 panic、不返回半成品
	if _, err := Inv(0x00); !errors.Is(err, ErrZeroInverse) {
		return fmt.Errorf("%w: Inv(0) must fail with ErrZeroInverse", ErrSelfCheck)
	}
	return nil
}
