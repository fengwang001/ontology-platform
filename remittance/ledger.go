package remittance

// limitLedger 维护单个汇款人的额度占用：当日占用与滚动年度占用。
//
// 不变式：advance(curDay) 之后，
//   - days 恰好保存所有满足 d > curDay-365 且占用非零的日序号 d；
//   - order 为 days 的键按非降序排列的队列（新占用日 >= 既往占用日，
//     因为时钟单调，故直接追加即有序）；
//   - rolling 等于 days 中全部占用之和，即滚动年度占用。
//
// 复杂度：每个占用日最多入队一次、出队一次，每条占用最多释放一次，
// 故任意操作序列的总代价与操作数成正比，单次操作摊还 O(1)，
// 不随该汇款人历史汇款总数增长，也不触碰其他汇款人的状态。
type limitLedger struct {
	days    map[int64]int64 // 日序号 -> 该日未释放占用（仅存非零、未滑出窗口者）
	order   []int64         // days 的键，非降序；head 之前的下标已出队
	head    int
	rolling int64 // 滚动年度占用合计
	curDay  int64 // 最近一次 advance 的日序号
}

func newLimitLedger() *limitLedger {
	return &limitLedger{days: make(map[int64]int64)}
}

// addHold 在占用日序号 day（必为当前日）上增加占用。
func (l *limitLedger) addHold(day, amount int64) {
	if l.days[day] == 0 {
		l.order = append(l.order, day)
	}
	l.days[day] += amount
	l.rolling += amount
}

// releaseHold 从原占用日序号 day 上释放占用。
// 释放始终归还到当初占用的日序号；若该日已滑出滚动年度窗口，
// 其占用早已从 days 与 rolling 中剔除，此处为空操作。
func (l *limitLedger) releaseHold(day, amount int64) {
	v, ok := l.days[day]
	if !ok {
		return
	}
	v -= amount
	if v == 0 {
		delete(l.days, day)
	} else {
		l.days[day] = v
	}
	l.rolling -= amount
}

// advance 将窗口推进到当前日序号，剔除满足 d <= curDay-365 的占用。
func (l *limitLedger) advance(curDay int64) {
	l.curDay = curDay
	for l.head < len(l.order) && l.order[l.head] <= curDay-rollingDays {
		day := l.order[l.head]
		l.head++
		if amount, ok := l.days[day]; ok {
			delete(l.days, day)
			l.rolling -= amount
		}
	}
	// 队列前缀压缩，保证底层数组不随历史长度无限增长。
	if l.head >= 64 && l.head*2 >= len(l.order) {
		l.order = append([]int64(nil), l.order[l.head:]...)
		l.head = 0
	}
}

// dayUsed 返回当前日占用。
func (l *limitLedger) dayUsed() int64 { return l.days[l.curDay] }

// rollingUsed 返回滚动年度占用。
func (l *limitLedger) rollingUsed() int64 { return l.rolling }
