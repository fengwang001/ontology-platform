package flowtable

import (
	"errors"
	"sort"
	"sync"

	"ontology/match"
)

var (
	ErrInvalid  = errors.New("flowtable: invalid argument")
	ErrClockBk  = errors.New("flowtable: clock moved backwards")
	ErrOverlap  = errors.New("flowtable: overlapping entry")
	ErrFull     = errors.New("flowtable: table full")
	ErrNoBundle = errors.New("flowtable: bundle not found")
)

type Reason string

const (
	Idle   Reason = "Idle"
	Hard   Reason = "Hard"
	Delete Reason = "Delete"
	Evict  Reason = "Evict"
)

type Event struct {
	ID      uint64
	Reason  Reason
	At      int64
	Packets uint64
	Bytes   uint64
}

type AddMsg struct {
	Match        match.Match
	Prio         uint16
	CheckOverlap bool
	Action       string
	Importance   uint16
	Idle         int64
	Hard         int64
}

type ModifyMsg struct {
	Match  match.Match
	Prio   uint16
	Strict bool
	Action string
}

type DeleteMsg struct {
	Match  match.Match
	Prio   uint16
	Strict bool
}

type AddResult struct {
	ID uint64
}

// CommitError 表示整批提交中第 Index 条消息被拒。
type CommitError struct {
	Index int
	Err   error
}

func (e *CommitError) Error() string { return "flowtable: bundle commit rejected at index" }
func (e *CommitError) Unwrap() error { return e.Err }

// entry 是单条流表项；expire 缓存的到期时刻随安装/命中重算。
type entry struct {
	id         uint64
	mt         match.Match
	prio       uint16
	action     string
	importance uint16
	idle       int64
	hard       int64
	installed  int64
	lastHit    int64
	packets    uint64
	bytes      uint64
}

func (e *entry) deadline() (int64, Reason) {
	h := int64(-1)
	if e.hard > 0 {
		h = e.installed + e.hard
	}
	i := int64(-1)
	if e.idle > 0 {
		i = e.lastHit + e.idle
	}
	switch {
	case h < 0:
		return i, Idle
	case i < 0:
		return h, Hard
	case i < h:
		return i, Idle
	default:
		// 同刻记 Hard。
		return h, Hard
	}
}

// table 是全部可变状态；bundle 在其深拷贝上演练，成功后整体替换。
type table struct {
	capacity int
	evict    bool
	entries  map[uint64]*entry
	byPrio   map[uint16]map[uint64]*entry
	prios    []uint16 // 升序
	nextID   uint64
	maxNow   int64
}

func newTable(capacity int, evict bool) *table {
	return &table{
		capacity: capacity,
		evict:    evict,
		entries:  map[uint64]*entry{},
		byPrio:   map[uint16]map[uint64]*entry{},
		maxNow:   -1,
	}
}

func (tb *table) clone() *table {
	c := &table{
		capacity: tb.capacity,
		evict:    tb.evict,
		entries:  make(map[uint64]*entry, len(tb.entries)),
		byPrio:   make(map[uint16]map[uint64]*entry, len(tb.byPrio)),
		prios:    append([]uint16(nil), tb.prios...),
		nextID:   tb.nextID,
		maxNow:   tb.maxNow,
	}
	for p, set := range tb.byPrio {
		cs := make(map[uint64]*entry, len(set))
		for id, e := range set {
			ec := *e
			cs[id] = &ec
			c.entries[id] = &ec
		}
		c.byPrio[p] = cs
	}
	return c
}

func (tb *table) addPrio(p uint16) {
	i := sort.Search(len(tb.prios), func(i int) bool { return tb.prios[i] >= p })
	if i < len(tb.prios) && tb.prios[i] == p {
		return
	}
	tb.prios = append(tb.prios, 0)
	copy(tb.prios[i+1:], tb.prios[i:])
	tb.prios[i] = p
	tb.byPrio[p] = map[uint64]*entry{}
}

func (tb *table) put(e *entry) {
	tb.entries[e.id] = e
	set, ok := tb.byPrio[e.prio]
	if !ok {
		tb.addPrio(e.prio)
		set = tb.byPrio[e.prio]
	}
	set[e.id] = e
}

func (tb *table) remove(id uint64) {
	if e, ok := tb.entries[id]; ok {
		delete(tb.entries, id)
		delete(tb.byPrio[e.prio], id)
	}
}

// expire 移除到期时刻 <= now 的表项，按（到期时刻，序号）生成事件。
func (tb *table) expire(now int64) []Event {
	type dead struct {
		at int64
		e  *entry
	}
	var ds []dead
	for _, e := range tb.entries {
		at, _ := e.deadline()
		if at >= 0 && at <= now {
			ds = append(ds, dead{at, e})
		}
	}
	sort.Slice(ds, func(i, j int) bool {
		if ds[i].at != ds[j].at {
			return ds[i].at < ds[j].at
		}
		return ds[i].e.id < ds[j].e.id
	})
	ev := make([]Event, 0, len(ds))
	for _, d := range ds {
		_, reason := d.e.deadline()
		ev = append(ev, Event{ID: d.e.id, Reason: reason, At: d.at, Packets: d.e.packets, Bytes: d.e.bytes})
		tb.remove(d.e.id)
	}
	return ev
}

