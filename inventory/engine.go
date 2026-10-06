package inventory

import "sync"

// Engine 多航段联程座位库存与超售控制系统。
//
// 所有方法可并发调用：内部以单一互斥锁串行化，结果等价于按加锁顺序的
// 某个串行执行。条目号由单调计数器产生，到期时刻 = 接受时刻 + 预占时长，
// 不依赖任何真实时钟或随机源，因此相同操作序列重放得到完全相同的结果。
type Engine struct {
	mu       sync.Mutex
	holdDur  uint64 // 预占时长（秒），系统配置，创建后不变
	lastTime uint64 // 上一次被接受操作的时刻
	nextID   uint64 // 下一个条目号，从 1 开始
	segments map[string]*segment
	entries  map[uint64]*entry
	stats    PerfStats
}

// NewEngine 创建一个空系统，holdDurationSeconds 为预占时长配置。
func NewEngine(holdDurationSeconds uint64) *Engine {
	return &Engine{
		holdDur:  holdDurationSeconds,
		nextID:   1,
		segments: make(map[string]*segment),
		entries:  make(map[uint64]*entry),
	}
}

// Stats 返回内部工作量计数（用于性能证明），只读。
func (e *Engine) Stats() PerfStats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stats
}

// LastTime 返回上一次被接受操作的时刻，只读。
func (e *Engine) LastTime() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastTime
}

// checkClock 校验时钟回退。被拒绝的操作不推进时钟。
func (e *Engine) checkClock(now uint64) *Error {
	if now < e.lastTime {
		return &Error{Kind: ErrClockRollback, Detail: "当前时刻小于上一次被接受操作的时刻"}
	}
	return nil
}

// sweepForQuery 只读查询前的惰性归档：归档水位只推进到
// min(t, lastTime)，保证对未来一切被接受操作（时刻 >= lastTime）
// 已过期的预占才会被归档，语义完全中性。
func (e *Engine) sweepForQuery(s *segment, t uint64) {
	target := t
	if target > e.lastTime {
		target = e.lastTime
	}
	s.sweep(target, &e.stats)
}

// AddSegment 注册一个航段。航段标识非空、物理座位数为正、超售上限非负、
// 三级授权量满足嵌套约束，否则报参数非法；重复注册同标识航段也报参数非法。
func (e *Engine) AddSegment(now uint64, id string, seats, overbook int, auth [3]int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" {
		return paramErr("航段标识为空")
	}
	if seats <= 0 {
		return paramErr("物理座位数必须为正整数")
	}
	if overbook < 0 {
		return paramErr("超售上限必须为非负整数")
	}
	if err := checkAuth(seats, overbook, auth); err != nil {
		return err
	}
	if _, dup := e.segments[id]; dup {
		return paramErr("航段已存在: " + id)
	}
	if err := e.checkClock(now); err != nil {
		return err
	}
	e.segments[id] = &segment{id: id, seats: seats, overbook: overbook, auth: auth}
	e.lastTime = now
	return nil
}

// AdjustAuth 调整航段某舱位等级的授权量。允许调到低于当前已占用数；
// 不得破坏嵌套次序，最高等级不得超过物理座位数与超售上限之和。
func (e *Engine) AdjustAuth(now uint64, id string, class, newAuth int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" {
		return paramErr("航段标识为空")
	}
	if class < 0 || class > 2 {
		return paramErr("舱位等级必须为 0..2")
	}
	if newAuth < 0 {
		return paramErr("授权量不得为负")
	}
	s, ok := e.segments[id]
	if ok {
		// 嵌套次序属于参数非法，按拒绝次序先于时钟回退判定。
		if err := s.checkAuthAdjust(class, newAuth); err != nil {
			return err
		}
	}
	if err := e.checkClock(now); err != nil {
		return err
	}
	if !ok {
		return &Error{Kind: ErrNotFound, Segment: id, Detail: "航段不存在"}
	}
	s.auth[class] = newAuth
	e.lastTime = now
	return nil
}

// checkLegs 校验行程参数（预占与可售判定共用）。
func checkLegs(legs []Leg, pax int) *Error {
	if pax < 1 || pax > 9 {
		return paramErr("旅客人数必须为 1..9")
	}
	if len(legs) < 1 || len(legs) > 4 {
		return paramErr("行程必须由一到四个航段组成")
	}
	seen := make(map[string]bool, len(legs))
	for _, l := range legs {
		if l.Segment == "" {
			return paramErr("航段标识为空")
		}
		if l.Class < 0 || l.Class > 2 {
			return paramErr("舱位等级必须为 0..2")
		}
		if seen[l.Segment] {
			return paramErr("同一航段在一条行程中出现两次: " + l.Segment)
		}
		seen[l.Segment] = true
	}
	return nil
}

