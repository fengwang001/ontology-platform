package scd

import (
	"fmt"
	"sort"
)

// InvariantError 表示自检发现的不变量违例。
type InvariantError struct {
	Key     string
	Message string
}

func (e *InvariantError) Error() string {
	return fmt.Sprintf("scd: invariant violation for key=%q: %s", e.Key, e.Message)
}

// Commit 原子提交一批变更事件。
// 任一事件非法则整批拒绝并返回全部可区分原因，不改变任何变更点与历史。
// 同批内同一键同一生效时间的多个事件，只保留该批中后出现者。
func (s *Store) Commit(events []Event) []Rejection {
	if rejections := validate(events); rejections != nil {
		s.debugf("commit rejected at validation: %d reason(s)", len(rejections))
		return rejections
	}

	// 按 (键, 生效时间) 折叠本批；后到者覆盖先到者，规则与“同点只保留后到者”一致。
	type pos struct {
		key string
		at  int64
	}
	latest := make(map[pos]int, len(events))
	for i, e := range events {
		latest[pos{e.Key, e.EffectiveTime}] = i
	}
	byKey := make(map[string][]Event, len(latest))
	for p, i := range latest {
		byKey[p.key] = append(byKey[p.key], events[i])
	}
	for _, es := range byKey {
		sort.Slice(es, func(i, j int) bool { return es[i].EffectiveTime < es[j].EffectiveTime })
	}

	keyNames := make([]string, 0, len(byKey))
	for k := range byKey {
		keyNames = append(keyNames, k)
	}
	sort.Strings(keyNames)

	s.mu.Lock()
	defer s.mu.Unlock()

	// 限额检查在持锁状态下基于当前点集合进行；超限则直接返回、不留痕。
	overLimit := map[string]int{}
	for _, k := range keyNames {
		ks := s.keys[k]
		current := 0
		if ks != nil {
			current = len(ks.points)
		}
		newCount := current
		for _, e := range byKey[k] {
			if ks == nil || findPoint(ks.points, e.EffectiveTime) < 0 {
				newCount++
			}
		}
		if newCount > s.maxPointsPerKey {
			overLimit[k] = newCount
		}
	}
	if len(overLimit) > 0 {
		var rejections []Rejection
		// 以原批次顺序输出每个超限键的首个原因，保证稳定可复现。
		seen := map[string]bool{}
		for i, e := range events {
			if n, ok := overLimit[e.Key]; ok && !seen[e.Key] {
				seen[e.Key] = true
				rejections = append(rejections, newRejection(i, e.Key, ReasonTooManyChangePoints,
					"key would have %d change points after commit, limit is %d", n, s.maxPointsPerKey))
			}
		}
		s.debugf("commit rejected at limit check: %d key(s) over limit", len(rejections))
		return rejections
	}

	// 全部校验通过后才应用；应用阶段不会失败，因此提交具备原子性。
	for _, k := range keyNames {
		ks := s.keys[k]
		if ks == nil {
			ks = &keyState{}
			s.keys[k] = ks
		}
		for _, e := range byKey[k] {
			s.applyOne(k, ks, e)
		}
	}
	s.debugf("commit accepted: %d event(s) across %d key(s)", len(events), len(keyNames))
	return nil
}

func validate(events []Event) []Rejection {
	if len(events) == 0 {
		return []Rejection{newRejection(-1, "", ReasonEmptyBatch,
			"commit batch must contain at least one event")}
	}
	var rejections []Rejection
	for i, e := range events {
		if e.Key == "" {
			rejections = append(rejections, newRejection(i, e.Key, ReasonEmptyKey,
				"event key must not be empty"))
		}
		if !e.Deleted && e.Value == "" {
			rejections = append(rejections, newRejection(i, e.Key, ReasonEmptyValue,
				"update event value must not be empty (use a delete event to remove)"))
		}
		if e.EffectiveTime < MinEffectiveTime || e.EffectiveTime > MaxEffectiveTime {
			rejections = append(rejections, newRejection(i, e.Key, ReasonEffectiveTimeOutOfRange,
				"effective time %d outside [%d, %d]", e.EffectiveTime, MinEffectiveTime, MaxEffectiveTime))
		}
	}
	return rejections
}

// applyOne 把单个（已按时间折叠的）事件增量并入某键状态。
func (s *Store) applyOne(key string, ks *keyState, e Event) {
	p := point{at: e.EffectiveTime, value: e.Value, deleted: e.Deleted}
	if idx := findPoint(ks.points, e.EffectiveTime); idx >= 0 {
		// 恰等于已有变更点：以后到者替换该点（值或删除标记）。
		s.debugf("key=%s t=%d: replace existing change point (deleted=%v value=%q)",
			key, e.EffectiveTime, e.Deleted, e.Value)
		ks.points[idx] = p
		s.rebuildIntervals(key, ks)
		return
	}

	ins := sort.Search(len(ks.points), func(i int) bool { return ks.points[i].at > e.EffectiveTime })
	ks.points = append(ks.points, point{})
	copy(ks.points[ins+1:], ks.points[ins:])
	ks.points[ins] = p
	if ins == 0 {
		s.debugf("key=%s t=%d: insert new earliest change point (opens a new row)",
			key, e.EffectiveTime)
	} else {
		s.debugf("key=%s t=%d: split the row containing t (insert at rank %d)",
			key, e.EffectiveTime, ins)
	}
	s.rebuildIntervals(key, ks)
}

