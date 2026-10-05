package flowtable

import (
	"errors"
	"math/rand"
	"sort"
	"testing"

	"ontology/match"
)

// naive 是逐项线性扫描的朴素模拟，作为参照实现。
type naive struct {
	capacity int
	evict    bool
	clock    uint64
	nextSeq  uint64
	entries  []*entry
	installs int // 新装次数（不含替换）
	removals int // 各原因移除事件数
}

func newNaive(capacity int, evict bool) *naive {
	return &naive{capacity: capacity, evict: evict, nextSeq: 1}
}

func (n *naive) expired(now uint64) ([]*entry, []Reason) {
	var es []*entry
	var rs []Reason
	for _, e := range n.entries {
		if at, r, ok := e.expiry(); ok && at <= now {
			es = append(es, e)
			rs = append(rs, r)
		}
	}
	sort.Slice(es, func(i, j int) bool {
		ai, _, _ := es[i].expiry()
		aj, _, _ := es[j].expiry()
		if ai != aj {
			return ai < aj
		}
		return es[i].seq < es[j].seq
	})
	rs = rs[:0]
	for _, e := range es {
		_, r, _ := e.expiry()
		rs = append(rs, r)
	}
	return es, rs
}

func (n *naive) remove(e *entry) {
	for i, x := range n.entries {
		if x == e {
			n.entries = append(n.entries[:i], n.entries[i+1:]...)
			return
		}
	}
}

func (n *naive) landExpiry(now uint64) []Event {
	es, rs := n.expired(now)
	var evs []Event
	for i, e := range es {
		at, _, _ := e.expiry()
		n.remove(e)
		n.removals++
		evs = append(evs, Event{Seq: e.seq, Reason: rs[i], Time: at, Packets: e.packets, Bytes: e.bytes})
	}
	return evs
}

func (n *naive) live() []*entry {
	// 调用方保证已落地到期，现存即存活。
	return n.entries
}

func (n *naive) add(m match.Match, prio uint16, check bool, action uint32, importance uint16, idle, hard uint32, now uint64) (uint64, []Event, error) {
	if now < n.clock {
		return 0, nil, ErrClock
	}
	// 纯判定：把到期项视为不存在，但不修改状态。
	exp, _ := n.expired(now)
	isExp := make(map[*entry]bool)
	for _, e := range exp {
		isExp[e] = true
	}
	if check {
		for _, e := range n.entries {
			if !isExp[e] && e.prio == prio && e.m.Overlaps(m) {
				return 0, nil, ErrOverlap
			}
		}
	}
	for _, e := range n.entries {
		if !isExp[e] && e.prio == prio && e.m.Equal(m) {
			evs := n.landExpiry(now)
			n.remove(e)
			ne := &entry{m: m, prio: prio, action: action, importance: importance,
				idle: idle, hard: hard, installed: now, lastHit: now, seq: n.nextSeq}
			n.nextSeq++
			n.entries = append(n.entries, ne)
			n.clock = now
			return ne.seq, evs, nil
		}
	}
	if len(n.entries)-len(exp) >= n.capacity {
		if !n.evict {
			return 0, nil, ErrTableFull
		}
		var victim *entry
		for _, e := range n.entries {
			if isExp[e] {
				continue
			}
			if victim == nil || e.importance < victim.importance ||
				(e.importance == victim.importance && e.seq < victim.seq) {
				victim = e
			}
		}
		if victim == nil || victim.importance >= importance {
			return 0, nil, ErrTableFull
		}
		evs := n.landExpiry(now)
		n.remove(victim)
		n.removals++
		evs = append(evs, Event{Seq: victim.seq, Reason: Evict, Time: now, Packets: victim.packets, Bytes: victim.bytes})
		ne := &entry{m: m, prio: prio, action: action, importance: importance,
			idle: idle, hard: hard, installed: now, lastHit: now, seq: n.nextSeq}
		n.nextSeq++
		n.entries = append(n.entries, ne)
		n.installs++
		n.clock = now
		return ne.seq, evs, nil
	}
	evs := n.landExpiry(now)
	ne := &entry{m: m, prio: prio, action: action, importance: importance,
		idle: idle, hard: hard, installed: now, lastHit: now, seq: n.nextSeq}
	n.nextSeq++
	n.entries = append(n.entries, ne)
	n.installs++
	n.clock = now
	return ne.seq, evs, nil
}

