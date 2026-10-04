package schedule_test

// 朴素模拟器：独立于生产代码，直接按题面规则做区间扫描，
// 给出每次操作的期望结果，并核对全部不变量。

import (
	"sort"
)

type nSurgery struct {
	id        string
	room      string
	start     int64
	dur       int64
	surgeon   string
	needs     map[string]int
	emergency bool
}

func (s nSurgery) end() int64 { return s.start + s.dur }

type naive struct {
	rooms   map[string]int
	equip   map[string][2]int
	list    []nSurgery
	now     int64
	haveNow bool
}

func newNaive() *naive {
	return &naive{rooms: map[string]int{}, equip: map[string][2]int{}}
}

func overlap(a0, a1, b0, b1 int64) bool { return a0 < b1 && b0 < a1 }

type nResult struct {
	code      string
	room      string
	start     int64
	displaced []string
}

func (nm *naive) advance(now int64) {
	if !nm.haveNow || now > nm.now {
		nm.now, nm.haveNow = now, true
	}
}

func (nm *naive) addRoom(now int64, roomID string, turn int) string {
	if now < 0 || roomID == "" || turn < 0 || turn > 240 {
		return "invalid"
	}
	if nm.haveNow && now < nm.now {
		return "clock"
	}
	if _, ok := nm.rooms[roomID]; ok {
		return "dup"
	}
	nm.rooms[roomID] = turn
	nm.advance(now)
	return "ok"
}

func (nm *naive) addEquip(now int64, t string, n, st int) string {
	if now < 0 || t == "" || n < 1 || n > 100 || st < 0 || st > 240 {
		return "invalid"
	}
	if nm.haveNow && now < nm.now {
		return "clock"
	}
	if _, ok := nm.equip[t]; ok {
		return "dup"
	}
	nm.equip[t] = [2]int{n, st}
	nm.advance(now)
	return "ok"
}

// equipPeak 端点事件扫描：同点先结束后开始；既有占用自身合法，全局峰值
// 只会在 extra 占用区间内抬高，故直接取全局峰值。
func (nm *naive) equipPeak(t string, extra nSurgery, includeExtra bool, removed map[string]bool) int {
	st := int64(nm.equip[t][1])
	type ev struct {
		at    int64
		delta int
		end   bool
	}
	var evs []ev
	add := func(s, e int64, q int) {
		if q <= 0 || s >= e {
			return
		}
		evs = append(evs, ev{s, q, false}, ev{e, -q, true})
	}
	for _, x := range nm.list {
		if removed[x.id] {
			continue
		}
		add(x.start, x.end()+st, x.needs[t])
	}
	if includeExtra {
		add(extra.start, extra.end()+st, extra.needs[t])
	}
	sort.Slice(evs, func(i, j int) bool {
		if evs[i].at != evs[j].at {
			return evs[i].at < evs[j].at
		}
		if evs[i].end != evs[j].end {
			return evs[i].end
		}
		return evs[i].delta < evs[j].delta
	})
	cur, peak := 0, 0
	for _, e := range evs {
		cur += e.delta
		if cur > peak {
			peak = cur
		}
	}
	return peak
}

func (nm *naive) validNeeds(needs map[string]int) (ok bool, missing bool) {
	if len(needs) > 8 {
		return false, false
	}
	for t, q := range needs {
		if t == "" || q < 1 {
			return false, false
		}
		if _, ex := nm.equip[t]; !ex {
			missing = true
		}
	}
	return true, missing
}

func (nm *naive) book(now int64, s nSurgery) string {
	if now < 0 || s.id == "" || s.room == "" || s.surgeon == "" ||
		s.start < now || s.start < 0 || s.dur < 1 || s.dur > 1440 ||
		s.end() > 1_000_000_000 {
		return "invalid"
	}
	if ok, _ := nm.validNeeds(s.needs); !ok {
		return "invalid"
	}
	if nm.haveNow && now < nm.now {
		return "clock"
	}
	for _, x := range nm.list {
		if x.id == s.id {
			return "dup"
		}
	}
	turn, rok := nm.rooms[s.room]
	if !rok {
		return "notfound"
	}
	for t := range s.needs {
		if _, ok := nm.equip[t]; !ok {
			return "notfound"
		}
	}
	for t, q := range s.needs {
		if q > nm.equip[t][0] {
			return "invalid"
		}
	}
	// 同间冲突。
	tt := int64(turn)
	for _, x := range nm.list {
		if x.room == s.room && s.start < x.end()+tt && x.start < s.end()+tt {
			return "room"
		}
	}
	// 医生冲突。
	for _, x := range nm.list {
		if x.surgeon == s.surgeon && overlap(s.start, s.end(), x.start, x.end()) {
			return "surgeon"
		}
	}
	// 设备不足，类型名升序。
	ts := make([]string, 0, len(s.needs))
	for t := range s.needs {
		ts = append(ts, t)
	}
	sort.Strings(ts)
	for _, t := range ts {
		if nm.equipPeak(t, s, true, nil) > nm.equip[t][0] {
			return "equip:" + t
		}
	}
	nm.advance(now)
	nm.list = append(nm.list, s)
	return "ok"
}

func (nm *naive) cancel(now int64, id string) string {
	if now < 0 || id == "" {
		return "invalid"
	}
	if nm.haveNow && now < nm.now {
		return "clock"
	}
	for i, x := range nm.list {
		if x.id != id {
			continue
		}
		if x.emergency || x.start <= now {
			return "state"
		}
		nm.list = append(nm.list[:i], nm.list[i+1:]...)
		nm.advance(now)
		return "ok"
	}
	return "state"
}

