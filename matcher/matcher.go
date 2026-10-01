// Package matcher 提供流式近似子串匹配器。
package matcher

import "errors"

// 操作被拒绝时返回的哨兵错误，按构造/调用顺序只报第一个。
var (
	// ErrInvalidPattern 模式为空或长于 64 字节。
	ErrInvalidPattern = errors.New("matcher: pattern must be 1 to 64 bytes")
	// ErrInvalidThreshold 阈值 k 小于 0 或不小于模式长度。
	ErrInvalidThreshold = errors.New("matcher: k must satisfy 0 <= k < len(pattern)")
	// ErrClosed 在匹配器已关闭之后调用 Feed 或再次 Close。
	ErrClosed = errors.New("matcher: matcher is closed")
)

// Match 是一条报告：模式 P 与文本 T[Start:End] 的字节级 Levenshtein
// 距离为 Dist，且 Start 是该结束位置上取得该距离的最左起点。
type Match struct {
	End   int
	Dist  int
	Start int
}

// Stats 是 Close 后的统计快照。
type Stats struct {
	Consumed   int
	Reports    int
	Suppressed int
}

// maxPatternLen 是模式允许的最大字节数。
const maxPatternLen = 64

// cell 是一个 DP 单元：到达 P[:i] 与某段文本结尾对齐状态的最小代价，
// 以及取得该代价的最左起点。比较按 (dist, start) 字典序。
type cell struct {
	dist  int
	start int
}

// Matcher 是线程安全的流式近似子串匹配器。
//
// DP 状态只保留两行（长度 m+1），与已消费字节数无关；连续命中段也只
// 维护段内代表这一个 cell 与段起点，状态大小 O(m)，不随段长增长。
type Matcher struct {
	mu chan struct{}

	pattern []byte
	k       int
	closed  bool

	consumed int
	reports  int
	suppress int

	// 动态规划：prev[i] 表示处理到上一文本字节后，匹配 P[:i] 的最优单元。
	prev []cell
	cur  []cell

	// 非导出计数器：被更新的 DP 单元数，恰为 m 乘以已消费字节数。
	cellUpdates int

	// 连续命中段的增量状态。
	segOpen bool
	segRep  Match // 段内代表：Dist 最小，并列时 End 最小

	// 已报告匹配的最大 End；代表 Start < lastEnd 时被抑制。
	lastEnd int
}

// New 创建匹配器：P 长度 m 必须在 1..64 之间，k 必须满足 0 <= k < m。
// 被拒绝时不产生任何匹配器状态。
func New(pattern []byte, k int) (*Matcher, error) {
	m := len(pattern)
	if m == 0 || m > maxPatternLen {
		return nil, ErrInvalidPattern
	}
	if k < 0 || k >= m {
		return nil, ErrInvalidThreshold
	}

	match := &Matcher{
		mu:      make(chan struct{}, 1),
		pattern: append([]byte(nil), pattern...),
		k:       k,
		prev:    make([]cell, m+1),
		cur:     make([]cell, m+1),
	}
	// 初始文本列 e=0（尚未消费字节）：
	// prev[0] = (0, 0)：空模式前缀与空文本区间 T[0:0) 距离 0、起点 0；
	// prev[i] = (i, 0)：P[:i] 与空文本区间 T[0:0) 距离 i、起点 0。
	for i := 1; i <= m; i++ {
		match.prev[i] = cell{dist: i, start: 0}
	}
	match.mu <- struct{}{}
	return match, nil
}

// better 按 (dist, start) 字典序报告 c 是否严格优于 d。
func better(c, d cell) bool {
	return c.dist < d.dist || (c.dist == d.dist && c.start < d.start)
}

// Feed 消费一个字节块，返回本次块内结束的各连续命中段的报告，按 End 升序。
// 空块合法：不产生报告也不改变状态。已关闭后调用返回 ErrClosed。
func (m *Matcher) Feed(chunk []byte) ([]Match, error) {
	<-m.mu
	defer func() { m.mu <- struct{}{} }()

	if m.closed {
		return nil, ErrClosed
	}

	var out []Match
	pat := m.pattern
	plen := len(pat)

	for _, b := range chunk {
		// 新文本列首行（i=0）：空模式前缀对空文本区间 T[e:e)，
		// 距离 0、起点 e。其余单元先置不可达，再由三条边到达。
		m.cur[0] = cell{dist: 0, start: m.consumed + 1}
		for i := 1; i <= plen; i++ {
			m.cur[i] = cell{dist: plen + 1}
		}

		for i := 1; i <= plen; i++ {
			// 上方 cur[i-1]：删除 P[i-1]，文本区间同为 [s,e)，代价 +1、起点继承。
			best := m.cur[i-1]
			best.dist++
			// 左方 prev[i]：插入文本字节 T[e-1]，区间 [s,e-1)→[s,e)，代价 +1、起点继承。
			ins := m.prev[i]
			ins.dist++
			if better(ins, best) {
				best = ins
			}
			// 对角 prev[i-1]：匹配或替换 P[i-1] 与 T[e-1]，起点继承。
			sub := m.prev[i-1]
			if b != pat[i-1] {
				sub.dist++ // 替换（相等即匹配，代价不变）
			}
			if better(sub, best) {
				best = sub
			}
			m.cur[i] = best
			m.cellUpdates++
		}

		m.consumed++
		m.prev, m.cur = m.cur, m.prev

		// 以结束位置 e=consumed 结尾的最优子串。
		hit := m.prev[plen]
		if hit.dist <= m.k {
			rep := Match{End: m.consumed, Dist: hit.dist, Start: hit.start}
			if !m.segOpen {
				m.segOpen = true
				m.segRep = rep
			} else if rep.Dist < m.segRep.Dist {
				// Dist 并列时保留先进入段的代表，即 End 最小者。
				m.segRep = rep
			}
			continue
		}

		// D(e) > k：若有打开的段，该段在此刻结束。
		if m.segOpen {
			out = m.emitSegment(out)
		}
	}

	return out, nil
}

// emitSegment 结束当前段并应用抑制规则，把保留的代表追加到 out。
func (m *Matcher) emitSegment(out []Match) []Match {
	rep := m.segRep
	m.segOpen = false
	m.segRep = Match{}

	if rep.Start < m.lastEnd {
		m.suppress++
		return out
	}
	out = append(out, rep)
	m.reports++
	if rep.End > m.lastEnd {
		m.lastEnd = rep.End
	}
	return out
}

// Close 结束流，返回流末尾仍开着的命中段的报告（若有）。
// 首次调用成功；再次调用返回 ErrClosed。
func (m *Matcher) Close() ([]Match, error) {
	<-m.mu
	defer func() { m.mu <- struct{}{} }()

	if m.closed {
		return nil, ErrClosed
	}
	m.closed = true

	var out []Match
	if m.segOpen {
		out = m.emitSegment(out)
	}
	return out, nil
}

// Stats 返回消耗字节数、报告条数与被抑制条数；任何时刻均可并发查询。
func (m *Matcher) Stats() Stats {
	<-m.mu
	defer func() { m.mu <- struct{}{} }()
	return Stats{
		Consumed:   m.consumed,
		Reports:    m.reports,
		Suppressed: m.suppress,
	}
}

// cellUpdateCount 返回非导出 DP 单元更新计数，仅供本包测试断言。
func (m *Matcher) cellUpdateCount() int {
	<-m.mu
	defer func() { m.mu <- struct{}{} }()
	return m.cellUpdates
}
