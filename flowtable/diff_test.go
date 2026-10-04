package flowtable

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"ontology/match"
)

// naiveEntry 是朴素逐项线性扫描模拟中的表项。
type naiveEntry struct {
	id                 uint64
	mt                 match.Match
	prio               uint16
	action             string
	importance         uint16
	idle, hard         int64
	installed, lastHit int64
	packets, bytes     uint64
}

func (e *naiveEntry) deadline() (int64, Reason) {
	h, i := int64(-1), int64(-1)
	if e.hard > 0 {
		h = e.installed + e.hard
	}
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
		return h, Hard
	}
}

// naive 模型按规格逐项线性扫描复算一切判定。
type naive struct {
	cap         int
	evict       bool
	nextID      uint64
	maxNow      int64
	es          []*naiveEntry
	newInstalls int
	removals    int
}

type naiveOut struct {
	id       uint64
	affected int
	miss     bool
	action   string
	events   []Event
	err      error
}

func (n *naive) expire(now int64) []Event {
	type dead struct {
		at int64
		e  *naiveEntry
	}
	var ds []dead
	for _, e := range n.es {
		at, _ := e.deadline()
		if at >= 0 && at <= now {
			ds = append(ds, dead{at, e})
		}
	}
	sort.Slice(ds, func(a, b int) bool {
		if ds[a].at != ds[b].at {
			return ds[a].at < ds[b].at
		}
		return ds[a].e.id < ds[b].e.id
	})
	var ev []Event
	for _, d := range ds {
		_, r := d.e.deadline()
		ev = append(ev, Event{ID: d.e.id, Reason: r, At: d.at, Packets: d.e.packets, Bytes: d.e.bytes})
		n.removals++
		for i, e := range n.es {
			if e == d.e {
				n.es = append(n.es[:i], n.es[i+1:]...)
				break
			}
		}
	}
	return ev
}

func (n *naive) add(m AddMsg, now int64) naiveOut {
	if !m.valid() {
		return naiveOut{err: ErrInvalid}
	}
	if !validNow(now) || now < n.maxNow {
		return naiveOut{err: ErrClockBk}
	}
	ev := n.expire(now)
	n.maxNow = now
	if m.CheckOverlap {
		var minID uint64
		for _, e := range n.es {
			if e.prio == m.Prio && e.mt.Overlap(m.Match) {
				if minID == 0 || e.id < minID {
					minID = e.id
				}
			}
		}
		if minID != 0 {
			return naiveOut{events: ev, err: ErrOverlap}
		}
	}
	for _, e := range n.es {
		if e.prio == m.Prio && e.mt.Equal(m.Match) {
			e.action, e.importance, e.idle, e.hard = m.Action, m.Importance, m.Idle, m.Hard
			e.installed, e.lastHit = now, now
			e.packets, e.bytes = 0, 0
			n.nextID++
			e.id = n.nextID
			return naiveOut{id: e.id, events: ev}
		}
	}
	install := func() uint64 {
		n.nextID++
		n.newInstalls++
		n.es = append(n.es, &naiveEntry{
			id: n.nextID, mt: m.Match, prio: m.Prio, action: m.Action,
			importance: m.Importance, idle: m.Idle, hard: m.Hard,
			installed: now, lastHit: now,
		})
		return n.nextID
	}
	if len(n.es) < n.cap {
		return naiveOut{id: install(), events: ev}
	}
	if !n.evict {
		return naiveOut{events: ev, err: ErrFull}
	}
	var vic *naiveEntry
	for _, e := range n.es {
		if vic == nil || e.importance < vic.importance ||
			(e.importance == vic.importance && e.id < vic.id) {
			vic = e
		}
	}
	if vic.importance >= m.Importance {
		return naiveOut{events: ev, err: ErrFull}
	}
	ev = append(ev, Event{ID: vic.id, Reason: Evict, At: now, Packets: vic.packets, Bytes: vic.bytes})
	n.removals++
	for i, e := range n.es {
		if e == vic {
			n.es = append(n.es[:i], n.es[i+1:]...)
			break
		}
	}
	return naiveOut{id: install(), events: ev}
}