// Hold 预占：一条行程（1..4 个航段，各自指定舱位等级）加旅客人数（1..9）。
// 全有或全无：任一航段不满足即整条拒绝并报库存不足，报出的航段为行程
// 顺序中第一个不满足的航段；被拒绝的预占不改动任何状态。
// 成功时返回条目号与到期时刻（= 接受时刻 + 预占时长）。
func (e *Engine) Hold(now uint64, legs []Leg, pax int) (uint64, uint64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := checkLegs(legs, pax); err != nil {
		return 0, 0, err
	}
	if err := e.checkClock(now); err != nil {
		return 0, 0, err
	}
	segs := make([]*segment, len(legs))
	for i, l := range legs {
		s, ok := e.segments[l.Segment]
		if !ok {
			return 0, 0, &Error{Kind: ErrNotFound, Segment: l.Segment, Detail: "航段不存在"}
		}
		segs[i] = s
	}
	for _, s := range segs {
		s.sweep(now, &e.stats)
	}
	for i, l := range legs {
		if !segs[i].legFits(l.Class, pax, now, &e.stats) {
			return 0, 0, &Error{Kind: ErrInsufficientInventory, Segment: l.Segment, Detail: "航段可用数不足"}
		}
	}
	id := e.nextID
	e.nextID++
	expiry := now + e.holdDur
	ent := &entry{
		id:     id,
		legs:   append([]Leg(nil), legs...),
		pax:    pax,
		expiry: expiry,
		state:  StateHold,
		hotIdx: make([]int, len(legs)),
	}
	for i, l := range legs {
		ent.hotIdx[i] = segs[i].occ[l.Class].addHold(expiry, pax)
	}
	e.entries[id] = ent
	e.lastTime = now
	return id, expiry, nil
}

// findEntry 出票/取消共用的前置检查：时钟回退 > 不存在 > 已过期。
func (e *Engine) findEntry(now, id uint64) (*entry, *Error) {
	if id == 0 {
		return nil, paramErr("条目号非法")
	}
	if err := e.checkClock(now); err != nil {
		return nil, err
	}
	ent, ok := e.entries[id]
	if !ok {
		return nil, &Error{Kind: ErrNotFound, EntryID: id, Detail: "条目不存在"}
	}
	// 已出票的条目永不过期；预占（含预占后被取消）在到期时刻及之后视为已过期。
	if ent.state.expires() && now >= ent.expiry {
		return nil, &Error{Kind: ErrHoldExpired, EntryID: id, Detail: "预占已过期"}
	}
	return ent, nil
}

// Confirm 出票：把一条未过期预占转为已确认，已确认的占用永不过期。
func (e *Engine) Confirm(now, id uint64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	ent, err := e.findEntry(now, id)
	if err != nil {
		return err
	}
	if ent.state != StateHold {
		return &Error{Kind: ErrStateConflict, EntryID: id, State: ent.state, Detail: "只有预占中的条目可以出票"}
	}
	for i, l := range ent.legs {
		s := e.segments[l.Segment]
		s.sweep(now, &e.stats)
		c := &s.occ[l.Class]
		c.removeHot(ent.hotIdx[i], ent.pax)
		c.confirmed += ent.pax
	}
	ent.state = StateConfirmed
	e.lastTime = now
	return nil
}

// Cancel 取消：既可针对预占也可针对已确认票，取消后占用立即释放。
// 取消总是整条行程，不支持部分取消。
func (e *Engine) Cancel(now, id uint64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	ent, err := e.findEntry(now, id)
	if err != nil {
		return err
	}
	switch ent.state {
	case StateHold:
		for i, l := range ent.legs {
			s := e.segments[l.Segment]
			s.sweep(now, &e.stats)
			s.occ[l.Class].removeHot(ent.hotIdx[i], ent.pax)
		}
		ent.state = StateCancelledHold
	case StateConfirmed:
		for _, l := range ent.legs {
			s := e.segments[l.Segment]
			s.occ[l.Class].confirmed -= ent.pax
		}
		ent.state = StateCancelledTicket
	default:
		return &Error{Kind: ErrStateConflict, EntryID: id, State: ent.state, Detail: "条目已取消"}
	}
	e.lastTime = now
	return nil
}

// Availability 查询某航段某舱位等级在时刻 t 的可用数（可以为负）。
// 只读查询不参与时钟约束；结果是当前已确认出票与尚未到期预占的纯函数。
func (e *Engine) Availability(t uint64, segID string, class int) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if segID == "" {
		return 0, paramErr("航段标识为空")
	}
	if class < 0 || class > 2 {
		return 0, paramErr("舱位等级必须为 0..2")
	}
	s, ok := e.segments[segID]
	if !ok {
		return 0, &Error{Kind: ErrNotFound, Segment: segID, Detail: "航段不存在"}
	}
	e.sweepForQuery(s, t)
	return s.avail(class, t, &e.stats), nil
}

// Sellable 判定某航段某舱位等级在时刻 t 是否可售：
// 该等级及所有比它更高的等级的可用数都大于零。
func (e *Engine) Sellable(t uint64, segID string, class int) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if segID == "" {
		return false, paramErr("航段标识为空")
	}
	if class < 0 || class > 2 {
		return false, paramErr("舱位等级必须为 0..2")
	}
	s, ok := e.segments[segID]
	if !ok {
		return false, &Error{Kind: ErrNotFound, Segment: segID, Detail: "航段不存在"}
	}
	e.sweepForQuery(s, t)
	return s.sellable(class, t, &e.stats), nil
}

// Quote 判定一条行程在时刻 t 是否可售（与预占使用完全相同的库存规则，
// 但不改动任何状态）。返回是否可售，以及不可售时行程顺序中第一个
// 不满足的航段。
func (e *Engine) Quote(t uint64, legs []Leg, pax int) (bool, string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := checkLegs(legs, pax); err != nil {
		return false, "", err
	}
	segs := make([]*segment, len(legs))
	for i, l := range legs {
		s, ok := e.segments[l.Segment]
		if !ok {
			return false, "", &Error{Kind: ErrNotFound, Segment: l.Segment, Detail: "航段不存在"}
		}
		segs[i] = s
	}
	for _, s := range segs {
		e.sweepForQuery(s, t)
	}
	for i, l := range legs {
		if !segs[i].legFits(l.Class, pax, t, &e.stats) {
			return false, l.Segment, nil
		}
	}
	return true, "", nil
}
