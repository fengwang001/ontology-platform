package calendar

// Block 是房东设置的封锁区间，封锁夜为 [Start, End)。
type Block struct {
	ID    int64
	Start int64 // 含
	End   int64 // 不含
}

// Listing 是房源的日历与约束配置。
type Listing struct {
	ID             string
	NightlyPrice   int64
	MinStayDefault int64 // 默认最短入住夜数
	Gap            int64 // 换客间隙天数（两笔预订之间必须空出的整天数）

	minStayByDay map[int64]int64 // 按日覆盖的最短入住
	maxMinStay   int64           // minStayByDay 与默认值的最大值，用于有界邻居扫描

	blocks     map[int64]*Block
	blockOrder []int64 // 固定迭代次序，保证重放结果一致

	occupied map[int64]int64 // 夜 -> 预订 ID（有效保留 + 已确认）

	probes int64 // 可订性判定中的占用探测计数，用于复杂度证明（测试可重置）
}

func newListing(id string, price, minStayDefault, gap int64) *Listing {
	return &Listing{
		ID:             id,
		NightlyPrice:   price,
		MinStayDefault: minStayDefault,
		Gap:            gap,
		minStayByDay:   map[int64]int64{},
		maxMinStay:     minStayDefault,
		blocks:         map[int64]*Block{},
		occupied:       map[int64]int64{},
	}
}

// minStayAt 返回某日适用的最短入住夜数。
func (l *Listing) minStayAt(day int64) int64 {
	if v, ok := l.minStayByDay[day]; ok {
		return v
	}
	return l.MinStayDefault
}

func (l *Listing) recomputeMaxMinStay() {
	m := l.MinStayDefault
	for _, v := range l.minStayByDay {
		if v > m {
			m = v
		}
	}
	l.maxMinStay = m
}

// isBlocked 报告某夜是否被任一封锁区间覆盖。
func (l *Listing) isBlocked(night int64) bool {
	for _, id := range l.blockOrder {
		b := l.blocks[id]
		if b.Start <= night && night < b.End {
			return true
		}
	}
	return false
}

// occupant 返回占用某夜的预订 ID；每次调用计一次探测。
func (l *Listing) occupant(night int64) (int64, bool) {
	l.probes++
	id, ok := l.occupied[night]
	return id, ok
}