func (n *naive) lookup(p match.Pkt, b uint64, now int64) naiveOut {
	if !validNow(now) || now < n.maxNow {
		return naiveOut{err: ErrClockBk}
	}
	ev := n.expire(now)
	n.maxNow = now
	var best *naiveEntry
	for _, e := range n.es {
		if e.mt.Hit(p) && (best == nil || e.prio > best.prio ||
			(e.prio == best.prio && e.id < best.id)) {
			best = e
		}
	}
	if best == nil {
		return naiveOut{miss: true, events: ev}
	}
	best.packets++
	best.bytes += b
	best.lastHit = now
	return naiveOut{id: best.id, action: best.action, events: ev}
}

func (n *naive) modify(m ModifyMsg, now int64) naiveOut {
	if !m.Match.Valid() {
		return naiveOut{err: ErrInvalid}
	}
	if !validNow(now) || now < n.maxNow {
		return naiveOut{err: ErrClockBk}
	}
	ev := n.expire(now)
	n.maxNow = now
	var ids []uint64
	for _, e := range n.es {
		ok := e.prio == m.Prio && e.mt.Equal(m.Match)
		if !m.Strict {
			ok = m.Match.Contains(e.mt)
		}
		if ok {
			ids = append(ids, e.id)
		}
	}
	sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })
	for _, id := range ids {
		for _, e := range n.es {
			if e.id == id {
				e.action = m.Action
			}
		}
	}
	return naiveOut{affected: len(ids), events: ev}
}

func (n *naive) del(d DeleteMsg, now int64) naiveOut {
	if !d.Match.Valid() {
		return naiveOut{err: ErrInvalid}
	}
	if !validNow(now) || now < n.maxNow {
		return naiveOut{err: ErrClockBk}
	}
	ev := n.expire(now)
	n.maxNow = now
	var ids []uint64
	for _, e := range n.es {
		ok := e.prio == d.Prio && e.mt.Equal(d.Match)
		if !d.Strict {
			ok = d.Match.Contains(e.mt)
		}
		if ok {
			ids = append(ids, e.id)
		}
	}
	sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })
	for _, id := range ids {
		for i, e := range n.es {
			if e.id == id {
				ev = append(ev, Event{ID: e.id, Reason: Delete, At: now, Packets: e.packets, Bytes: e.bytes})
				n.removals++
				n.es = append(n.es[:i], n.es[i+1:]...)
				break
			}
		}
	}
	return naiveOut{affected: len(ids), events: ev}
}

func sameEvents(a, b []Event) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// rndMatch 从少量掩码中生成，保证产生重叠/包含关系。
func rndMatch(r *rand.Rand) match.Match {
	masks := []uint32{0, 0xFF000000, 0xFFFF0000, 0xFFFFFF00, 0xFFFFFFFF, 0xF0000000}
	pick := func() match.Field {
		m := masks[r.Intn(len(masks))]
		top := []uint32{0x0A, 0x0B, 0x0A, 0x0A}[r.Intn(4)]
		v := (top << 24) & m
		return match.Field{Value: v, Mask: m}
	}
	return match.Match{F0: pick(), F1: match.Field{}}
}

func joinLog(log []string) string { return strings.Join(log, "\n") }

