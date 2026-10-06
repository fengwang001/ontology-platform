// Package inventory 实现多航段联程座位库存与超售控制。
//
// 一条行程由 1..4 个航段按顺序组成；航段被多条行程共用，具有物理座位数、
// 超售上限与三个有序嵌套的舱位等级。系统支持预占、出票、取消、授权量调整
// 与只读查询。所有方法并发安全，结果等价于某个串行顺序；相同操作序列重放
// 得到完全相同的结果、条目标识与到期时刻。
package inventory

import (
	"fmt"
	"sync"
)

// System 座位库存与超售控制系统。所有方法并发安全。
type System struct {
	mu           sync.Mutex
	holdDuration int64 // 预占时长（秒）
	clock        int64 // 上一次被接受操作的时刻
	sweptTo      int64 // 惰性过期水位线：到期时刻 <= sweptTo 的预占已不计入占用
	seq          int   // 条目 ID 计数器，保证重放得到相同标识
	segments     map[string]*Segment
	segmentOrder []string
	entries      map[string]*Entry
	expiryQ      []expiryRef // 到期时刻非递减（时钟单调 ⇒ 新预占到期时刻单调）
	sweptTotal   int64       // 累计出队的到期引用数，用于均摊复杂度验证
}

// expiryRef 到期队列元素。已取消/已出票的条目成为陈旧引用，出队时跳过。
type expiryRef struct {
	expiry  int64
	entryID string
}

// NewSystem 创建系统，holdDuration 为预占时长（秒），必须非负。
func NewSystem(holdDuration int64) (*System, error) {
	if holdDuration < 0 {
		return nil, &Error{Kind: ErrInvalidParam, Detail: "hold duration must be non-negative"}
	}
	return &System{
		holdDuration: holdDuration,
		segments:     make(map[string]*Segment),
		entries:      make(map[string]*Entry),
	}, nil
}

// AddSegment 注册航段（配置操作，不参与时钟约束）。
// seats 为正整数，overbook 为非负整数，au 必须满足嵌套次序且
// 最高等级不超过 seats+overbook，否则报参数非法。
func (s *System) AddSegment(id string, seats, overbook int, au [numClasses]int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addSegmentLocked(id, seats, overbook, au)
}

func (s *System) addSegmentLocked(id string, seats, overbook int, au [numClasses]int) error {
	if id == "" {
		return invalidParamf("segment id must be non-empty")
	}
	if _, dup := s.segments[id]; dup {
		return invalidParamf("duplicate segment %q", id)
	}
	if seats <= 0 {
		return invalidParamf("segment %q: seats must be positive, got %d", id, seats)
	}
	if overbook < 0 {
		return invalidParamf("segment %q: overbook must be non-negative, got %d", id, overbook)
	}
	if !validAuthorization(au, seats+overbook) {
		return invalidParamf("segment %q: authorization %v violates nesting or capacity %d", id, au, seats+overbook)
	}
	s.segments[id] = &Segment{id: id, seats: seats, overbook: overbook, au: au}
	s.segmentOrder = append(s.segmentOrder, id)
	return nil
}

// checkClock 时钟约束：非负且不小于上一次被接受操作的时刻。
func (s *System) checkClock(now int64) *Error {
	if now < 0 {
		return invalidParamf("negative time %d", now)
	}
	if now < s.clock {
		return &Error{Kind: ErrClockRegression, Detail: fmt.Sprintf("time %d < last accepted time %d", now, s.clock)}
	}
	return nil
}

// sweep 惰性过期：把到期时刻 <= t 的未出票未取消预占从占用计数中移除。
// 每条预占入队一次、出队一次，均摊 O(1)；与累计条目数无关。
func (s *System) sweep(t int64) {
	if t <= s.sweptTo {
		return
	}
	s.sweptTo = t
	for len(s.expiryQ) > 0 && s.expiryQ[0].expiry <= t {
		ref := s.expiryQ[0]
		s.expiryQ = s.expiryQ[1:]
		s.sweptTotal++
		e := s.entries[ref.entryID]
		if e.State != StateHeld {
			continue // 已出票/已取消：计数早已调整
		}
		for _, leg := range e.Legs {
			s.segments[leg.Segment].held[int(leg.Class)] -= e.Party
		}
	}
}

