package seats

import (
	"fmt"
	"sync"
)

// System 是多航段联程座位库存与超售控制系统。
//
// 并发：所有公开方法持有同一把互斥锁，效果等价于按锁获取顺序的某个串行
// 执行。确定性：条目标识来自单调计数器，到期时刻 = 接受时刻 + 预占时长，
// 相同操作序列重放得到完全相同的结果。
//
// 时钟：now 是最近一次被接受操作的时刻；被拒绝的操作不推进时钟。只读查
// 询以系统当前时刻求值，不参与时钟约束。
type System struct {
	mu      sync.Mutex
	holdDur int64
	now     int64
	nextID  int
	segs    map[string]*Segment
	entries map[string]*Entry
	swept   int64 // 累计清扫的过期队列记录数，用于性能证明与测试
}

// NewSystem 创建一个空系统。holdDuration 为预占时长（秒），必须非负。
func NewSystem(holdDuration int64) (*System, error) {
	if holdDuration < 0 {
		return nil, errInvalid("hold duration must be non-negative")
	}
	return &System{
		holdDur: holdDuration,
		segs:    make(map[string]*Segment),
		entries: make(map[string]*Entry),
	}, nil
}

// Now 返回系统当前时刻，即最近一次被接受操作的时刻。
func (s *System) Now() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

// checkClock 实现“时钟回退”拒绝：操作时刻不得小于上一次被接受操作的时刻。
func (s *System) checkClock(now int64) *Error {
	if now < s.now {
		return &Error{Kind: KindClockRollback,
			Msg: fmt.Sprintf("time %d is before last accepted time %d", now, s.now)}
	}
	return nil
}

// sweep 把航段上所有到期时刻 <= now 的队列记录弹出；仍处预占状态的记录从
// held 中扣除（已出票/已取消的记录在状态变更时已处理，直接丢弃）。每条记
// 录入队一次、出队一次，摊还 O(1)。只在 now 不超过系统当前时刻（被接受操
// 作或查询的时刻）时调用，保证已提交状态单调前进、被拒绝的操作不留痕迹。
func (s *System) sweep(seg *Segment, now int64) {
	for len(seg.queue) > 0 && seg.queue[0].expiry <= now {
		qe := seg.queue[0]
		seg.queue = seg.queue[1:]
		s.swept++
		if e, ok := s.entries[qe.entryID]; ok && e.Status == StatusHold {
			seg.held[qe.class] -= qe.count
		}
	}
	if len(seg.queue) == 0 {
		seg.queue = nil // 释放底层数组，避免长期持有
	}
}

// probeExpired 只读地统计航段上在时刻 now 已过期但尚未提交清扫的预占计
// 数，用于评估一个尚不一定会被接受的预占：被拒绝的操作不得改变任何状态，
// 因此不能提前提交清扫。队列按到期时刻有序，扫描在遇到第一个未过期记录
// 时停止。
func (s *System) probeExpired(seg *Segment, now int64) [NumClasses]int {
	var expired [NumClasses]int
	for _, qe := range seg.queue {
		if qe.expiry > now {
			break
		}
		if e, ok := s.entries[qe.entryID]; ok && e.Status == StatusHold {
			expired[qe.class] += qe.count
		}
	}
	return expired
}

// AddSegment 注册一个航段。授权量必须满足嵌套次序（高等级不小于低等级）
// 且最高等级授权量不得超过物理座位数与超售上限之和。
func (s *System) AddSegment(id string, physical, overbook int, auth [NumClasses]int, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		return errInvalid("segment id must be non-empty")
	}
	if physical <= 0 {
		return errInvalid("physical seats must be positive")
	}
	if overbook < 0 {
		return errInvalid("overbook limit must be non-negative")
	}
	if now < 0 {
		return errInvalid("time must be non-negative")
	}
	if err := checkAuth(auth, physical, overbook); err != nil {
		return err
	}
	if _, dup := s.segs[id]; dup {
		return errInvalid("segment " + id + " already exists")
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	s.segs[id] = &Segment{ID: id, Physical: physical, Overbook: overbook, Auth: auth}
	s.now = now
	return nil
}

