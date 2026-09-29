// Package session 提供按活动间隙切分会话窗口的切分器。
//
// 切分规则：同一键下两个事件时间差绝对值不超过间隙阈值（gap）即相连，
// 会话是相连关系的连通分量；恰好等于间隙时仍属同一会话（闭区间判定）。
// 乱序到达的事件插入后：与左右两侧会话都相连则合并两侧，只与一侧相连则
// 并入该侧，两侧都不连则自成新会话；相同时刻的事件归入同一会话。
//
// 由于会话集合只取决于事件时间集合（连通分量），与到达顺序无关，因此
// 同一批事件按任意顺序到达，切分结果始终一致且可复现。
//
// Partitioner 的所有方法均可并发调用：写入串行化，查询与自检可并发执行。
package session

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// 可区分的拒绝原因，调用方可用 errors.Is 判定。
var (
	// ErrInvalidGap 间隙阈值非正。
	ErrInvalidGap = errors.New("session: gap must be positive")
	// ErrInvalidCapacity 键容量上限非正。
	ErrInvalidCapacity = errors.New("session: capacity must be positive")
	// ErrEmptyKey 事件键为空。
	ErrEmptyKey = errors.New("session: event key must not be empty")
	// ErrCapacityExceeded 引入的新键超出容量上限。
	ErrCapacityExceeded = errors.New("session: key capacity exceeded")
)

// Event 是一条输入事件：Key 为分组键，Time 为事件时间戳。
type Event struct {
	Key  string
	Time int64
}

// Session 是某个键下的一个会话窗口，[Start, End] 为闭区间，
// Count 为归入该会话的事件数（含相同时刻的重复事件）。
type Session struct {
	Key   string
	Start int64
	End   int64
	Count int
}

// span 是内部的会话区间，同一键下各 span 按 start 升序排列，
// 且任意相邻两个 span 满足 next.start - prev.end > gap（否则应已合并）。
type span struct {
	start int64
	end   int64
	count int
}

// Partitioner 按活动间隙把每个键的事件切分成会话窗口。
type Partitioner struct {
	mu       sync.RWMutex
	gap      int64
	capacity int
	total    int64 // 已成功摄入的事件总数，用于自检
	data     map[string][]span
}

// NewPartitioner 创建切分器。gap 为间隙阈值（必须为正），
// capacity 为允许的最大键数量（必须为正）。
func NewPartitioner(gap int64, capacity int) (*Partitioner, error) {
	if gap <= 0 {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidGap, gap)
	}
	if capacity <= 0 {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidCapacity, capacity)
	}
	return &Partitioner{
		gap:      gap,
		capacity: capacity,
		data:     make(map[string][]span),
	}, nil
}

// Gap 返回间隙阈值。
func (p *Partitioner) Gap() int64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.gap
}

// Capacity 返回键容量上限。
func (p *Partitioner) Capacity() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.capacity
}

// Add 原子地摄入一批事件：先整批校验，任一事件非法（空键、
// 引入的新键超出容量上限）则整批拒绝且不改变任何状态。
func (p *Partitioner) Add(events ...Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	newKeys := make(map[string]struct{})
	for _, e := range events {
		if e.Key == "" {
			return fmt.Errorf("%w: time=%d", ErrEmptyKey, e.Time)
		}
		if _, ok := p.data[e.Key]; !ok {
			newKeys[e.Key] = struct{}{}
		}
	}
	if len(p.data)+len(newKeys) > p.capacity {
		return fmt.Errorf("%w: existing=%d new=%d capacity=%d",
			ErrCapacityExceeded, len(p.data), len(newKeys), p.capacity)
	}

	for _, e := range events {
		p.insert(e.Key, e.Time)
		p.total++
	}
	return nil
}

