package keygroup

import (
	"fmt"
	"hash/fnv"
)

// Range 表示一段闭区间 [Start, End] 的键组编号。
type Range struct {
	Start int
	End   int
}

// Contains 判断键组 g 是否落在区间内。
func (r Range) Contains(g int) bool { return g >= r.Start && g <= r.End }

// Size 返回区间包含的键组个数。
func (r Range) Size() int { return r.End - r.Start + 1 }

func (r Range) String() string { return fmt.Sprintf("[%d,%d]", r.Start, r.End) }

// KeyGroupOf 计算键所属的键组编号：fnv32a(key) % maxParallelism。
// 哈希函数确定，同一输入序列反复计算结果完全一致。
func KeyGroupOf(key string, maxParallelism int) (int, error) {
	if key == "" {
		return 0, invalidInput("KeyGroupOf", ErrEmptyKey, "key=\"\"")
	}
	if maxParallelism <= 0 {
		return 0, invalidInput("KeyGroupOf", ErrInvalidMaxParallelism,
			fmt.Sprintf("maxParallelism=%d", maxParallelism))
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key)) // hash.Hash 的 Write 永不返回错误
	return int(h.Sum32() % uint32(maxParallelism)), nil
}

// ComputeKeyGroupRange 计算第 operatorIndex 个实例在 parallelism 并行度下
// 分得的连续键组区间。
//
// 划分规则（与 Flink KeyGroupRangeAssignment 一致）：
//
//	start = floor((index*maxParallelism + parallelism - 1) / parallelism)
//	end   = floor(((index+1)*maxParallelism - 1) / parallelism)
//
// 该规则保证各实例区间两两不交、首尾相接，且恰好覆盖 [0, maxParallelism-1]。
func ComputeKeyGroupRange(maxParallelism, parallelism, operatorIndex int) (Range, error) {
	if maxParallelism <= 0 {
		return Range{}, invalidInput("ComputeKeyGroupRange", ErrInvalidMaxParallelism,
			fmt.Sprintf("maxParallelism=%d", maxParallelism))
	}
	if parallelism <= 0 || parallelism > maxParallelism {
		return Range{}, invalidInput("ComputeKeyGroupRange", ErrInvalidParallelism,
			fmt.Sprintf("parallelism=%d, maxParallelism=%d", parallelism, maxParallelism))
	}
	if operatorIndex < 0 || operatorIndex >= parallelism {
		return Range{}, invalidInput("ComputeKeyGroupRange", ErrInvalidOperatorIndex,
			fmt.Sprintf("index=%d, parallelism=%d", operatorIndex, parallelism))
	}
	start := (operatorIndex*maxParallelism + parallelism - 1) / parallelism
	end := ((operatorIndex+1)*maxParallelism - 1) / parallelism
	return Range{Start: start, End: end}, nil
}

// computeAssignment 返回 parallelism 并行度下每个键组的归属实例下标，
// 长度为 maxParallelism。供 Store 与测试共同使用，保证判定依据唯一。
func computeAssignment(maxParallelism, parallelism int) ([]int, error) {
	owners := make([]int, maxParallelism)
	for i := 0; i < parallelism; i++ {
		r, err := ComputeKeyGroupRange(maxParallelism, parallelism, i)
		if err != nil {
			return nil, err
		}
		for g := r.Start; g <= r.End; g++ {
			owners[g] = i
		}
	}
	return owners, nil
}
