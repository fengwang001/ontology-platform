// Package flowtable 实现带优先级、重叠检查、超时与驱逐的流表。
//
// 所有操作可并发调用，结果等价于某个串行顺序。每个被接受的操作
// 开始时先落地到期表项（产生事件），再执行自身判定；被拒绝的操作
// 不改任何状态、不落地到期、不推进时钟。
package flowtable

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"ontology/match"
)

// 哨兵错误，可用 errors.Is 区分。
var (
	ErrInvalidParam = errors.New("flowtable: invalid parameter")
	ErrClock        = errors.New("flowtable: clock regression")
	ErrOverlap      = errors.New("flowtable: overlapping entry with same priority")
	ErrTableFull    = errors.New("flowtable: table full")
)

const (
	maxCapacity = 100000
	maxTimeout  = 1_000_000_000
	maxClock    = 1_000_000_000_000
)

// Reason 是移除事件的原因。
type Reason int

const (
	Idle   Reason = iota // idle 超时
	Hard                 // hard 超时
	Delete               // Delete 操作移除
	Evict                // 被新表项驱逐
)

func (r Reason) String() string {
	switch r {
	case Idle:
		return "Idle"
	case Hard:
		return "Hard"
	case Delete:
		return "Delete"
	case Evict:
		return "Evict"
	}
	return "Unknown"
}

// Event 是一条移除事件：（安装序号，原因，时刻，包计数，字节计数）。
// 到期事件的时刻取到期时刻，其余取操作时刻。
type Event struct {
	Seq     uint64
	Reason  Reason
	Time    uint64
	Packets uint64
	Bytes   uint64
}

func (e Event) String() string {
	return fmt.Sprintf("Event{seq=%d %s t=%d pkts=%d bytes=%d}",
		e.Seq, e.Reason, e.Time, e.Packets, e.Bytes)
}

// entry 是一条流表项。
type entry struct {
	m          match.Match
	prio       uint16
	action     uint32
	importance uint16
	idle       uint32
	hard       uint32
	installed  uint64
	lastHit    uint64
	seq        uint64
	packets    uint64
	bytes      uint64
}

// expiry 返回表项的到期时刻与原因；hard 与 idle 取较早者，同刻记 Hard。
// 两个超时都未启用时 ok 为假。
func (e *entry) expiry() (at uint64, reason Reason, ok bool) {
	if e.hard > 0 {
		at, reason, ok = e.installed+uint64(e.hard), Hard, true
	}
	if e.idle > 0 {
		if ia := e.lastHit + uint64(e.idle); !ok || ia < at {
			at, reason, ok = ia, Idle, true
		}
	}
	return at, reason, ok
}

// Table 是流表。零值不可用，须用 New 构造。
type Table struct {
	mu       sync.Mutex
	capacity int
	evict    bool
	clock    uint64 // 已接受操作的最大 now
	nextSeq  uint64 // 下一个安装序号，从 1 起
	entries  map[uint64]*entry
	levels   map[uint16]map[uint64]*entry // prio -> seq -> entry
	prios    []uint16                     // 降序排列的现存优先级
	examined uint64                       // Lookup 累计考察的表项数
}

// New 创建容量为 capacity 的流表；capacity 须在 [1, 1e5]。
// evict 为真时表满可按规则驱逐旧表项。
func New(capacity int, evict bool) (*Table, error) {
	if capacity < 1 || capacity > maxCapacity {
		return nil, fmt.Errorf("%w: capacity %d out of [1,%d]", ErrInvalidParam, capacity, maxCapacity)
	}
	return &Table{
		capacity: capacity,
		evict:    evict,
		nextSeq:  1,
		entries:  make(map[uint64]*entry),
		levels:   make(map[uint16]map[uint64]*entry),
	}, nil
}

// Len 返回现存表项数。
func (t *Table) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.entries)
}

// Now 返回已接受操作的最大时刻。
func (t *Table) Now() uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.clock
}

