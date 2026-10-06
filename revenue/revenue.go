// Package revenue 实现创作者分成结算账本的收入层：
// 分成表、收入事件、份额拆分，以及供 hold/payout 层使用的共享状态原语。
//
// Book 持有账本的全部共享状态（分成表、事件、份额、创作者聚合、时钟），
// 所有修改都必须发生在 Do 的临界区内，因此并发调用等价于某个串行顺序。
package revenue

import (
	"container/list"
	"errors"
	"sync"
)

// 账本共用的哨兵错误，调用方用 errors.Is 区分。
var (
	ErrInvalidParam  = errors.New("revenue: invalid parameter")
	ErrClockRewind   = errors.New("revenue: clock rewind")
	ErrNoSplit       = errors.New("revenue: content has no split")
	ErrEventConflict = errors.New("revenue: event conflict")
)

const (
	maxNow    = int64(1_000_000_000_000)
	maxAmount = int64(1_000_000_000_000)
	maxWd     = int64(1_000_000_000)
	totalBPS  = 10_000
	maxParts  = 8
)

// Part 是分成表的一项：创作者及其基点（万分比）。
type Part struct {
	Creator string
	BPS     int
}

// Share 是一位创作者在某个收入事件中的份额。
type Share struct {
	Event     *Event   // 所属事件
	Owner     *Creator // 所属创作者
	Share     int64    // 应得（分），入账后恒定
	Remaining int64    // 尚未付出也未用于抵债的部分
	Holds     int      // 覆盖该份额的活动冻结条数

	elem *list.Element
}

// Next 返回所属创作者活跃份额表中的下一份额；已出列或在末尾时返回 nil。
func (s *Share) Next() *Share {
	if s.elem == nil {
		return nil
	}
	if e := s.elem.Next(); e != nil {
		return e.Value.(*Share)
	}
	return nil
}

// Event 是一笔收入事件。
type Event struct {
	ID       string
	Content  string
	Amount   int64
	Time     int64
	Shares   []*Share // 与入账时的分成表逐项对应
	Refunded bool
}

// immEntry 是同一成熟时刻的未成熟份额聚合桶。
type immEntry struct {
	time int64 // 成熟时刻（事件时刻 + Wd）
	sum  int64
}

// Creator 记录一位创作者的活跃份额队列、三维聚合与欠款。
type Creator struct {
	Name string

	wd     int64
	active *list.List // *Share：Remaining>0，按入账先后排列（事件时刻单调不减）
	avail  int64      // 未冻结且已成熟的 Remaining 合计
	held   int64      // 被任一冻结覆盖的 Remaining 合计
	immSum int64      // 未冻结且未成熟的 Remaining 合计
	imm    *list.List // *immEntry，按成熟时刻升序
	immBy  map[int64]*list.Element
	debt   int64
}

// Promote 把成熟时刻不超过 now 的未成熟聚合迁入 available（只动聚合，不碰份额）。
func (c *Creator) Promote(now int64) {
	for e := c.imm.Front(); e != nil; {
		en := e.Value.(*immEntry)
		if en.time > now {
			return
		}
		next := e.Next()
		c.avail += en.sum
		c.immSum -= en.sum
		delete(c.immBy, en.time)
		c.imm.Remove(e)
		e = next
	}
}

// Available 返回未冻结且已成熟的 Remaining 合计（须先 Promote）。
func (c *Creator) Available() int64 { return c.avail }

// Held 返回被冻结的 Remaining 合计。
func (c *Creator) Held() int64 { return c.held }

// Pending 返回未冻结且未成熟的 Remaining 合计（须先 Promote）。
func (c *Creator) Pending() int64 { return c.immSum }

// Debt 返回当前欠款。
func (c *Creator) Debt() int64 { return c.debt }

// AddDebt 调整欠款（delta 可负，用于抵债）。
func (c *Creator) AddDebt(delta int64) { c.debt += delta }

// Front 返回活跃份额表的队首（FIFO 消耗起点）。
func (c *Creator) Front() *Share {
	if e := c.active.Front(); e != nil {
		return e.Value.(*Share)
	}
	return nil
}

func (c *Creator) mature(eventTime, now int64) bool { return eventTime+c.wd <= now }

