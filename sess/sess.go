// Package sess 是会话集合本体：接收可乱序到达的事件，增量维护每个 Key 的规范会话序列。
// 依赖只有 evt 包。所有方法均 goroutine 安全。
package sess

import (
	"errors"
	"sort"
	"sync"

	"ontology/evt"
)

// Session 是一个对外会话：闭区间 [Start,End] 与其中的事件计数 N。
type Session struct {
	Start int64
	End   int64
	N     int
}

// 两类哨兵错误；事件非法复用 evt.ErrInvalidEvent。
var (
	ErrBadGap  = errors.New("sess: gap must be positive")      // gap 非正
	ErrTooMany = errors.New("sess: too many sessions for key") // 会话数超上限
)

// Set 是按 Key 分组的会话集合。
type Set struct {
	mu   sync.RWMutex
	gap  int64
	max  int // <=0 表示不限
	keys map[string][]Session
	// cmp 记录最近一次 Add 实际比较过边界的「已有会话」个数（仅左右紧邻，最多 2）；
	// 二分定位 sort.Search 的比较不计入。非导出，不出现于任何公开接口。
	cmp int
}

// NewSet 构造集合：gap 必须为正；maxSessions<=0 表示不限制每 Key 会话数。
func NewSet(gap int64, maxSessions int) (*Set, error) {
	if gap <= 0 {
		return nil, ErrBadGap
	}
	return &Set{gap: gap, max: maxSessions, keys: map[string][]Session{}}, nil
}

// Add 增量加入单个事件。被拒（事件非法 / 超限）时不留任何痕迹。
func (s *Set) Add(e evt.Event) error {
	if !e.Valid() {
		return evt.ErrInvalidEvent
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next, cmp := insert(s.keys[e.Key], e.TS, s.gap)
	if s.max > 0 && len(next) > s.max {
		return ErrTooMany
	}
	s.keys[e.Key] = next
	s.cmp = cmp
	return nil
}

// Feed 原子地批量加入：任一事件非法或任一会导致超限，则整批不留痕。
func (s *Set) Feed(evs []evt.Event) error {
	for _, e := range evs {
		if !e.Valid() {
			return evt.ErrInvalidEvent
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	work := map[string][]Session{}
	for _, e := range evs { // 在每 Key 的副本上计算，全部成功后才提交
		cur, ok := work[e.Key]
		if !ok {
			cur = append([]Session(nil), s.keys[e.Key]...)
		}
		next, _ := insert(cur, e.TS, s.gap)
		if s.max > 0 && len(next) > s.max {
			return ErrTooMany
		}
		work[e.Key] = next
	}
	for k, v := range work {
		s.keys[k] = v
	}
	return nil
}

// Sessions 返回某 Key 的规范会话序列副本（按 start 升序），无则 nil。
func (s *Set) Sessions(key string) []Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Session(nil), s.keys[key]...)
}

// Equal 逐字段比较两个会话序列（供测试与演示使用）。
func Equal(a, b []Session) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// insert 是纯函数：把 ts 增量并入规范序列，返回新序列与本次比较过的已有会话数。
// 定位用二分；至多检查左、右紧邻各一个并按 evt 判定吸收。归纳：既有序列相邻间隔
// 恒 >gap，故无论单侧扩展还是双侧桥接，新界都不可能再触及更远的会话。
func insert(ss []Session, ts, gap int64) ([]Session, int) {
	j := sort.Search(len(ss), func(k int) bool { return ss[k].Start > ts })
	if j > 0 && ss[j-1].End >= ts { // ts 落在已有会话内部（含重复时间戳）
		ss[j-1].N++
		return ss, 1
	}
	cmp := 0
	joinL, joinR := false, false
	if j > 0 {
		cmp++
		joinL = evt.ShouldMerge(ss[j-1].End, ts, gap)
	}
	if j < len(ss) {
		cmp++
		joinR = evt.ShouldMerge(ts, ss[j].Start, gap)
	}
	switch {
	case joinL && joinR: // 桥接：左右两个会话与 ts 融成一个
		m := Session{Start: ss[j-1].Start, End: ss[j].End, N: ss[j-1].N + ss[j].N + 1}
		return append(append(ss[:j-1], m), ss[j+1:]...), cmp
	case joinL:
		ss[j-1].End, ss[j-1].N = ts, ss[j-1].N+1
	case joinR:
		ss[j].Start, ss[j].N = ts, ss[j].N+1
	default: // 两侧都够不着：新建独立会话（118 的情形）
		ss = append(ss, Session{})
		copy(ss[j+1:], ss[j:])
		ss[j] = Session{Start: ts, End: ts, N: 1}
	}
	return ss, cmp
}