// checkAuth 校验一组授权量的嵌套次序与上限。
func checkAuth(auth [NumClasses]int, physical, overbook int) *Error {
	for _, a := range auth {
		if a < 0 {
			return errInvalid("authorization must be non-negative")
		}
	}
	if auth[0] < auth[1] || auth[1] < auth[2] {
		return errInvalid("authorizations must be nested: higher class >= lower class")
	}
	if auth[0] > physical+overbook {
		return errInvalid("top authorization exceeds physical seats plus overbook limit")
	}
	return nil
}

// AdjustAuth 调整某航段某等级的授权量。允许调到低于当前已占用数（该等级
// 及更低等级随后不可售，已有占用不受影响），但不得破坏嵌套次序与上限。
func (s *System) AdjustAuth(segID string, class int, newAuth int, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if segID == "" {
		return errInvalid("segment id must be non-empty")
	}
	if class < 0 || class >= NumClasses {
		return errInvalid("class out of range")
	}
	if newAuth < 0 {
		return errInvalid("authorization must be non-negative")
	}
	if now < 0 {
		return errInvalid("time must be non-negative")
	}
	// 嵌套次序与上限属于“参数非法”，按拒绝次序须先于时钟回退判定；
	// 航段不存在时无法评估嵌套，跳过（随后报不存在）。
	if seg, ok := s.segs[segID]; ok {
		candidate := seg.Auth
		candidate[class] = newAuth
		if err := checkAuth(candidate, seg.Physical, seg.Overbook); err != nil {
			return err
		}
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	seg, ok := s.segs[segID]
	if !ok {
		return errNotFound("segment " + segID)
	}
	seg.Auth[class] = newAuth
	s.now = now
	return nil
}

// checkLegs 校验行程与旅客人数的参数合法性。
func checkLegs(legs []Leg, pax int) *Error {
	if len(legs) < 1 || len(legs) > 4 {
		return errInvalid("itinerary must have 1 to 4 legs")
	}
	if pax < 1 || pax > 9 {
		return errInvalid("passenger count must be between 1 and 9")
	}
	seen := make(map[string]bool, len(legs))
	for _, l := range legs {
		if l.Segment == "" {
			return errInvalid("segment id must be non-empty")
		}
		if l.Class < 0 || l.Class >= NumClasses {
			return errInvalid("class out of range")
		}
		if seen[l.Segment] {
			return errInvalid("segment " + l.Segment + " appears twice in itinerary")
		}
		seen[l.Segment] = true
	}
	return nil
}

// Hold 预占一条行程的 pax 个座位，全有或全无。成功时返回条目标识与到期
// 时刻（= 接受时刻 + 预占时长）；任一航段不满足即整条拒绝，报出的航段为
// 行程顺序中第一个不满足的航段，且不改动任何状态。
func (s *System) Hold(legs []Leg, pax int, now int64) (string, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 {
		return "", 0, errInvalid("time must be non-negative")
	}
	if err := checkLegs(legs, pax); err != nil {
		return "", 0, err
	}
	if err := s.checkClock(now); err != nil {
		return "", 0, err
	}
	for _, l := range legs {
		if _, ok := s.segs[l.Segment]; !ok {
			return "", 0, &Error{Kind: KindNotFound, Segment: l.Segment,
				Msg: "segment " + l.Segment + " does not exist"}
		}
	}
	// 评估可用数：now == 系统当前时刻时直接提交清扫（摊还 O(1)）；now 更
	// 大时只能只读探测，因为本操作可能被拒绝，而被拒绝的操作不得改变任
	// 何状态。
	var expired [4][NumClasses]int
	for i, l := range legs {
		seg := s.segs[l.Segment]
		if now > s.now {
			expired[i] = s.probeExpired(seg, now)
		} else {
			s.sweep(seg, now)
		}
	}
	for i, l := range legs {
		if !s.segs[l.Segment].fitsWith(l.Class, pax, expired[i]) {
			return "", 0, &Error{Kind: KindInsufficient, Segment: l.Segment,
				Msg: fmt.Sprintf("segment %s class %d cannot sell %d seat(s)", l.Segment, l.Class, pax)}
		}
	}
	// 操作被接受：提交清扫（与只读探测扫描同一前缀，每条记录至多被提交
	// 一次），然后占用座位。
	if now > s.now {
		for _, l := range legs {
			s.sweep(s.segs[l.Segment], now)
		}
	}
	id := fmt.Sprintf("E%06d", s.nextID)
	s.nextID++
	expiry := now + s.holdDur
	s.entries[id] = &Entry{ID: id, Status: StatusHold, Legs: append([]Leg(nil), legs...), Pax: pax, Expiry: expiry}
	for _, l := range legs {
		seg := s.segs[l.Segment]
		seg.held[l.Class] += pax
		seg.queue = append(seg.queue, qentry{expiry: expiry, class: l.Class, count: pax, entryID: id})
	}
	s.now = now
	return id, expiry, nil
}

// findEntry 实现出票/取消共用的前置拒绝链：参数非法 > 时钟回退 > 不存在
// > 预占已过期。
func (s *System) findEntry(id string, now int64) (*Entry, *Error) {
	if id == "" {
		return nil, errInvalid("entry id must be non-empty")
	}
	if now < 0 {
		return nil, errInvalid("time must be non-negative")
	}
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	e, ok := s.entries[id]
	if !ok {
		return nil, errNotFound("entry " + id)
	}
	if e.Status == StatusHold && now >= e.Expiry {
		return nil, &Error{Kind: KindHoldExpired,
			Msg: fmt.Sprintf("hold %s expired at %d (now %d)", id, e.Expiry, now)}
	}
	return e, nil
}

// Ticket 把一条未过期预占转为已确认出票，已确认的占用永不过期。出票只改
// 变占用性质（held -> confirmed），不改变占用总量。
func (s *System) Ticket(id string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.findEntry(id, now)
	if err != nil {
		return err
	}
	if e.Status != StatusHold {
		return errConflict(e.Status, id)
	}
	for _, l := range e.Legs {
		seg := s.segs[l.Segment]
		seg.held[l.Class] -= e.Pax
		seg.confirmed[l.Class] += e.Pax
	}
	e.Status = StatusTicketed
	s.now = now
	return nil
}

// Cancel 取消一条预占或已出票记录，占用立即释放。取消总是整条行程。
func (s *System) Cancel(id string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.findEntry(id, now)
	if err != nil {
		return err
	}
	if e.Status == StatusCancelled {
		return errConflict(StatusCancelled, id)
	}
	for _, l := range e.Legs {
		seg := s.segs[l.Segment]
		if e.Status == StatusHold {
			seg.held[l.Class] -= e.Pax
		} else { // StatusTicketed
			seg.confirmed[l.Class] -= e.Pax
		}
	}
	e.Status = StatusCancelled
	s.now = now
	return nil
}

// Availability 是只读查询：返回某航段某等级在系统当前时刻的可用数。
func (s *System) Availability(segID string, class int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if class < 0 || class >= NumClasses {
		return 0, errInvalid("class out of range")
	}
	seg, ok := s.segs[segID]
	if !ok {
		return 0, errNotFound("segment " + segID)
	}
	s.sweep(seg, s.now)
	return seg.avail(class), nil
}

// CanHold 是只读查询：判定一条行程在系统当前时刻是否可售。不可售时返回
// 行程顺序中第一个不满足的航段。不改变时钟与任何条目状态。
func (s *System) CanHold(legs []Leg, pax int) (bool, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := checkLegs(legs, pax); err != nil {
		return false, "", err
	}
	for _, l := range legs {
		if _, ok := s.segs[l.Segment]; !ok {
			return false, "", &Error{Kind: KindNotFound, Segment: l.Segment,
				Msg: "segment " + l.Segment + " does not exist"}
		}
	}
	for _, l := range legs {
		s.sweep(s.segs[l.Segment], s.now)
	}
	for _, l := range legs {
		if !s.segs[l.Segment].fits(l.Class, pax) {
			return false, l.Segment, nil
		}
	}
	return true, "", nil
}

// Lookup 是只读查询：返回条目快照，可区分不存在 / 预占 / 已出票 / 已取消。
func (s *System) Lookup(id string) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[id]
	if !ok {
		return Entry{}, errNotFound("entry " + id)
	}
	cp := *e
	cp.Legs = append([]Leg(nil), e.Legs...)
	return cp, nil
}
