package lab

import "sync/atomic"

// Metrics 记录关键路径上访问的申请项记录数，用于以可验证方式证明
// 签收与查询的开销只与相关集合大小相关，与历史总量无关。
type Metrics struct {
	itemsScanned atomic.Int64
}

// scan 记录访问了 n 条申请项记录。
func (m *Metrics) scan(n int) { m.itemsScanned.Add(int64(n)) }

// ItemsScanned 返回累计访问的申请项记录数。
func (m *Metrics) ItemsScanned() int64 { return m.itemsScanned.Load() }

// ResetItemsScanned 清零计数，供测试与基准在两档规模间对照。
func (m *Metrics) ResetItemsScanned() { m.itemsScanned.Store(0) }
