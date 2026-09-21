package alloc

import (
	"math/bits"
	"sort"
)

// Allocate 按权重比例分摊金额（最小货币单位）。
//
// 先对每一方取 floor(amount*weights[i]/total) 的保底份额，再用最大余数法
// 把未分完的单位逐一补给余数最大的一方；余数相同时索引小的一方优先。
// amount 为负时规则一致，只是保底份额向下取整、余数以非负值表达。
func Allocate(amount int64, weights []int64) ([]int64, error) {
	if len(weights) == 0 {
		return nil, ErrNoWeights
	}

	result := make([]int64, len(weights))

	var total int64
	var ok bool
	for _, w := range weights {
		if w < 0 {
			return nil, ErrNegativeWeight
		}
		total, ok = add64(total, w)
		if !ok {
			return nil, ErrOverflow
		}
	}
	if total <= 0 {
		return nil, ErrZeroTotalWeight
	}

	type remainder struct {
		index int
		value int64
	}
	remainders := make([]remainder, 0, len(weights))

	var distributed int64
	for i, w := range weights {
		if w == 0 {
			continue
		}
		product, ok := mul64(amount, w)
		if !ok {
			return nil, ErrOverflow
		}
		share := floorDiv(product, total)
		result[i] = share
		distributed, ok = add64(distributed, share)
		if !ok {
			return nil, ErrOverflow
		}
		// floorMod(product,total) 是未被 floor 保底覆盖的比例（非负）。
		// 正金额时它是“零头”，负金额时它是欠账比例，两种情形下
		// 都是这个值最大的一方最需要 +1 修正，故排序方向一致。
		remainders = append(remainders, remainder{i, floorMod(product, total)})
	}

	// 剩余待分单位数：正负金额下都落在 [0, 正权重方数量)，减法不会溢出。
	leftover := amount - distributed

	sort.Slice(remainders, func(a, b int) bool {
		if remainders[a].value != remainders[b].value {
			return remainders[a].value > remainders[b].value
		}
		return remainders[a].index < remainders[b].index
	})

	// 负金额的补足同样是 +1：floor 保底份额已经偏少（更负），加 1 即向 0 靠拢。
	for k := int64(0); k < leftover; k++ {
		result[remainders[k].index]++
	}
	return result, nil
}

// floorDiv 返回向负无穷取整的除法结果。
func floorDiv(a, b int64) int64 {
	q := a / b
	r := a % b
	if r != 0 && ((r < 0) != (b < 0)) {
		q--
	}
	return q
}

// floorMod 返回与 floorDiv 配套的非负余数：a = floorDiv(a,b)*b + floorMod(a,b)。
func floorMod(a, b int64) int64 {
	r := a % b
	if r != 0 && ((r < 0) != (b < 0)) {
		r += b
	}
	return r
}

// mul64 返回 a*b，并在溢出时返回 ErrOverflow。
func mul64(a, b int64) (int64, bool) {
	u1, u2 := uint64(a), uint64(b)
	hi, lo := bits.Mul64(u1, u2)
	// 把无符号乘积的高 64 位修正为有符号乘积的高 64 位。
	if a < 0 {
		hi -= u2
	}
	if b < 0 {
		hi -= u1
	}
	// 有符号乘积不溢出，当且仅当高 64 位等于低 64 位的符号扩展。
	if hi != uint64(int64(lo)>>63) {
		return 0, false
	}
	return int64(lo), true
}

// add64 返回 a+b，并在溢出时返回 false。
func add64(a, b int64) (int64, bool) {
	sum := a + b
	// 同号两数相加，结果符号与操作数相反即为溢出。
	if (a > 0 && b > 0 && sum < 0) || (a < 0 && b < 0 && sum >= 0) {
		return 0, false
	}
	return sum, true
}