// insert 把一个事件时间并入该键的会话区间。调用方须持有写锁。
func (p *Partitioner) insert(key string, t int64) {
	spans := p.data[key]

	// idx 为第一个 start > t 的区间下标：spans[idx-1] 是左邻，spans[idx] 是右邻。
	idx := sort.Search(len(spans), func(i int) bool { return spans[i].start > t })

	var left, right *span
	if idx > 0 {
		left = &spans[idx-1]
	}
	if idx < len(spans) {
		right = &spans[idx]
	}

	// t 落在左邻区间内时 t-left.end <= 0，自然相连；相同时刻差为 0 也相连。
	leftConnected := left != nil && t-left.end <= p.gap
	rightConnected := right != nil && right.start-t <= p.gap

	switch {
	case leftConnected && rightConnected:
		// 与两侧都相连：合并为一个大区间。
		merged := span{start: left.start, end: right.end, count: left.count + right.count + 1}
		out := make([]span, 0, len(spans)-1)
		out = append(out, spans[:idx-1]...)
		out = append(out, merged)
		out = append(out, spans[idx+1:]...)
		spans = out
	case leftConnected:
		if t > left.end {
			left.end = t
		}
		left.count++
	case rightConnected:
		// right.start > t 恒成立，直接左扩。
		right.start = t
		right.count++
	default:
		// 两侧都不连：自成新会话。
		out := make([]span, 0, len(spans)+1)
		out = append(out, spans[:idx]...)
		out = append(out, span{start: t, end: t, count: 1})
		out = append(out, spans[idx:]...)
		spans = out
	}
	p.data[key] = spans
}

// Sessions 返回指定键的会话列表（按开始时间升序）。未知键返回空列表。
func (p *Partitioner) Sessions(key string) []Session {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return sessionsOf(key, p.data[key])
}

// Keys 返回当前所有键（按字典序升序，保证结果可复现）。
func (p *Partitioner) Keys() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	keys := make([]string, 0, len(p.data))
	for k := range p.data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// AllSessions 返回所有键的会话列表（每个键内按开始时间升序）。
func (p *Partitioner) AllSessions() map[string][]Session {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(map[string][]Session, len(p.data))
	for k, spans := range p.data {
		out[k] = sessionsOf(k, spans)
	}
	return out
}

func sessionsOf(key string, spans []span) []Session {
	out := make([]Session, len(spans))
	for i, s := range spans {
		out[i] = Session{Key: key, Start: s.start, End: s.end, Count: s.count}
	}
	return out
}

// Check 自检内部不变量，返回 nil 表示状态一致：
//   - 每个键的区间按开始时间严格升序且 start <= end；
//   - 相邻区间不相连（间隔大于 gap，否则应已合并）；
//   - 区间事件计数与已摄入事件总数一致；
//   - 键数量不超过容量上限。
func (p *Partitioner) Check() error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if len(p.data) > p.capacity {
		return fmt.Errorf("session: key count %d exceeds capacity %d", len(p.data), p.capacity)
	}
	var total int64
	for key, spans := range p.data {
		if key == "" {
			return errors.New("session: empty key present")
		}
		if len(spans) == 0 {
			return fmt.Errorf("session: key %q has no spans", key)
		}
		for i, s := range spans {
			if s.start > s.end {
				return fmt.Errorf("session: key %q span %d inverted: [%d,%d]", key, i, s.start, s.end)
			}
			if s.count < 1 {
				return fmt.Errorf("session: key %q span %d has non-positive count %d", key, i, s.count)
			}
			if i > 0 && s.start-spans[i-1].end <= p.gap {
				return fmt.Errorf("session: key %q spans %d and %d should have merged (gap=%d): [%d,%d] [%d,%d]",
					key, i-1, i, p.gap, spans[i-1].start, spans[i-1].end, s.start, s.end)
			}
			total += int64(s.count)
		}
	}
	if total != p.total {
		return fmt.Errorf("session: event count mismatch: spans hold %d, ingested %d", total, p.total)
	}
	return nil
}
