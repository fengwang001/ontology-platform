package iter

import "bytes"

// Merger 用堆做多路归并：键小者优先，同键按 (Level 升, Seq 降) 取新者。
// 堆中每条源同一时刻至多驻留一条条目。
type Merger struct {
	h        []item
	emitTomb bool
	compares int64
	maxResid int
}

type item struct {
	entry Entry
	src   *Source
}

// NewMerger 预取每条源的首条并建堆。emitTomb 为真时吐出删除标记（供合并用）。
func NewMerger(srcs []*Source, emitTomb bool) *Merger {
	m := &Merger{emitTomb: emitTomb}
	for _, s := range srcs {
		if s.Next() {
			m.h = append(m.h, item{entry: s.Entry(), src: s})
		}
	}
	for i := len(m.h)/2 - 1; i >= 0; i-- {
		m.siftDown(i)
	}
	m.maxResid = len(m.h)
	return m
}

func (m *Merger) less(i, j int) bool {
	m.compares++
	a, b := m.h[i], m.h[j]
	if c := bytes.Compare(a.entry.Key, b.entry.Key); c != 0 {
		return c < 0
	}
	if a.src.Level != b.src.Level {
		return a.src.Level < b.src.Level
	}
	return a.src.Seq > b.src.Seq
}

func (m *Merger) siftDown(i int) {
	for {
		l, r, best := 2*i+1, 2*i+2, i
		if l < len(m.h) && m.less(l, best) {
			best = l
		}
		if r < len(m.h) && m.less(r, best) {
			best = r
		}
		if best == i {
			return
		}
		m.h[i], m.h[best] = m.h[best], m.h[i]
		i = best
	}
}

// advanceTop 推进堆顶所属源；源耗尽则摘除堆顶。
func (m *Merger) advanceTop() {
	top := m.h[0]
	if top.src.Next() {
		m.h[0].entry = top.src.Entry()
		m.siftDown(0)
		return
	}
	m.h[0] = m.h[len(m.h)-1]
	m.h = m.h[:len(m.h)-1]
	if len(m.h) > 0 {
		m.siftDown(0)
	}
}

// Next 吐出归并后的下一条；同键只留最新版本，删除标记按配置吐出或跳过。
func (m *Merger) Next() (Entry, bool) {
	for len(m.h) > 0 {
		winner := m.h[0].entry
		key := winner.Key
		m.advanceTop()
		for len(m.h) > 0 && bytes.Equal(m.h[0].entry.Key, key) {
			m.advanceTop() // 丢弃同键旧版本
		}
		if winner.Deleted && !m.emitTomb {
			continue
		}
		return winner, true
	}
	return Entry{}, false
}

// Compares 返回堆比较次数（供上界断言）。
func (m *Merger) Compares() int64 { return m.compares }

// MaxResident 返回归并期间同时驻留内存的条目数峰值（即堆大小峰值）。
func (m *Merger) MaxResident() int { return m.maxResid }
