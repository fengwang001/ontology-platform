package booking

import (
	"errors"

	"ontology/credit"
	"ontology/slotpool"
)

var (
	ErrInvalid     = errors.New("invalid argument")
	ErrClockBack   = errors.New("clock moved backwards")
	ErrSlotMissing = errors.New("slot not found")
	ErrOpen        = errors.New("slot already open")
	ErrDuplicate   = errors.New("patient already booked or waiting")
	ErrBanned      = errors.New("patient is banned from online booking")
	ErrNoQuota     = errors.New("no quota available")
	ErrTooEarly    = errors.New("check-in too early")
	ErrNoBooking   = errors.New("no active booking")
	ErrAlreadyIn   = errors.New("already checked in")
	ErrLateCancel  = errors.New("late cancellation")
	ErrNotWaitable = errors.New("cannot join waitlist")
)

// New 创建系统。参数越界 panic（构造期编程错误）。
func New(r, e, g, c int, k, w int) *System {
	if r < 0 || r > 10000 || e < 0 || e > 10000 || g < 0 || g > 10000 || c < 0 || c > 10000 {
		panic("booking: R/E/G/C out of range")
	}
	if k < 1 || k > 10 || w < 1 || w > 10000000 {
		panic("booking: K/W out of range")
	}
	return &System{
		r: int64(r), e: int64(e), g: int64(g), c: int64(c),
		k: int64(k), w: int64(w),
		pool:     slotpool.New(),
		cred:     credit.New(),
		slots:    map[string]*slotState{},
		due:      &dueHeap{},
		waitDead: &waitHeap{},
	}
}

func validChannel(ch slotpool.Channel) bool { return ch == slotpool.Online || ch == slotpool.OnSite }

// precheck 执行无副作用的前三项校验：参数非法 > 时钟回退 > 时段不存在。
// 通过则已持锁；落地由调用方完成。
func (s *System) precheck(now int64, slot string, patient []byte) (*slotState, *slotpool.Slot, error) {
	if slot == "" || patient == nil || len(patient) == 0 {
		return nil, nil, ErrInvalid
	}
	s.mu.Lock()
	if now < s.maxNow {
		s.mu.Unlock()
		return nil, nil, ErrClockBack
	}
	st := s.slots[slot]
	sp := s.pool.Get(slot)
	if st == nil || sp == nil {
		s.mu.Unlock()
		return nil, nil, ErrSlotMissing
	}
	return st, sp, nil
}

// land 落地所有已到期未签到预约（dead < now，恰等不算），
// 每释放一号立即递补候补；并经 waitDead 堆惰性作废槽死后仍在队的候补。
// 调用方须持锁。到期堆：每次 Pop 必对应一次落地，至多额外探测堆顶 1 次。
func (s *System) land(now int64) {
	s.landTaken = 0
	s.landProbed = 0
	s.landApplied = 0
	for s.due.Len() > 0 {
		top := s.due.list[0]
		if top.dead >= now {
			s.landProbed = 1
			break
		}
		popRemove(s.due, 0)
		s.landTaken++
		s.applyNoShow(top, now)
	}
	for s.waitDead.Len() > 0 {
		top := s.waitDead.list[0]
		if top.dead >= now || top.pending != 0 {
			break
		}
		s.waitDead.remove(top)
		if st := s.slots[top.slot]; st != nil && st.waitItem == top {
			st.waitItem = nil
			st.wait = nil
		}
	}
	s.maxNow = now
}

// syncWaitItem 依据候补是否存在与 pending 维护槽的作废堆项。
func (s *System) syncWaitItem(st *slotState) {
	if len(st.wait) > 0 && st.pending == 0 && st.waitItem == nil {
		st.waitItem = &waitHeapItem{slot: st.deadSlot, dead: st.dead, pending: 0}
		s.waitDead.push(st.waitItem)
		return
	}
	if (len(st.wait) == 0 || st.pending > 0) && st.waitItem != nil {
		s.waitDead.remove(st.waitItem)
		st.waitItem = nil
	}
}

// applyNoShow 把一条到期预约落地为时刻 dead 的爽约：释放号源、记 credit、递补候补。
func (s *System) applyNoShow(e *entry, now int64) {
	st := s.slots[e.slot]
	sp := s.pool.Get(e.slot)
	delete(st.active, e.patient)
	st.pending--
	s.pool.Release(e.slot, e.channel)
	s.cred.Add(e.patient, e.dead)
	s.landApplied++
	if sp != nil {
		s.promote(e.slot, st, sp, now)
	}
	s.syncWaitItem(st)
}

// AddSlot 增加时段。
func (s *System) AddSlot(now int64, slot string, start int64, cap, on int) error {
	if slot == "" || start <= now || cap < 1 || cap > 1000 || on < 0 || on > cap {
		return ErrInvalid
	}
	s.mu.Lock()
	if now < s.maxNow {
		s.mu.Unlock()
		return ErrClockBack
	}
	s.land(now)
	if _, ok := s.slots[slot]; ok {
		s.mu.Unlock()
		return ErrInvalid
	}
	if !s.pool.Add(slot, start, cap, on, s.r) {
		s.mu.Unlock()
		return ErrInvalid
	}
	s.slots[slot] = &slotState{
		active:   map[string]*entry{},
		dead:     start + s.g,
		deadSlot: slot,
		waitItem: nil,
	}
	s.mu.Unlock()
	return nil
}

