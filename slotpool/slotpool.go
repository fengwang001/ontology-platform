// Package slotpool 维护各时段的号源与渠道配额计数。
package slotpool

import "sync"

// Channel 为订号渠道。
type Channel int

const (
	Online Channel = iota
	OnSite
)

// Slot 是一个时段的号源池状态。
type Slot struct {
	Start int64
	Cap   int
	On    int
	R     int64
	UO    int
	US    int
}

// Pool 为全部时段的号源池。
type Pool struct {
	mu    sync.RWMutex
	slots map[string]*Slot
}

// New 创建空号源池。
func New() *Pool { return &Pool{slots: map[string]*Slot{}} }

// Add 加入一个时段；重复返回 false。
func (p *Pool) Add(id string, start int64, cap, on int, r int64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.slots[id]; ok {
		return false
	}
	p.slots[id] = &Slot{Start: start, Cap: cap, On: on, R: r}
	return true
}

// Get 读取时段；不存在返回 nil。
func (p *Pool) Get(id string) *Slot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.slots[id]
}

// TryBook 尝试订号；成功返回 true。
// now < start-R 时两渠道分账互不借用；恰等及之后并入公共池。
func (p *Pool) TryBook(id string, now int64, ch Channel) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	sl := p.slots[id]
	if sl == nil {
		return false
	}
	if now < sl.Start-sl.R {
		if ch == Online {
			if sl.UO < sl.On {
				sl.UO++
				return true
			}
			return false
		}
		if sl.US < sl.Cap-sl.On {
			sl.US++
			return true
		}
		return false
	}
	if sl.UO+sl.US < sl.Cap {
		if ch == Online {
			sl.UO++
		} else {
			sl.US++
		}
		return true
	}
	return false
}

// Release 从所属渠道归还一个号。
func (p *Pool) Release(id string, ch Channel) {
	p.mu.Lock()
	defer p.mu.Unlock()
	sl := p.slots[id]
	if sl == nil {
		return
	}
	if ch == Online {
		sl.UO--
	} else {
		sl.US--
	}
}
