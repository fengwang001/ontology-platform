// Package dv 实现距离向量路由表（RIP 风格）。
//
// 链路代价恒为 1，度量上限为 16，16 一律表示不可达。
// 所有携带时刻的操作（Receive、Sweep）使用调用方注入的时刻，
// 保证相同操作序列重放结果完全相同，并据此检测时钟回拨。
package dv

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Infinity 是不可达度量。任何表项度量恒不超过该值。
const Infinity = 16

var (
	// ErrClockRollback 表示操作携带的时刻早于已见过的最大时刻。
	ErrClockRollback = errors.New("dv: clock rollback")
	// ErrEmptyNeighbor 表示邻居名为空。
	ErrEmptyNeighbor = errors.New("dv: empty neighbor")
	// ErrEmptyPrefix 表示前缀为空。
	ErrEmptyPrefix = errors.New("dv: empty prefix")
	// ErrMetricOutOfRange 表示通告度量超出 [0, 16]。
	ErrMetricOutOfRange = errors.New("dv: metric out of range [0,16]")
)

// Entry 是一条路由表项。每个前缀至多一条。
type Entry struct {
	Prefix  string
	NextHop string
	Metric  int
	// UpdatedAt 是最近一次接受通告（或建项）的时刻。
	UpdatedAt time.Time
	// Garbage 为真表示表项处于垃圾保持期，Metric 恒为 16。
	Garbage bool
	// GarbageStart 是进入垃圾期的起点，仅在 Garbage 为真时有效。
	GarbageStart time.Time
}

// Advertisement 是发往某邻居的一条通告。
type Advertisement struct {
	Prefix string
	Metric int
}

// Table 是并发安全的距离向量路由表。
type Table struct {
	mu          sync.Mutex
	timeout     time.Duration // T：路由超时
	garbageHold time.Duration // G：垃圾保持时间
	entries     map[string]*Entry
	lastTime    time.Time
	hasLastTime bool
}

// New 创建路由表，timeout 为路由超时 T，garbageHold 为垃圾保持时间 G。
func New(timeout, garbageHold time.Duration) *Table {
	return &Table{
		timeout:     timeout,
		garbageHold: garbageHold,
		entries:     make(map[string]*Entry),
	}
}

// checkClock 校验时刻不回拨；被拒绝的操作不得改变表项与更新时刻。
func (t *Table) checkClock(now time.Time) error {
	if t.hasLastTime && now.Before(t.lastTime) {
		return fmt.Errorf("%w: now=%s last=%s", ErrClockRollback,
			now.Format(time.RFC3339Nano), t.lastTime.Format(time.RFC3339Nano))
	}
	return nil
}

func (t *Table) advanceClock(now time.Time) {
	t.lastTime = now
	t.hasLastTime = true
}

// Receive 处理邻居 neighbor 对前缀 prefix 通告度量 metric（0 至 16）。
//
// 拒绝顺序（只报第一个）：时钟回拨、邻居或前缀为空、度量超出范围。
// 新度量 c = min(metric+1, 16)。接受规则：
//   - 无表项：仅 c < 16 才建项；
//   - neighbor 是当前下一跳：总是接受 c 并刷新更新时刻，c==16 时转垃圾
//     （原本已是垃圾则垃圾起点不变），c<16 时垃圾项恢复有效；
//   - neighbor 不是当前下一跳：仅当 c 严格小于现有度量才替换并刷新，
//     等值不替换。
func (t *Table) Receive(now time.Time, neighbor, prefix string, metric int) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if err := t.checkClock(now); err != nil {
		return err
	}
	if neighbor == "" {
		return ErrEmptyNeighbor
	}
	if prefix == "" {
		return ErrEmptyPrefix
	}
	if metric < 0 || metric > Infinity {
		return fmt.Errorf("%w: got %d", ErrMetricOutOfRange, metric)
	}
	t.advanceClock(now)

	c := metric + 1
	if c > Infinity {
		c = Infinity
	}

	e, ok := t.entries[prefix]
	if !ok {
		if c < Infinity {
			t.entries[prefix] = &Entry{
				Prefix:    prefix,
				NextHop:   neighbor,
				Metric:    c,
				UpdatedAt: now,
			}
		}
		return nil
	}

	if e.NextHop == neighbor {
		e.Metric = c
		e.UpdatedAt = now
		if c == Infinity {
			if !e.Garbage {
				e.Garbage = true
				e.GarbageStart = now
			}
		} else {
			e.Garbage = false
			e.GarbageStart = time.Time{}
		}
		return nil
	}

	if c < e.Metric {
		e.NextHop = neighbor
		e.Metric = c
		e.UpdatedAt = now
		e.Garbage = false
		e.GarbageStart = time.Time{}
	}
	return nil
}

// Sweep 在时刻 now 整理表项，可对同一表项连续完成两步：
//  1. 有效表项 UpdatedAt+T 不晚于 now：转垃圾，度量置 16，
//     垃圾起点为 UpdatedAt+T（而非 now）；
//  2. 垃圾起点+G 不晚于 now：删除该表项。
func (t *Table) Sweep(now time.Time) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if err := t.checkClock(now); err != nil {
		return err
	}
	t.advanceClock(now)

	for prefix, e := range t.entries {
		if !e.Garbage {
			if deadline := e.UpdatedAt.Add(t.timeout); !deadline.After(now) {
				e.Garbage = true
				e.Metric = Infinity
				e.GarbageStart = deadline
			}
		}
		if e.Garbage && !e.GarbageStart.Add(t.garbageHold).After(now) {
			delete(t.entries, prefix)
		}
	}
	return nil
}

// Lookup 查询前缀的下一跳，只返回度量小于 16 的有效（非垃圾）表项。
func (t *Table) Lookup(prefix string) (nextHop string, metric int, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	e, ok := t.entries[prefix]
	if !ok || e.Garbage || e.Metric >= Infinity {
		return "", 0, false
	}
	return e.NextHop, e.Metric, true
}

// Advertise 为邻居 x 生成通告：每个未删除表项一条，按前缀升序。
// 水平分割加毒性逆转：下一跳是 x 或表项处于垃圾期则度量为 16，
// 否则为表项度量。
func (t *Table) Advertise(x string) []Advertisement {
	t.mu.Lock()
	defer t.mu.Unlock()

	ads := make([]Advertisement, 0, len(t.entries))
	for _, e := range t.entries {
		m := e.Metric
		if e.NextHop == x || e.Garbage {
			m = Infinity
		}
		ads = append(ads, Advertisement{Prefix: e.Prefix, Metric: m})
	}
	sort.Slice(ads, func(i, j int) bool { return ads[i].Prefix < ads[j].Prefix })
	return ads
}

// Snapshot 返回全部未删除表项的副本，按前缀升序。主要用于测试与调试。
func (t *Table) Snapshot() []Entry {
	t.mu.Lock()
	defer t.mu.Unlock()

	out := make([]Entry, 0, len(t.entries))
	for _, e := range t.entries {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Prefix < out[j].Prefix })
	return out
}
