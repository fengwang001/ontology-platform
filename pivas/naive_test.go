package pivas

import (
	"sort"
	"strconv"
)

// naive 是与 System 完全独立的朴素参考模型（仅共享数据类型与错误码）。
// 每次受理都全量重建批次时间线并暴力枚举，不做任何增量剪枝。
type naive struct {
	now      int
	drugs    map[string]Drug
	bad      map[pairKey]struct{}
	benches  []Bench
	dur      map[int]int
	trans    map[Storage]int
	orders   map[string]*nOrder
	start    map[string]int
	batchSeq int
}

type nOrder struct {
	in         OrderInput
	room, cold int
	cover      bool
	st         Storage
	bench      int
	batch      string
}

type nBatch struct {
	id      string
	bench   int
	solvent string
	cover   bool
	ids     []string
	start   int
	dur     int
}

func newNaive() *naive {
	return &naive{
		drugs:  map[string]Drug{},
		bad:    map[pairKey]struct{}{},
		dur:    map[int]int{},
		trans:  map[Storage]int{},
		orders: map[string]*nOrder{},
		start:  map[string]int{},
	}
}

func (m *naive) newBatchID() string {
	m.batchSeq++
	return "NB" + strconv.Itoa(m.batchSeq)
}

func (m *naive) setTransport(now int, st Storage, sec int) error {
	if (st != Room && st != Cold) || sec <= 0 {
		return errf(ErrInvalidParam, "x")
	}
	if now < m.now {
		return errf(ErrClockRollback, "x")
	}
	m.trans[st] = sec
	m.now = now
	return nil
}

func (m *naive) accept(now int, in OrderInput) error {
	if in.ID == "" || in.Solvent == "" || len(in.Drugs) < 1 || len(in.Drugs) > 6 {
		return errf(ErrInvalidParam, "x")
	}
	seen := map[string]bool{}
	for _, d := range in.Drugs {
		if d == "" || seen[d] {
			return errf(ErrInvalidParam, "x")
		}
		seen[d] = true
	}
	if now < m.now {
		return errf(ErrClockRollback, "x")
	}
	if m.orders[in.ID] != nil {
		return errf(ErrInvalidParam, "x")
	}
	for _, d := range in.Drugs {
		if _, ok := m.drugs[d]; !ok {
			return errf(ErrDrugNotFound, "x")
		}
	}
	for i := range in.Drugs {
		for j := i + 1; j < len(in.Drugs); j++ {
			if _, bad := m.bad[pairKeyOf(in.Drugs[i], in.Drugs[j])]; bad {
				return errf(ErrIncompatiblePair, "x")
			}
		}
	}
	cover, room, cold := false, 0, 0
	for i, d := range in.Drugs {
		info := m.drugs[d]
		if info.SolventClass != in.Solvent {
			return errf(ErrSolventMismatch, "x")
		}
		if info.LightSensitive {
			cover = true
		}
		if i == 0 || info.RoomStableSec < room {
			room = info.RoomStableSec
		}
		if i == 0 || info.ColdStableSec < cold {
			cold = info.ColdStableSec
		}
	}

	groups := m.batches()
	var best *nCand
	for bi := range m.benches {
		list := groups[bi]
		for seq, b := range list {
			if b.solvent != in.Solvent || b.cover != cover || b.start < now {
				continue
			}
			for _, st := range []Storage{Room, Cold} {
				no := &nOrder{in: in, room: room, cold: cold, cover: cover, st: st}
				moves, newEnd, ok := m.simJoin(no, b, list, in.Urgent, now)
				if !ok {
					continue
				}
				c := &nCand{
					deliver: newEnd + m.trans[st], bench: bi, seq: seq,
					existing: true, st: st, batch: b.id, moves: moves,
				}
				if best == nil || m.better(c, best) {
					best = c
				}
			}
		}
		if d1, ok := m.dur[1]; ok {
			gap := m.benches[bi].ClearGap
			start := now
			for _, b := range list {
				if b.start+b.dur <= now {
					continue
				}
				if start+d1+gap <= b.start {
					break
				}
				start = b.start + b.dur + gap
			}
			ready := start + d1
			for _, st := range []Storage{Room, Cold} {
				no := &nOrder{in: in, room: room, cold: cold, cover: cover, st: st}
				if !m.valid(no, ready, int(st)) {
					continue
				}
				c := &nCand{
					deliver: ready + m.trans[st], bench: bi, seq: 1 << 30,
					existing: false, st: st, isNew: 1, newStart: start,
					batch: m.newBatchID(),
				}
				if best == nil || m.better(c, best) {
					best = c
				}
			}
		}
	}

	if best == nil {
		if cover && m.lightConflict(in, groups, now) {
			return errf(ErrLightConflict, "x")
		}
		return errf(ErrNoFeasiblePlan, "x")
	}

	no := &nOrder{in: in, room: room, cold: cold, cover: cover, st: best.st, bench: best.bench, batch: best.batch}
	m.orders[in.ID] = no
	if best.isNew == 1 {
		m.start[best.batch] = best.newStart
	} else {
		for id, st0 := range best.moves {
			m.start[id] = st0
		}
	}
	m.now = now
	return nil
}

