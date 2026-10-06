package scheduler

import (
	"sort"
)

// venue.go 负责考场容量与考场时段占用账目。
//
// 一门考试可拆到多个考场。分配规则（确定性）：
//   - 考场按标识升序；
//   - 每个考场 CapacityUsed 不得超过自身容量；
//   - 各考场 CapacityUsed 之和不得小于参考人数（全体学生数）；
//   - 需要延长时间的学生优先就座：按考场标识顺序尽量放入，
//     容纳了延长学生的考场标记 Extended=true，并在延长部分时段同样被占用。

// roomInfo 保存归一化的考场静态信息。
type roomInfo struct {
	id       string
	capacity int
}

// venueCell 记录一个考场时段的占用者。
type venueCell struct {
	examID string
}

// venueBook 维护每个考场在每个时段被哪门考试占用。
type venueBook struct {
	rooms     map[string]roomInfo
	roomOrder []string
	busy      map[string]map[int]string // roomID -> slot -> examID
}

func newVenueBook(rooms []Room) *venueBook {
	b := &venueBook{
		rooms: map[string]roomInfo{},
		busy:  map[string]map[int]string{},
	}
	for _, r := range rooms {
		if _, exists := b.rooms[r.ID]; !exists {
			b.roomOrder = append(b.roomOrder, r.ID)
		}
		b.rooms[r.ID] = roomInfo{id: r.ID, capacity: r.Capacity}
	}
	sort.Strings(b.roomOrder)
	return b
}

func (b *venueBook) roomExists(id string) bool {
	_, ok := b.rooms[id]
	return ok
}

// seatStudents 在考场组合通过校验后，确定性地填充学生：
// 学生按考场标识顺序、每考场从 0 号座位起依次入座；
// 需要延长时间的学生被视为最先入座的 extendedCount 人。
// 结果给出每个考场实际入座人数 CapacityUsed（0..容量），
// 以及该考场是否容纳延长学生（Extended）。
func (b *venueBook) seatStudents(roomIDs []string, studentCount, extendedCount int) []RoomAssign {
	ids := append([]string(nil), roomIDs...)
	sort.Strings(ids)
	out := make([]RoomAssign, 0, len(ids))
	seated := 0
	for _, id := range ids {
		cap := b.rooms[id].capacity
		use := cap
		if seated+use > studentCount {
			use = studentCount - seated
		}
		if use < 0 {
			use = 0
		}
		ext := seated < extendedCount && use > 0
		out = append(out, RoomAssign{RoomID: id, CapacityUsed: use, Extended: ext})
		seated += use
	}
	return out
}

// commit 将一组考场占用写入账目。
func (b *venueBook) commit(examID string, assigns []RoomAssign, start, baseEnd, extEnd int) {
	for _, a := range assigns {
		cells, ok := b.busy[a.RoomID]
		if !ok {
			cells = map[int]string{}
			b.busy[a.RoomID] = cells
		}
		last := baseEnd
		if a.Extended {
			last = extEnd
		}
		for slot := start; slot <= last; slot++ {
			cells[slot] = examID
		}
	}
}

// release 释放一门考试的全部考场占用。
func (b *venueBook) release(examID string, assigns []RoomAssign, start, baseEnd, extEnd int) {
	for _, a := range assigns {
		cells := b.busy[a.RoomID]
		last := baseEnd
		if a.Extended {
			last = extEnd
		}
		for slot := start; slot <= last; slot++ {
			if cells[slot] == examID {
				delete(cells, slot)
			}
		}
	}
}

// slotsOfRoom 返回某考场被占用的时段列表（升序）。
func (b *venueBook) slotsOfRoom(roomID string) []Occupy {
	cells := b.busy[roomID]
	out := make([]Occupy, 0, len(cells))
	for slot, examID := range cells {
		out = append(out, Occupy{Slot: slot, ExamID: examID, RoomID: roomID})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slot < out[j].Slot })
	return out
}
