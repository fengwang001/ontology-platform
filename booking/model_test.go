package booking

import (
	"fmt"
	"sort"
	"strings"
)

// modelOp 朴素模拟与真实实现共用的操作描述。
type modelOp struct {
	kind    string // addslot/book/checkin/cancel/joinwait
	now     int64
	p, slot string
	ch      Channel
	start   int64
	cap, on int
}

type modelRes struct {
	id        int64
	checkedIn bool
}

// naive 完全照题目规则逐步书写的朴素参照。允许 O(n) 扫描；
// 被接受操作先在快照副本上落地与判定，拒绝即整体恢复（镜像事务回滚）。
type naive struct {
	r, e, g, c int64
	k          int
	w          int64
	now        int64
	nextID     int64
	slots      map[string]mSlot
	res        map[int64]mRes
	hold       map[string]int64 // p\x00slot -> id
	wait       map[string][]string
	waiting    map[string]bool
	records    map[string][]int64
}

type mSlot struct {
	start   int64
	cap, on int
}

type mRes struct {
	id        int64
	p         string
	slot      string
	ch        Channel
	checkedIn bool
	dueAt     int64
}

func newNaive(r, e, g, c int64, k int, w int64) *naive {
	return &naive{
		r: r, e: e, g: g, c: c, k: k, w: w, nextID: 1,
		slots:   map[string]mSlot{},
		res:     map[int64]mRes{},
		hold:    map[string]int64{},
		wait:    map[string][]string{},
		waiting: map[string]bool{},
		records: map[string][]int64{},
	}
}

type snap struct {
	now     int64
	nextID  int64
	slots   map[string]mSlot
	res     map[int64]mRes
	hold    map[string]int64
	wait    map[string][]string
	waiting map[string]bool
	records map[string][]int64
}

func cpSlice[V any](x []V) []V {
	if x == nil {
		return nil
	}
	return append([]V(nil), x...)
}

func cpMap[K comparable, V any](m map[K]V, deep func(V) V) map[K]V {
	out := make(map[K]V, len(m))
	for k, v := range m {
		if deep != nil {
			v = deep(v)
		}
		out[k] = v
	}
	return out
}

func (m *naive) snapshot() snap {
	return snap{
		now:     m.now,
		nextID:  m.nextID,
		slots:   cpMap(m.slots, func(s mSlot) mSlot { return s }),
		res:     cpMap(m.res, func(r mRes) mRes { return r }),
		hold:    cpMap(m.hold, nil),
		wait:    cpMap(m.wait, func(w []string) []string { return cpSlice(w) }),
		waiting: cpMap(m.waiting, nil),
		records: cpMap(m.records, func(r []int64) []int64 { return cpSlice(r) }),
	}
}

func (m *naive) restore(s snap) {
	m.now, m.nextID = s.now, s.nextID
	m.slots, m.res, m.hold = s.slots, s.res, s.hold
	m.wait, m.waiting, m.records = s.wait, s.waiting, s.records
}

func (m *naive) counts(slotName string) (uo, us int) {
	for _, r := range m.res {
		if r.slot != slotName {
			continue
		}
		if r.ch == Online {
			uo++
		} else {
			us++
		}
	}
	return
}

func (m *naive) canBook(slotName string, now int64, ch Channel) bool {
	s := m.slots[slotName]
	uo, us := m.counts(slotName)
	if now < s.start-m.r {
		if ch == Online {
			return uo < s.on
		}
		return us < s.cap-s.on
	}
	return uo+us < s.cap
}

func (m *naive) banned(now int64, p string) bool {
	rec := m.records[p]
	cnt := 0
	for i := len(rec) - 1; i >= 0; i-- {
		if rec[i] <= now-m.w {
			break
		}
		if cnt++; cnt >= m.k {
			return true
		}
	}
	return false
}

// settle 先按 (dueAt,id) 升序落地全部到期未签到预约，
// 再把全槽落地结束后仍在队中的候补作废。
func (m *naive) settle(now int64) {
	type due struct {
		at int64
		id int64
	}
	var pending []due
	for id, r := range m.res {
		if !r.checkedIn {
			pending = append(pending, due{r.dueAt, id})
		}
	}
	sort.Slice(pending, func(i, j int) bool {
		if pending[i].at != pending[j].at {
			return pending[i].at < pending[j].at
		}
		return pending[i].id < pending[j].id
	})
	for _, d := range pending {
		if d.at >= now { // 恰等不算
			break
		}
		r := m.res[d.id]
		m.landOne(d.id, r, d.at, now)
	}
	for slotName, s := range m.slots {
		if now > s.start+m.g && len(m.wait[slotName]) > 0 {
			for _, p := range m.wait[slotName] {
				delete(m.waiting, p+"\x00"+slotName)
			}
			m.wait[slotName] = nil
		}
	}
}

func (m *naive) landOne(id int64, r mRes, dueAt, now int64) {
	slotName := r.slot
	s := m.slots[slotName]
	delete(m.res, id)
	delete(m.hold, r.p+"\x00"+slotName)
	m.records[r.p] = append(m.records[r.p], dueAt)
	if len(m.wait[slotName]) > 0 {
		p := m.wait[slotName][0]
		m.wait[slotName] = m.wait[slotName][1:]
		delete(m.waiting, p+"\x00"+slotName)
		id := m.nextID
		m.nextID++
		nr := mRes{id: id, p: p, slot: slotName, ch: Onsite, dueAt: s.start + m.g}
		if now > s.start {
			nr.checkedIn = true
		}
		m.res[id] = nr
		m.hold[p+"\x00"+slotName] = id
	}
}