// queryTime 只读查询的求值时刻：不小于操作时钟与过期水位线。
// 为保证惰性扫描正确，所有调用的求值时刻应单调不减。
func (s *System) queryTime(now int64) int64 {
	if now > s.sweptTo {
		return now
	}
	return s.sweptTo
}

func validateLegs(legs []Leg, party int) *Error {
	if party < 1 || party > 9 {
		return invalidParamf("party size %d out of range [1,9]", party)
	}
	if len(legs) < 1 || len(legs) > 4 {
		return invalidParamf("itinerary must have 1..4 legs, got %d", len(legs))
	}
	seen := make(map[string]bool, len(legs))
	for _, leg := range legs {
		if leg.Segment == "" {
			return invalidParamf("empty segment id in itinerary")
		}
		if !leg.Class.valid() {
			return invalidParamf("invalid class %d", int(leg.Class))
		}
		if seen[leg.Segment] {
			return invalidParamf("duplicate segment %q in itinerary", leg.Segment)
		}
		seen[leg.Segment] = true
	}
	return nil
}

// Hold 预占：一条行程加 1..9 名旅客，全有或全无。
// 成功返回条目标识与到期时刻（发起时刻 + 预占时长）。
func (s *System) Hold(legs []Leg, party int, now int64) (string, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateLegs(legs, party); err != nil {
		return "", 0, err
	}
	if err := s.checkClock(now); err != nil {
		return "", 0, err
	}
	for _, leg := range legs {
		if _, ok := s.segments[leg.Segment]; !ok {
			return "", 0, &Error{Kind: ErrNotFound, Segment: leg.Segment, Detail: "segment " + leg.Segment + " does not exist"}
		}
	}
	s.sweep(now)
	for _, leg := range legs {
		if !s.segments[leg.Segment].canBook(leg.Class, party) {
			return "", 0, &Error{Kind: ErrInsufficientInventory, Segment: leg.Segment,
				Detail: fmt.Sprintf("segment %q class %s cannot accommodate %d passenger(s)", leg.Segment, leg.Class, party)}
		}
	}
	s.seq++
	id := fmt.Sprintf("H%06d", s.seq)
	expiry := now + s.holdDuration
	s.entries[id] = &Entry{ID: id, Legs: append([]Leg(nil), legs...), Party: party, State: StateHeld, Expiry: expiry}
	for _, leg := range legs {
		s.segments[leg.Segment].held[int(leg.Class)] += party
	}
	s.expiryQ = append(s.expiryQ, expiryRef{expiry: expiry, entryID: id})
	s.clock = now
	return id, expiry, nil
}

// findEntry 出票/取消的公共校验：时钟 > 不存在 > 已过期 > 状态不符。
func (s *System) findEntry(id string, now int64) (*Entry, *Error) {
	if id == "" {
		return nil, invalidParamf("entry id must be non-empty")
	}
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	e, ok := s.entries[id]
	if !ok {
		return nil, &Error{Kind: ErrNotFound, Detail: "entry " + id + " does not exist"}
	}
	if e.State == StateHeld && now >= e.Expiry {
		return nil, &Error{Kind: ErrHoldExpired, Detail: fmt.Sprintf("entry %s expired at %d, now %d", id, e.Expiry, now)}
	}
	return e, nil
}

// Ticket 出票：把一条未过期预占转为已确认，已确认的占用永不过期。
func (s *System) Ticket(id string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.findEntry(id, now)
	if err != nil {
		return err
	}
	if e.State != StateHeld {
		return &Error{Kind: ErrInvalidState, State: e.State, Detail: "ticket requires a held entry"}
	}
	s.sweep(now)
	for _, leg := range e.Legs {
		seg := s.segments[leg.Segment]
		seg.held[int(leg.Class)] -= e.Party
		seg.confirmed[int(leg.Class)] += e.Party
	}
	e.State = StateTicketed
	s.clock = now
	return nil
}

