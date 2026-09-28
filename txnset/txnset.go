package txnset

import (
	"slices"
	"strconv"
	"sync"
)

// TxnSet 是一个按来源分组、内部始终规范化的事务标识集合。
//
// 所有方法均可被并发调用：读方法（Canonical/Diff/Sources/Intervals/
// IntervalCount）之间共享读锁；写方法（Merge/MergeText）串行化。
// Merge 与 Diff 都不在持锁期间嵌套获取另一个集合的锁，因此两个集合
// 互相合并也不会死锁；Diff 不修改任何输入。
type TxnSet struct {
	mu sync.RWMutex
	// m 的每个值都已规范化（升序、互不重叠、互不相邻），调用方不得外泄。
	m map[string][]Interval
}

// New 返回空集合。
func New() *TxnSet {
	return &TxnSet{m: make(map[string][]Interval)}
}

// Merge 把 other 并入接收者，重叠或相邻区间会被合并。other 为 nil 或
// 即接收者自身时无操作（自身并集幂等）。
// 先在 other 的读锁下复制其数据并释放，再获取接收者写锁完成合并，
// 从而避免交叉加锁导致的死锁。
func (s *TxnSet) Merge(other *TxnSet) {
	if other == nil || other == s {
		return
	}
	snapshot := other.snapshot()

	s.mu.Lock()
	defer s.mu.Unlock()
	for src, ivs := range snapshot {
		s.m[src] = normalizeIntervals(append(append([]Interval{}, s.m[src]...), ivs...))
	}
	logMerge("<*TxnSet>", s.canonicalLocked(),
		"union per source, merge overlap and adjacency", nil)
}

// MergeText 解析 text 并原子地并入接收者。解析失败时直接返回错误，
// 接收者状态保持不变（前段合法条目不并入）。
func (s *TxnSet) MergeText(text string) error {
	parsed, err := Parse(text)
	if err != nil {
		logMerge(text, "", "", err)
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for src, ivs := range parsed.m {
		s.m[src] = normalizeIntervals(append(append([]Interval{}, s.m[src]...), ivs...))
	}
	canonical := s.canonicalLocked()
	logMerge(text, canonical,
		"parse fully before applying; union per source, merge overlap and adjacency", nil)
	return nil
}

// Diff 返回新集合 s - other：对每个来源做闭区间差集。
// 不修改 s 与 other；other 为 nil 时返回 s 的深拷贝。
func (s *TxnSet) Diff(other *TxnSet) *TxnSet {
	a := s.snapshot()
	var b map[string][]Interval
	if other != nil {
		b = other.snapshot()
	}

	result := &TxnSet{m: make(map[string][]Interval)}
	for src, ivsA := range a {
		rest := subtractIntervals(ivsA, b[src])
		if len(rest) > 0 {
			result.m[src] = rest
		}
	}
	logDiff(canonicalMap(a), canonicalMap(b), result.Canonical())
	return result
}

// Equal 判断两个集合是否包含完全相同的来源与区间。
func (s *TxnSet) Equal(other *TxnSet) bool {
	if s == other {
		return true
	}
	a := s.snapshot()
	b := other.snapshot()
	if len(a) != len(b) {
		return false
	}
	for src, ivsA := range a {
		ivsB, ok := b[src]
		if !ok || !slices.Equal(ivsA, ivsB) {
			return false
		}
	}
	return true
}

// Canonical 返回逐字节确定的规范文本：
// 来源按字节序升序，来源内区间按下限升序，单点渲染为 N，闭区间渲染为 N-M。
// 集合为空时返回空字符串。
func (s *TxnSet) Canonical() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.canonicalLocked()
}

// String 与 Canonical 相同，使 *TxnSet 满足 fmt.Stringer。
func (s *TxnSet) String() string { return s.Canonical() }

// Sources 返回按字节序升序排列的来源标识列表（副本）。
func (s *TxnSet) Sources() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.m))
	for src := range s.m {
		out = append(out, src)
	}
	slices.Sort(out)
	return out
}

// Intervals 返回某来源已规范化区间的副本；来源不存在或为空时返回 nil。
func (s *TxnSet) Intervals(source string) []Interval {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ivs := s.m[source]
	if len(ivs) == 0 {
		return nil
	}
	out := make([]Interval, len(ivs))
	copy(out, ivs)
	return out
}

// IntervalCount 返回规范化后的区间总数（跨所有来源）。
func (s *TxnSet) IntervalCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, ivs := range s.m {
		n += len(ivs)
	}
	return n
}

// snapshot 在读锁保护下返回内部映射的深拷贝，释放锁后可安全使用。
func (s *TxnSet) snapshot() map[string][]Interval {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string][]Interval, len(s.m))
	for src, ivs := range s.m {
		cp := make([]Interval, len(ivs))
		copy(cp, ivs)
		out[src] = cp
	}
	return out
}

func (s *TxnSet) canonicalLocked() string {
	return canonicalMap(s.m)
}

// canonicalMap 渲染内部映射：来源字节序升序，区间下限升序。
func canonicalMap(m map[string][]Interval) string {
	sources := make([]string, 0, len(m))
	for src, ivs := range m {
		if len(ivs) > 0 {
			sources = append(sources, src)
		}
	}
	slices.Sort(sources)

	size := 0
	for _, src := range sources {
		size += len(src) + 1 // src + ':'
		for _, iv := range m[src] {
			size += digits(iv.Lo)
			if iv.Lo != iv.Hi {
				size += 1 + digits(iv.Hi)
			}
		}
		size += len(m[src]) // 每个条目一个分隔逗号
	}
	if size == 0 {
		return ""
	}

	buf := make([]byte, 0, size)
	for _, src := range sources {
		for _, iv := range m[src] {
			if len(buf) > 0 {
				buf = append(buf, ',')
			}
			buf = append(buf, src...)
			buf = append(buf, ':')
			buf = strconv.AppendInt(buf, iv.Lo, 10)
			if iv.Lo != iv.Hi {
				buf = append(buf, '-')
				buf = strconv.AppendInt(buf, iv.Hi, 10)
			}
		}
	}
	return string(buf)
}

// digits 返回非负 int64 的十进制位数。
func digits(n int64) int {
	if n == 0 {
		return 1
	}
	d := 0
	for n > 0 {
		d++
		n /= 10
	}
	return d
}
