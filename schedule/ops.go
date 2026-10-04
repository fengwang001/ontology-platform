package schedule

import (
	"sort"

	"ontology/equip"
	"ontology/room"
)

const window = int64(1680)

// Book 预订择期手术。冲突只报第一个，次序见 DESIGN.md。
func (sc *Scheduler) Book(now int64, id, roomID string, start, dur int64, surgeon string, needs map[string]int) error {
	s := Surgery{ID: id, Room: roomID, Start: start, Dur: dur, Surgeon: surgeon, Needs: cloneNeeds(needs)}
	sc.mu.Lock()
	defer sc.mu.Unlock()

	if !validBook(now, s) {
		return ErrInvalid
	}
	if err := validateNeeds(s.Needs, sc.pool, false); err != nil {
		return err
	}
	if sc.haveNow && now < sc.lastNow {
		return ErrClockBack
	}
	if _, ok := sc.byID[id]; ok {
		return ErrDuplicate
	}
	if !sc.rooms.Has(roomID) {
		return ErrNotFound
	}
	for t := range s.Needs {
		if !sc.pool.Has(t) {
			return ErrNotFound
		}
	}
	for t, q := range s.Needs {
		if q > sc.pool.Total(t) {
			return ErrInvalid
		}
	}
	if len(sc.rooms.Names()) == 0 {
		return ErrNoRoom
	}

	// 只考察与 [start-1680, start+dur+1680) 相交的既有手术。
	candidates := sc.windowEntries(start-window, start+dur+window)

	turn := sc.rooms.Turn(roomID)
	iv := room.Interval{Start: start, End: start + dur}
	for _, e := range candidates {
		if e.s.Room != roomID {
			continue
		}
		if room.Conflict(iv, room.Interval{Start: e.s.Start, End: e.s.end()}, turn) {
			return ErrRoomBusy
		}
	}

	for _, e := range candidates {
		if e.s.Surgeon != surgeon {
			continue
		}
		// [start,end) 相交即冲突；首尾相接不冲突。
		if start < e.s.end() && e.s.Start < start+dur {
			return ErrSurgeon
		}
	}

	uses := toUses(candidates)
	add := equip.Use{ID: id, Start: start, End: start + dur, Quantities: s.Needs}
	types := make([]string, 0, len(s.Needs))
	for t := range s.Needs {
		types = append(types, t)
	}
	sort.Strings(types)
	for _, t := range types {
		if sc.pool.Shortage(t, uses, &add) > 0 {
			return &EquipShortError{Type: t}
		}
	}

	sc.advance(now)
	sc.insert(&entry{s: s})
	return nil
}

// Cancel 只能取消未开始（start > now）的择期手术。
func (sc *Scheduler) Cancel(now int64, id string) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if now < 0 || id == "" {
		return ErrInvalid
	}
	if sc.haveNow && now < sc.lastNow {
		return ErrClockBack
	}
	e, ok := sc.byID[id]
	if !ok {
		return ErrState
	}
	if e.s.Emergency || e.s.Start <= now {
		return ErrState
	}
	sc.advance(now)
	sc.remove(e)
	return nil
}

