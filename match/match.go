// Package match 在滑动窗口内用哈希链找最长匹配，候选链长度有上限。
// 依赖 window 包保存历史字节。
package match

import (
	"errors"

	"ontology/window"
)

// ErrChainLen 表示非法的链长上限（<= 0）。
var ErrChainLen = errors.New("match: chain length must be positive")

// Matcher 是哈希链匹配器：4 字节哈希 -> 位置链，链上按窗口容量剪枝。
// 非并发安全。
type Matcher struct {
	win        *window.Window
	maxChain   int
	maxMatch   int
	head       map[uint32]int64
	prev       []int64 // 按位置取模的环形前驱链，-1 表示链尾
	candidates int64   // 考察过的候选位置总数（非导出，测试经 Candidates 读取）
}

// New 创建匹配器；winCap 或 maxChain 非正时返回错误。
func New(winCap, maxChain, maxMatch int) (*Matcher, error) {
	w, err := window.New(winCap)
	if err != nil {
		return nil, err
	}
	if maxChain <= 0 {
		return nil, ErrChainLen
	}
	prev := make([]int64, winCap)
	for i := range prev {
		prev[i] = -1
	}
	return &Matcher{win: w, maxChain: maxChain, maxMatch: maxMatch,
		head: make(map[uint32]int64), prev: prev}, nil
}

// Window 返回匹配器持有的历史窗口。
func (m *Matcher) Window() *window.Window { return m.win }

// Candidates 返回至今考察过的候选位置总数。
func (m *Matcher) Candidates() int64 { return m.candidates }

func hash4(a, b, c, d byte) uint32 {
	return (uint32(a)<<24 | uint32(b)<<16 | uint32(c)<<8 | uint32(d)) * 0x9E3779B1
}

func (m *Matcher) insert(h uint32, pos int64) {
	m.prev[pos%int64(len(m.prev))] = m.head[h] - 1 // head 存 pos+1，缺省即 -1（链尾）
	m.head[h] = pos + 1
}

// Seed 把预置字典送入窗口并登记可哈希的位置；next 提供字典之后的 lookahead。
func (m *Matcher) Seed(dict, next []byte) {
	base := m.win.Total()
	for j := range dict {
		if j+4 <= len(dict)+len(next) {
			at := func(k int) byte {
				if k < len(dict) {
					return dict[k]
				}
				return next[k-len(dict)]
			}
			m.insert(hash4(at(j), at(j+1), at(j+2), at(j+3)), base+int64(j))
		}
		m.win.Append(dict[j])
	}
}

// Advance 登记位置 win.Total()（若可哈希）并把 pending[i] 追加进窗口。
func (m *Matcher) Advance(pending []byte, i int) {
	if i+4 <= len(pending) {
		m.insert(hash4(pending[i], pending[i+1], pending[i+2], pending[i+3]), m.win.Total())
	}
	m.win.Append(pending[i])
}

// Best 在 pending[i] 处找最长匹配，返回距离与长度；无匹配时长度为 0。
// 调用前窗口必须已包含 i 之前的全部字节（win.Total() == 当前绝对位置）。
func (m *Matcher) Best(pending []byte, i int) (dist, length int) {
	pos := m.win.Total()
	remain := int64(len(pending) - i)
	if remain < 4 {
		return 0, 0
	}
	base := pos - int64(i)
	get := func(abs int64) byte {
		if abs >= base {
			return pending[abs-base]
		}
		return m.win.Abs(abs)
	}
	max := remain
	if int64(m.maxMatch) < max {
		max = int64(m.maxMatch)
	}
	h := hash4(pending[i], pending[i+1], pending[i+2], pending[i+3])
	p := m.head[h] - 1
	for tries := 0; p >= 0 && pos-p <= int64(m.win.Cap()) && tries < m.maxChain; tries++ {
		m.candidates++
		k := int64(0)
		for k < max && get(p+k) == get(pos+k) {
			k++
		}
		if k > int64(length) {
			length, dist = int(k), int(pos-p)
			if k == max {
				break
			}
		}
		p = m.prev[p%int64(len(m.prev))]
	}
	return dist, length
}
