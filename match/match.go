// Package match 实现哈希链匹配器：在窗口内找最长匹配，候选链长度有上限。
// 依赖 window 包：窗口容量由 window.Window 定义。
package match

import (
	"errors"

	"ontology/window"
)

// 匹配参数常量。
const (
	MinLen   = 4       // 最小匹配长度（哈希按 4 字节计算）
	MaxLen   = 1 << 12 // 单回指最大长度
	hashBits = 15
)

// ErrBadConfig 表示窗口或链长上限非法（<=0）。
var ErrBadConfig = errors.New("match: window and max chain must be positive")

// Matcher 保存全部已见字节与哈希链。head/prev 存「位置+1」，0 表示空。
type Matcher struct {
	win   *window.Window
	chain int
	data  []byte
	head  []int
	prev  []int // 环形，下标 pos%win
	next  int   // 下一个待插入哈希的位置
	cand  int64 // 考察过的候选位置总数（非导出计数器）
}

// New 创建匹配器；win 为滑动窗口（容量即回指距离上限），chain 为候选链
// 长度上限，需 >0。
func New(win *window.Window, chain int) (*Matcher, error) {
	if win == nil || chain <= 0 {
		return nil, ErrBadConfig
	}
	return &Matcher{win: win, chain: chain, head: make([]int, 1<<hashBits), prev: make([]int, win.Cap())}, nil
}

// Window 返回匹配器使用的窗口。
func (m *Matcher) Window() *window.Window { return m.win }

// Append 追加输入字节（也用于并行压缩时先追加预置字典）。
func (m *Matcher) Append(p []byte) { m.data = append(m.data, p...) }

// Data 返回全部已追加字节。
func (m *Matcher) Data() []byte { return m.data }

// Len 返回已追加字节数。
func (m *Matcher) Len() int { return len(m.data) }

// Candidates 返回考察过的候选位置总数。
func (m *Matcher) Candidates() int64 { return m.cand }

func hash4(b []byte) int {
	v := uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
	return int((v * 2654435761) >> (32 - hashBits))
}

func (m *Matcher) insert(pos int) {
	h := hash4(m.data[pos:])
	m.prev[pos%m.win.Cap()] = m.head[h]
	m.head[h] = pos + 1
}

// Find 在 pos 处找最长匹配，返回 (dist, length)；无匹配返回 (0,0)。
// 匹配长度不超过 MaxLen 与可用数据；候选距 pos 不超过窗口容量。
func (m *Matcher) Find(pos int) (dist, length int) {
	for m.next < pos && m.next+MinLen <= len(m.data) {
		m.insert(m.next)
		m.next++
	}
	limit := len(m.data) - pos
	if limit > MaxLen {
		limit = MaxLen
	}
	if limit < MinLen {
		return 0, 0
	}
	best, bestAt := 0, -1
	for c, steps := m.head[hash4(m.data[pos:])], 0; c > 0 && steps < m.chain; steps++ {
		cand := c - 1
		if pos-cand > m.win.Cap() {
			break
		}
		m.cand++
		if m.data[cand+best] == m.data[pos+best] {
			l := 0
			for l < limit && m.data[cand+l] == m.data[pos+l] {
				l++
			}
			if l > best {
				best, bestAt = l, cand
				if best >= limit {
					break
				}
			}
		}
		n := m.prev[cand%m.win.Cap()]
		if n <= 0 || n-1 >= cand {
			break
		}
		c = n
	}
	if best >= MinLen {
		return pos - bestAt, best
	}
	return 0, 0
}
