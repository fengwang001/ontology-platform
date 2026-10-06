package inventory

import (
	"encoding/json"
	"sort"
)

// Snapshot 是系统全部状态的可序列化快照。字段均导出，可直接 JSON 编解码。
// 快照不依赖任何内部指针，导出时刻不影响内容：
// 热表（未归档预占）由处于预占状态的条目在恢复时重建。
type Snapshot struct {
	HoldDuration uint64
	LastTime     uint64
	NextEntryID  uint64
	Segments     []SegmentSnap
	Entries      []EntrySnap
}

// SegmentSnap 单个航段的快照。Archive 为已归档（对一切未来操作均已过期）
// 的预占占用，按到期时刻不减排列。
type SegmentSnap struct {
	ID        string
	Seats     int
	Overbook  int
	Auth      [3]int
	Confirmed [3]int
	Archive   [3][]ExpiryPax
	SweptTo   uint64
}

// ExpiryPax 某一到期时刻的占用人数。
type ExpiryPax struct {
	Expiry uint64
	Pax    int
}

// EntrySnap 单条条目的快照，保留原到期时刻与状态。
type EntrySnap struct {
	ID     uint64
	State  State
	Legs   []Leg
	Pax    int
	Expiry uint64
}

// Export 导出全部状态。只读操作，不参与时钟约束。
// 导出内容按标识排序，同一状态导出结果逐字节确定。
func (e *Engine) Export() Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	snap := Snapshot{
		HoldDuration: e.holdDur,
		LastTime:     e.lastTime,
		NextEntryID:  e.nextID,
	}
	segIDs := make([]string, 0, len(e.segments))
	for id := range e.segments {
		segIDs = append(segIDs, id)
	}
	sort.Strings(segIDs)
	for _, id := range segIDs {
		s := e.segments[id]
		ss := SegmentSnap{
			ID:       id,
			Seats:    s.seats,
			Overbook: s.overbook,
			Auth:     s.auth,
			SweptTo:  s.sweptTo,
		}
		for k := 0; k < 3; k++ {
			ss.Confirmed[k] = s.occ[k].confirmed
			if n := len(s.occ[k].arch); n > 0 {
				arch := make([]ExpiryPax, n)
				for i, a := range s.occ[k].arch {
					arch[i] = ExpiryPax{Expiry: a.Expiry, Pax: a.Pax}
				}
				ss.Archive[k] = arch
			}
		}
		snap.Segments = append(snap.Segments, ss)
	}
	entryIDs := make([]uint64, 0, len(e.entries))
	for id := range e.entries {
		entryIDs = append(entryIDs, id)
	}
	sort.Slice(entryIDs, func(i, j int) bool { return entryIDs[i] < entryIDs[j] })
	for _, id := range entryIDs {
		ent := e.entries[id]
		snap.Entries = append(snap.Entries, EntrySnap{
			ID:     ent.id,
			State:  ent.state,
			Legs:   append([]Leg(nil), ent.legs...),
			Pax:    ent.pax,
			Expiry: ent.expiry,
		})
	}
	return snap
}

// ExportJSON 导出并编码为 JSON。
func (e *Engine) ExportJSON() ([]byte, error) {
	return json.Marshal(e.Export())
}

// Import 从快照恢复一个新实例。恢复后未过期预占保留原到期时刻，
// 条目号序列、时钟、授权量与占用都与导出时刻一致。
func Import(snap Snapshot) (*Engine, error) {
	e := &Engine{
		holdDur:  snap.HoldDuration,
		lastTime: snap.LastTime,
		nextID:   snap.NextEntryID,
		segments: make(map[string]*segment, len(snap.Segments)),
		entries:  make(map[uint64]*entry, len(snap.Entries)),
	}
	if e.nextID == 0 {
		e.nextID = 1
	}
	for _, ss := range snap.Segments {
		if err := checkAuth(ss.Seats, ss.Overbook, ss.Auth); err != nil {
			return nil, err
		}
		if ss.ID == "" || ss.Seats <= 0 || ss.Overbook < 0 {
			return nil, paramErr("快照中航段参数非法: " + ss.ID)
		}
		if _, dup := e.segments[ss.ID]; dup {
			return nil, paramErr("快照中航段重复: " + ss.ID)
		}
		s := &segment{id: ss.ID, seats: ss.Seats, overbook: ss.Overbook, auth: ss.Auth, sweptTo: ss.SweptTo}
		for k := 0; k < 3; k++ {
			s.occ[k].confirmed = ss.Confirmed[k]
			prevExpiry := uint64(0)
			for i, a := range ss.Archive[k] {
				if a.Pax <= 0 {
					return nil, paramErr("快照中归档占用人数非法")
				}
				if i > 0 && a.Expiry < prevExpiry {
					return nil, paramErr("快照中归档未按到期时刻排序")
				}
				prevExpiry = a.Expiry
				s.occ[k].arch = append(s.occ[k].arch, expiryPax{Expiry: a.Expiry, Pax: a.Pax})
				s.occ[k].archTotal += a.Pax
			}
			for i, a := range s.occ[k].arch {
				prev := 0
				if i > 0 {
					prev = s.occ[k].archPrefix[i-1]
				}
				s.occ[k].archPrefix = append(s.occ[k].archPrefix, prev+a.Pax)
			}
		}
		e.segments[ss.ID] = s
	}
	maxID := uint64(0)
	for _, es := range snap.Entries {
		if es.ID == 0 || es.ID >= e.nextID {
			return nil, paramErr("快照中条目号非法")
		}
		if _, dup := e.entries[es.ID]; dup {
			return nil, paramErr("快照中条目号重复")
		}
		if err := checkLegs(es.Legs, es.Pax); err != nil {
			return nil, err
		}
		if es.State < StateHold || es.State > StateCancelledTicket {
			return nil, paramErr("快照中条目状态非法")
		}
		ent := &entry{
			id:     es.ID,
			legs:   append([]Leg(nil), es.Legs...),
			pax:    es.Pax,
			expiry: es.Expiry,
			state:  es.State,
			hotIdx: make([]int, len(es.Legs)),
		}
		for _, l := range es.Legs {
			if _, ok := e.segments[l.Segment]; !ok {
				return nil, paramErr("快照中条目引用了不存在的航段: " + l.Segment)
			}
		}
		e.entries[es.ID] = ent
		if es.ID > maxID {
			maxID = es.ID
		}
	}
	if maxID >= e.nextID {
		e.nextID = maxID + 1
	}
	// 重建热表：仍处于预占状态且未被归档（到期时刻晚于归档水位）的条目，
	// 按条目号（即创建顺序，到期时刻不减）追加，恢复 hotIdx。
	for id := uint64(1); id < e.nextID; id++ {
		ent, ok := e.entries[id]
		if !ok || ent.state != StateHold {
			continue
		}
		for i, l := range ent.legs {
			s := e.segments[l.Segment]
			if ent.expiry > s.sweptTo {
				ent.hotIdx[i] = s.occ[l.Class].addHold(ent.expiry, ent.pax)
			} else {
				ent.hotIdx[i] = -1 // 已归档，占用由归档区承载
			}
		}
	}
	return e, nil
}

// ImportJSON 从 JSON 快照恢复。
func ImportJSON(data []byte) (*Engine, error) {
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, paramErr("快照 JSON 解码失败: " + err.Error())
	}
	return Import(snap)
}