// installLocked 把表项放入索引，调用前须已分配序号。
func (t *Table) installLocked(e *entry) {
	t.entries[e.seq] = e
	lv := t.levels[e.prio]
	if lv == nil {
		lv = make(map[uint64]*entry)
		t.levels[e.prio] = lv
		i := sort.Search(len(t.prios), func(i int) bool { return t.prios[i] <= e.prio })
		t.prios = append(t.prios, 0)
		copy(t.prios[i+1:], t.prios[i:])
		t.prios[i] = e.prio
	}
	lv[e.seq] = e
}

// removeLocked 把表项从索引摘除，不产生事件。
func (t *Table) removeLocked(e *entry) {
	delete(t.entries, e.seq)
	lv := t.levels[e.prio]
	delete(lv, e.seq)
	if len(lv) == 0 {
		delete(t.levels, e.prio)
		i := sort.Search(len(t.prios), func(i int) bool { return t.prios[i] <= e.prio })
		t.prios = append(t.prios[:i], t.prios[i+1:]...)
	}
}

// expiredLocked 收集到期时刻不大于 now 的表项，
// 按（到期时刻，安装序号）升序返回，附各自到期原因。不修改表。
func (t *Table) expiredLocked(now uint64) ([]*entry, []Reason) {
	type expiring struct {
		e      *entry
		at     uint64
		reason Reason
	}
	var list []expiring
	for _, e := range t.entries {
		if at, r, ok := e.expiry(); ok && at <= now {
			list = append(list, expiring{e, at, r})
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].at != list[j].at {
			return list[i].at < list[j].at
		}
		return list[i].e.seq < list[j].e.seq
	})
	es := make([]*entry, len(list))
	rs := make([]Reason, len(list))
	for i, x := range list {
		es[i], rs[i] = x.e, x.reason
	}
	return es, rs
}

// applyExpiredLocked 落地到期：移除表项并生成事件（时刻取到期时刻）。
func (t *Table) applyExpiredLocked(es []*entry, rs []Reason) []Event {
	var evs []Event
	for i, e := range es {
		at, _, _ := e.expiry()
		t.removeLocked(e)
		evs = append(evs, Event{Seq: e.seq, Reason: rs[i], Time: at, Packets: e.packets, Bytes: e.bytes})
	}
	return evs
}

// checkArgsLocked 校验公共参数（匹配合法性、超时与时刻范围、时钟回退）。
func (t *Table) checkArgsLocked(m match.Match, idle, hard uint32, now uint64) error {
	if !m.Valid() {
		return fmt.Errorf("%w: match value outside mask", ErrInvalidParam)
	}
	if idle > maxTimeout || hard > maxTimeout {
		return fmt.Errorf("%w: timeout out of [0,%d]", ErrInvalidParam, maxTimeout)
	}
	if now > maxClock {
		return fmt.Errorf("%w: now %d out of [0,%d]", ErrInvalidParam, now, maxClock)
	}
	if now < t.clock {
		return fmt.Errorf("%w: now %d < clock %d", ErrClock, now, t.clock)
	}
	return nil
}

