package railway

// 选座与空闲判定的耗时为 O(座位数 * 站数)，与已售车票数无关：
// 每个座位只保留长度为“相邻区段数”的占用位图，任意一张票的占用/释放
// 都是其覆盖边的置位/清零；相接信息直接由位图读出。

// seatMap 管理一趟列车全部座位的区段占用，以及每个相邻站间区段的无座名额。
type seatMap struct {
	stationCount int
	seats        []Seat
	// occ[seatIndex][edge] 为 true 表示该座位在相邻区段 edge（站 edge->edge+1）被占用。
	occ [][]bool
	// standing[edge] 为该区段当前已售无座票数。
	standing []int
	standCap int
}

func newSeatMap(stationCount int, seats []Seat, standCap int) *seatMap {
	occ := make([][]bool, len(seats))
	for i := range occ {
		occ[i] = make([]bool, stationCount-1)
	}
	return &seatMap{
		stationCount: stationCount,
		seats:        seats,
		occ:          occ,
		standing:     make([]int, stationCount-1),
		standCap:     standCap,
	}
}

// adjClass 返回相接优先级：2=两端都恰好相接，1=仅一端相接，0=两端都不相接。
// 调用前已保证 (from,to) 整条区段空闲，因此只可能在两端点处与既有区段相接。
// 左相接：存在已售区段恰好结束于 from（边 from-1 占用、边 from 空闲）。
// 右相接：存在已售区段恰好开始于 to（边 to 占用、边 to-1 空闲）。
func (m *seatMap) adjClass(o []bool, from, to int) int {
	left := from > 0 && o[from-1]
	right := to < m.stationCount-1 && o[to]
	switch {
	case left && right:
		return 2
	case left || right:
		return 1
	default:
		return 0
	}
}

// pickSeat 按相接优先级、车厢号、座位号选出整个 [from,to) 空闲的唯一座位；无则返回 -1。
// seats 已按 (车厢号, 座位号) 排序，故同优先级内取扫描到的第一个即唯一结果。
func (m *seatMap) pickSeat(from, to int) int {
	best := -1
	bestClass := -1
	for i, o := range m.occ {
		free := true
		for e := from; e < to; e++ {
			if o[e] {
				free = false
				break
			}
		}
		if !free {
			continue
		}
		c := m.adjClass(o, from, to)
		if c > bestClass {
			bestClass = c
			best = i
		}
	}
	return best
}

func (m *seatMap) occupy(idx, from, to int) {
	for e := from; e < to; e++ {
		m.occ[idx][e] = true
	}
}

func (m *seatMap) release(idx, from, to int) {
	for e := from; e < to; e++ {
		m.occ[idx][e] = false
	}
}

func (m *seatMap) standingFree(from, to int) bool {
	for e := from; e < to; e++ {
		if m.standing[e] >= m.standCap {
			return false
		}
	}
	return true
}

func (m *seatMap) standingAdd(from, to int) {
	for e := from; e < to; e++ {
		m.standing[e]++
	}
}

func (m *seatMap) standingRelease(from, to int) {
	for e := from; e < to; e++ {
		m.standing[e]--
	}
}
