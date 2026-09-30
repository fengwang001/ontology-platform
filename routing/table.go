// Package routing 实现距离向量路由表（RIP 风格）。
//
// 路由表从邻居通告学习各前缀的路由，按超时与垃圾保持时间老化路由，
// 并按水平分割加毒性逆转生成对外通告。度量恒不超过 16，16 一律表示不可达。
package routing

import (
	"errors"
	"sort"
	"sync"
	"time"
)

// Infinity 表示不可达度量。
const Infinity = 16

// 拒绝原因，按此顺序只报第一个。
var (
	ErrClockBackward = errors.New("routing: 时钟回拨")
	ErrEmptyNeighbor = errors.New("routing: 邻居为空")
	ErrEmptyPrefix   = errors.New("routing: 前缀为空")
	ErrMetricRange   = errors.New("routing: 通告度量超出 0 至 16")
)

// Entry 是单条路由表项。
type Entry struct {
	Prefix  string
	Metric  int
	NextHop string
}

// Advertisement 是一条对外通告。
type Advertisement struct {
	Prefix string
	Metric int
}

// Table 是并发安全的距离向量路由表。
type Table struct {
	mu        sync.Mutex
	timeout   time.Duration
	garbage   time.Duration
	lastClock time.Time
	entries   map[string]*entry
}

type entry struct {
	metric       int
	nextHop      string
	updatedAt    time.Time
	garbage      bool
	garbageSince time.Time
}

// New 创建路由表，t 为路由超时，g 为垃圾保持时间。
func New(t, g time.Duration) *Table {
	return &Table{
		timeout: t,
		garbage: g,
		entries: make(map[string]*entry),
	}
}

// Receive 处理邻居 n 对前缀 p 通告度量 m，now 为当前时刻。
func (t *Table) Receive(now time.Time, n, p string, m int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.check(now, n, p, m); err != nil {
		return err
	}
	t.lastClock = now
	c := m + 1
	if c > Infinity {
		c = Infinity
	}
	e, ok := t.entries[p]
	if !ok {
		if c < Infinity {
			t.entries[p] = &entry{metric: c, nextHop: n, updatedAt: now}
		}
		return nil
	}
	if n == e.nextHop {
		// 当前下一跳的通告总是接受（含变大变小），并刷新更新时刻。
		e.metric = c
		e.updatedAt = now
		if c == Infinity {
			if !e.garbage {
				e.garbage = true
				e.garbageSince = now
			}
		} else {
			e.garbage = false
			e.garbageSince = time.Time{}
		}
		return nil
	}
	if c < e.metric {
		// 其他邻居仅当严格更优才替换，等值不替换；垃圾项由此恢复有效。
		e.metric = c
		e.nextHop = n
		e.updatedAt = now
		e.garbage = false
		e.garbageSince = time.Time{}
	}
	return nil
}

// check 按固定顺序校验，只报第一个错误；被拒绝的操作不改变表项与时刻。
func (t *Table) check(now time.Time, n, p string, m int) error {
	if now.Before(t.lastClock) {
		return ErrClockBackward
	}
	if n == "" {
		return ErrEmptyNeighbor
	}
	if p == "" {
		return ErrEmptyPrefix
	}
	if m < 0 || m > Infinity {
		return ErrMetricRange
	}
	return nil
}

// Sweep 在 now 时刻整理表项：超时转垃圾，垃圾过期删除。
func (t *Table) Sweep(now time.Time) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if now.Before(t.lastClock) {
		return ErrClockBackward
	}
	t.lastClock = now
	for p, e := range t.entries {
		if !e.garbage && !e.updatedAt.Add(t.timeout).After(now) {
			// 转垃圾：垃圾起点为「更新时刻加 T」而非现在。
			e.garbage = true
			e.metric = Infinity
			e.garbageSince = e.updatedAt.Add(t.timeout)
		}
		if e.garbage && !e.garbageSince.Add(t.garbage).After(now) {
			// 一次整理可对同一表项连续完成两步。
			delete(t.entries, p)
		}
	}
	return nil
}

// NextHop 查询前缀 p 的下一跳，只返回度量小于 16 的有效表项。
func (t *Table) NextHop(p string) (Entry, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.entries[p]
	if !ok || e.garbage || e.metric >= Infinity {
		return Entry{}, false
	}
	return Entry{Prefix: p, Metric: e.metric, NextHop: e.nextHop}, true
}

// Advertisements 为邻居 x 生成通告，按前缀升序。
// 下一跳是 x 或表项为垃圾则度量为 16（水平分割加毒性逆转）。
func (t *Table) Advertisements(x string) []Advertisement {
	t.mu.Lock()
	defer t.mu.Unlock()
	prefixes := make([]string, 0, len(t.entries))
	for p := range t.entries {
		prefixes = append(prefixes, p)
	}
	sort.Strings(prefixes)
	ads := make([]Advertisement, 0, len(prefixes))
	for _, p := range prefixes {
		e := t.entries[p]
		metric := e.metric
		if e.nextHop == x || e.garbage {
			metric = Infinity
		}
		ads = append(ads, Advertisement{Prefix: p, Metric: metric})
	}
	return ads
}