func (c *Creator) immAdd(time, delta int64) {
	c.immSum += delta
	if e, ok := c.immBy[time]; ok {
		e.Value.(*immEntry).sum += delta
		return
	}
	en := &immEntry{time: time, sum: delta}
	for e := c.imm.Back(); e != nil; e = e.Prev() {
		if e.Value.(*immEntry).time < time {
			c.immBy[time] = c.imm.InsertAfter(en, e)
			return
		}
	}
	c.immBy[time] = c.imm.PushFront(en)
}

func (c *Creator) immRemove(time, delta int64) {
	e, ok := c.immBy[time]
	if !ok {
		return
	}
	en := e.Value.(*immEntry)
	en.sum -= delta
	c.immSum -= delta
	if en.sum == 0 {
		delete(c.immBy, time)
		c.imm.Remove(e)
	}
}

// Freeze 把份额的 Remaining 从 available/未成熟聚合移入 held（Holds 0→1 时调用）。
func (c *Creator) Freeze(s *Share, now int64) {
	r := s.Remaining
	if c.mature(s.Event.Time, now) {
		c.avail -= r
	} else {
		c.immRemove(s.Event.Time+c.wd, r)
	}
	c.held += r
}

// Unfreeze 把份额的 Remaining 从 held 移回 available/未成熟聚合（Holds 1→0 时调用）。
func (c *Creator) Unfreeze(s *Share, now int64) {
	r := s.Remaining
	c.held -= r
	if c.mature(s.Event.Time, now) {
		c.avail += r
	} else {
		c.immAdd(s.Event.Time+c.wd, r)
	}
}

// RemoveRemaining 退款时移除份额的全部 Remaining（聚合扣减并出列）。
func (c *Creator) RemoveRemaining(s *Share, now int64) {
	r := s.Remaining
	switch {
	case s.Holds > 0:
		c.held -= r
	case c.mature(s.Event.Time, now):
		c.avail -= r
	default:
		c.immRemove(s.Event.Time+c.wd, r)
	}
	c.detach(s)
}

// Consume 从可用份额中扣减 amount（调用方保证份额未冻结且已成熟）。
func (c *Creator) Consume(s *Share, amount int64) {
	c.avail -= amount
	s.Remaining -= amount
	if s.Remaining == 0 {
		c.detach(s)
	}
}

func (c *Creator) detach(s *Share) {
	if s.elem != nil {
		c.active.Remove(s.elem)
		s.elem = nil
	}
}

// Book 是账本的共享状态：分成表、事件、份额、创作者聚合与时钟。
type Book struct {
	mu       sync.Mutex
	wd       int64
	maxNow   int64
	hasNow   bool
	splits   map[string][]Part
	events   map[string]*Event
	byCont   map[string][]*Event // 每内容的事件，按时刻单调不减
	creators map[string]*Creator

	holdCountFn func(content string, eventTime int64) int // hold 层注入
}

// NewBook 创建收入账本；wd 为成熟期（秒），须在 [0, 1e9]。
func NewBook(wd int64) (*Book, error) {
	if wd < 0 || wd > maxWd {
		return nil, ErrInvalidParam
	}
	return &Book{
		wd:       wd,
		splits:   make(map[string][]Part),
		events:   make(map[string]*Event),
		byCont:   make(map[string][]*Event),
		creators: make(map[string]*Creator),
	}, nil
}

// Wd 返回成熟期（秒）。
func (b *Book) Wd() int64 { return b.wd }

// Do 在账本互斥锁内执行 fn，保证跨包操作的整体串行化。
func (b *Book) Do(fn func() error) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return fn()
}

// SetHoldCounter 注入冻结覆盖计数钩子（由 hold 层在 Earn 前设置）。
func (b *Book) SetHoldCounter(fn func(content string, eventTime int64) int) {
	b.holdCountFn = fn
}

// CheckClock 校验 now 不小于已接受操作的最大 now（须在 Do 内调用）。
func (b *Book) CheckClock(now int64) error {
	if b.hasNow && now < b.maxNow {
		return ErrClockRewind
	}
	return nil
}

// AcceptClock 在接受操作后推进时钟（须在 Do 内调用）。
func (b *Book) AcceptClock(now int64) {
	if !b.hasNow || now > b.maxNow {
		b.maxNow = now
		b.hasNow = true
	}
}

// HasSplit 报告内容是否已设定分成表。
func (b *Book) HasSplit(content string) bool {
	_, ok := b.splits[content]
	return ok
}

// EventsOf 返回内容的全部事件（按时刻单调不减）；调用方只读。
func (b *Book) EventsOf(content string) []*Event { return b.byCont[content] }