// TestRandomDifferential 用 1500 组随机序列对照朴素模拟；
// 日志打印每步输入、输出与判定依据（错误原因/事件）。
func TestRandomDifferential(t *testing.T) {
	const groups = 1500
	for g := 0; g < groups; g++ {
		seed := int64(1000 + g)
		r := rand.New(rand.NewSource(seed))
		capacity := 1 + r.Intn(6)
		ft, _ := New(capacity, r.Intn(2) == 0)
		nv := &naive{cap: capacity, evict: ft.tb.evict, maxNow: -1}
		var log []string
		now := int64(0)
		removed := 0
		failf := func(format string, a ...any) {
			t.Fatalf("seed=%d %s\n判定依据日志:\n%s", seed, fmt.Sprintf(format, a...), joinLog(log))
		}
		for step := 0; step < 40; step++ {
			now += int64(r.Intn(3))
			if r.Intn(20) == 0 {
				now -= int64(r.Intn(3))
			}
			if now < 0 {
				now = 0
			}
			mt := rndMatch(r)
			prio := uint16(r.Intn(3))
			opKind := r.Intn(5)
			switch opKind {
			case 0:
				m := AddMsg{Match: mt, Prio: prio, CheckOverlap: r.Intn(2) == 0,
					Action: fmt.Sprintf("act%d", r.Intn(4)), Importance: uint16(r.Intn(3)),
					Idle: int64([]int{0, 2, 5}[r.Intn(3)]), Hard: int64([]int{0, 4, 9}[r.Intn(3)])}
				if r.Intn(20) == 0 {
					m.Idle = 1e9 + 1
				}
				rid, rev, rerr := ft.Add(m, now)
				o := nv.add(m, now)
				log = append(log, fmt.Sprintf("Add F0=%v prio=%d chk=%v imp=%d idle=%d hard=%d now=%d => id=%d ev=%+v 依据err=%v",
					m.Match.F0, m.Prio, m.CheckOverlap, m.Importance, m.Idle, m.Hard, now, rid.ID, rev, rerr))
				if !errors.Is(rerr, o.err) || rid.ID != o.id || !sameEvents(rev, o.events) {
					failf("Add mismatch real=(id=%d ev=%+v err=%v) model=(id=%d ev=%+v err=%v)",
						rid.ID, rev, rerr, o.id, o.events, o.err)
				}
				if rerr == nil {
					removed += len(rev)
				}
			case 1:
				p := match.Pkt{F0: r.Uint32() & 0x0FFFFFFF}
				b := uint64(1 + r.Intn(9))
				act, id, miss, rev, rerr := ft.Lookup(p, b, now)
				o := nv.lookup(p, b, now)
				log = append(log, fmt.Sprintf("Lookup pkt=%#x now=%d => act=%s id=%d miss=%v ev=%+v 依据err=%v",
					p.F0, now, act, id, miss, rev, rerr))
				if !errors.Is(rerr, o.err) || miss != o.miss || id != o.id || act != o.action || !sameEvents(rev, o.events) {
					failf("Lookup mismatch real=(act=%s id=%d miss=%v ev=%+v err=%v) model=%+v",
						act, id, miss, rev, rerr, o)
				}
				if rerr == nil {
					removed += len(rev)
				}
			case 2, 3:
				m := ModifyMsg{Match: mt, Prio: prio, Strict: opKind == 3, Action: "M"}
				n, rev, rerr := ft.Modify(m, now)
				o := nv.modify(m, now)
				log = append(log, fmt.Sprintf("Modify F0=%v prio=%d strict=%v now=%d => n=%d ev=%+v 依据err=%v",
					m.Match.F0, prio, m.Strict, now, n, rev, rerr))
				if !errors.Is(rerr, o.err) || n != o.affected || !sameEvents(rev, o.events) {
					failf("Modify mismatch real=(n=%d ev=%+v err=%v) model=%+v", n, rev, rerr, o)
				}
				if rerr == nil {
					removed += len(rev)
				}
			default:
				d := DeleteMsg{Match: mt, Prio: prio, Strict: r.Intn(2) == 0}
				n, rev, rerr := ft.Delete(d, now)
				o := nv.del(d, now)
				log = append(log, fmt.Sprintf("Delete F0=%v prio=%d strict=%v now=%d => n=%d ev=%+v 依据err=%v",
					d.Match.F0, prio, d.Strict, now, n, rev, rerr))
				if !errors.Is(rerr, o.err) || n != o.affected || !sameEvents(rev, o.events) {
					failf("Delete mismatch real=(n=%d ev=%+v err=%v) model=%+v", n, rev, rerr, o)
				}
				if rerr == nil {
					removed += len(rev)
				}
			}
			if ft.Len() != len(nv.es) {
				failf("len real=%d model=%d", ft.Len(), len(nv.es))
			}
			if ft.Len() > capacity {
				failf("capacity violated len=%d cap=%d", ft.Len(), capacity)
			}
		}
		// 不变量：新装次数（不含替换）= 现存数 + 各原因移除事件数。
		if nv.newInstalls != len(nv.es)+nv.removals {
			failf("invariant newInstalls=%d != live=%d + removals=%d",
				nv.newInstalls, len(nv.es), nv.removals)
		}
		_ = removed
		t.Logf("seed=%d 输入40步随机序列 输出与朴素线性扫描逐项一致 判定依据: 终态表项=%d 累计新装=%d 累计移除=%d",
			seed, len(nv.es), nv.newInstalls, nv.removals)
	}
}