func (n *naive) lookup(pkt match.Packet, bytes, now uint64) (uint32, uint64, bool, []Event, error) {
	if now < n.clock {
		return 0, 0, false, nil, ErrClock
	}
	evs := n.landExpiry(now)
	var best *entry
	for _, e := range n.live() {
		if !e.m.Hit(pkt) {
			continue
		}
		if best == nil || e.prio > best.prio || (e.prio == best.prio && e.seq < best.seq) {
			best = e
		}
	}
	n.clock = now
	if best == nil {
		return 0, 0, false, evs, nil
	}
	best.packets++
	best.bytes += bytes
	best.lastHit = now
	return best.action, best.seq, true, evs, nil
}

func (n *naive) modify(m match.Match, prio uint16, strict bool, action uint32, now uint64) (int, []Event, error) {
	if now < n.clock {
		return 0, nil, ErrClock
	}
	evs := n.landExpiry(now)
	cnt := 0
	for _, e := range n.live() {
		if (strict && e.prio == prio && e.m.Equal(m)) || (!strict && m.Contains(e.m)) {
			e.action = action
			cnt++
		}
	}
	n.clock = now
	return cnt, evs, nil
}

func (n *naive) delete(m match.Match, prio uint16, strict bool, now uint64) (int, []Event, error) {
	if now < n.clock {
		return 0, nil, ErrClock
	}
	evs := n.landExpiry(now)
	var targets []*entry
	for _, e := range n.live() {
		if (strict && e.prio == prio && e.m.Equal(m)) || (!strict && m.Contains(e.m)) {
			targets = append(targets, e)
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].seq < targets[j].seq })
	for _, e := range targets {
		n.remove(e)
		n.removals++
		evs = append(evs, Event{Seq: e.seq, Reason: Delete, Time: now, Packets: e.packets, Bytes: e.bytes})
	}
	n.clock = now
	return len(targets), evs, nil
}

func (n *naive) advance(now uint64) ([]Event, error) {
	if now < n.clock {
		return nil, ErrClock
	}
	evs := n.landExpiry(now)
	n.clock = now
	return evs, nil
}

