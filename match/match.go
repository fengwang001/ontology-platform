// Package match 在滑动窗口内用哈希链查找最长匹配，
// 候选链长度有上限。依赖 window 包。
package match

import (
	"encoding/binary"
	"errors"

	"ontology/window"
)

// ErrBadChain 表示链长上限非法（<= 0）。
var ErrBadChain = errors.New("match: max chain must be positive")

// MinMatch 是最短匹配长度。
const MinMatch = 4

// MaxMatch 是最长匹配长度上限，保证单次查询的比较工作量有界。
const MaxMatch = 1 << 12

// Matcher 在 data 上工作：调用方先 SetData，再用 Advance 把已消费的
// 位置逐个纳入历史（窗口 + 哈希链），Longest 查询当前位置的最长匹配。
// 单个实例不是并发安全的。
type Matcher struct {
	win      *window.Window
	data     []byte
	head     map[uint32]int // 哈希 -> 最近位置+1，0 表示空
	prev     []int          // 链环：prev[pos&mask] = 前一位置+1，0 表示空
	mask     int
	maxChain int
	cands    int64 // 考察过的候选位置总数（非导出，供测试断言）
}

// New 创建匹配器。maxChain <= 0 时返回 ErrBadChain。
func New(win *window.Window, maxChain int) (*Matcher, error) {
	if maxChain <= 0 {
		return nil, ErrBadChain
	}
	size := 1
	for size < win.Cap() {
		size <<= 1
	}
	return &Matcher{
		win: win, head: make(map[uint32]int),
		prev: make([]int, size), mask: size - 1, maxChain: maxChain,
	}, nil
}

// Candidates 返回至今考察过的候选位置总数。
func (m *Matcher) Candidates() int64 { return m.cands }

// SetData 设置工作数据（可随输入增长反复调用，位置坐标保持不变）。
func (m *Matcher) SetData(data []byte) { m.data = data }

func hash(b []byte) uint32 { return binary.LittleEndian.Uint32(b) * 2654435761 }

// Advance 把位置 pos 纳入历史（写入窗口并插入哈希链）。
func (m *Matcher) Advance(pos int) {
	if pos+MinMatch <= len(m.data) {
		h := hash(m.data[pos:])
		m.prev[pos&m.mask] = m.head[h]
		m.head[h] = pos + 1
	}
	m.win.Write(m.data[pos])
}

// LoadRange 把 [start, end) 全部纳入历史（用于预置字典）。
func (m *Matcher) LoadRange(start, end int) {
	for p := start; p < end; p++ {
		m.Advance(p)
	}
}

// Longest 返回位置 pos 的最长匹配（dist, n）；无匹配时 n < MinMatch。
// 允许 dist < n 的重叠匹配：第 l 字节在 l < dist 时读窗口历史，
// 否则读本数据内已匹配的前缀，与解压侧逐字节前向复制语义一致。
func (m *Matcher) Longest(pos int) (dist, n int) {
	if pos+MinMatch > len(m.data) {
		return 0, 0
	}
	best, bestDist := 0, 0
	max := len(m.data) - pos
	if max > MaxMatch {
		max = MaxMatch
	}
	limit := m.maxChain
	for c := m.head[hash(m.data[pos:])] - 1; c >= 0 && limit > 0; {
		d := pos - c
		if d > m.win.Cap() {
			break // 链按新旧排序，更老的候选必然也超窗
		}
		limit--
		m.cands++
		l := 0
		for l < max {
			var b byte
			if l < d {
				b, _ = m.win.At(d - l) // 历史字节，必然在窗口内
			} else {
				b = m.data[pos+l-d] // 重叠段：已匹配的本轮前缀
			}
			if b != m.data[pos+l] {
				break
			}
			l++
		}
		if l > best {
			best, bestDist = l, d
			if l == max {
				break
			}
		}
		ne := m.prev[c&m.mask]
		if ne == 0 || ne-1 >= c {
			break // 链结束或槽位被更新的位置覆盖
		}
		c = ne - 1
	}
	if best < MinMatch {
		return 0, 0
	}
	return bestDist, best
}
