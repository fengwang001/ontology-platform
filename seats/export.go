package seats

import "sort"

// SegmentSnap 是航段的导出快照。占用计数与过期队列不导出，恢复时由条目
// 列表确定性重建。
type SegmentSnap struct {
	ID       string          `json:"id"`
	Physical int             `json:"physical"`
	Overbook int             `json:"overbook"`
	Auth     [NumClasses]int `json:"auth"`
}

// EntrySnap 是条目的导出快照。未过期预占保留原到期时刻。
type EntrySnap struct {
	ID     string `json:"id"`
	Status Status `json:"status"`
	Legs   []Leg  `json:"legs"`
	Pax    int    `json:"pax"`
	Expiry int64  `json:"expiry"`
}

// Snapshot 是系统的全量状态快照，可 JSON 序列化。字段顺序确定性生成
// （航段与条目均按标识排序），相同状态导出结果逐字节一致。
type Snapshot struct {
	HoldDur  int64         `json:"hold_dur"`
	Now      int64         `json:"now"`
	NextID   int           `json:"next_id"`
	Segments []SegmentSnap `json:"segments"`
	Entries  []EntrySnap   `json:"entries"`
}

// Export 导出系统全部状态。
func (s *System) Export() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := Snapshot{HoldDur: s.holdDur, Now: s.now, NextID: s.nextID}
	for _, seg := range s.segs {
		snap.Segments = append(snap.Segments, SegmentSnap{
			ID: seg.ID, Physical: seg.Physical, Overbook: seg.Overbook, Auth: seg.Auth,
		})
	}
	sort.Slice(snap.Segments, func(i, j int) bool { return snap.Segments[i].ID < snap.Segments[j].ID })
	for _, e := range s.entries {
		snap.Entries = append(snap.Entries, EntrySnap{
			ID: e.ID, Status: e.Status, Legs: append([]Leg(nil), e.Legs...), Pax: e.Pax, Expiry: e.Expiry,
		})
	}
	sort.Slice(snap.Entries, func(i, j int) bool { return snap.Entries[i].ID < snap.Entries[j].ID })
	return snap
}

// Restore 从快照在新实例中恢复系统。恢复后未过期预占保留原到期时刻，条
// 目标识计数器延续，恢复前后对同一组查询的结果逐项相等。
func Restore(snap Snapshot) (*System, error) {
	if snap.HoldDur < 0 {
		return nil, errInvalid("hold duration must be non-negative")
	}
	if snap.Now < 0 {
		return nil, errInvalid("time must be non-negative")
	}
	s := &System{
		holdDur: snap.HoldDur,
		now:     snap.Now,
		nextID:  snap.NextID,
		segs:    make(map[string]*Segment, len(snap.Segments)),
		entries: make(map[string]*Entry, len(snap.Entries)),
	}
	for _, ss := range snap.Segments {
		if ss.ID == "" {
			return nil, errInvalid("segment id must be non-empty")
		}
		if _, dup := s.segs[ss.ID]; dup {
			return nil, errInvalid("duplicate segment " + ss.ID + " in snapshot")
		}
		if ss.Physical <= 0 || ss.Overbook < 0 {
			return nil, errInvalid("invalid physical seats or overbook limit in snapshot")
		}
		if err := checkAuth(ss.Auth, ss.Physical, ss.Overbook); err != nil {
			return nil, err
		}
		s.segs[ss.ID] = &Segment{ID: ss.ID, Physical: ss.Physical, Overbook: ss.Overbook, Auth: ss.Auth}
	}
	// 条目按标识序（即创建序）重建；因时钟单调，预占到期时刻随创建序单调
	// 不减，直接按序入队即满足 FIFO 队列的有序性。
	entries := append([]EntrySnap(nil), snap.Entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	for _, es := range entries {
		if _, dup := s.entries[es.ID]; dup {
			return nil, errInvalid("duplicate entry " + es.ID + " in snapshot")
		}
		if err := checkLegs(es.Legs, es.Pax); err != nil {
			return nil, err
		}
		e := &Entry{ID: es.ID, Status: es.Status, Legs: append([]Leg(nil), es.Legs...), Pax: es.Pax, Expiry: es.Expiry}
		s.entries[es.ID] = e
		if es.Status == StatusCancelled {
			continue
		}
		for _, l := range es.Legs {
			seg, ok := s.segs[l.Segment]
			if !ok {
				return nil, errNotFound("segment " + l.Segment)
			}
			switch es.Status {
			case StatusHold:
				seg.held[l.Class] += es.Pax
				seg.queue = append(seg.queue, qentry{expiry: es.Expiry, class: l.Class, count: es.Pax, entryID: es.ID})
			case StatusTicketed:
				seg.confirmed[l.Class] += es.Pax
			default:
				return nil, errInvalid("unknown entry status in snapshot")
			}
		}
	}
	return s, nil
}
