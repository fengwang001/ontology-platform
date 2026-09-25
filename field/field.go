// Package field 在 poly 原语之上构建 GF(2^8)（模 0x11B）的 exp/log 表与域运算。
package field

import (
	"errors"
	"sync"
	"sync/atomic"

	"ontology/poly"
)

// 可判定哨兵错误，互不相同。
var (
	ErrZeroInverse      = errors.New("field: zero has no multiplicative inverse")
	ErrExponentTooLarge = errors.New("field: exponent exceeds 1<<63")
)

var (
	once sync.Once
	expT [512]uint8 // 双倍长，省去取模
	logT [256]uint8
	modP poly.Poly
)

// powMuls 记录最近一次 Pow 执行的域乘法次数；非导出，不出现在公开接口。
var powMuls atomic.Int64

// buildTables 以生成元 0x03 循环 255 步构建 exp/log 表，构建一次后不可变。
func buildTables() {
	modP, _ = poly.New(0x1B) // 0x1B 不可约性由 poly 自检与测试保证
	x := uint8(1)
	for i := 0; i < 255; i++ {
		expT[i] = x
		expT[i+255] = x
		logT[x] = uint8(i)
		x = modP.Mul(x, 0x03)
	}
}

// Add 域加法即异或（特征 2，加即减）。
func Add(a, b uint8) uint8 { return a ^ b }

// Mul 域乘法：查 exp/log 表；log[0] 无定义，零元素单独短路。
func Mul(a, b uint8) uint8 {
	once.Do(buildTables)
	if a == 0 || b == 0 {
		return 0
	}
	return expT[int(logT[a])+int(logT[b])]
}

// Inv 乘法逆元；0 无逆元，返回 ErrZeroInverse，不产生任何副作用。
func Inv(a uint8) (uint8, error) {
	once.Do(buildTables)
	if a == 0 {
		return 0, ErrZeroInverse
	}
	return expT[255-int(logT[a])], nil
}

// Pow 域内平方乘快速幂；e==0 恒返回 1；e>1<<63 返回 ErrExponentTooLarge。
func Pow(a uint8, e uint64) (uint8, error) {
	once.Do(buildTables)
	if e > 1<<63 {
		return 0, ErrExponentTooLarge
	}
	var n int64
	result := uint8(1)
	base := a
	for e > 0 {
		if e&1 != 0 {
			result = Mul(result, base)
			n++
		}
		e >>= 1
		if e > 0 {
			base = Mul(base, base)
			n++
		}
	}
	powMuls.Store(n)
	return result, nil
}
