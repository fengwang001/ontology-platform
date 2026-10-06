package inventory

import (
	"encoding/json"
	"fmt"
	"sort"
)

// SegmentSnapshot 航段配置快照。占用计数不导出，恢复时由条目重放推导。
type SegmentSnapshot struct {
	ID       string          `json:"id"`
	Seats    int             `json:"seats"`
	Overbook int             `json:"overbook"`
	AU       [numClasses]int `json:"au"`
}

// EntrySnapshot 条目快照。未过期预占保留原到期时刻。
type EntrySnapshot struct {
	ID     string     `json:"id"`
	Legs   []Leg      `json:"legs"`
	Party  int        `json:"party"`
	State  EntryState `json:"state"`
	Expiry int64      `json:"expiry"`
}

// Snapshot 系统全部状态的可序列化快照。
type Snapshot struct {
	HoldDuration int64             `json:"holdDuration"`
	Clock        int64             `json:"clock"`
	Seq          int               `json:"seq"`
	Segments     []SegmentSnapshot `json:"segments"`
	Entries      []EntrySnapshot   `json:"entries"`
}

// Export 导出全部状态。条目按标识排序，结果确定。
func (s *System) Export() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := Snapshot{
		HoldDuration: s.holdDuration,
		Clock:        s.clock,
		Seq:          s.seq,
		Segments:     make([]SegmentSnapshot, 0, len(s.segments)),
		Entries:      make([]EntrySnapshot, 0, len(s.entries)),
	}
	for _, id := range s.segmentOrder {
		seg := s.segments[id]
		snap.Segments = append(snap.Segments, SegmentSnapshot{ID: id, Seats: seg.seats, Overbook: seg.overbook, AU: seg.au})
	}
	ids := make([]string, 0, len(s.entries))
	for id := range s.entries {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		e := s.entries[id]
		snap.Entries = append(snap.Entries, EntrySnapshot{
			ID: e.ID, Legs: append([]Leg(nil), e.Legs...), Party: e.Party, State: e.State, Expiry: e.Expiry,
		})
	}
	return snap
}

// ExportJSON 导出为 JSON。
func (s *System) ExportJSON() ([]byte, error) {
	return json.Marshal(s.Export())
}

// Restore 从快照在新实例中恢复。占用计数由条目重放推导：
// 已出票计入 confirmed；未过期预占（到期时刻 > 快照时钟）计入 held 并
// 重新进入到期队列；已过期预占不计入任何占用，但条目保留以区分
// “预占已过期”与“条目不存在”。
func Restore(snap Snapshot) (*System, error) {
	sys, err := NewSystem(snap.HoldDuration)
	if err != nil {
		return nil, err
	}
	for _, ss := range snap.Segments {
		if err := sys.addSegmentLocked(ss.ID, ss.Seats, ss.Overbook, ss.AU); err != nil {
			return nil, fmt.Errorf("restore segment %q: %w", ss.ID, err)
		}
	}
	for _, es := range snap.Entries {
		if es.ID == "" {
			return nil, invalidParamf("restore: empty entry id")
		}
		if _, dup := sys.entries[es.ID]; dup {
			return nil, invalidParamf("restore: duplicate entry %q", es.ID)
		}
		if err := validateLegs(es.Legs, es.Party); err != nil {
			return nil, fmt.Errorf("restore entry %q: %w", es.ID, err)
		}
		if es.State < StateHeld || es.State > StateCancelled {
			return nil, invalidParamf("restore entry %q: invalid state %d", es.ID, int(es.State))
		}
		for _, leg := range es.Legs {
			if _, ok := sys.segments[leg.Segment]; !ok {
				return nil, invalidParamf("restore entry %q: unknown segment %q", es.ID, leg.Segment)
			}
		}
		e := &Entry{ID: es.ID, Legs: append([]Leg(nil), es.Legs...), Party: es.Party, State: es.State, Expiry: es.Expiry}
		sys.entries[e.ID] = e
		switch e.State {
		case StateTicketed:
			for _, leg := range e.Legs {
				sys.segments[leg.Segment].confirmed[int(leg.Class)] += e.Party
			}
		case StateHeld:
			if e.Expiry > snap.Clock {
				for _, leg := range e.Legs {
					sys.segments[leg.Segment].held[int(leg.Class)] += e.Party
				}
				sys.expiryQ = append(sys.expiryQ, expiryRef{expiry: e.Expiry, entryID: e.ID})
			}
		}
		var n int
		if _, scanErr := fmt.Sscanf(e.ID, "H%d", &n); scanErr == nil && n > sys.seq {
			sys.seq = n
		}
	}
	sort.SliceStable(sys.expiryQ, func(i, j int) bool { return sys.expiryQ[i].expiry < sys.expiryQ[j].expiry })
	if snap.Seq > sys.seq {
		sys.seq = snap.Seq
	}
	sys.clock = snap.Clock
	sys.sweptTo = snap.Clock
	return sys, nil
}

// RestoreJSON 从 JSON 快照恢复。
func RestoreJSON(data []byte) (*System, error) {
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("restore: %w", err)
	}
	return Restore(snap)
}