func validTimeouts(idle, hard int64) bool {
	return 0 <= idle && idle <= 1e9 && 0 <= hard && hard <= 1e9
}

// OpKind 标识批内操作类型。
type OpKind int

const (
	OpAdd OpKind = iota
	OpModify
	OpDelete
)

// Op 是批内一条操作。
type Op struct {
	Kind   OpKind
	Add    AddMsg
	Modify ModifyMsg
	Delete DeleteMsg
}

// applyAt 在工作副本上执行一条操作。
func applyAt(tb *table, op Op, now int64) (opEvents []Event, addID uint64, affected int, err error) {
	switch op.Kind {
	case OpAdd:
		r, ev, e := tb.add(op.Add, now)
		return ev, r.ID, 0, e
	case OpModify:
		n, ev, e := tb.modify(op.Modify, now)
		return ev, 0, n, e
	default:
		n, ev, e := tb.del(op.Delete, now)
		return ev, 0, n, e
	}
}

// CommitBundleResult 是 Commit 的执行结果。
type CommitBundleResult struct {
	Events []Event
}

func (tb *table) add(m AddMsg, now int64) (AddResult, []Event, error) {
	if !validTimeouts(m.Idle, m.Hard) {
		return AddResult{}, nil, ErrInvalid
	}
	events := tb.expire(now)
	tb.maxNow = now

	set := tb.byPrio[m.Prio]
	var overlapID uint64
	if m.CheckOverlap {
		var found bool
		for _, e := range set {
			if e.mt.Overlap(m.Match) {
				if !found || e.id < overlapID {
					overlapID, found = e.id, true
				}
			}
		}
	}
	if found := overlapID != 0; found {
		return AddResult{}, events, ErrOverlap
	}

	// 同 prio 相同匹配：原地替换，不占新名额。
	for _, e := range set {
		if e.mt.Equal(m.Match) {
			e.action = m.Action
			e.importance = m.Importance
			e.idle = m.Idle
			e.hard = m.Hard
			e.installed = now
			e.lastHit = now
			e.packets = 0
			e.bytes = 0
			tb.nextID++
			newID := tb.nextID
			tb.remove(e.id)
			e.id = newID
			tb.put(e)
			return AddResult{ID: newID}, events, nil
		}
	}

	if len(tb.entries) < tb.capacity {
		return AddResult{ID: tb.install(m, now)}, events, nil
	}
	if !tb.evict {
		return AddResult{}, events, ErrFull
	}
	var victim *entry
	for _, e := range tb.entries {
		if victim == nil || e.importance < victim.importance ||
			(e.importance == victim.importance && e.id < victim.id) {
			victim = e
		}
	}
	if victim.importance >= m.Importance {
		return AddResult{}, events, ErrFull
	}
	vid := victim.id
	vp, vb := victim.packets, victim.bytes
	tb.remove(vid)
	events = append(events, Event{ID: vid, Reason: Evict, At: now, Packets: vp, Bytes: vb})
	return AddResult{ID: tb.install(m, now)}, events, nil
}

func (tb *table) install(m AddMsg, now int64) uint64 {
	tb.nextID++
	e := &entry{
		id:         tb.nextID,
		mt:         m.Match,
		prio:       m.Prio,
		action:     m.Action,
		importance: m.Importance,
		idle:       m.Idle,
		hard:       m.Hard,
		installed:  now,
		lastHit:    now,
	}
	tb.put(e)
	return e.id
}

func (tb *table) lookup(p match.Pkt, bytes uint64, now int64) (string, uint64, bool, []Event, int) {
	events := tb.expire(now)
	tb.maxNow = now
	examined := 0
	for i := len(tb.prios) - 1; i >= 0; i-- {
		set := tb.byPrio[tb.prios[i]]
		ids := make([]uint64, 0, len(set))
		for id := range set {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })
		for _, id := range ids {
			e := set[id]
			examined++
			if e.mt.Hit(p) {
				e.packets++
				e.bytes += bytes
				e.lastHit = now
				return e.action, e.id, false, events, examined
			}
		}
	}
	return "", 0, true, events, examined
}

func (tb *table) modify(m ModifyMsg, now int64) (int, []Event, error) {
	events := tb.expire(now)
	tb.maxNow = now
	var ids []uint64
	if m.Strict {
		for _, e := range tb.byPrio[m.Prio] {
			if e.mt.Equal(m.Match) {
				ids = append(ids, e.id)
			}
		}
	} else {
		for _, e := range tb.entries {
			if m.Match.Contains(e.mt) {
				ids = append(ids, e.id)
			}
		}
	}
	sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })
	for _, id := range ids {
		tb.entries[id].action = m.Action
	}
	return len(ids), events, nil
}