// Book 订号，成功返回全局递增预约序号。
func (s *System) Book(now int64, patient []byte, slot string, ch slotpool.Channel) (int64, error) {
	if !validChannel(ch) {
		return 0, ErrInvalid
	}
	st, sp, err := s.precheck(now, slot, patient)
	if err != nil {
		return 0, err
	}
	defer s.mu.Unlock()
	s.land(now)
	if now >= sp.Start {
		return 0, ErrOpen
	}
	key := string(patient)
	if _, ok := st.active[key]; ok {
		return 0, ErrDuplicate
	}
	if _, ok := s.waiting(st, key); ok {
		return 0, ErrDuplicate
	}
	if ch == slotpool.Online && s.cred.Banned(now, key, s.k, s.w) {
		return 0, ErrBanned
	}
	if !s.pool.TryBook(slot, now, ch) {
		return 0, ErrNoQuota
	}
	s.seq++
	e := &entry{patient: key, slot: slot, seq: s.seq, dead: sp.Start + s.g, channel: ch, heapIdx: -1}
	st.active[key] = e
	st.pending++
	heapPush(s.due, e)
	s.syncWaitItem(st)
	return s.seq, nil
}

// CheckIn 签到。
func (s *System) CheckIn(now int64, patient []byte, slot string) error {
	st, sp, err := s.precheck(now, slot, patient)
	if err != nil {
		return err
	}
	defer s.mu.Unlock()
	s.land(now)
	if now < sp.Start-s.e {
		return ErrTooEarly
	}
	key := string(patient)
	e, ok := st.active[key]
	if !ok {
		return ErrNoBooking
	}
	if e.checked {
		return nil
	}
	if now > sp.Start+s.g {
		return ErrNoBooking
	}
	e.checked = true
	st.pending--
	popRemove(s.due, e.heapIdx)
	s.syncWaitItem(st)
	return nil
}

// Cancel 退号。
func (s *System) Cancel(now int64, patient []byte, slot string) error {
	st, sp, err := s.precheck(now, slot, patient)
	if err != nil {
		return err
	}
	defer s.mu.Unlock()
	s.land(now)
	key := string(patient)
	e, ok := st.active[key]
	if !ok {
		return ErrNoBooking
	}
	if e.checked {
		return ErrAlreadyIn
	}
	popRemove(s.due, e.heapIdx)
	delete(st.active, key)
	st.pending--
	s.pool.Release(slot, e.channel)
	s.promote(slot, st, sp, now)
	s.syncWaitItem(st)
	if now <= sp.Start-s.c {
		return nil
	}
	s.cred.Add(key, now)
	return ErrLateCancel
}

// JoinWait 加入候补。
func (s *System) JoinWait(now int64, patient []byte, slot string) error {
	st, sp, err := s.precheck(now, slot, patient)
	if err != nil {
		return err
	}
	defer s.mu.Unlock()
	s.land(now)
	key := string(patient)
	if _, ok := st.active[key]; ok {
		return ErrDuplicate
	}
	if _, ok := s.waiting(st, key); ok {
		return ErrDuplicate
	}
	waitable := sp.Start-s.r <= now && now <= sp.Start+s.g && sp.UO+sp.US >= sp.Cap
	if !waitable {
		return ErrNotWaitable
	}
	st.wait = append(st.wait, waiter{patient: key})
	s.syncWaitItem(st)
	return nil
}

// Banned 查询禁约；只读、不落地、不推进时钟。
func (s *System) Banned(now int64, patient []byte) bool {
	return s.cred.Banned(now, string(patient), s.k, s.w)
}

func (s *System) waiting(st *slotState, key string) (int, bool) {
	for i, w := range st.wait {
		if w.patient == key {
			return i, true
		}
	}
	return 0, false
}

// promote 用释放出的号立即递补候补队首；候补一律现场（计 us）。
func (s *System) promote(slot string, st *slotState, sp *slotpool.Slot, now int64) {
	if len(st.wait) == 0 {
		return
	}
	uo, us := sp.UO, sp.US
	if uo+us >= sp.Cap {
		return
	}
	w := st.wait[0]
	st.wait = st.wait[1:]
	s.pool.TryBook(slot, now, slotpool.OnSite)
	s.seq++
	e := &entry{patient: w.patient, slot: slot, seq: s.seq, dead: sp.Start + s.g, channel: slotpool.OnSite, heapIdx: -1}
	if now > sp.Start {
		e.checked = true
	} else {
		st.pending++
		heapPush(s.due, e)
	}
	st.active[w.patient] = e
	s.syncWaitItem(st)
}
