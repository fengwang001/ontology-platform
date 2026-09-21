package mux

import (
	"sync"
	"time"
)

// Stats 汇总匹配器的计数。除 Pending 外均为累计值。
type Stats struct {
	Pending   int // 当前在等的请求数
	Orphans   int // 收到但从未注册过的响应数（累计）
	Late      int // 认领者已离开后才到的响应数（累计）
	Delivered int // 成功派发数（累计）
}

// Mux 是请求-响应关联匹配器。零值不可用，请用 New 构造。
type Mux struct {
	mu      sync.Mutex
	now     func() time.Time
	pending map[string]*waiter
	known   map[string]bool // 曾经注册过的 id，用于区分 Late 与 Orphans
	closed  bool

	delivered   int
	orphans     int
	late        int
	timedOut    int
	interrupted int
}

// New 创建一个匹配器。now 为可注入时钟，传 nil 时使用 time.Now。
func New(now func() time.Time) *Mux {
	if now == nil {
		now = time.Now
	}
	return &Mux{
		now:     now,
		pending: make(map[string]*waiter),
		known:   make(map[string]bool),
	}
}

// Stats 返回当前计数快照。
func (m *Mux) Stats() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Stats{
		Pending:   len(m.pending),
		Orphans:   m.orphans,
		Late:      m.late,
		Delivered: m.delivered,
	}
}
