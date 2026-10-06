package consignment

import "sort"

// Line 是一条结算行：一笔领用拆分为若干行，一行对应一个批次。
type Line struct {
	ID        uint64 // 结算行号（全局递增）
	Supplier  uint64
	Item      uint64
	BatchID   uint64
	Qty       uint64 // 领用数量
	Reversed  uint64 // 已冲销数量，恒满足 Reversed <= Qty
	UnitPrice int64  // 领用时刻的有效单价（冲销沿用此价）
	Amount    int64  // Qty * UnitPrice
	Time      int64  // 领用发生时刻
}

// supplierLog 是某供应商的对账事件流。
// 时钟单调保证事件按发生时刻非降追加，故可用二分查找圈定周期。
type supplierLog struct {
	times     []int64 // 事件发生时刻，非降
	drawSum   []int64 // 领用金额前缀和
	reversSum []int64 // 冲销金额前缀和
}

func (lg *supplierLog) append(t int64, draw, reversal int64) {
	n := len(lg.times)
	lg.times = append(lg.times, t)
	if n == 0 {
		lg.drawSum = append(lg.drawSum, draw)
		lg.reversSum = append(lg.reversSum, reversal)
		return
	}
	lg.drawSum = append(lg.drawSum, lg.drawSum[n-1]+draw)
	lg.reversSum = append(lg.reversSum, lg.reversSum[n-1]+reversal)
}

// sumRange 返回 [from, to) 内领用与冲销各自的总金额。
func (lg *supplierLog) sumRange(from, to int64) (draws, reversals int64) {
	lo := sort.Search(len(lg.times), func(i int) bool { return lg.times[i] >= from })
	hi := sort.Search(len(lg.times), func(i int) bool { return lg.times[i] >= to })
	if hi > lo {
		draws = lg.drawSum[hi-1]
		reversals = lg.reversSum[hi-1]
	}
	if lo > 0 && hi > lo {
		draws -= lg.drawSum[lo-1]
		reversals -= lg.reversSum[lo-1]
	}
	return draws, reversals
}

// ledger 记录结算行、冲销与按供应商的对账事件流。
type ledger struct {
	lines map[uint64]*Line
	logs  map[uint64]*supplierLog
}

func newLedger() *ledger {
	return &ledger{lines: make(map[uint64]*Line), logs: make(map[uint64]*supplierLog)}
}

func (l *ledger) get(lineID uint64) *Line {
	return l.lines[lineID]
}

// addLine 登记一条结算行，并按其发生时刻追加领用事件。
func (l *ledger) addLine(line *Line) {
	l.lines[line.ID] = line
	l.logOf(line.Supplier).append(line.Time, line.Amount, 0)
}

// recordReversal 登记一次冲销：累加行内已冲销数量，按冲销时刻追加事件。
func (l *ledger) recordReversal(line *Line, qty uint64, t int64) {
	line.Reversed += qty
	l.logOf(line.Supplier).append(t, 0, int64(qty)*line.UnitPrice)
}

func (l *ledger) logOf(supplier uint64) *supplierLog {
	lg := l.logs[supplier]
	if lg == nil {
		lg = &supplierLog{}
		l.logs[supplier] = lg
	}
	return lg
}

// statement 汇总某供应商在 [from, to) 周期内的领用与冲销金额。
func (l *ledger) statement(supplier uint64, from, to int64) (draws, reversals int64) {
	lg := l.logs[supplier]
	if lg == nil {
		return 0, 0
	}
	return lg.sumRange(from, to)
}
