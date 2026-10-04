// Package seat 维护房间席位表：按队组织的固定席位及其空位选取原语。
package seat

// Status 为单个席位的占用状态。
type Status uint8

const (
	// Empty 空席。
	Empty Status = iota
	// Reserved 已被某玩家预留，尚未确认入座。
	Reserved
	// Seated 玩家已确认在座。
	Seated
)

// Cell 描述一个席位的当前内容。
type Cell struct {
	Team   int
	Index  int
	Status Status
	Player string
}

// Table 是 T 队、每队 S 席的固定席位表。
type Table struct {
	teams int
	seats int
	cells [][]Cell
}

// NewTable 创建 teams 队、每队 seatsPerTeam 席的空席位表。
func NewTable(teams, seatsPerTeam int) *Table {
	t := &Table{teams: teams, seats: seatsPerTeam}
	t.cells = make([][]Cell, teams)
	for tm := 0; tm < teams; tm++ {
		t.cells[tm] = make([]Cell, seatsPerTeam)
		for idx := 0; idx < seatsPerTeam; idx++ {
			t.cells[tm][idx] = Cell{Team: tm, Index: idx, Status: Empty}
		}
	}
	return t
}

// Teams 返回队数。
func (t *Table) Teams() int { return t.teams }

// SeatsPerTeam 返回每队席位数。
func (t *Table) SeatsPerTeam() int { return t.seats }

// Snapshot 按队号、席号升序返回全部席位的副本。
func (t *Table) Snapshot() []Cell {
	out := make([]Cell, 0, t.teams*t.seats)
	for tm := 0; tm < t.teams; tm++ {
		for idx := 0; idx < t.seats; idx++ {
			out = append(out, t.cells[tm][idx])
		}
	}
	return out
}

// FreeIndices 返回指定队内编号从小到大的空席编号。
func (t *Table) FreeIndices(team int) []int {
	if team < 0 || team >= t.teams {
		return nil
	}
	var out []int
	for idx := 0; idx < t.seats; idx++ {
		if t.cells[team][idx].Status == Empty {
			out = append(out, idx)
		}
	}
	return out
}

// Place 在指定席位放置玩家并赋状态；席位非空或坐标非法时返回 false。
func (t *Table) Place(team, idx int, player string, st Status) bool {
	if team < 0 || team >= t.teams || idx < 0 || idx >= t.seats {
		return false
	}
	if t.cells[team][idx].Status != Empty {
		return false
	}
	t.cells[team][idx].Status = st
	t.cells[team][idx].Player = player
	return true
}

// SetStatus 修改指定席位的状态。
func (t *Table) SetStatus(team, idx int, st Status) bool {
	if team < 0 || team >= t.teams || idx < 0 || idx >= t.seats {
		return false
	}
	if t.cells[team][idx].Status == Empty {
		return false
	}
	t.cells[team][idx].Status = st
	return true
}

// Release 清空指定席位。
func (t *Table) Release(team, idx int) bool {
	if team < 0 || team >= t.teams || idx < 0 || idx >= t.seats {
		return false
	}
	if t.cells[team][idx].Status == Empty {
		return false
	}
	t.cells[team][idx] = Cell{Team: team, Index: idx, Status: Empty}
	return true
}
