// Package match 在滑动窗口内用哈希链找最长匹配，候选链长度有上限。
// 依赖 window 包以窗口容量约束回指距离。单个实例不是并发安全的。
package match

import (
	"encoding/binary"
	"errors"

	"ontology/window"
)

// MinMatch 是能构成回指的最短匹配长度（哈希按 4 字节计算）。
const MinMatch = 4

// MaxLen 是单个回指的最大长度；截断使贪心解析能在缓冲内定案，
// 并使最坏情形下的候选考察数与输入规模无关。
const MaxLen = 1 << 16

// ErrZeroChain 在链长上限为 0 时返回。
var ErrZeroChain = errors.New("match: max chain must be positive")

// Matcher 维护已扫描位置的哈希链索引。data 只增不改（调用方追加后
// 以同一底层数组的更长切片重新传入）；位置在查询时按需自动入链。
type Matcher struct {
	win      *window.Window
	maxChain int
	head     map[uint32]int // 4 字节哈希 -> 最新位置
	prev     []int          // 位置 -> 链上前一个位置
	indexed  int            // 已入链位置
	cand     int64          // 考察过的候选位置总数（非导出）
}

// New 创建匹配器；win 提供回指距离上限，maxChain 为候选链长度上限。
func New(win *window.Window, maxChain int) (*Matcher, error) {
	if maxChain <= 0 {
		return nil, ErrZeroChain
	}
	return &Matcher{win: win, maxChain: maxChain, head: make(map[uint32]int)}, nil
}

// Candidates 返回考察过的候选位置总数。
func (m *Matcher) Candidates() int64 { return m.cand }

// Longest 在 pos 处找最长匹配，返回距离与长度；无匹配时长度为 0。
// 匹配长度被 MaxLen 与可用数据截断。要求 pos+MinMatch <= len(data)。
func (m *Matcher) Longest(data []byte, pos int) (dist, length int) {
	for m.indexed < pos && m.indexed+MinMatch <= len(data) {
		h := binary.LittleEndian.Uint32(data[m.indexed : m.indexed+MinMatch])
		for len(m.prev) <= m.indexed {
			m.prev = append(m.prev, -1)
		}
		m.prev[m.indexed] = m.head[h]
		m.head[h] = m.indexed
		m.indexed++
	}
	limit := min(len(data)-pos, MaxLen)
	best, bestDist := 0, 0
	h := binary.LittleEndian.Uint32(data[pos : pos+MinMatch])
	c, ok := m.head[h]
	if !ok {
		c = -1
	}
	for n := 0; c >= 0 && n < m.maxChain && pos-c <= m.win.Cap(); c, n = m.prev[c], n+1 {
		m.cand++
		if l := commonLen(data, c, pos, limit); l > best {
			best, bestDist = l, pos-c
			if best >= limit {
				break
			}
		}
	}
	if best < MinMatch {
		return 0, 0
	}
	return bestDist, best
}

// commonLen 返回 data[c:] 与 data[pos:] 的最长公共前缀，至多 limit。
// 允许 pos-c < limit 的重叠情形（与解压端逐字节复制语义一致）。
func commonLen(data []byte, c, pos, limit int) int {
	l := 0
	for l < limit && data[c+l] == data[pos+l] {
		l++
	}
	return l
}
