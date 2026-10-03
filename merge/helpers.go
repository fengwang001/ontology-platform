package merge

import (
	"math"
	"math/bits"
	"sync"
)

var cmpMu sync.Mutex
var cmpCount int

func addCmp(n int) {
	cmpMu.Lock()
	cmpCount += n
	cmpMu.Unlock()
}

// resetComparisons / comparisonCount 仅供包内测试断言比较次数。
func resetComparisons() {
	cmpMu.Lock()
	cmpCount = 0
	cmpMu.Unlock()
}

func comparisonCount() int {
	cmpMu.Lock()
	defer cmpMu.Unlock()
	return cmpCount
}

func addUint64(x, y uint64) (uint64, bool) {
	if math.MaxUint64-y < x {
		return 0, false
	}
	return x + y, true
}

func addInt64(x, y int64) (int64, bool) {
	if y > 0 && x > math.MaxInt64-y {
		return 0, false
	}
	return x + y, true
}

// u128 是 128 位无符号累加器，保证中途不会回绕。
type u128 struct{ hi, lo uint64 }

func add128(a u128, x uint64) u128 {
	lo, carry := bits.Add64(a.lo, x, 0)
	return u128{a.hi + carry, lo}
}

func addU128(a, b u128) u128 {
	lo, carry := bits.Add64(a.lo, b.lo, 0)
	hi, carry2 := bits.Add64(a.hi, b.hi, carry)
	return u128{hi + carry2, lo}
}

func (a u128) exceedsInt64() bool {
	return a.hi != 0 || a.lo > math.MaxInt64
}

// project 把单个直方图的桶计数投影到公共边界 common 上（含溢出桶）。
// 每个源桶并入"不小于该桶上界的最小公共边界"所在桶；不存在则入溢出桶。
func project(bounds, common []int64, counts []uint64) []u128 {
	out := make([]u128, len(common)+1)
	var pending u128
	bi, ci := 0, 0
	for bi < len(bounds) && ci < len(common) {
		switch {
		case bounds[bi] == common[ci]:
			pending = add128(pending, counts[bi])
			out[ci] = addU128(out[ci], pending)
			pending = u128{}
			bi++
			ci++
		case bounds[bi] < common[ci]:
			pending = add128(pending, counts[bi])
			bi++
		default:
			if pending.hi != 0 || pending.lo != 0 {
				out[ci] = addU128(out[ci], pending)
				pending = u128{}
			}
			ci++
		}
	}
	for ; bi < len(bounds); bi++ {
		pending = add128(pending, counts[bi])
	}
	last := addU128(out[len(common)], pending)
	last = add128(last, counts[len(bounds)])
	out[len(common)] = last
	return out
}