// rebuildIntervals 依据变更点集合重新派生区间。
// 历史完全由变更点集合决定，重建结果与批量重算一致；单事件只改动一个位置，
// 该位置之外的区间首尾均不变化，实现增量维护。
func (s *Store) rebuildIntervals(key string, ks *keyState) {
	ks.intervals = intervalsFromPoints(key, ks.points)
}

func findPoint(points []point, at int64) int {
	idx := sort.Search(len(points), func(i int) bool { return points[i].at >= at })
	if idx < len(points) && points[idx].at == at {
		return idx
	}
	return -1
}

func intervalsFromPoints(key string, points []point) []Interval {
	out := make([]Interval, 0, len(points))
	for i, p := range points {
		if p.deleted {
			continue
		}
		end := OpenEnd
		if i+1 < len(points) {
			end = points[i+1].at
		}
		out = append(out, Interval{Key: key, Start: p.at, End: end, Value: p.value})
	}
	return out
}

// History 返回某键当前历史区间的快照（按 Start 升序）。
func (s *Store) History(key string) []Interval {
	s.mu.Lock()
	ks := s.keys[key]
	var out []Interval
	if ks != nil {
		out = append([]Interval(nil), ks.intervals...)
	}
	s.mu.Unlock()
	s.debugf("history key=%s: %d interval(s)", key, len(out))
	return out
}

// ValueAt 返回某键在时间 t 的生效值；t 恰等于变更点时间时命中该点所在行。
// 未命中任何区间（无数据或处于删除空隙）时返回 ("", false)。
func (s *Store) ValueAt(key string, t int64) (string, bool) {
	s.mu.Lock()
	ks := s.keys[key]
	var iv []Interval
	if ks != nil {
		iv = ks.intervals
	}
	s.mu.Unlock()

	idx := sort.Search(len(iv), func(i int) bool { return iv[i].Start > t }) - 1
	if idx < 0 {
		s.debugf("value-at key=%s t=%d: no row (before first change point)", key, t)
		return "", false
	}
	row := iv[idx]
	if t < row.Start || t >= row.End {
		s.debugf("value-at key=%s t=%d: no row (delete gap before next row)", key, t)
		return "", false
	}
	s.debugf("value-at key=%s t=%d: hit [%d,%d) value=%q", key, t, row.Start, row.End, row.Value)
	return row.Value, true
}

// CheckInvariants 对全部键执行自检，返回首个发现的不变量违例描述。
func (s *Store) CheckInvariants() error {
	s.mu.Lock()
	names := make([]string, 0, len(s.keys))
	for k := range s.keys {
		names = append(names, k)
	}
	sort.Strings(names)
	// 锁内深拷贝快照，锁外校验，避免与并发提交相互阻塞或竞争。
	snaps := make([]*keyState, 0, len(names))
	for _, k := range names {
		ks := s.keys[k]
		snaps = append(snaps, &keyState{
			points:    append([]point(nil), ks.points...),
			intervals: append([]Interval(nil), ks.intervals...),
		})
	}
	s.mu.Unlock()

	for i, ks := range snaps {
		if err := checkKey(names[i], ks); err != nil {
			return err
		}
	}
	return nil
}

func checkKey(key string, ks *keyState) error {
	for i, p := range ks.points {
		if p.at < MinEffectiveTime || p.at > MaxEffectiveTime {
			return &InvariantError{Key: key, Message: "change point time out of range"}
		}
		if i > 0 && ks.points[i-1].at >= p.at {
			return &InvariantError{Key: key, Message: "change points not strictly ascending"}
		}
	}

	want := intervalsFromPoints(key, ks.points)
	if len(want) != len(ks.intervals) {
		return &InvariantError{Key: key, Message: "interval count does not match change points"}
	}
	for i := range want {
		got := ks.intervals[i]
		if want[i] != got {
			return &InvariantError{Key: key, Message: "interval drift from change points"}
		}
		if got.Start >= got.End {
			return &InvariantError{Key: key, Message: "non-positive-length interval"}
		}
		if i > 0 {
			prev := ks.intervals[i-1]
			if got.Start <= prev.Start {
				return &InvariantError{Key: key, Message: "intervals not ascending"}
			}
			if prev.End > got.Start {
				return &InvariantError{Key: key, Message: "adjacent intervals overlap"}
			}
		}
	}
	return nil
}

// Recompute 从单键的一批事件重算历史区间，用于与增量结果做一致性比对。
// 折叠规则与提交一致：同一键同一生效时间只保留后到者；历史仅由变更点集合决定。
func Recompute(key string, events []Event) []Interval {
	byTime := make(map[int64]Event, len(events))
	times := make([]int64, 0, len(events))
	for _, e := range events {
		if _, ok := byTime[e.EffectiveTime]; !ok {
			times = append(times, e.EffectiveTime)
		}
		byTime[e.EffectiveTime] = e
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })

	points := make([]point, 0, len(times))
	for _, t := range times {
		e := byTime[t]
		points = append(points, point{at: t, value: e.Value, deleted: e.Deleted})
	}
	return intervalsFromPoints(key, points)
}
