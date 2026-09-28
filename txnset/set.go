// Package txnset 实现事务标识集合的解析、合并、差集与规范化输出。
//
// 集合以“来源 + 事务号闭区间”表示，用于复制断点续传场景中
// “源端已执行事务集合 - 本地已执行事务集合 = 待补偿事务集合”。
// 任意写法、顺序、相互重叠或相邻的输入，经规范化后都得到唯一、
// 逐字节确定的文本结果。
//
// 所有面向 *Set 的操作均支持并发调用：Merge 之间互斥串行，
// Difference / Canonical 等读取操作使用读锁且不改变任何状态。
package txnset

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// setIDSeed 为每个 Set 分配唯一 id，供多集合操作按固定顺序加锁，
// 避免双向并发差集时锁序反转造成死锁。
var setIDSeed atomic.Uint64

// Set 是一个并发安全的、内部始终保持规范化的事务标识集合。
type Set struct {
	id      uint64
	mu      sync.RWMutex
	sources map[string][]Interval
}

// New 返回空集合。
func New() *Set {
	return &Set{id: setIDSeed.Add(1), sources: make(map[string][]Interval)}
}

// Copy 返回与接收者内容相同的独立深拷贝。
func (s *Set) Copy() *Set {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := New()
	for src, ivs := range s.sources {
		dup := make([]Interval, len(ivs))
		copy(dup, ivs)
		out.sources[src] = dup
	}
	return out
}

// Merge 解析 text 并把其中全部区间并入接收者。text 非法时
// （包括前段合法、后段非法，或区间总数超限）返回错误且接收者
// 字节级保持不变：先整体解析成功，再加锁一次性并入。
//
// 可安全并发调用；并发合并不重叠区间与一次合并结果一致，
// 同一批数据以任意切分、任意顺序输入都得到相同的规范结果。
func (s *Set) Merge(text string) error {
	parsed, err := Parse(text)
	if err != nil {
		logf("merge rejected input=%q | decision: %v", text, err)
		return err
	}

	s.mu.Lock()
	before := s.renderLocked()
	for src, add := range parsed.sources {
		if len(add) == 0 {
			continue
		}
		combined := make([]Interval, 0, len(s.sources[src])+len(add))
		combined = append(combined, s.sources[src]...)
		combined = append(combined, add...)
		s.sources[src] = mergeIntervals(combined)
	}
	after := s.renderLocked()
	s.mu.Unlock()

	logf("merge input=%q before=%q after=%q | decision: union per source, merge overlapping/adjacent intervals",
		text, before, after)
	return nil
}

// Difference 返回新集合 s - other：逐来源做闭区间减法。
// 接收者与 other 均不被修改，可与任意操作并发调用。
func (s *Set) Difference(other *Set) *Set {
	if s == other {
		logf("difference | decision: identical operands, result is empty")
		return New()
	}
	if other == nil {
		logf("difference | decision: right operand is nil, result equals left operand")
		return s.Copy()
	}

	// 按唯一 id 固定加锁顺序，避免 s.Difference(other) 与
	// other.Difference(s) 并发时锁序反转死锁。
	first, second := s, other
	if other.id < s.id {
		first, second = other, s
	}
	first.mu.RLock()
	defer first.mu.RUnlock()
	second.mu.RLock()
	defer second.mu.RUnlock()

	out := New()
	for src, a := range s.sources {
		rem := subtractIntervals(a, other.sources[src])
		if len(rem) > 0 {
			out.sources[src] = rem
		}
	}
	logf("difference left=%q right=%q -> %q | decision: closed-interval subtraction per source, states unchanged",
		s.renderLocked(), other.renderLocked(), out.Canonical())
	return out
}

// Canonical 返回规范文本：
//
//	来源按字典序升序，条目之间以 ';' 连接，形如 "a:i1;i2;b:.." 的
//	单行文本（条目即语法中的“来源:区间列表”，可被 Parse 原样解析）；
//	行内区间按起点升序，已合并重叠与相邻区间；
//	单点渲染为 "n"，闭区间渲染为 "lo-hi"；空集合返回空字符串。
//
// 同一逻辑集合无论以何种输入构造，本方法输出逐字节相同。
func (s *Set) Canonical() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.renderLocked()
}

// renderLocked 渲染规范文本，调用方须持有 s.mu（读锁或写锁）。
func (s *Set) renderLocked() string {
	if len(s.sources) == 0 {
		return ""
	}
	names := make([]string, 0, len(s.sources))
	for src := range s.sources {
		names = append(names, src)
	}
	sort.Strings(names)

	var b strings.Builder
	first := true
	for _, src := range names {
		if len(s.sources[src]) == 0 {
			continue
		}
		if !first {
			b.WriteByte(';')
		}
		first = false
		b.WriteString(src)
		b.WriteByte(':')
		for j, iv := range s.sources[src] {
			if j > 0 {
				b.WriteByte(',')
			}
			b.WriteString(formatInterval(iv))
		}
	}
	return b.String()
}

// formatInterval 按规范形式渲染单个区间。
func formatInterval(iv Interval) string {
	if iv.Lo == iv.Hi {
		return strconv.FormatUint(iv.Lo, 10)
	}
	return strconv.FormatUint(iv.Lo, 10) + "-" + strconv.FormatUint(iv.Hi, 10)
}

// String 等价于 Canonical，空集合返回空字符串。
func (s *Set) String() string {
	return s.Canonical()
}

// Len 返回不同来源的数量。
func (s *Set) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.sources)
}

// Sources 返回按字典序升序排列的来源标识副本。
func (s *Set) Sources() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.sources))
	for src := range s.sources {
		names = append(names, src)
	}
	sort.Strings(names)
	return names
}

// Intervals 返回指定来源的规范区间切片副本；
// 来源不存在或已减空时返回 nil。调用方修改返回值不影响集合。
func (s *Set) Intervals(source string) []Interval {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src := s.sources[source]
	if len(src) == 0 {
		return nil
	}
	out := make([]Interval, len(src))
	copy(out, src)
	return out
}