// GetEvent 按 ID 查找事件。
func (b *Book) GetEvent(id string) (*Event, bool) {
	ev, ok := b.events[id]
	return ev, ok
}

// GetCreator 按名字查找创作者（出现在任一分成表中即存在）。
func (b *Book) GetCreator(name string) (*Creator, bool) {
	c, ok := b.creators[name]
	return c, ok
}

func (b *Book) ensureCreator(name string) *Creator {
	c, ok := b.creators[name]
	if !ok {
		c = &Creator{
			Name:   name,
			wd:     b.wd,
			active: list.New(),
			imm:    list.New(),
			immBy:  make(map[int64]*list.Element),
		}
		b.creators[name] = c
	}
	return c
}

func validNow(now int64) bool { return now >= 0 && now <= maxNow }

// SetSplit 设定内容的分成表，只影响其后的收入事件。
// 拒绝次序：参数非法 > 时钟回退。
func (b *Book) SetSplit(now int64, content string, parts []Part) error {
	if !validNow(now) || content == "" {
		return ErrInvalidParam
	}
	if err := validateParts(parts); err != nil {
		return err
	}
	return b.Do(func() error {
		if err := b.CheckClock(now); err != nil {
			return err
		}
		b.splits[content] = append([]Part(nil), parts...)
		for _, p := range parts {
			b.ensureCreator(p.Creator)
		}
		b.AcceptClock(now)
		return nil
	})
}

func validateParts(parts []Part) error {
	if len(parts) < 1 || len(parts) > maxParts {
		return ErrInvalidParam
	}
	seen := make(map[string]bool, len(parts))
	sum := 0
	for _, p := range parts {
		if p.Creator == "" || p.BPS < 1 || p.BPS > totalBPS || seen[p.Creator] {
			return ErrInvalidParam
		}
		seen[p.Creator] = true
		sum += p.BPS
	}
	if sum != totalBPS {
		return ErrInvalidParam
	}
	return nil
}

// Earn 记录一笔收入事件，按当前分成表拆分份额并返回各份额（与分成表同序）。
// 取整余下的分全部加给分成表第一位。eventId 幂等：重复且 content 与 amount
// 都相同则为空操作并返回原份额（视为已接受，推进时钟）；任一不同则报事件冲突。
// 拒绝次序：参数非法 > 时钟回退 > 内容无分成表 > 事件冲突。
func (b *Book) Earn(now int64, eventID, content string, amount int64) ([]int64, error) {
	if !validNow(now) || eventID == "" || content == "" || amount < 1 || amount > maxAmount {
		return nil, ErrInvalidParam
	}
	var result []int64
	err := b.Do(func() error {
		if err := b.CheckClock(now); err != nil {
			return err
		}
		parts, ok := b.splits[content]
		if !ok {
			return ErrNoSplit
		}
		if ev, ok := b.events[eventID]; ok {
			if ev.Content != content || ev.Amount != amount {
				return ErrEventConflict
			}
			result = shareAmounts(ev)
			b.AcceptClock(now)
			return nil
		}
		amounts := make([]int64, len(parts))
		var assigned int64
		for i, p := range parts {
			amounts[i] = amount * int64(p.BPS) / totalBPS
			assigned += amounts[i]
		}
		amounts[0] += amount - assigned // 取整余数归分成表第一位
		holds := 0
		if b.holdCountFn != nil {
			holds = b.holdCountFn(content, now)
		}
		ev := &Event{ID: eventID, Content: content, Amount: amount, Time: now}
		ev.Shares = make([]*Share, len(parts))
		for i, p := range parts {
			c := b.ensureCreator(p.Creator)
			s := &Share{Event: ev, Owner: c, Share: amounts[i], Remaining: amounts[i], Holds: holds}
			ev.Shares[i] = s
			if amounts[i] == 0 {
				continue // 零份额不进入活跃表
			}
			s.elem = c.active.PushBack(s)
			switch {
			case holds > 0:
				c.held += amounts[i]
			case b.wd == 0:
				c.avail += amounts[i]
			default:
				c.immAdd(now+b.wd, amounts[i])
			}
		}
		b.events[eventID] = ev
		b.byCont[content] = append(b.byCont[content], ev)
		b.AcceptClock(now)
		result = amounts
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func shareAmounts(ev *Event) []int64 {
	amounts := make([]int64, len(ev.Shares))
	for i, s := range ev.Shares {
		amounts[i] = s.Share
	}
	return amounts
}