// Cancel 取消：可针对预占或已确认票，取消后占用立即释放，总是整条行程。
func (s *System) Cancel(id string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.findEntry(id, now)
	if err != nil {
		return err
	}
	if e.State == StateCancelled {
		return &Error{Kind: ErrInvalidState, State: e.State, Detail: "entry already cancelled"}
	}
	s.sweep(now)
	for _, leg := range e.Legs {
		seg := s.segments[leg.Segment]
		if e.State == StateHeld {
			seg.held[int(leg.Class)] -= e.Party
		} else {
			seg.confirmed[int(leg.Class)] -= e.Party
		}
	}
	e.State = StateCancelled
	s.clock = now
	return nil
}

// AdjustAuthorization 调整某航段某等级的授权量。
// 允许调到低于当前已占用数（已有占用不受影响，该等级及更低等级不可售），
// 但不得破坏嵌套次序，最高等级不得超过物理座位数与超售上限之和。
func (s *System) AdjustAuthorization(segID string, class Class, newAU int, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if segID == "" {
		return invalidParamf("segment id must be non-empty")
	}
	if !class.valid() {
		return invalidParamf("invalid class %d", int(class))
	}
	if newAU < 0 {
		return invalidParamf("authorization must be non-negative, got %d", newAU)
	}
	seg, ok := s.segments[segID]
	if ok {
		au := seg.au
		au[int(class)] = newAU
		if !validAuthorization(au, seg.seats+seg.overbook) {
			return invalidParamf("segment %q: authorization %v violates nesting or capacity %d", segID, au, seg.seats+seg.overbook)
		}
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	if !ok {
		return &Error{Kind: ErrNotFound, Segment: segID, Detail: "segment " + segID + " does not exist"}
	}
	s.sweep(now)
	seg.au[int(class)] = newAU
	s.clock = now
	return nil
}

// Availability 只读查询：某航段某等级在指定时刻的可用数。不参与时钟约束。
func (s *System) Availability(segID string, class Class, now int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if segID == "" {
		return 0, invalidParamf("segment id must be non-empty")
	}
	if !class.valid() {
		return 0, invalidParamf("invalid class %d", int(class))
	}
	if now < 0 {
		return 0, invalidParamf("negative time %d", now)
	}
	seg, ok := s.segments[segID]
	if !ok {
		return 0, &Error{Kind: ErrNotFound, Segment: segID, Detail: "segment " + segID + " does not exist"}
	}
	s.sweep(s.queryTime(now))
	return seg.available(class), nil
}

// ItinerarySellable 只读查询：判定一条行程对 party 名旅客是否可售。
// 不可售时返回行程顺序中第一个不满足的航段。不参与时钟约束。
func (s *System) ItinerarySellable(legs []Leg, party int, now int64) (bool, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateLegs(legs, party); err != nil {
		return false, "", err
	}
	if now < 0 {
		return false, "", invalidParamf("negative time %d", now)
	}
	for _, leg := range legs {
		if _, ok := s.segments[leg.Segment]; !ok {
			return false, "", &Error{Kind: ErrNotFound, Segment: leg.Segment, Detail: "segment " + leg.Segment + " does not exist"}
		}
	}
	s.sweep(s.queryTime(now))
	for _, leg := range legs {
		if !s.segments[leg.Segment].canBook(leg.Class, party) {
			return false, leg.Segment, nil
		}
	}
	return true, "", nil
}

// Load 只读查询：航段在指定时刻的占用合计（已确认, 未到期预占）。
func (s *System) Load(segID string, now int64) (confirmed, held int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seg, ok := s.segments[segID]
	if !ok {
		return 0, 0, &Error{Kind: ErrNotFound, Segment: segID, Detail: "segment " + segID + " does not exist"}
	}
	if now < 0 {
		return 0, 0, invalidParamf("negative time %d", now)
	}
	s.sweep(s.queryTime(now))
	for i := 0; i < numClasses; i++ {
		confirmed += seg.confirmed[i]
		held += seg.held[i]
	}
	return confirmed, held, nil
}

// Clock 返回上一次被接受操作的时刻。只读查询不推进该时钟。
func (s *System) Clock() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clock
}

// Stats 用于验证均摊复杂度的内部计数。
type Stats struct {
	SweptExpiryRefs int64 // 累计出队的到期引用数，恒不超过累计预占数
	Entries         int   // 当前条目总数
}

// Stats 返回内部计数快照。
func (s *System) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{SweptExpiryRefs: s.sweptTotal, Entries: len(s.entries)}
}
