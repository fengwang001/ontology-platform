// Package sdot 用两指针归并计算规范形稀疏向量的点积，只访问非零条目。
package sdot

import (
	"sync/atomic"

	"ontology/spv"
)

// dotCounter 是一次 Dot 调用私有的访问计数器；字段非导出，
// 不进入任何公开接口，白盒测试经同包函数 dotAccesses 读取。
type dotCounter struct {
	seen atomic.Int64
}

func (c *dotCounter) add(n int64) { c.seen.Add(n) }

// Dot 返回 a·b。归并过程中每个非零条目恰好被消费一次，
// 访问条目数恒为 nnz(a)+nnz(b)，与稠密维度 m 无关。
// Dot 只读，多个 goroutine 可并发对同一向量调用。
func Dot(a, b *spv.Vec) float64 {
	sum, _ := dotAccesses(a, b)
	return sum
}

// dotAccesses 执行归并并返回（点积, 访问条目数），仅供同包测试使用。
func dotAccesses(a, b *spv.Vec) (float64, int64) {
	ai, av := a.Snapshot()
	bi, bv := b.Snapshot()
	var c dotCounter
	i, j, sum := 0, 0, 0.0
	for i < len(ai) && j < len(bi) {
		switch {
		case ai[i] == bi[j]:
			sum += av[i] * bv[j]
			i++
			j++
			c.add(2) // 两条目同时被消费
		case ai[i] < bi[j]:
			i++
			c.add(1)
		default:
			j++
			c.add(1)
		}
	}
	c.add(int64(len(ai) - i)) // 尾部排空：剩余条目同样被访问
	c.add(int64(len(bi) - j))
	return sum, c.seen.Load()
}
