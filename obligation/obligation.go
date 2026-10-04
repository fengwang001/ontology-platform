// Package obligation 维护单个做市关系的合格时长增量累计与每标的豁免窗口。
// 合格时长在每次事件到来时增量累计，累计时即扣除已知豁免窗口，
// 日终结算无需重放任何报价事件。
package obligation

import "sort"

// SecPerDay 每日秒数。
const SecPerDay int64 = 86400

// Day 返回时刻 t 所属的日号。
func Day(t int64) int64 { return t / SecPerDay }

// Window 是一个左闭右开的豁免时间段（绝对秒）。
type Window struct {
	From int64
	To   int64
}

// Windows 是某标的已登记的豁免窗口集合，按 From 排序且互不重叠
// （首尾相接不算重叠）。同一标的的所有做市商共享同一集合。
type Windows struct {
	ws []Window
}

// Add 登记窗口 [from, to)；与已有窗口重叠时返回 false 且不改状态。
func (w *Windows) Add(from, to int64) bool {
	for _, x := range w.ws {
		if from < x.To && x.From < to {
			return false
		}
	}
	w.ws = append(w.ws, Window{From: from, To: to})
	sort.Slice(w.ws, func(i, j int) bool { return w.ws[i].From < w.ws[j].From })
	return true
}

// Overlap 返回 [lo, hi) 与所有豁免窗口的交集总长度。
func (w *Windows) Overlap(lo, hi int64) int64 {
	var total int64
	for _, x := range w.ws {
		if x.From >= hi {
			break
		}
		if x.To <= lo {
			continue
		}
		total += min(hi, x.To) - max(lo, x.From)
	}
	return total
}

// Tracker 增量累计一个做市关系在当前日的合格秒数。
// 所有方法由引擎在持有锁时按单调时钟驱动。
type Tracker struct {
	open     int64 // 日内时段 [open, close)
	close    int64
	graceLen int64

	day     int64           // 当前累计日
	lastT   int64           // 上次累计到的时刻
	qual    bool            // 当前是否处于合格状态
	graceOn bool            // 是否处于补单宽限中
	t0      int64           // 宽限起点
	acc     int64           // 当前日已累计的合格秒数
	done    map[int64]int64 // 已跨过的未结算日的终值
}

// NewTracker 创建累计器；day 为登记日，t 为登记时刻。
func NewTracker(open, close, graceLen, day, t int64) *Tracker {
	return &Tracker{
		open:     open,
		close:    close,
		graceLen: graceLen,
		day:      day,
		lastT:    t,
		done:     map[int64]int64{},
	}
}

// accrueTo 把 [lastT, t) 与当日时段的交集按当前合格态入账，
// 入账时扣除已知豁免窗口；t 到达或越过收盘时宽限作废。
func (tr *Tracker) accrueTo(t int64, ws *Windows) {
	dayOpen := tr.day*SecPerDay + tr.open
	dayClose := tr.day*SecPerDay + tr.close
	end := min(t, dayClose)
	lo := max(tr.lastT, dayOpen)
	if tr.qual && lo < end {
		tr.acc += end - lo - ws.Overlap(lo, end)
	}
	if t >= dayClose {
		tr.graceOn = false
	}
	tr.lastT = t
}

// advanceTo 把累计器推进到 (day, t)。跨日时把每个被跨过日的终值
// 存入 done（仅当该日尚未结算，即日号 >= settledUpto）。
func (tr *Tracker) advanceTo(day, t, settledUpto int64, ws *Windows) {
	for tr.day < day {
		tr.accrueTo(tr.day*SecPerDay+tr.close, ws)
		if tr.day >= settledUpto {
			tr.done[tr.day] = tr.acc
		}
		tr.acc = 0
		tr.day++
		tr.graceOn = false
	}
	tr.accrueTo(t, ws)
}

// OnQuote 在报价被整体替换后调用，qualAfter 为替换后的合格态。
// 若处于宽限中且恢复合格，且 now<=t0+G、与 t0 同日、早于当日收盘，
// 则把 [t0, now)（扣豁免、限时段内）补记为合格；否则宽限作废、整段不补。
func (tr *Tracker) OnQuote(t, settledUpto int64, qualAfter bool, ws *Windows) {
	tr.advanceTo(Day(t), t, settledUpto, ws)
	if tr.graceOn {
		tr.graceOn = false
		dayClose := tr.day*SecPerDay + tr.close
		if qualAfter && t <= tr.t0+tr.graceLen && t < dayClose {
			lo := max(tr.t0, tr.day*SecPerDay+tr.open)
			if lo < t {
				tr.acc += t - lo - ws.Overlap(lo, t)
			}
		}
	}
	tr.qual = qualAfter
}

// OnFill 在成交扣减后调用。仅当成交使状态由合格变为不合格时
// 开启宽限并记 t0；宽限中再次成交不改 t0。
func (tr *Tracker) OnFill(t, settledUpto int64, qualBefore, qualAfter bool, ws *Windows) {
	tr.advanceTo(Day(t), t, settledUpto, ws)
	if qualBefore && !qualAfter && !tr.graceOn {
		tr.graceOn = true
		tr.t0 = t
	}
	tr.qual = qualAfter
}

// OnWithdraw 在撤单（或暂停清报价）后调用：宽限作废，状态变为不合格。
func (tr *Tracker) OnWithdraw(t, settledUpto int64, ws *Windows) {
	tr.advanceTo(Day(t), t, settledUpto, ws)
	tr.graceOn = false
	tr.qual = false
}

// SettleDay 返回 day 的合格秒数终值。day 已跨过时取 done 中的存档；
// 否则把累计器推进到当日收盘后取当前累计值。
func (tr *Tracker) SettleDay(day, settledUpto int64, ws *Windows) int64 {
	if a, ok := tr.done[day]; ok {
		delete(tr.done, day)
		return a
	}
	tr.advanceTo(day, day*SecPerDay+tr.close, settledUpto, ws)
	return tr.acc
}

// DiscardBefore 丢弃日号小于 day 的存档（用于资格恢复后跳过未结算日）。
func (tr *Tracker) DiscardBefore(day int64) {
	for d := range tr.done {
		if d < day {
			delete(tr.done, d)
		}
	}
}
