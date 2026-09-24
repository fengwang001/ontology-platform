// Package pool 实现有界蓄存：Track、按 (lastSeen, slot) 有序定位受害者、驱逐、计数。
package pool

import (
	"math/bits"
	"sort"
	"sync"

	"ontology/rk"
)

type entry struct {
	key string
	st  rk.State
}

// Pool 是容量固定的最近键蓄存，并发安全。请用 New 构造。
type Pool struct {
	mu       sync.Mutex
	n        int
	nextSlot int64
	states   map[string]rk.State
	sorted   []entry // 按 (LastSeen, Slot) 升序，首元素即驱逐受害者
	lastScan int     // 最近一次驱逐时扫描过的键个数（非导出，不进公开接口）
}

// New 构造容量为 n 的蓄存，调用方保证 n > 0。
func New(n int) *Pool {
	return &Pool{n: n, states: make(map[string]rk.State, n)}
}

// Track 上报一次活跃事件，调用方保证 key 非空、ts 非负。
// 已存在的键只刷新 recency；新键在满员时先驱逐 (lastSeen, slot) 最小者。
func (p *Pool) Track(key string, ts int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if st, ok := p.states[key]; ok {
		p.remove(st)
		st.LastSeen = ts
		p.states[key] = st
		p.insert(key, st)
		return
	}
	st := rk.State{LastSeen: ts, Slot: p.nextSlot}
	p.nextSlot++
	if len(p.states) == p.n {
		victim := p.sorted[0] // 有序定位：最小者恒为首元素，扫描 1 个键
		delete(p.states, victim.key)
		p.sorted = p.sorted[1:]
		pos, scans := p.bsearch(st)
		p.lastScan = 1 + scans // 受害者定位 + 新键二分插入的扫描总量
		p.sorted = insertAt(p.sorted, pos, entry{key, st})
	} else {
		p.insert(key, st)
	}
	p.states[key] = st
}

// Count 返回当前集合大小，恒等于集合内键数，永不超过容量。
func (p *Pool) Count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.states)
}

// Snapshot 返回当前键集合（排序输出，同状态下多次调用结果一致）。
func (p *Pool) Snapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.states))
	for k := range p.states {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ScanWithinBound 报告最近一次驱逐的扫描量是否落在对数量级上界内。
// 只给出判定结论，不暴露计数器数值。
func (p *Pool) ScanWithinBound() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastScan <= 2*bits.Len(uint(p.n))+3
}

// bsearch 在升序切片里二分定位 st 的插入点，返回位置与扫描过的键个数。
func (p *Pool) bsearch(st rk.State) (pos, scans int) {
	lo, hi := 0, len(p.sorted)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		scans++
		if rk.Less(p.sorted[mid].st, st) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo, scans
}

// remove 删除状态恰为 st 的槽位（slot 唯一，故 (lastSeen, slot) 唯一）。
func (p *Pool) remove(st rk.State) {
	pos, _ := p.bsearch(st)
	p.sorted = append(p.sorted[:pos], p.sorted[pos+1:]...)
}

func (p *Pool) insert(key string, st rk.State) {
	pos, _ := p.bsearch(st)
	p.sorted = insertAt(p.sorted, pos, entry{key, st})
}

func insertAt(s []entry, i int, e entry) []entry {
	s = append(s, entry{})
	copy(s[i+1:], s[i:])
	s[i] = e
	return s
}
