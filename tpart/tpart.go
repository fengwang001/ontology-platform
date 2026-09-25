// Package tpart 维护 (Key, 桶) 计数、cur 滚动、保留窗口与清理、丢弃计数。
package tpart

import (
	"errors"
	"sort"
	"sync"

	"ontology/tbucket"
)

var (
	ErrBadSize  = errors.New("tpart: size 必须为正数")
	ErrBadR     = errors.New("tpart: R 必须为正数")
	ErrEmptyKey = errors.New("tpart: 事件 Key 为空串")
)

// Event 是一条到达事件。
type Event struct {
	TS  int64
	Key string
}

// Partition 是按时间分区的物化视图，并发安全。
type Partition struct {
	mu      sync.RWMutex
	size, R int64
	counts  map[int64]map[string]int64 // 桶键 -> Key -> 计数
	keys    []int64                    // 有序桶键，清理只扫越界前缀
	cur     int64
	hasCur  bool
	dropped int64
	checked int64 // 最近一次推进 cur 时清理检查过的桶数（非导出，仅供包内测试）
}

// New 构造视图；size、R 必须为正，否则整体失败。
func New(size, r int64) (*Partition, error) {
	if size <= 0 {
		return nil, ErrBadSize
	}
	if r <= 0 {
		return nil, ErrBadR
	}
	return &Partition{size: size, R: r, counts: map[int64]map[string]int64{}}, nil
}

// Feed 逐事件处理一批事件；任一条 Key 为空则整批不生效。
func (p *Partition) Feed(evs []Event) error {
	for _, e := range evs {
		if e.Key == "" {
			return ErrEmptyKey
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range evs {
		p.feedOne(e)
	}
	return nil
}

func (p *Partition) feedOne(e Event) {
	k := tbucket.Key(e.TS, p.size)
	if p.hasCur && k < p.cur-p.R+1 { // 迟到事件：丢弃，不重开已清理的桶
		p.dropped++
		return
	}
	if !p.hasCur || k > p.cur {
		p.cur, p.hasCur = k, true
		p.cleanup()
	}
	b := p.counts[k]
	if b == nil {
		b = map[string]int64{}
		p.counts[k] = b
		p.keys = insertSorted(p.keys, k)
	}
	b[e.Key]++
}

// cleanup 丢弃所有桶键 < cur-R+1 的桶；keys 有序，只检查越界前缀与首个界内桶。
func (p *Partition) cleanup() {
	lo := p.cur - p.R + 1
	i := 0
	for i < len(p.keys) && p.keys[i] < lo {
		for _, c := range p.counts[p.keys[i]] {
			p.dropped += c
		}
		delete(p.counts, p.keys[i])
		i++
	}
	p.checked = int64(i)
	if i < len(p.keys) {
		p.checked++ // 第一个未越界的桶也算被检查
	}
	p.keys = p.keys[i:]
}

func insertSorted(s []int64, k int64) []int64 {
	i := sort.Search(len(s), func(i int) bool { return s[i] >= k })
	s = append(s, 0)
	copy(s[i+1:], s[i:])
	s[i] = k
	return s
}

// View 返回 (Key -> 桶键 -> 计数) 的深拷贝快照。
func (p *Partition) View() map[string]map[int64]int64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := map[string]map[int64]int64{}
	for k, b := range p.counts {
		for key, c := range b {
			if out[key] == nil {
				out[key] = map[int64]int64{}
			}
			out[key][k] = c
		}
	}
	return out
}

// Dropped 返回累计被丢弃的事件总数（迟到丢弃 + 桶清理丢弃）。
func (p *Partition) Dropped() int64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.dropped
}
