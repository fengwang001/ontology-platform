// Package keygroup 提供键组划分与扩缩容状态重分配能力。
//
// 键先经哈希映射到固定数量（maxParallelism）的键组，
// 键组再按连续区间划分给 parallelism 个实例；
// 实例区间两两不交且恰好覆盖全部键组。
// 并行度改变时，仅归属发生变化的键组整组迁移，其余原地不动。
package keygroup

import (
	"errors"
	"fmt"
	"hash/fnv"
)

var (
	// ErrInvalidMaxParallelism 表示最大并行度非法（<= 0）。
	ErrInvalidMaxParallelism = errors.New("keygroup: maxParallelism 必须为正整数")
	// ErrInvalidParallelism 表示并行度非法（<= 0）。
	ErrInvalidParallelism = errors.New("keygroup: parallelism 必须为正整数")
	// ErrParallelismExceedsMax 表示并行度超过最大并行度。
	ErrParallelismExceedsMax = errors.New("keygroup: parallelism 不能超过 maxParallelism")
	// ErrInstanceIndexOutOfRange 表示实例下标越界。
	ErrInstanceIndexOutOfRange = errors.New("keygroup: 实例下标越界")
	// ErrEmptyKey 表示键为空。
	ErrEmptyKey = errors.New("keygroup: 键不能为空")
)

// KeyGroupRange 表示一个闭区间 [Start, End] 的键组区间。
type KeyGroupRange struct {
	Start int
	End   int
}

// Size 返回区间内键组数量。
func (r KeyGroupRange) Size() int { return r.End - r.Start + 1 }

// Contains 判断键组是否落在区间内。
func (r KeyGroupRange) Contains(group int) bool {
	return group >= r.Start && group <= r.End
}

// ValidateParams 校验 maxParallelism 与 parallelism 的合法性。
func ValidateParams(maxParallelism, parallelism int) error {
	if maxParallelism <= 0 {
		return fmt.Errorf("%w: 得到 %d", ErrInvalidMaxParallelism, maxParallelism)
	}
	if parallelism <= 0 {
		return fmt.Errorf("%w: 得到 %d", ErrInvalidParallelism, parallelism)
	}
	if parallelism > maxParallelism {
		return fmt.Errorf("%w: parallelism=%d maxParallelism=%d",
			ErrParallelismExceedsMax, parallelism, maxParallelism)
	}
	return nil
}

// KeyGroupRangeForInstance 计算实例 operatorIndex 在
// (maxParallelism, parallelism) 参数下分得的键组区间。
//
// 划分公式（与 Flink KeyGroupRangeAssignment 一致）：
//
//	start = floor((operatorIndex * maxParallelism + parallelism - 1) / parallelism)
//	end   = floor(((operatorIndex+1) * maxParallelism - 1) / parallelism)
//
// 该公式保证各实例区间连续、两两不交且恰好覆盖 [0, maxParallelism-1]。
func KeyGroupRangeForInstance(maxParallelism, parallelism, operatorIndex int) (KeyGroupRange, error) {
	if err := ValidateParams(maxParallelism, parallelism); err != nil {
		return KeyGroupRange{}, err
	}
	if operatorIndex < 0 || operatorIndex >= parallelism {
		return KeyGroupRange{}, fmt.Errorf("%w: operatorIndex=%d parallelism=%d",
			ErrInstanceIndexOutOfRange, operatorIndex, parallelism)
	}
	start := (operatorIndex*maxParallelism + parallelism - 1) / parallelism
	end := ((operatorIndex+1)*maxParallelism - 1) / parallelism
	return KeyGroupRange{Start: start, End: end}, nil
}

// ComputeKeyGroupAssignment 返回每个实例分得的键组区间，
// 区间两两不交且恰好覆盖 [0, maxParallelism-1]。
func ComputeKeyGroupAssignment(maxParallelism, parallelism int) ([]KeyGroupRange, error) {
	if err := ValidateParams(maxParallelism, parallelism); err != nil {
		return nil, err
	}
	assignment := make([]KeyGroupRange, parallelism)
	for i := range assignment {
		r, err := KeyGroupRangeForInstance(maxParallelism, parallelism, i)
		if err != nil {
			return nil, err
		}
		assignment[i] = r
	}
	return assignment, nil
}

// KeyToGroup 将键确定性地映射到 [0, maxParallelism) 的键组。
// 采用 FNV-1a 哈希，同一输入序列必得同一输出。
func KeyToGroup(key string, maxParallelism int) (int, error) {
	if key == "" {
		return 0, ErrEmptyKey
	}
	if maxParallelism <= 0 {
		return 0, fmt.Errorf("%w: 得到 %d", ErrInvalidMaxParallelism, maxParallelism)
	}
	h := fnv.New32a()
	if _, err := h.Write([]byte(key)); err != nil {
		return 0, err
	}
	return int(h.Sum32() % uint32(maxParallelism)), nil
}

// OwnerOf 返回键组 group 在 assignment 下归属的实例下标。
// group 越界或 assignment 为空时返回 -1。
func OwnerOf(assignment []KeyGroupRange, group int) int {
	if group < 0 {
		return -1
	}
	for i, r := range assignment {
		if r.Contains(group) {
			return i
		}
	}
	return -1
}
