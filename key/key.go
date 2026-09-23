// Package key 定义多重集元素的键、全序与确定性层高导出。
package key

import "math/bits"

// Key 是多重集元素的键类型。
type Key = int64

// Less 报告 a 是否严格小于 b。
func Less(a, b Key) bool { return a < b }

// Equal 报告两个键是否相等。
func Equal(a, b Key) bool { return a == b }

// Height 由键本身确定性地导出层高，与插入顺序和已有结构无关。
// 规则：h = 1 + min(ctz(mix(k)), maxHeight-1)，mix 为 SplitMix64 最终化。
func Height(k Key, maxHeight int) int {
	h := bits.TrailingZeros64(mix(uint64(k))) + 1
	if h > maxHeight {
		return maxHeight
	}
	return h
}

// Distribution 返回键集合 keys 在 maxHeight 下各层（下标即层号，0 未用）的节点数，供测试核验。
func Distribution(keys []Key, maxHeight int) []int {
	counts := make([]int, maxHeight+1)
	for _, k := range keys {
		counts[Height(k, maxHeight)]++
	}
	return counts
}

func mix(x uint64) uint64 {
	x ^= x >> 33
	x *= 0xff51afd7ed558ccd
	x ^= x >> 33
	x *= 0xc4ceb9fe1a85ec53
	x ^= x >> 33
	return x
}
