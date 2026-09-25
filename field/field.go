// Package field 在 poly 的归约原语之上，以生成元 0x03 构建 exp/log 表，
// 实现 GF(2^8)（模 0x11B）的 Mul/Inv/Pow。表用 sync.Once 构建一次后不可变。
package field

import (
	"errors"
	"sync"
	"sync/atomic"

	"ontology/poly"
)

var (
	// ErrZeroInverse：0x00 无乘法逆元。
	ErrZeroInverse = errors.New("field: zero has no inverse")
	// ErrExponentRange：幂指数超过约定的 1<<63 上限。
	ErrExponentRange = errors.New("field: exponent exceeds 1<<63")
)

const maxExponent = uint64(1) << 63

var (
	exp     [255]uint8 // exp[i] = 0x03^i
	log     [256]uint8 // log[0] 无定义，永不查表（零值一律单独分支）
	reducer *poly.Reducer
	once    sync.Once

	lastPowMuls atomic.Int64 // 非导出：最近一次 Pow 的域乘法次数
)

func build() {
	r, err := poly.NewReducer(0x1B)
	if err != nil { // 0x1B 是已验证的合法归约字节，构建失败即程序错误
		panic(err)
	}
	reducer = r
	x := uint8(1)
	for i := 0; i < 255; i++ {
		exp[i] = x
		log[x] = uint8(i)
		x = r.Mul(x, 0x03)
	}
	if x != 1 { // 0x03 必须是 255 阶生成元
		panic("field: 0x03 is not a generator")
	}
}

// Mul 用 exp/log 表做域乘法；a 或 b 为零直接返回零（不查 log[0]）。
func Mul(a, b uint8) uint8 {
	once.Do(build)
	if a == 0 || b == 0 {
		return 0
	}
	return exp[(int(log[a])+int(log[b]))%255]
}

// Inv 返回 a 的乘法逆元；a 为零报 ErrZeroInverse，不返回半成品。
func Inv(a uint8) (uint8, error) {
	once.Do(build)
	if a == 0 {
		return 0, ErrZeroInverse
	}
	return exp[(255-int(log[a]))%255], nil
}

// Pow 平方乘求 a^e。e==0 恒返回 1（含 Pow(0,0)==1）；e 超过 1<<63 报 ErrExponentRange。
func Pow(a uint8, e uint64) (uint8, error) {
	if e > maxExponent {
		return 0, ErrExponentRange
	}
	once.Do(build)
	result, base, muls := uint8(1), a, 0
	for ee := e; ee != 0; ee >>= 1 {
		if ee&1 != 0 {
			result = Mul(result, base)
			muls++
		}
		if ee > 1 {
			base = Mul(base, base)
			muls++
		}
	}
	lastPowMuls.Store(int64(muls))
	return result, nil
}
