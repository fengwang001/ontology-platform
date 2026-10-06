package fencing

// Cell 是商家基础范围中的一个网格单元，Ring 为与商家的环距（层级距离）。
type Cell struct {
	ID   string
	Ring int
}

// merchant 记录一个商家的基础范围与商家自设收缩等级。
// cells 用哈希表存储，单次可达判定为 O(1)，与单元总数无关。
type merchant struct {
	id         string
	region     string
	cells      map[string]int
	baseRadius int
	level      int
}

func newMerchant(id, region string, cells []Cell) (*merchant, error) {
	if len(cells) == 0 {
		return nil, newErr(ErrKindInvalidParam, "merchant %q: base range must contain at least one cell", id)
	}
	m := &merchant{id: id, region: region, cells: make(map[string]int, len(cells))}
	for _, c := range cells {
		if c.ID == "" {
			return nil, newErr(ErrKindInvalidParam, "merchant %q: cell id must be non-empty", id)
		}
		if c.Ring < 0 {
			return nil, newErr(ErrKindInvalidParam, "merchant %q: cell %q has negative ring %d", id, c.ID, c.Ring)
		}
		if _, dup := m.cells[c.ID]; dup {
			return nil, newErr(ErrKindInvalidParam, "merchant %q: duplicate cell %q", id, c.ID)
		}
		m.cells[c.ID] = c.Ring
		if c.Ring > m.baseRadius {
			m.baseRadius = c.Ring
		}
	}
	return m, nil
}

// effectiveRadius 返回有效半径：基础半径减有效等级，不为负。
func (m *merchant) effectiveRadius(level int) int {
	r := m.baseRadius - level
	if r < 0 {
		return 0
	}
	return r
}

// Reachability 是只读可达查询的结果。
type Reachability int

const (
	ReachableNow Reachability = iota
	UnreachablePermanent
	UnreachableTemporary
)

func (r Reachability) String() string {
	switch r {
	case ReachableNow:
		return "reachable"
	case UnreachablePermanent:
		return "permanent_out_of_range"
	case UnreachableTemporary:
		return "temporarily_unreachable"
	}
	return "unknown"
}

// reachability 判定单元在给定有效等级下的可达性。
// 环距不大于有效半径即可达；环距恰等于有效半径仍可达。
func (m *merchant) reachability(cellID string, level int) Reachability {
	ring, ok := m.cells[cellID]
	if !ok {
		return UnreachablePermanent
	}
	if ring > m.effectiveRadius(level) {
		return UnreachableTemporary
	}
	return ReachableNow
}
