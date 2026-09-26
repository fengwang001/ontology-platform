// Package sdot 用两指针归并计算稀疏向量点积。
package sdot

import (
	"errors"
	"sync/atomic"

	"ontology/spv"
)

// stats 是包内单例；accessed 为非导出字段，累计各次 Dot 归并访问过的 (idx,val)
// 条目数。并发 Dot 共享此字段，故只经原子操作读写；其数值不出现在任何公开接口里。
var stats struct {
	accessed atomic.Int64
}

// Dot 返回 a·b。只访问两个向量的非零条目，复杂度 O(lenA+lenB)，与稠密维度 m 无关。
// 每个非零条目恰被「消费」一次，故一次调用新增的计数恒为 lenA+lenB。
func Dot(a, b *spv.Vec) float64 {
	ia, va := a.Snapshot()
	ib, vb := b.Snapshot()
	var sum float64
	i, j := 0, 0
	la, lb := len(ia), len(ib)
	for i < la && j < lb {
		switch {
		case ia[i] == ib[j]:
			sum += va[i] * vb[j]
			stats.accessed.Add(2) // 两侧各消费一个条目
			i++
			j++
		case ia[i] < ib[j]:
			stats.accessed.Add(1) // 只前进较小下标一侧
			i++
		default:
			stats.accessed.Add(1)
			j++
		}
	}
	// 归并结束后较长一侧的尾部条目不可能再匹配，计入访问数即可。
	stats.accessed.Add(int64(la - i + lb - j))
	return sum
}

// accessedCount 返回累计访问数。非导出：仅供同包白盒测试读取，
// 用「调用后 − 调用前」的差值度量单次 Dot 的访问条目数。
func accessedCount() int64 { return stats.accessed.Load() }

// SelfCheck 用内置向量核验不变量 3：单次 Dot 的访问计数恒为两侧非零条目数之和，
// 不随稠密维度 m 增长。只返回成败，计数数值不进入任何公开接口。
func SelfCheck() error {
	const nnz = 10
	for _, m := range []int{100, 1000, 10000} {
		a := spv.New(m)
		b := spv.New(m)
		for k := 0; k < nnz; k++ {
			if err := a.Set((k*7+1)%m, float64(k+1)); err != nil {
				return err
			}
			if err := b.Set((k*13+2)%m, float64(k+2)); err != nil {
				return err
			}
		}
		before := accessedCount()
		Dot(a, b)
		if got := accessedCount() - before; got != 2*nnz {
			return errors.New("sdot: access count depends on dense dimension m")
		}
	}
	return nil
}
