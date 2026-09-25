// Package chain 维护单个 key 的 MVCC 版本链：按 ts 升序、只插不改、as-of 前驱查找。
package chain

import (
	"fmt"
	"sort"
	"sync/atomic"

	"ontology/ver"
)

// Chain 是单个 key 的版本链。版本一旦插入永不修改或删除（MVCC 不可变性）。
// 并发安全仅覆盖 AsOf/Snapshot；Insert 需调用方外部串行化。
type Chain struct {
	vs       []ver.Version // 始终按 ts 升序
	compared atomic.Int64  // 最近一次 AsOf 比较过的版本个数（非导出，不进公开接口）
}

// Insert 把版本 v 插入链中保持升序的正确位置；ts 可乱序到达。
// 只插入新版本，从不改写已有版本。
func (c *Chain) Insert(v ver.Version) {
	i := sort.Search(len(c.vs), func(i int) bool { return !c.vs[i].Less(v) })
	c.vs = append(c.vs, ver.Version{})
	copy(c.vs[i+1:], c.vs[i:])
	c.vs[i] = v
}

// AsOf 返回链中 ts <= T（含等于）的最新版本；该版本是 tombstone
// 或不存在这样的版本时，ok 为 false。二分查找前驱，比较次数 O(log n)。
func (c *Chain) AsOf(T int64) (v ver.Version, ok bool) {
	n := int64(0)
	i := sort.Search(len(c.vs), func(i int) bool {
		n++
		return c.vs[i].TS > T
	})
	c.compared.Store(n)
	if i == 0 {
		return ver.Version{}, false
	}
	v = c.vs[i-1]
	if v.Del {
		return ver.Version{}, false
	}
	return v, true
}

// Snapshot 返回整条链的副本（ts 升序）。返回副本，调用方改不动内部状态。
func (c *Chain) Snapshot() []ver.Version {
	out := make([]ver.Version, len(c.vs))
	copy(out, c.vs)
	return out
}

// maxCompares 是与链长无关的比较次数上界（2^64 个版本也只需 64 次）。
const maxCompares = 64

// SelfCheckCompares 构造 m=100..10000 的链做 AsOf，核验比较次数不随 m 增长。
// 只返回成败，不泄露计数器数值。
func SelfCheckCompares() error {
	for _, m := range []int{100, 1000, 10000} {
		c := &Chain{}
		for ts := 1; ts <= m; ts++ {
			c.Insert(ver.ValueVersion(int64(ts), "v"))
		}
		for _, T := range []int64{1, int64(m / 2), int64(m)} {
			if _, ok := c.AsOf(T); !ok {
				return fmt.Errorf("m=%d T=%d: 应存在", m, T)
			}
			if got := c.compared.Load(); got > maxCompares {
				return fmt.Errorf("m=%d T=%d: 比较 %d 次，超过上界 %d", m, T, got, maxCompares)
			}
		}
	}
	return nil
}