func (m *naive) lightConflict(in OrderInput, groups [][]*nBatch, now int) bool {
	for bi := range m.benches {
		for _, b := range groups[bi] {
			if b.solvent != in.Solvent || b.cover || b.start < now {
				continue
			}
			if len(b.ids) >= m.benches[bi].Capacity {
				continue
			}
			newDur, ok := m.dur[len(b.ids)+1]
			if !ok {
				continue
			}
			end := b.start + newDur
			if bi+1 <= len(m.benches) {
				list := groups[bi]
				idx := 0
				for i, x := range list {
					if x == b {
						idx = i
					}
				}
				if idx+1 < len(list) && end+m.benches[bi].ClearGap > list[idx+1].start {
					continue
				}
			}
			for _, st := range []Storage{Room, Cold} {
				probe := &nOrder{in: in, room: 1 << 30, cold: 1 << 30, cover: true, st: st}
				for _, d := range in.Drugs {
					info := m.drugs[d]
					if info.RoomStableSec < probe.room {
						probe.room = info.RoomStableSec
					}
					if info.ColdStableSec < probe.cold {
						probe.cold = info.ColdStableSec
					}
				}
				if m.valid(probe, end, int(st)) {
					return true
				}
			}
		}
	}
	return false
}

// snapshot 导出朴素模型的完整排程，供与主系统逐字段对照。
func (m *naive) snapshot() map[string]struct {
	bench, batch    string
	start, ready    int
	st              Storage
	deliver, expire int
	onTime, cover   bool
} {
	groups := m.batches()
	out := map[string]struct {
		bench, batch    string
		start, ready    int
		st              Storage
		deliver, expire int
		onTime, cover   bool
	}{}
	for bi := range groups {
		for _, b := range groups[bi] {
			for _, id := range b.ids {
				o := m.orders[id]
				sec := m.trans[o.st]
				ready := b.start + b.dur
				stable := o.room
				if o.st == Cold {
					stable = o.cold
				}
				deliver := ready + sec
				expire := ready + stable
				out[id] = struct {
					bench, batch    string
					start, ready    int
					st              Storage
					deliver, expire int
					onTime, cover   bool
				}{
					bench: m.benches[bi].ID, batch: b.id,
					start: b.start, ready: ready, st: o.st,
					deliver: deliver, expire: expire,
					onTime: deliver < expire && deliver <= o.in.DueAt, cover: o.cover,
				}
			}
		}
	}
	return out
}

func (m *naive) setDuration(now, n, sec int) error {
	if n <= 0 || sec <= 0 {
		return errf(ErrInvalidParam, "x")
	}
	if now < m.now {
		return errf(ErrClockRollback, "x")
	}
	m.dur[n] = sec
	m.now = now
	return nil
}

func (m *naive) addBench(now int, b Bench) error {
	if b.ID == "" || b.Capacity <= 0 || b.ClearGap <= 0 {
		return errf(ErrInvalidParam, "x")
	}
	if now < m.now {
		return errf(ErrClockRollback, "x")
	}
	for _, x := range m.benches {
		if x.ID == b.ID {
			return errf(ErrInvalidParam, "x")
		}
	}
	m.benches = append(m.benches, b)
	sort.Slice(m.benches, func(i, j int) bool { return m.benches[i].ID < m.benches[j].ID })
	m.now = now
	return nil
}

func (m *naive) addDrug(now int, id string, d Drug) error {
	if id == "" || d.SolventClass == "" || d.RoomStableSec <= 0 || d.ColdStableSec <= 0 {
		return errf(ErrInvalidParam, "x")
	}
	if now < m.now {
		return errf(ErrClockRollback, "x")
	}
	m.drugs[id] = d
	m.now = now
	return nil
}