// Emergency 插入急诊，返回落点与被顶替清单；全有或全无。
func (sc *Scheduler) Emergency(now int64, id string, dur int64, surgeon string, needs map[string]int) (*Result, error) {
	s := Surgery{ID: id, Start: now, Dur: dur, Surgeon: surgeon, Needs: cloneNeeds(needs), Emergency: true}
	sc.mu.Lock()
	defer sc.mu.Unlock()

	if !validEmergency(now, id, dur, surgeon) {
		return nil, ErrInvalid
	}
	if err := validateNeeds(s.Needs, sc.pool, false); err != nil {
		return nil, err
	}
	if sc.haveNow && now < sc.lastNow {
		return nil, ErrClockBack
	}
	if _, ok := sc.byID[id]; ok {
		return nil, ErrDuplicate
	}
	for t := range s.Needs {
		if !sc.pool.Has(t) {
			return nil, ErrNotFound
		}
	}
	for t, q := range s.Needs {
		if q > sc.pool.Total(t) {
			return nil, ErrInvalid
		}
	}
	rooms := sc.rooms.Names()
	if len(rooms) == 0 {
		return nil, ErrNoRoom
	}

	type choice struct {
		room     string
		start    int64
		displace []*entry
	}
	var best *choice
	for _, r := range rooms {
		turn := int64(sc.rooms.Turn(r))
		sr := now
		for _, e := range sc.list {
			if e.s.ID == id || e.s.Room != r || !sc.immovable(e, now) {
				continue
			}
			if v := e.s.end() + turn; v > sr {
				sr = v
			}
		}
		var dis []*entry
		civ := room.Interval{Start: sr, End: sr + dur}
		for _, e := range sc.list {
			if e.s.ID == id || e.s.Room != r || sc.immovable(e, now) {
				continue
			}
			if room.Conflict(civ, room.Interval{Start: e.s.Start, End: e.s.end()}, int(turn)) {
				dis = append(dis, e)
			}
		}
		c := choice{room: r, start: sr, displace: dis}
		if best == nil {
			best = &c
		} else if sr < best.start ||
			(sr == best.start && len(dis) < len(best.displace)) ||
			(sr == best.start && len(dis) == len(best.displace) && r < best.room) {
			best = &c
		}
	}

	removed := map[*entry]bool{}
	for _, e := range best.displace {
		removed[e] = true
	}
	var step1, step2, step3 []*entry
	step1 = append(step1, best.displace...)
	sortEntries(step1)

	// 第二步：医生。与不可顶替手术相交则拒绝。
	for _, e := range sc.list {
		if e.s.ID == id {
			continue
		}
		if e.s.Surgeon != surgeon {
			continue
		}
		if !(best.start < e.s.end() && e.s.Start < best.start+dur) {
			continue
		}
		if sc.immovable(e, now) {
			return nil, ErrSurgeon
		}
		if !removed[e] {
			removed[e] = true
			step2 = append(step2, e)
		}
	}
	sortEntries(step2)

	// 第三步：设备。逐类型按名升序检查，不足时按 (start 降序, id 降序) 顶替。
	types := make([]string, 0, len(s.Needs))
	for t := range s.Needs {
		types = append(types, t)
	}
	sort.Strings(types)
	add := equip.Use{ID: id, Start: best.start, End: best.start + dur, Quantities: s.Needs}
	for _, t := range types {
		// 移除已顶替者后，构造当前占用。
		var keep []*entry
		for _, e := range sc.list {
			if !removed[e] && e.s.ID != id {
				keep = append(keep, e)
			}
		}
		for sc.pool.Shortage(t, toUses(keep), &add) > 0 {
			cand := sc.equipCandidates(t, best.start, best.start+dur, removed, now)
			if len(cand) == 0 {
				return nil, &EquipShortError{Type: t}
			}
			pick := cand[0]
			removed[pick] = true
			step3 = append(step3, pick)
			keep = nil
			for _, e := range sc.list {
				if !removed[e] && e.s.ID != id {
					keep = append(keep, e)
				}
			}
		}
	}

	// 提交：移除全部被顶替者，插入急诊，推进时钟。
	for e := range removed {
		sc.remove(e)
	}
	s.Start = best.start
	s.Room = best.room
	sc.insert(&entry{s: s})
	sc.advance(now)

	res := &Result{Room: best.room, Start: best.start}
	for _, e := range step1 {
		res.Displaced = append(res.Displaced, e.s.ID)
	}
	for _, e := range step2 {
		res.Displaced = append(res.Displaced, e.s.ID)
	}
	for _, e := range step3 {
		res.Displaced = append(res.Displaced, e.s.ID)
	}
	return res, nil
}

// immovable：已开始的手术（start <= now）与全部急诊不可顶替。
func (sc *Scheduler) immovable(e *entry, now int64) bool {
	return e.s.Emergency || e.s.Start <= now
}

// equipCandidates 求可顶替且占用该类型、占用区间与急诊占用相交者，
// 按 (start 降序, id 降序) 排序。
func (sc *Scheduler) equipCandidates(t string, start, end int64, removed map[*entry]bool, now int64) []*entry {
	st := int64(sc.pool.ST(t))
	lo, hi := start, end+st
	var cand []*entry
	for _, e := range sc.list {
		if removed[e] || sc.immovable(e, now) {
			continue
		}
		if sc.pool.Occupies(equip.Use{Start: e.s.Start, End: e.s.end(), Quantities: e.s.Needs}, t, lo, hi) {
			cand = append(cand, e)
		}
	}
	sort.Slice(cand, func(i, j int) bool {
		if cand[i].s.Start != cand[j].s.Start {
			return cand[i].s.Start > cand[j].s.Start
		}
		return cand[i].s.ID > cand[j].s.ID
	})
	return cand
}

func (sc *Scheduler) insert(e *entry) {
	i := sort.Search(len(sc.list), func(i int) bool {
		if sc.list[i].s.Start != e.s.Start {
			return sc.list[i].s.Start > e.s.Start
		}
		return sc.list[i].s.ID > e.s.ID
	})
	sc.list = append(sc.list, nil)
	copy(sc.list[i+1:], sc.list[i:])
	sc.list[i] = e
	sc.byID[e.s.ID] = e
}

func (sc *Scheduler) remove(e *entry) {
	i := sort.Search(len(sc.list), func(i int) bool {
		if sc.list[i].s.Start != e.s.Start {
			return sc.list[i].s.Start >= e.s.Start
		}
		return sc.list[i].s.ID >= e.s.ID
	})
	if i < len(sc.list) && sc.list[i] == e {
		sc.list = append(sc.list[:i], sc.list[i+1:]...)
	}
	delete(sc.byID, e.s.ID)
}

func sortEntries(es []*entry) {
	sort.Slice(es, func(i, j int) bool {
		if es[i].s.Start != es[j].s.Start {
			return es[i].s.Start < es[j].s.Start
		}
		return es[i].s.ID < es[j].s.ID
	})
}

func cloneNeeds(needs map[string]int) map[string]int {
	if needs == nil {
		return map[string]int{}
	}
	m := make(map[string]int, len(needs))
	for k, v := range needs {
		m[k] = v
	}
	return m
}

// Examined 返回最近一次 Book 考察过的既有手术数。
func (sc *Scheduler) Examined() int {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.examined
}