func (m *naive) promote(slotName string, now int64) {
	s := m.slots[slotName]
	if len(m.wait[slotName]) == 0 {
		return
	}
	p := m.wait[slotName][0]
	m.wait[slotName] = m.wait[slotName][1:]
	delete(m.waiting, p+"\x00"+slotName)
	id := m.nextID
	m.nextID++
	nr := mRes{id: id, p: p, slot: slotName, ch: Onsite, dueAt: s.start + m.g}
	if now > s.start {
		nr.checkedIn = true
	}
	m.res[id] = nr
	m.hold[p+"\x00"+slotName] = id
}

// run 返回 (id, errName)。
func (m *naive) run(op modelOp) (int64, string) {
	chOK := op.ch == Online || op.ch == Onsite
	switch op.kind {
	case "addslot":
		if op.now < 0 || op.slot == "" || op.start <= op.now ||
			op.cap < 1 || op.cap > 1000 || op.on < 0 || op.on > op.cap {
			return 0, ErrInvalid.Error()
		}
		if op.now < m.now {
			return 0, ErrClockRollback.Error()
		}
		if _, dup := m.slots[op.slot]; dup {
			return 0, ErrInvalid.Error()
		}
		m.settle(op.now)
		m.slots[op.slot] = mSlot{start: op.start, cap: op.cap, on: op.on}
		m.now = op.now
		return 0, "ok"
	}
	if op.now < 0 || op.p == "" || op.slot == "" || !chOK {
		return 0, ErrInvalid.Error()
	}
	if op.now < m.now {
		return 0, ErrClockRollback.Error()
	}
	s, ok := m.slots[op.slot]
	if !ok {
		return 0, ErrSlotNotFound.Error()
	}
	pre := m.snapshot()
	m.settle(op.now)
	key := op.p + "\x00" + op.slot
	reject := func(e error) (int64, string) {
		m.restore(pre)
		return 0, e.Error()
	}
	switch op.kind {
	case "book":
		if op.now >= s.start {
			return reject(ErrSlotOpen)
		}
		if id, dup := m.hold[key]; dup {
			_ = id
			return reject(ErrDuplicate)
		}
		if m.waiting[key] {
			return reject(ErrDuplicate)
		}
		if op.ch == Online && m.banned(op.now, op.p) {
			return reject(ErrBanned)
		}
		if !m.canBook(op.slot, op.now, op.ch) {
			return reject(ErrNoQuota)
		}
		id := m.nextID
		m.nextID++
		m.res[id] = mRes{id: id, p: op.p, slot: op.slot, ch: op.ch, dueAt: s.start + m.g}
		m.hold[key] = id
		m.now = op.now
		return id, "ok"
	case "checkin":
		if op.now < s.start-m.e {
			return reject(ErrTooEarly)
		}
		id, has := m.hold[key]
		if !has {
			return reject(ErrNoReservation)
		}
		r := m.res[id]
		if r.checkedIn {
			return reject(ErrAlreadyChecked)
		}
		r.checkedIn = true
		m.res[id] = r
		m.now = op.now
		return 0, "ok"
	case "cancel":
		id, has := m.hold[key]
		if !has {
			return reject(ErrNoReservation)
		}
		r := m.res[id]
		if r.checkedIn {
			return reject(ErrAlreadyChecked)
		}
		delete(m.res, id)
		delete(m.hold, key)
		if op.now > s.start-m.c {
			m.records[op.p] = append(m.records[op.p], op.now)
		}
		m.promote(op.slot, op.now)
		m.now = op.now
		return 0, "ok"
	case "joinwait":
		if _, dup := m.hold[key]; dup || m.waiting[key] {
			return reject(ErrDuplicate)
		}
		if op.ch != Onsite || op.now < s.start-m.r || op.now > s.start+m.g {
			return reject(ErrCannotWait)
		}
		uo, us := m.counts(op.slot)
		if uo+us < s.cap {
			return reject(ErrCannotWait)
		}
		m.wait[op.slot] = append(m.wait[op.slot], op.p)
		m.waiting[key] = true
		m.now = op.now
		return 0, "ok"
	}
	return reject(ErrInvalid)
}

func (m *naive) digest() string {
	uo, us := map[string]int{}, map[string]int{}
	for _, r := range m.res {
		if r.ch == Online {
			uo[r.slot]++
		} else {
			us[r.slot]++
		}
	}
	slots := make([]string, 0, len(m.slots))
	for k := range m.slots {
		slots = append(slots, k)
	}
	sort.Strings(slots)
	var b strings.Builder
	for _, sn := range slots {
		fmt.Fprintf(&b, "%s{uo=%d,us=%d,w=%d};", sn, uo[sn], us[sn], len(m.wait[sn]))
	}
	ps := make([]string, 0)
	for p, rec := range m.records {
		ps = append(ps, fmt.Sprintf("%s%v", p, rec))
	}
	sort.Strings(ps)
	fmt.Fprintf(&b, "rec=%v,next=%d,now=%d", ps, m.nextID, m.now)
	return b.String()
}