func (m *naive) addBad(now int, a, b string) error {
	if a == "" || b == "" || a == b {
		return errf(ErrInvalidParam, "x")
	}
	if now < m.now {
		return errf(ErrClockRollback, "x")
	}
	m.bad[pairKeyOf(a, b)] = struct{}{}
	m.now = now
	return nil
}

// batches 全量重建全部批次（无剪枝），按台、开始时刻、批次号排序。
func (m *naive) batches() [][]*nBatch {
	groups := make([][]*nBatch, len(m.benches))
	byID := map[string]*nBatch{}
	for _, o := range m.orders {
		b := byID[o.batch]
		if b == nil {
			b = &nBatch{id: o.batch, bench: o.bench, solvent: o.in.Solvent, cover: o.cover, start: m.start[o.batch]}
			byID[o.batch] = b
		}
		b.ids = append(b.ids, o.in.ID)
	}
	for _, b := range byID {
		b.dur = m.dur[len(b.ids)]
		groups[b.bench] = append(groups[b.bench], b)
	}
	for bi := range groups {
		sort.Slice(groups[bi], func(i, j int) bool {
			if groups[bi][i].start != groups[bi][j].start {
				return groups[bi][i].start < groups[bi][j].start
			}
			return groups[bi][i].id < groups[bi][j].id
		})
	}
	return groups
}

func (m *naive) valid(o *nOrder, ready, st int) bool {
	sec, ok := m.trans[Storage(st)]
	if !ok {
		return false
	}
	stable := o.room
	if Storage(st) == Cold {
		stable = o.cold
	}
	deliver := ready + sec
	return deliver < ready+stable && deliver <= o.in.DueAt
}

type nCand struct {
	deliver, bench, seq int
	existing            bool
	st                  Storage
	batch               string
	isNew, newStart     int
	moves               map[string]int
}

func (m *naive) better(a, b *nCand) bool {
	if a.deliver != b.deliver {
		return a.deliver < b.deliver
	}
	if m.benches[a.bench].ID != m.benches[b.bench].ID {
		return m.benches[a.bench].ID < m.benches[b.bench].ID
	}
	if a.existing != b.existing {
		return a.existing
	}
	return a.seq < b.seq
}

// simJoin 暴力评估把新医嘱（存放 st）加入 target 的可行性。
func (m *naive) simJoin(no *nOrder, target *nBatch, list []*nBatch, urgent bool, now int) (map[string]int, int, bool) {
	if len(target.ids) >= m.benches[target.bench].Capacity {
		return nil, 0, false
	}
	newDur, ok := m.dur[len(target.ids)+1]
	if !ok {
		return nil, 0, false
	}
	gap := m.benches[target.bench].ClearGap
	idx := 0
	for i, b := range list {
		if b == target {
			idx = i
		}
	}
	newEnd := target.start + newDur
	moves := map[string]int{target.id: target.start}

	if !urgent {
		if idx+1 < len(list) && newEnd+gap > list[idx+1].start {
			return nil, 0, false
		}
	} else {
		prevEnd := newEnd
		for k := idx + 1; k < len(list); k++ {
			cur := list[k]
			st0 := cur.start
			if prevEnd+gap > st0 {
				if st0 < now {
					return nil, 0, false
				}
				st0 = prevEnd + gap
			}
			moves[cur.id] = st0
			prevEnd = st0 + cur.dur
		}
	}

	for k := idx; k < len(list); k++ {
		b := list[k]
		ready := b.start + b.dur
		if k == idx {
			ready = newEnd
		} else if st0, moved := moves[b.id]; moved {
			ready = st0 + b.dur
		} else if !urgent {
			break
		}
		for _, id := range b.ids {
			if !m.valid(m.orders[id], ready, int(m.orders[id].st)) {
				return nil, 0, false
			}
		}
	}
	if !m.valid(no, newEnd, int(no.st)) {
		return nil, 0, false
	}
	return moves, newEnd, true
}

func (m *naive) cancel(now int, id string) error {
	if id == "" {
		return errf(ErrInvalidParam, "x")
	}
	if now < m.now {
		return errf(ErrClockRollback, "x")
	}
	o := m.orders[id]
	if o == nil {
		return errf(ErrInvalidParam, "x")
	}
	if m.start[o.batch] < now {
		return errf(ErrBadState, "x")
	}
	delete(m.orders, id)
	empty := true
	for _, x := range m.orders {
		if x.batch == o.batch {
			empty = false
		}
	}
	if empty {
		delete(m.start, o.batch)
	}
	m.now = now
	return nil
}
