package shophours

const noTime int64 = -1 << 62

// closure 是一条临时歇业记录。
type closure struct {
	start      int64
	end        int64 // 计划结束 = start+duration
	actualEnd  int64 // 实际结束（提前结束时 < end）
	endedEarly bool
}

// closureBook 管理临时歇业记录与强制停业状态。
type closureBook struct {
	maxDuration int64
	minGap      int64
	records     []*closure // 按 start 排序，头指针之前的记录均已结束
	head        int
	lastEnd     int64 // 最近一次已结束歇业实际结束；无则 noTime
	forceStart  int64 // 已登记强制停业起点；noTime 表示无登记
	forceActive bool  // 起点是否已到达（已实际生效）
}

func newClosureBook(maxDuration, minGap int64) *closureBook {
	return &closureBook{
		maxDuration: maxDuration,
		minGap:      minGap,
		lastEnd:     noTime,
		forceStart:  noTime,
	}
}

// prune 清理实际已结束的歇业，推进头指针与 lastEnd。
func (b *closureBook) prune(now int64) {
	for b.head < len(b.records) {
		c := b.records[b.head]
		if c.actualEnd > now {
			break
		}
		if c.actualEnd > b.lastEnd {
			b.lastEnd = c.actualEnd
		}
		b.head++
	}
	// 保留近期历史供查询，但准入只扫描 [head,len)，其规模不随历史增长。
	if b.head > 64 && b.head*2 >= len(b.records) {
		b.records = append([]*closure(nil), b.records[b.head:]...)
		b.head = 0
	}
}

// advance 推进惰性状态：清理过期歇业；若登记的强制停业在本时刻首次生效，
// 返回 activated=true 及其起点（用于触发一次平台责任取消）。
func (b *closureBook) advance(now int64) (bool, int64) {
	b.prune(now)
	if !b.forceActive && b.forceStart != noTime && b.forceStart <= now {
		b.forceActive = true
		return true, b.forceStart
	}
	return false, 0
}

func (b *closureBook) forceOn(now int64) bool {
	return b.forceActive && b.forceStart <= now
}

// contains 报告时刻 t 是否落在任一已登记（含未来）歇业的计划区间内。
func (b *closureBook) contains(t int64) bool {
	for i := b.head; i < len(b.records); i++ {
		c := b.records[i]
		if c.start <= t && t < c.end {
			return true
		}
	}
	return false
}

// covering 返回覆盖时刻 t 的未结束歇业（无则 nil）。
func (b *closureBook) covering(t int64) *closure {
	for i := b.head; i < len(b.records); i++ {
		c := b.records[i]
		if c.start <= t && t < c.actualEnd {
			return c
		}
	}
	return nil
}

// overlaps 报告拟登记区间 [start,end) 是否与未结束歇业重叠（端点相接不算重叠）。
func (b *closureBook) overlaps(start, end int64) bool {
	for i := b.head; i < len(b.records); i++ {
		c := b.records[i]
		if start < c.end && c.start < end {
			return true
		}
	}
	return false
}

// gapOK 报告起点距上一次歇业实际结束是否满足最短间隔（取等允许）。
func (b *closureBook) gapOK(start int64) bool {
	return b.lastEnd == noTime || start-b.lastEnd >= b.minGap
}

func (b *closureBook) add(c *closure) {
	// 时钟单调且 start>=now，起点非递减，追加即保持按 start 有序。
	b.records = append(b.records, c)
}
