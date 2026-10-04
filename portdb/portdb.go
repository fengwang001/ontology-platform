// Package portdb 维护携转单、携转生效与销号冷冻记录。
package portdb

import (
	"sync"

	"ontology/numplan"
)

const jumpLevels = 32

// evtKind 区分历史事件类型。
type evtKind int

const (
	evtPort evtKind = iota // 携转单（撤销只置 canceled 并从有效链摘除）
	evtDisc                // 销号
)

// seg 是一段有效链区间内“最后一条某类事件”的内联载荷，
// 命中后无需再跳去考察目标记录。
type seg struct {
	portAt        int64 // -1 表示段内无携转
	portRecipient int
	discAt        int64 // -1 表示段内无销号
}

// event 是某号码历史上的一条事实；撤销不删除，只置 canceled 并从有效链摘除。
type event struct {
	at        int64
	seq       int64 // 全局插入序号，同 at 时定序
	kind      evtKind
	recipient int
	orderID   int64
	canceled  bool

	prev int             // 有效链上一有效条目（(at,seq) 严格小序）
	jump [jumpLevels]int // jump[l]=沿 prev 走 2^l 步的条目，-1 无
	agg  [jumpLevels]seg // 对应 2^l 个条目的区间聚合计数
}

// ownSeg 返回事件自身作为长度 1 区间的聚合。
func (e *event) ownSeg() seg {
	s := seg{portAt: -1, discAt: -1}
	if e.kind == evtPort {
		s.portAt = e.at
		s.portRecipient = e.recipient
	} else {
		s.discAt = e.at
	}
	return s
}

type numberRec struct {
	events []event // 追加式；有效链按 (at,seq) 有序
	head   int     // 有效链头，-1 无
}

// DB 是携转数据库。
type DB struct {
	mu     sync.RWMutex
	plan   *numplan.Plan
	lmin   int64
	q      int64
	nextID int64
	seq    int64
	recs   map[string]*numberRec
}

// New 创建携转库，Lmin 为最短提前量、Q 为销号冷冻期（单位秒）。
func New(plan *numplan.Plan, lmin, q int64) *DB {
	return &DB{plan: plan, lmin: lmin, q: q, nextID: 1, recs: make(map[string]*numberRec)}
}

// Sync 暴露内部锁；调用方按 route→portdb→numplan 的顺序持锁。
func (d *DB) Sync() *sync.RWMutex { return &d.mu }

// Plan 返回底层号段库。
func (d *DB) Plan() *numplan.Plan { return d.plan }

