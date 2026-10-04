package slotpool

import (
	"errors"
	"sync"
)

// Channel 预约渠道。
type Channel int

const (
	Online Channel = iota + 1
	Onsite
)

// 号源类错误。
var (
	ErrSlotExists   = errors.New("slot already exists")
	ErrSlotNotFound = errors.New("slot not found")
	ErrNoQuota      = errors.New("no quota available")
)

// Slot 单个时段的号源计数。
type Slot struct {
	start     int64
	cap       int
	on        int
	uo        int
	us        int
	releaseAt int64
}

// Pool 全部时段的号源表。
type Pool struct {
	r     int64 // 释放提前量
	mu    sync.RWMutex
	slots map[string]*Slot
}

// New 创建空号源池。r 为释放提前量（释放点 = start-r）。
func New(r int64) *Pool {
	return &Pool{r: r, slots: map[string]*Slot{}}
}

// Add 新增时段。调用方保证 slot 非空、cap/on/start 合法、start>now。
func (p *Pool) Add(slot string, start int64, cap, on int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.slots[slot]; ok {
		return ErrSlotExists
	}
	p.slots[slot] = &Slot{
		start:     start,
		cap:       cap,
		on:        on,
		releaseAt: start - p.r,
	}
	return nil
}

// Get 返回时段只读副本，不存在返回 false。
func (p *Pool) Get(slot string) (start int64, cap, on, uo, us int, ok bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	s, exists := p.slots[slot]
	if !exists {
		return 0, 0, 0, 0, 0, false
	}
	return s.start, s.cap, s.on, s.uo, s.us, true
}

// CanBook 判定 now 时刻指定渠道是否可订，不改变状态。
func (p *Pool) CanBook(slot string, now int64, ch Channel) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	s, ok := p.slots[slot]
	if !ok {
		return false
	}
	return s.canBook(now, ch)
}

// Book 在判定可订后占用一个号。已释放后现场可借用线上余量。
func (p *Pool) Book(slot string, now int64, ch Channel) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.slots[slot]
	if !ok {
		return ErrSlotNotFound
	}
	if !s.canBook(now, ch) {
		return ErrNoQuota
	}
	if ch == Online {
		s.uo++
	} else {
		s.us++
	}
	return nil
}

// Release 了结一个预约，按其所属渠道减计数。
func (p *Pool) Release(slot string, ch Channel) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.slots[slot]
	if !ok {
		return
	}
	if ch == Online && s.uo > 0 {
		s.uo--
	} else if ch == Onsite && s.us > 0 {
		s.us--
	}
}

func (s *Slot) canBook(now int64, ch Channel) bool {
	if now < s.releaseAt {
		if ch == Online {
			return s.uo < s.on
		}
		return s.us < s.cap-s.on
	}
	return s.uo+s.us < s.cap
}