func (tb *table) del(d DeleteMsg, now int64) (int, []Event, error) {
	events := tb.expire(now)
	tb.maxNow = now
	var ids []uint64
	if d.Strict {
		for _, e := range tb.byPrio[d.Prio] {
			if e.mt.Equal(d.Match) {
				ids = append(ids, e.id)
			}
		}
	} else {
		for _, e := range tb.entries {
			if d.Match.Contains(e.mt) {
				ids = append(ids, e.id)
			}
		}
	}
	sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })
	for _, id := range ids {
		e := tb.entries[id]
		events = append(events, Event{ID: id, Reason: Delete, At: now, Packets: e.packets, Bytes: e.bytes})
		tb.remove(id)
	}
	return len(ids), events, nil
}

// FlowTable 是并发安全的流表。
type FlowTable struct {
	mu sync.Mutex
	tb *table

	// examined 是最近一次 Lookup 实际考察的表项数（非导出计数器）。
	examined int
}

const (
	maxCapacity = 100000
	maxNow      = int64(1e12)
	maxTimeout  = int64(1e9)
)

func validNow(now int64) bool { return 0 <= now && now <= maxNow }

// New 创建容量为 capacity 的流表；capacity 越界返回 ErrInvalid。
func New(capacity int, evict bool) (*FlowTable, error) {
	if capacity < 1 || capacity > maxCapacity {
		return nil, ErrInvalid
	}
	return &FlowTable{tb: newTable(capacity, evict)}, nil
}

func (m AddMsg) valid() bool {
	return m.Match.Valid() && validTimeouts(m.Idle, m.Hard)
}

// Add 安装或替换一条表项。
func (t *FlowTable) Add(m AddMsg, now int64) (AddResult, []Event, error) {
	if !m.valid() {
		return AddResult{}, nil, ErrInvalid
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !validNow(now) || now < t.tb.maxNow {
		return AddResult{}, nil, ErrClockBk
	}
	return t.tb.add(m, now)
}

// Lookup 执行一次查表；无命中时 miss 为 true（不是错误）。
func (t *FlowTable) Lookup(p match.Pkt, bytes uint64, now int64) (action string, id uint64, miss bool, events []Event, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !validNow(now) || now < t.tb.maxNow {
		return "", 0, false, nil, ErrClockBk
	}
	action, id, miss, events, t.examined = t.tb.lookup(p, bytes, now)
	return
}

// Modify 按 strict/非严格语义更换动作，返回作用项数。
func (t *FlowTable) Modify(m ModifyMsg, now int64) (int, []Event, error) {
	if !m.Match.Valid() {
		return 0, nil, ErrInvalid
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !validNow(now) || now < t.tb.maxNow {
		return 0, nil, ErrClockBk
	}
	return t.tb.modify(m, now)
}

// Delete 按 strict/非严格语义移除表项，返回作用项数与移除事件。
func (t *FlowTable) Delete(d DeleteMsg, now int64) (int, []Event, error) {
	if !d.Match.Valid() {
		return 0, nil, ErrInvalid
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !validNow(now) || now < t.tb.maxNow {
		return 0, nil, ErrClockBk
	}
	return t.tb.del(d, now)
}

// CommitBundle 在工作副本上按序执行 ops；任一被拒则整批无效果，
// 返回 *CommitError（下标可解析，errors.Is 可取出原因）。
func (t *FlowTable) CommitBundle(ops []Op, now int64) ([]Event, error) {
	if len(ops) == 0 {
		return nil, ErrInvalid
	}
	for _, op := range ops {
		switch op.Kind {
		case OpAdd:
			if !op.Add.valid() {
				return nil, ErrInvalid
			}
		case OpModify:
			if !op.Modify.Match.Valid() {
				return nil, ErrInvalid
			}
		case OpDelete:
			if !op.Delete.Match.Valid() {
				return nil, ErrInvalid
			}
		default:
			return nil, ErrInvalid
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !validNow(now) || now < t.tb.maxNow {
		return nil, ErrClockBk
	}
	work := t.tb.clone()
	expired := work.expire(now)
	work.maxNow = now
	var events []Event
	events = append(events, expired...)
	for i, op := range ops {
		opEvents, _, _, err := applyAt(work, op, now)
		events = append(events, opEvents...)
		if err != nil {
			return nil, &CommitError{Index: i, Err: err}
		}
	}
	t.tb = work
	return events, nil
}

// Len 返回现存表项数。
func (t *FlowTable) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.tb.entries)
}

// Examined 返回最近一次 Lookup 考察的表项数。
func (t *FlowTable) Examined() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.examined
}