func (d *DB) RequestPort(number string, donor, recipient int, at, now int64) (int64, error) {
	if !numplan.ValidNumber(number) || !numplan.ValidOp(donor) || !numplan.ValidOp(recipient) ||
		!numplan.ValidTime(at) || !numplan.ValidTime(now) {
		return 0, numplan.ErrInvalid
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	pk := d.plan.Sync()
	pk.Lock()
	defer pk.Unlock()
	if now < d.plan.MaxNowLocked() {
		return 0, numplan.ErrClockBack
	}
	st, err := d.stateAtLocked(number, now)
	if err != nil {
		return 0, err
	}
	if st.Frozen {
		return 0, numplan.ErrFrozen
	}
	rec := d.recs[number]
	if rec != nil {
		for i := range rec.events {
			e := &rec.events[i]
			if e.kind == evtPort && !e.canceled && e.at > now {
				return 0, numplan.ErrPending
			}
		}
	}
	if donor != st.Serving {
		return 0, numplan.ErrDonor
	}
	if recipient == donor {
		return 0, numplan.ErrSameOp
	}
	if at-now < d.lmin {
		return 0, numplan.ErrLeadTime
	}
	if err := d.plan.CheckTimeLocked(now); err != nil {
		return 0, err
	}
	id := d.nextID
	d.nextID++
	d.append(number, event{at: at, seq: d.tick(), kind: evtPort, recipient: recipient, orderID: id})
	return id, nil
}

func (d *DB) Cancel(order int64, now int64) error {
	if order < 1 || !numplan.ValidTime(now) {
		return numplan.ErrInvalid
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	pk := d.plan.Sync()
	pk.Lock()
	defer pk.Unlock()
	if now < d.plan.MaxNowLocked() {
		return numplan.ErrClockBack
	}
	for _, rec := range d.recs {
		for i := range rec.events {
			e := &rec.events[i]
			if e.kind != evtPort || e.orderID != order {
				continue
			}
			if e.canceled {
				return numplan.ErrNoOrder
			}
			if now >= e.at {
				return numplan.ErrEffective
			}
			if err := d.plan.CheckTimeLocked(now); err != nil {
				return err
			}
			d.unlink(rec, i)
			e.canceled = true
			return nil
		}
	}
	return numplan.ErrNoOrder
}

func (d *DB) Disconnect(number string, now int64) error {
	if !numplan.ValidNumber(number) || !numplan.ValidTime(now) {
		return numplan.ErrInvalid
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	pk := d.plan.Sync()
	pk.Lock()
	defer pk.Unlock()
	if now < d.plan.MaxNowLocked() {
		return numplan.ErrClockBack
	}
	st, err := d.stateAtLocked(number, now)
	if err != nil {
		return err
	}
	if st.Frozen {
		return numplan.ErrFrozen
	}
	if err := d.plan.CheckTimeLocked(now); err != nil {
		return err
	}
	rec := d.recs[number]
	if rec != nil {
		for i := range rec.events {
			e := &rec.events[i]
			if e.kind == evtPort && !e.canceled && e.at > now {
				d.unlink(rec, i)
				e.canceled = true
			}
		}
	}
	d.append(number, event{at: now, seq: d.tick(), kind: evtDisc})
	return nil
}

// State 是某号码在某时刻的服务状态。
type State struct {
	Home    int
	Serving int
	Ported  bool
	Frozen  bool
	Probe   numplan.Probe
}

// StateAtLocked 求值 number 在 t 时刻的状态；调用方须持有 d.mu 与 plan 的锁。
func (d *DB) StateAtLocked(number string, t int64) (State, error) {
	return d.stateAtLocked(number, t)
}

func (d *DB) tick() int64 {
	d.seq++
	return d.seq
}

func (d *DB) recFor(number string) *numberRec {
	r := d.recs[number]
	if r == nil {
		r = &numberRec{head: -1}
		d.recs[number] = r
	}
	return r
}

func lessKey(at1, seq1, at2, seq2 int64) bool {
	return at1 < at2 || at1 == at2 && seq1 < seq2
}

// fillJump 依据 prev 填充某事件的倍增跳转与区间聚合。
func fillJump(rec *numberRec, e *event) {
	for l := range e.jump {
		e.jump[l] = -1
		e.agg[l] = seg{portAt: -1, discAt: -1}
	}
	if e.prev == -1 {
		return
	}
	e.jump[0] = e.prev
	e.agg[0] = rec.events[e.prev].ownSeg()
	for l := 1; l < jumpLevels; l++ {
		mid := e.jump[l-1]
		far := rec.events[mid].jump[l-1]
		if mid == -1 || far == -1 {
			break
		}
		e.jump[l] = far
		e.agg[l] = mergeSeg(rec.events[mid].agg[l-1], e.agg[l-1])
	}
}

// mergeSeg 合并相邻两段：a 为更早段、b 为更晚段，取各类型的更晚者。
func mergeSeg(a, b seg) seg {
	out := b
	if b.portAt < a.portAt {
		out.portAt = a.portAt
		out.portRecipient = a.portRecipient
	}
	if b.discAt < a.discAt {
		out.discAt = a.discAt
	}
	return out
}

// append 追加一条有效事件。
func (d *DB) append(number string, ev event) {
	rec := d.recFor(number)
	node := rec.head
	ev.prev = -1
	for node != -1 && !lessKey(rec.events[node].at, rec.events[node].seq, ev.at, ev.seq) {
		node = rec.events[node].prev
	}
	ev.prev = node
	fillJump(rec, &ev)
	rec.events = append(rec.events, ev)
	idx := len(rec.events) - 1
	if rec.head == -1 || lessKey(rec.events[rec.head].at, rec.events[rec.head].seq, ev.at, ev.seq) {
		rec.head = idx
	}
}

// unlink 从有效链摘除 idx，并修复以其为前驱的后继跳转。
func (d *DB) unlink(rec *numberRec, idx int) {
	old := rec.events[idx]
	if rec.head == idx {
		rec.head = old.prev
	}
	for i := range rec.events {
		e := &rec.events[i]
		if e.prev == idx {
			e.prev = old.prev
			fillJump(rec, e)
		}
	}
}

// stateAtLocked 求值某号码在 t 的状态；调用方持有 d.mu 与 plan 锁。
func (d *DB) stateAtLocked(number string, t int64) (State, error) {
	home, ok, pr := d.plan.HomeAtLocked(number, t)
	if !ok {
		return State{}, numplan.ErrNotAssigned
	}
	st := State{Home: home, Serving: home, Probe: pr}
	rec := d.recs[number]
	if rec == nil || rec.head == -1 {
		return st, nil
	}
	floor, s, probes := chainFloor(rec, t)
	pr.History = probes
	if floor == -1 {
		st.Probe = pr
		return st, nil
	}
	if s.discAt != -1 && t >= s.discAt && t < s.discAt+d.q {
		st.Frozen = true
		st.Probe = pr
		return st, nil
	}
	if s.portAt != -1 && (s.discAt == -1 || s.discAt < s.portAt) {
		st.Serving = s.portRecipient
	}
	st.Ported = st.Serving != st.Home
	st.Probe = pr
	return st, nil
}

// chainFloor 以倍增下降求 at<=t 的最高有效条目，并给出截至该条目的区间聚合。
// probes 为实际考察（读取 at 字段）的历史记录数，满足 ≤ ⌊log2 k⌋+2。
// 聚合载荷随被考察条目一起读出，不额外考察其指向的记录。
func chainFloor(rec *numberRec, t int64) (floor int, s seg, probes int) {
	s = seg{portAt: -1, discAt: -1}
	cur := rec.head
	probes++ // 考察头
	if rec.events[cur].at <= t {
		return cur, prefixSeg(rec, cur), probes
	}
	// 标准倍增：每层最多考察一个候选；接受跨度则移动，拒绝则试更短跨度。
	for l := jumpLevels - 1; l >= 0; l-- {
		cand := rec.events[cur].jump[l]
		if cand == -1 {
			continue
		}
		probes++ // 考察跳转目标
		if rec.events[cand].at > t {
			cur = cand
		}
	}
	floor = rec.events[cur].prev
	if floor == -1 {
		return -1, s, probes
	}
	probes++ // 考察 prev 落地条目
	return floor, prefixSeg(rec, floor), probes
}

// prefixSeg 返回 floor 自身及其全部前驱的聚合；只读本条目上的预计算载荷。
func prefixSeg(rec *numberRec, floor int) seg {
	out := rec.events[floor].ownSeg()
	for l := jumpLevels - 1; l >= 0; l-- {
		if rec.events[floor].jump[l] != -1 {
			out = mergeSeg(rec.events[floor].agg[l], out)
		}
	}
	return out
}