func (nm *naive) emergency(now int64, s nSurgery) nResult {
	s.emergency = true
	s.start = now
	if now < 0 || s.id == "" || s.surgeon == "" || s.dur < 1 || s.dur > 1440 ||
		s.end() > 1_000_000_000 {
		return nResult{code: "invalid"}
	}
	if ok, _ := nm.validNeeds(s.needs); !ok {
		return nResult{code: "invalid"}
	}
	if nm.haveNow && now < nm.now {
		return nResult{code: "clock"}
	}
	for _, x := range nm.list {
		if x.id == s.id {
			return nResult{code: "dup"}
		}
	}
	for t := range s.needs {
		if _, ok := nm.equip[t]; !ok {
			return nResult{code: "notfound"}
		}
	}
	for t, q := range s.needs {
		if q > nm.equip[t][0] {
			return nResult{code: "invalid"}
		}
	}
	if len(nm.rooms) == 0 {
		return nResult{code: "noroom"}
	}
	immovable := func(x nSurgery) bool { return x.emergency || x.start <= now }

	type choice struct {
		room  string
		sr    int64
		step1 []nSurgery
	}
	roomNames := make([]string, 0, len(nm.rooms))
	for r := range nm.rooms {
		roomNames = append(roomNames, r)
	}
	sort.Strings(roomNames)
	var best *choice
	for _, r := range roomNames {
		turn := int64(nm.rooms[r])
		sr := now
		for _, x := range nm.list {
			if x.room == r && immovable(x) && x.end()+turn > sr {
				sr = x.end() + turn
			}
		}
		var step1 []nSurgery
		for _, x := range nm.list {
			if x.room != r || immovable(x) {
				continue
			}
			if sr < x.end()+turn && x.start < sr+s.dur+turn {
				step1 = append(step1, x)
			}
		}
		c := &choice{room: r, sr: sr, step1: step1}
		if best == nil || sr < best.sr ||
			(sr == best.sr && len(step1) < len(best.step1)) ||
			(sr == best.sr && len(step1) == len(best.step1) && r < best.room) {
			best = c
		}
	}

	removed := map[string]bool{}
	var d1, d2, d3 []string
	sort.Slice(best.step1, func(i, j int) bool {
		if best.step1[i].start != best.step1[j].start {
			return best.step1[i].start < best.step1[j].start
		}
		return best.step1[i].id < best.step1[j].id
	})
	for _, x := range best.step1 {
		removed[x.id] = true
		d1 = append(d1, x.id)
	}

	var step2 []nSurgery
	for _, x := range nm.list {
		if x.surgeon != s.surgeon || !overlap(best.sr, best.sr+s.dur, x.start, x.end()) {
			continue
		}
		if immovable(x) {
			return nResult{code: "surgeon"}
		}
		if !removed[x.id] {
			removed[x.id] = true
			step2 = append(step2, x)
		}
	}
	sort.Slice(step2, func(i, j int) bool {
		if step2[i].start != step2[j].start {
			return step2[i].start < step2[j].start
		}
		return step2[i].id < step2[j].id
	})
	for _, x := range step2 {
		d2 = append(d2, x.id)
	}

	s.start = best.sr
	s.room = best.room
	ts := make([]string, 0, len(s.needs))
	for t := range s.needs {
		ts = append(ts, t)
	}
	sort.Strings(ts)
	for _, t := range ts {
		for nm.equipPeak(t, s, true, removed) > nm.equip[t][0] {
			st := int64(nm.equip[t][1])
			var cand []nSurgery
			for _, x := range nm.list {
				if removed[x.id] || immovable(x) || x.needs[t] <= 0 {
					continue
				}
				if overlap(best.sr, best.sr+s.dur+st, x.start, x.end()+st) {
					cand = append(cand, x)
				}
			}
			if len(cand) == 0 {
				return nResult{code: "equip:" + t}
			}
			sort.Slice(cand, func(i, j int) bool {
				if cand[i].start != cand[j].start {
					return cand[i].start > cand[j].start
				}
				return cand[i].id > cand[j].id
			})
			removed[cand[0].id] = true
			d3 = append(d3, cand[0].id)
		}
	}

	keep := nm.list[:0:0]
	for _, x := range nm.list {
		if !removed[x.id] {
			keep = append(keep, x)
		}
	}
	nm.list = append(keep, s)
	nm.advance(now)
	res := nResult{code: "ok", room: best.room, start: best.sr}
	res.displaced = append(res.displaced, d1...)
	res.displaced = append(res.displaced, d2...)
	res.displaced = append(res.displaced, d3...)
	return res
}

// invariants 朴素逐对核对三类不变量。
func (nm *naive) invariants() string {
	for i := 0; i < len(nm.list); i++ {
		for j := i + 1; j < len(nm.list); j++ {
			x, y := nm.list[i], nm.list[j]
			if x.room == y.room {
				tt := int64(nm.rooms[x.room])
				if x.start < y.end()+tt && y.start < x.end()+tt {
					return "room invariant broken: " + x.id + " vs " + y.id
				}
			}
			if x.surgeon == y.surgeon && overlap(x.start, x.end(), y.start, y.end()) {
				return "surgeon invariant broken: " + x.id + " vs " + y.id
			}
		}
	}
	ts := make([]string, 0, len(nm.equip))
	for t := range nm.equip {
		ts = append(ts, t)
	}
	for _, t := range ts {
		extra := nSurgery{}
		if peak := nm.equipPeak(t, extra, false, nil); peak > nm.equip[t][0] {
			return "equip invariant broken: " + t
		}
	}
	return ""
}

var _ = sort.Strings