// Add 安装一条表项，返回安装序号与本次操作产生的事件。
//
// 判定次序：checkOverlap 且同 prio 存在重叠（含相同匹配）报 ErrOverlap；
// 同 prio 且匹配相同则原地替换（换新序号、计数清零、不占名额）；
// 表未满则安装；表满且不允许驱逐报 ErrTableFull；表满且允许驱逐时，
// 取（importance，安装序号）最小者为牺牲者，其 importance 严格小于
// 新表项才驱逐并安装，否则报 ErrTableFull。
func (t *Table) Add(m match.Match, prio uint32, checkOverlap bool, action, importance uint32, idle, hard uint32, now uint64) (uint64, []Event, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if prio > 65535 {
		return 0, nil, fmt.Errorf("%w: prio %d out of [0,65535]", ErrInvalidParam, prio)
	}
	if importance > 65535 {
		return 0, nil, fmt.Errorf("%w: importance %d out of [0,65535]", ErrInvalidParam, importance)
	}
	if err := t.checkArgsLocked(m, idle, hard, now); err != nil {
		return 0, nil, err
	}
	p16 := uint16(prio)
	exp, reasons := t.expiredLocked(now)
	expired := make(map[uint64]bool, len(exp))
	for _, e := range exp {
		expired[e.seq] = true
	}
	live := func(e *entry) bool { return !expired[e.seq] }

	if checkOverlap {
		for _, e := range t.levels[p16] {
			if live(e) && e.m.Overlaps(m) {
				return 0, nil, fmt.Errorf("%w: seq %d", ErrOverlap, e.seq)
			}
		}
	}

	// 同 prio 且匹配相同：原地替换，不占新名额。
	for _, e := range t.levels[p16] {
		if live(e) && e.m.Equal(m) {
			evs := t.applyExpiredLocked(exp, reasons)
			t.removeLocked(e)
			ne := t.newEntryLocked(m, p16, action, uint16(importance), idle, hard, now)
			t.installLocked(ne)
			t.clock = now
			return ne.seq, evs, nil
		}
	}

	if len(t.entries)-len(exp) >= t.capacity {
		if !t.evict {
			return 0, nil, ErrTableFull
		}
		var victim *entry
		for _, e := range t.entries {
			if !live(e) {
				continue
			}
			if victim == nil || e.importance < victim.importance ||
				(e.importance == victim.importance && e.seq < victim.seq) {
				victim = e
			}
		}
		if victim == nil || victim.importance >= uint16(importance) {
			return 0, nil, ErrTableFull
		}
		evs := t.applyExpiredLocked(exp, reasons)
		t.removeLocked(victim)
		evs = append(evs, Event{Seq: victim.seq, Reason: Evict, Time: now, Packets: victim.packets, Bytes: victim.bytes})
		ne := t.newEntryLocked(m, p16, action, uint16(importance), idle, hard, now)
		t.installLocked(ne)
		t.clock = now
		return ne.seq, evs, nil
	}

	evs := t.applyExpiredLocked(exp, reasons)
	ne := t.newEntryLocked(m, p16, action, uint16(importance), idle, hard, now)
	t.installLocked(ne)
	t.clock = now
	return ne.seq, evs, nil
}

// newEntryLocked 分配新序号并构造表项。
func (t *Table) newEntryLocked(m match.Match, prio uint16, action uint32, importance uint16, idle, hard uint32, now uint64) *entry {
	e := &entry{
		m: m, prio: prio, action: action, importance: importance,
		idle: idle, hard: hard,
		installed: now, lastHit: now,
		seq: t.nextSeq,
	}
	t.nextSeq++
	return e
}

// Lookup 查找命中表项：取 prio 最大者，同 prio 取安装序号最小者。
// 命中则包计数加 1、字节计数加 bytes、lastHit 置为 now。
// 未命中不是错误，hit 为假。
func (t *Table) Lookup(pkt match.Packet, bytes, now uint64) (action uint32, seq uint64, hit bool, evs []Event, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if now > maxClock {
		return 0, 0, false, nil, fmt.Errorf("%w: now %d out of [0,%d]", ErrInvalidParam, now, maxClock)
	}
	if now < t.clock {
		return 0, 0, false, nil, fmt.Errorf("%w: now %d < clock %d", ErrClock, now, t.clock)
	}
	exp, reasons := t.expiredLocked(now)
	evs = t.applyExpiredLocked(exp, reasons)
	for _, p := range t.prios {
		var best *entry
		for _, e := range t.levels[p] {
			t.examined++
			if e.m.Hit(pkt) && (best == nil || e.seq < best.seq) {
				best = e
			}
		}
		if best != nil {
			best.packets++
			best.bytes += bytes
			best.lastHit = now
			t.clock = now
			return best.action, best.seq, true, evs, nil
		}
	}
	t.clock = now
	return 0, 0, false, evs, nil
}

// targetsLocked 选出 Modify/Delete 的作用目标：
// strict 为真是同 prio 且匹配相同的那一项；为假是所有被 m 包含的表项。
// 调用方须已落地到期，故无需再排除到期表项。
func (t *Table) targetsLocked(m match.Match, prio uint16, strict bool) []*entry {
	var out []*entry
	if strict {
		for _, e := range t.levels[prio] {
			if e.m.Equal(m) {
				out = append(out, e)
				break
			}
		}
		return out
	}
	for _, e := range t.entries {
		if m.Contains(e.m) {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].seq < out[j].seq })
	return out
}