// randMatch 从小值域生成随机匹配，迫使重叠、包含与相同频繁出现。
func randMatch(r *rand.Rand) match.Match {
	vals := []uint32{0, 0x0A000000, 0x0A010000, 0x0B000000, 0x00000001}
	masks := []uint32{0, 0xFF000000, 0xFFFF0000, 0xFFFFFFFF}
	v0 := vals[r.Intn(len(vals))]
	m0 := masks[r.Intn(len(masks))]
	v1 := vals[r.Intn(len(vals))] & 0xFF
	m1 := masks[r.Intn(len(masks))] & 0xFF
	return match.Must(v0&m0, m0, v1&m1, m1)
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	for _, s := range []error{ErrInvalidParam, ErrClock, ErrOverlap, ErrTableFull} {
		if errors.Is(a, s) || errors.Is(b, s) {
			return errors.Is(a, s) && errors.Is(b, s)
		}
	}
	return false
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

// TestRandomAgainstNaive 1500 组随机操作序列与朴素线性扫描模拟对照。
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 1500
	for seq := 0; seq < sequences; seq++ {
		r := rand.New(rand.NewSource(int64(seq)))
		capacity := 1 + r.Intn(6)
		evict := r.Intn(2) == 0
		ft, err := New(capacity, evict)
		if err != nil {
			t.Fatal(err)
		}
		nv := newNaive(capacity, evict)
		now := uint64(0)
		ops := 10 + r.Intn(30)
		for op := 0; op < ops; op++ {
			now += uint64(r.Intn(40))
			if r.Intn(20) == 0 && now >= 10 {
				now -= 10 // 偶发时钟回退
			}
			kind := r.Intn(100)
			switch {
			case kind < 40: // Add
				m := randMatch(r)
				prio := uint32(r.Intn(4))
				check := r.Intn(2) == 0
				action := uint32(r.Intn(1000))
				importance := uint32(r.Intn(4))
				var idle, hard uint32
				if r.Intn(3) == 0 {
					idle = uint32(1 + r.Intn(100))
				}
				if r.Intn(3) == 0 {
					hard = uint32(1 + r.Intn(100))
				}
				gSeq, gEvs, gErr := ft.Add(m, prio, check, action, importance, idle, hard, now)
				nSeq, nEvs, nErr := nv.add(m, uint16(prio), check, action, uint16(importance), idle, hard, now)
				if !sameErr(gErr, nErr) || gSeq != nSeq || !sameEvents(gEvs, nEvs) {
					t.Fatalf("序列 %d 操作 %d Add(%v prio=%d chk=%v imp=%d idle=%d hard=%d now=%d):\n实现 seq=%d evs=%v err=%v\n朴素 seq=%d evs=%v err=%v",
						seq, op, m, prio, check, importance, idle, hard, now, gSeq, gEvs, gErr, nSeq, nEvs, nErr)
				}
				t.Logf("序列%d op%d Add prio=%d chk=%v now=%d → seq=%d evs=%v err=%v", seq, op, prio, check, now, gSeq, gEvs, gErr)
			case kind < 70: // Lookup
				pkt := match.Packet{uint32(r.Intn(4)) << 24, uint32(r.Intn(3))}
				bytes := uint64(r.Intn(100))
				gA, gS, gH, gEvs, gErr := ft.Lookup(pkt, bytes, now)
				nA, nS, nH, nEvs, nErr := nv.lookup(pkt, bytes, now)
				if !sameErr(gErr, nErr) || gA != nA || gS != nS || gH != nH || !sameEvents(gEvs, nEvs) {
					t.Fatalf("序列 %d 操作 %d Lookup(%v bytes=%d now=%d):\n实现 action=%d seq=%d hit=%v evs=%v err=%v\n朴素 action=%d seq=%d hit=%v evs=%v err=%v",
						seq, op, pkt, bytes, now, gA, gS, gH, gEvs, gErr, nA, nS, nH, nEvs, nErr)
				}
				t.Logf("序列%d op%d Lookup pkt=%v now=%d → hit=%v seq=%d evs=%v err=%v", seq, op, pkt, now, gH, gS, gEvs, gErr)
			case kind < 80: // Modify
				m := randMatch(r)
				prio := uint32(r.Intn(4))
				strict := r.Intn(2) == 0
				action := uint32(r.Intn(1000))
				gN, gEvs, gErr := ft.Modify(m, prio, strict, action, now)
				nN, nEvs, nErr := nv.modify(m, uint16(prio), strict, action, now)
				if !sameErr(gErr, nErr) || gN != nN || !sameEvents(gEvs, nEvs) {
					t.Fatalf("序列 %d 操作 %d Modify(%v prio=%d strict=%v now=%d):\n实现 n=%d evs=%v err=%v\n朴素 n=%d evs=%v err=%v",
						seq, op, m, prio, strict, now, gN, gEvs, gErr, nN, nEvs, nErr)
				}
				t.Logf("序列%d op%d Modify strict=%v now=%d → n=%d evs=%v err=%v", seq, op, strict, now, gN, gEvs, gErr)
			case kind < 90: // Delete
				m := randMatch(r)
				prio := uint32(r.Intn(4))
				strict := r.Intn(2) == 0
				gN, gEvs, gErr := ft.Delete(m, prio, strict, now)
				nN, nEvs, nErr := nv.delete(m, uint16(prio), strict, now)
				if !sameErr(gErr, nErr) || gN != nN || !sameEvents(gEvs, nEvs) {
					t.Fatalf("序列 %d 操作 %d Delete(%v prio=%d strict=%v now=%d):\n实现 n=%d evs=%v err=%v\n朴素 n=%d evs=%v err=%v",
						seq, op, m, prio, strict, now, gN, gEvs, gErr, nN, nEvs, nErr)
				}
				t.Logf("序列%d op%d Delete strict=%v now=%d → n=%d evs=%v err=%v", seq, op, strict, now, gN, gEvs, gErr)
			default: // Advance
				gEvs, gErr := ft.Advance(now)
				nEvs, nErr := nv.advance(now)
				if !sameErr(gErr, nErr) || !sameEvents(gEvs, nEvs) {
					t.Fatalf("序列 %d 操作 %d Advance(now=%d):\n实现 evs=%v err=%v\n朴素 evs=%v err=%v",
						seq, op, now, gEvs, gErr, nEvs, nErr)
				}
			}
			if ft.Len() != len(nv.entries) {
				t.Fatalf("序列 %d 操作 %d 后表项数不一致: 实现 %d 朴素 %d", seq, op, ft.Len(), len(nv.entries))
			}
			if ft.Len() > capacity {
				t.Fatalf("序列 %d 操作 %d 后表项数 %d 超过容量 %d", seq, op, ft.Len(), capacity)
			}
		}
		// 不变量：新装次数（不含替换）= 现存数 + 各原因移除事件数。
		if nv.installs != len(nv.entries)+nv.removals {
			t.Fatalf("序列 %d 不变量破坏: installs=%d 现存=%d removals=%d",
				seq, nv.installs, len(nv.entries), nv.removals)
		}
		if ft.Now() != nv.clock {
			t.Fatalf("序列 %d 时钟不一致: 实现 %d 朴素 %d", seq, ft.Now(), nv.clock)
		}
	}
}
