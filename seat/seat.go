// Package seat 维护单个房间的席位表与空位选取。
//
// 席位表不感知组队、房间与全局时钟，只提供"到期视角"：
// 状态为预留且到期时刻 <= now 的席位，在一切计数与枚举中等同于空位。
package seat

// State 表示席位的占用状态。
type State int

const (
	Empty    State = iota // 空位
	Reserved              // 预留（可能已到期，由 now 视角判定）
	Seated                // 已入座（不再到期）
)

func (s State) String() string {
	switch s {
	case Empty:
		return "空"
	case Reserved:
		return "预留"
	case Seated:
		return "入座"
	}
	return "未知"
}

// Seat 是单个席位的记录。Expiry 仅在 State 为 Reserved 时有意义。
type Seat struct {
	Occupant string
	State    State
	Expiry   int64
}

// alive 报告席位在 now 视角下是否被占用：
// 入座恒占用；预留仅在未到期（Expiry > now）时占用。
func (s Seat) alive(now int64) bool {
	switch s.State {
	case Seated:
		return true
	case Reserved:
		return s.Expiry > now
	}
	return false
}

// Table 是 T 队 × S 位的席位表。
type Table struct {
	teams int
	size  int
	cells [][]Seat
}

// NewTable 创建 teams 队、每队 size 席的空表。
func NewTable(teams, size int) *Table {
	cells := make([][]Seat, teams)
	for i := range cells {
		cells[i] = make([]Seat, size)
	}
	return &Table{teams: teams, size: size, cells: cells}
}

// Teams 返回队伍数。
func (t *Table) Teams() int { return t.teams }

// Size 返回每队席位数。
func (t *Table) Size() int { return t.size }

// Cell 返回席位原始记录（不做到期视角处理）。
func (t *Table) Cell(team, idx int) Seat { return t.cells[team][idx] }

// Occupancies 返回各队在 now 视角下的占用数（入座 + 有效预留）。
func (t *Table) Occupancies(now int64) []int {
	occ := make([]int, t.teams)
	for team := 0; team < t.teams; team++ {
		for idx := 0; idx < t.size; idx++ {
			if t.cells[team][idx].alive(now) {
				occ[team]++
			}
		}
	}
	return occ
}

// PickTeam 选取 now 视角下占用数最小的队，并列取队号最小者。
func (t *Table) PickTeam(now int64) int {
	occ := t.Occupancies(now)
	best := 0
	for team := 1; team < t.teams; team++ {
		if occ[team] < occ[best] {
			best = team
		}
	}
	return best
}

// FreeSeats 返回该队在 now 视角下的空位编号，从小到大。
func (t *Table) FreeSeats(team int, now int64) []int {
	free := make([]int, 0, t.size)
	for idx := 0; idx < t.size; idx++ {
		if !t.cells[team][idx].alive(now) {
			free = append(free, idx)
		}
	}
	return free
}

// Assign 将席位指给玩家。调用方保证席位在 now 视角下为空。
func (t *Table) Assign(team, idx int, player string, st State, expiry int64) {
	t.cells[team][idx] = Seat{Occupant: player, State: st, Expiry: expiry}
}

// Clear 释放席位，恢复为空位。
func (t *Table) Clear(team, idx int) {
	t.cells[team][idx] = Seat{}
}
