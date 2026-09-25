// Package mon 维护已完成请求的聚合统计与最近 W 个完成的滑动窗口告警。
// 依赖方向：mon -> sla，不允许反向依赖。
package mon

import (
	"errors"
	"sync"

	"ontology/sla"
)

// ErrInvalidConfig 表示构造参数非法：w<1、k<1、k>w 或阈值为负。
var ErrInvalidConfig = errors.New("mon: invalid config: t<0 or w<1 or k<1 or k>w")

// Monitor 并发安全地维护统计量与告警窗口。零值不可用，须用 New 构造。
type Monitor struct {
	threshold int64
	w, k      int

	mu sync.RWMutex

	count      int64 // 已完成请求总数
	violations int64 // 违例总数
	inFlight   int64 // 已 Begin 未 End
	sum        int64 // 延迟之和
	min, max   int64 // 最小/最大延迟（无完成时为 0）

	ring       []bool // 环形缓冲，true 表示该槽位是一次违例
	head       int    // 下一个写入（覆盖最旧）的槽位
	filled     int    // 已填充槽位数，区间 [0,w]
	windowViol int64  // 窗口内运行违例计数

	// checks 为非导出计数器：最近一次 Complete 为更新告警窗口
	// 而检查的完成记录个数。环形缓冲 O(1) 更新时恒 <= 2，
	// 重扫窗口会是 w、重扫全表会是 m。只能由同包白盒测试读取。
	checks int
}

// New 以阈值 t、窗口大小 w、告警阈值 k 构造 Monitor；参数非法返回 ErrInvalidConfig。
func New(t int64, w, k int) (*Monitor, error) {
	if t < 0 || w < 1 || k < 1 || k > w {
		return nil, ErrInvalidConfig
	}
	return &Monitor{threshold: t, w: w, k: k, ring: make([]bool, w)}, nil
}

// Begin 登记一个请求进入在途；在途请求不进入任何完成统计。
func (m *Monitor) Begin() {
	m.mu.Lock()
	m.inFlight++
	m.mu.Unlock()
}

// Complete 登记一次已确认非负的完成：离开在途、更新统计，并以环形缓冲 O(1) 滑入窗口。
// 负延迟由上层（api）拒绝；本层不改变该判定职责。
func (m *Monitor) Complete(latency int64) sla.Outcome {
	m.mu.Lock()
	defer m.mu.Unlock()
	v := sla.Classify(latency, m.threshold)

	m.count++
	m.sum += latency
	if m.count == 1 || latency < m.min {
		m.min = latency
	}
	if latency > m.max {
		m.max = latency
	}
	if v == sla.Violation {
		m.violations++
	}
	m.inFlight--

	// 滑入最新一条；窗口已满时同时滑出被覆盖的最旧一条。
	m.checks = 1
	if m.filled == m.w {
		m.checks = 2
		if m.ring[m.head] {
			m.windowViol--
		}
	} else {
		m.filled++
	}
	m.ring[m.head] = v == sla.Violation
	if m.ring[m.head] {
		m.windowViol++
	}
	m.head = (m.head + 1) % m.w
	return v
}

// Count 返回已完成请求总数。
func (m *Monitor) Count() int64 { m.mu.RLock(); defer m.mu.RUnlock(); return m.count }

// Violations 返回违例总数。
func (m *Monitor) Violations() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.violations
}

// InFlight 返回在途请求数。
func (m *Monitor) InFlight() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.inFlight
}

// MinLatency 返回最小完成延迟；无完成时为 0。
func (m *Monitor) MinLatency() int64 { m.mu.RLock(); defer m.mu.RUnlock(); return m.min }

// MaxLatency 返回最大完成延迟；无完成时为 0。
func (m *Monitor) MaxLatency() int64 { m.mu.RLock(); defer m.mu.RUnlock(); return m.max }

// AvgLatency 返回平均完成延迟；无完成时为 0。
func (m *Monitor) AvgLatency() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.count == 0 {
		return 0
	}
	return float64(m.sum) / float64(m.count)
}

// Breached 报告最近 w 个完成（不足取全部）中违例数是否 >= k。
func (m *Monitor) Breached() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.windowViol >= int64(m.k)
}