// Modify 修改目标表项的动作；计数、时刻与序号不变。返回作用项数。
// 作用 0 项不是错误。
func (t *Table) Modify(m match.Match, prio uint32, strict bool, action uint32, now uint64) (int, []Event, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if prio > 65535 {
		return 0, nil, fmt.Errorf("%w: prio %d out of [0,65535]", ErrInvalidParam, prio)
	}
	if err := t.checkArgsLocked(m, 0, 0, now); err != nil {
		return 0, nil, err
	}
	exp, reasons := t.expiredLocked(now)
	evs := t.applyExpiredLocked(exp, reasons)
	targets := t.targetsLocked(m, uint16(prio), strict)
	for _, e := range targets {
		e.action = action
	}
	t.clock = now
	return len(targets), evs, nil
}

// Delete 按安装序号升序移除目标表项，返回作用项数与事件。
// 作用 0 项不是错误。
func (t *Table) Delete(m match.Match, prio uint32, strict bool, now uint64) (int, []Event, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if prio > 65535 {
		return 0, nil, fmt.Errorf("%w: prio %d out of [0,65535]", ErrInvalidParam, prio)
	}
	if err := t.checkArgsLocked(m, 0, 0, now); err != nil {
		return 0, nil, err
	}
	exp, reasons := t.expiredLocked(now)
	evs := t.applyExpiredLocked(exp, reasons)
	targets := t.targetsLocked(m, uint16(prio), strict)
	for _, e := range targets {
		t.removeLocked(e)
		evs = append(evs, Event{Seq: e.seq, Reason: Delete, Time: now, Packets: e.packets, Bytes: e.bytes})
	}
	t.clock = now
	return len(targets), evs, nil
}

// Advance 只落地到期并推进时钟，返回到期事件。
func (t *Table) Advance(now uint64) ([]Event, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if now > maxClock {
		return nil, fmt.Errorf("%w: now %d out of [0,%d]", ErrInvalidParam, now, maxClock)
	}
	if now < t.clock {
		return nil, fmt.Errorf("%w: now %d < clock %d", ErrClock, now, t.clock)
	}
	exp, reasons := t.expiredLocked(now)
	evs := t.applyExpiredLocked(exp, reasons)
	t.clock = now
	return evs, nil
}

// Transact 在表锁内克隆工作副本并交给 fn 执行：fn 返回错误时
// 流表、序号、时钟与事件均不变；fn 成功时工作副本整体替换原表。
// now 先于 fn 校验（参数非法、时钟回退）。
func (t *Table) Transact(now uint64, fn func(w *Table) ([]Event, error)) ([]Event, error) {
	if now > maxClock {
		return nil, fmt.Errorf("%w: now %d out of [0,%d]", ErrInvalidParam, now, maxClock)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if now < t.clock {
		return nil, fmt.Errorf("%w: now %d < clock %d", ErrClock, now, t.clock)
	}
	w := t.cloneLocked()
	evs, err := fn(w)
	if err != nil {
		return nil, err
	}
	t.absorbLocked(w)
	return evs, nil
}

// cloneLocked 深拷贝整张表。
func (t *Table) cloneLocked() *Table {
	w := &Table{
		capacity: t.capacity,
		evict:    t.evict,
		clock:    t.clock,
		nextSeq:  t.nextSeq,
		examined: t.examined,
		entries:  make(map[uint64]*entry, len(t.entries)),
		levels:   make(map[uint16]map[uint64]*entry, len(t.levels)),
		prios:    append([]uint16(nil), t.prios...),
	}
	for seq, e := range t.entries {
		ne := *e
		w.entries[seq] = &ne
		lv := w.levels[ne.prio]
		if lv == nil {
			lv = make(map[uint64]*entry)
			w.levels[ne.prio] = lv
		}
		lv[seq] = &ne
	}
	return w
}

// absorbLocked 用工作副本的状态整体替换本表。
func (t *Table) absorbLocked(w *Table) {
	t.clock = w.clock
	t.nextSeq = w.nextSeq
	t.entries = w.entries
	t.levels = w.levels
	t.prios = w.prios
	t.examined = w.examined
}
