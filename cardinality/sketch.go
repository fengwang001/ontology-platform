package cardinality

import (
	"hash/fnv"
	"math"
	"math/bits"
)

const (
	// MinPrecision 是允许的最小精度。
	MinPrecision = 4
	// MaxPrecision 是允许的最大精度。
	MaxPrecision = 18
)

// hashKey 使用 FNV-1a 64 位哈希叠加 fmix64 混合收尾的确定哈希，
// 同一键永远得到同一哈希值，且高位充分雪崩以保证寄存器均匀分布。
func hashKey(key string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return fmix64(h.Sum64())
}

// fmix64 是 MurmurHash3 的 64 位最终混合函数，确定性且雪崩充分。
func fmix64(k uint64) uint64 {
	k ^= k >> 33
	k *= 0xff51afd7ed558ccd
	k ^= k >> 33
	k *= 0xc4ceb9fe1a85ec53
	k ^= k >> 33
	return k
}

// locate 按确定哈希把键落到固定寄存器与秩上：
// 高 precision 位为寄存器下标，剩余位的前导零个数加一为秩。
func locate(precision uint8, hash uint64) (reg uint32, rank uint8) {
	reg = uint32(hash >> (64 - precision))
	lz := bits.LeadingZeros64(hash << precision)
	if max := 64 - int(precision); lz > max {
		lz = max
	}
	rank = uint8(lz) + 1
	return reg, rank
}

// maxRank 返回给定精度下秩的最大可能取值。
func maxRank(precision uint8) int {
	return 64 - int(precision) + 1
}

// alpha 是固定公式中的偏差修正常数。
func alpha(m float64) float64 {
	switch m {
	case 16:
		return 0.673
	case 32:
		return 0.697
	case 64:
		return 0.709
	default:
		return 0.7213 / (1 + 1.079/m)
	}
}

// estimate 由寄存器秩经固定公式（HyperLogLog 及小基数线性计数修正）算出。
func estimate(registers []uint8) uint64 {
	m := float64(len(registers))
	sum := 0.0
	zeros := 0
	for _, r := range registers {
		sum += math.Ldexp(1, -int(r))
		if r == 0 {
			zeros++
		}
	}
	raw := alpha(m) * m * m / sum
	if raw <= 2.5*m && zeros > 0 {
		raw = m * math.Log(m/float64(zeros))
	}
	return uint64(math.Round(raw))
}
