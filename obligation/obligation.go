// Package obligation 负责合格判定与合格时长的增量累计。
//
// 合格时长在每个事件到来时把 [last, now) 按当时状态计入当日累计，
// 并扣除已知豁免窗口（Exempt 要求 now<=from，故累计时相关窗口必然已知），
// 结算时无需重放任何报价事件。
package obligation

import (
	"sort"

	"ontology/quote"
)

// DayLen 一天的秒数。
const DayLen = 86400

// Params 是累计所需的常量参数。
type Params struct {
	Open  int64 // 日内秒，每日交易时段为 [Open, Close)
	Close int64
	Qmin  int64 // 最小报价量
	S     int64 // 最大价差（基点）
	G     int64 // 补单宽限（秒）
}

// Qualified 判定报价是否合格：有报价、两侧数量均不小于 qmin，
// 且 (ask-bid)*20000 <= s*(ask+bid)（取等合格）。
func Qualified(q quote.Quote, hasQuote bool, qmin, s int64) bool {
	if !hasQuote || q.BidQty < qmin || q.AskQty < qmin {
		return false
	}
	return (q.Ask-q.Bid)*20000 <= s*(q.Ask+q.Bid)
}

// Windows 是某标的已登记豁免窗口的集合，互不重叠（首尾相接允许）。
type Windows struct {
	ws []window
}

type window struct{ from, to int64 }

// Add 登记窗口 [from, to)；与已有窗口重叠时返回 false 且不加。
func (w *Windows) Add(from, to int64) bool {
	for _, x := range w.ws {
		if from < x.to && x.from < to {
			return false
		}
	}
	w.ws = append(w.ws, window{from, to})
	sort.Slice(w.ws, func(i, j int) bool { return w.ws[i].from < w.ws[j].from })
	return true
}

// Intersection 返回 [a, b) 与所有窗口的交集总长度。
func (w *Windows) Intersection(a, b int64) int64 {
	var n int64
	for _, x := range w.ws {
		if x.from >= b {
			break
		}
		if x.to > a {
			n += min(x.to, b) - max(x.from, a)
		}
	}
	return n
}

// QuoteEvent 是报价事件记录的类型占位。本实现不保存事件日志：
// 合格时长在事件到来时增量累计，结算访问的事件记录数恒为 0。
type QuoteEvent struct {
	Now int64
	Q   quote.Quote
}

// Tracker 增量累计单个 (mm, sym) 的每日合格时长。
type Tracker struct {
	p   Params
	win *Windows

	q    quote.Quote
	hasQ bool
	qual bool

	last int64           // 上次累计到的时间
	day  int64           // 当前累计所属日号
	acc  int64           // 当日已累计合格秒数
	past map[int64]int64 // 已结束日期的合格秒数

	quotes  int64 // 已接受的 Quote 事件数（对照用）
	grace   bool  // 宽限激活（激活时当前状态必为不合格）
	graceT0 int64 // 宽限起点
}

// NewTracker 创建累计器，now 为登记（或恢复资格）时刻。
func NewTracker(p Params, w *Windows, now int64) *Tracker {
	return &Tracker{p: p, win: w, last: now, day: now / DayLen, past: map[int64]int64{}}
}

// HasQuote 报告当前是否有报价。
func (t *Tracker) HasQuote() bool { return t.hasQ }

// Qty 返回当前报价某一侧的剩余数量。
func (t *Tracker) Qty(side quote.Side) int64 {
	if side == quote.Bid {
		return t.q.BidQty
	}
	return t.q.AskQty
}

// Quotes 返回已接受的 Quote 事件总数。
func (t *Tracker) Quotes() int64 { return t.quotes }

// DayEvents 恒返回 nil：合格时长在事件到来时增量累计，
// 结算无需重放当日事件，访问的报价事件记录数为 0。
func (t *Tracker) DayEvents(day int64) []QuoteEvent { return nil }

// Sync 把 [last, now) 按当前状态增量计入当日累计，并扣除已知豁免窗口。
func (t *Tracker) Sync(now int64) {
	if !t.qual {
		// 不合格时段不产生合格时长，直接跨越。
		if d := now / DayLen; d > t.day {
			t.past[t.day] = t.acc
			t.acc = 0
			t.day = d
		}
		t.last = now
		return
	}
	for t.last < now {
		end := min(now, (t.day+1)*DayLen)
		s := max(t.last, t.day*DayLen+t.p.Open)
		e := min(end, t.day*DayLen+t.p.Close)
		if s < e {
			t.acc += (e - s) - t.win.Intersection(s, e)
		}
		t.last = end
		if t.last == (t.day+1)*DayLen {
			t.past[t.day] = t.acc
			t.acc = 0
			t.day++
		}
	}
}

// A 返回某日已累计的合格时长（调用前须先 Sync 到该日收盘之后）。
func (t *Tracker) A(day int64) int64 {
	if day == t.day {
		return t.acc
	}
	return t.past[day]
}

// OnQuote 整体替换报价。若宽限中恢复合格，且 now<=t0+G、与 t0 同日、
// 早于当日收盘，则把 [t0, now) 补记为合格；否则该段整段不合格。
func (t *Tracker) OnQuote(now int64, q quote.Quote) {
	t.Sync(now)
	qual := Qualified(q, true, t.p.Qmin, t.p.S)
	if t.grace && qual {
		t0 := t.graceT0
		dayStart := (t0 / DayLen) * DayLen
		if now <= t0+t.p.G && now/DayLen == t0/DayLen && now < dayStart+t.p.Close {
			if s := max(t0, dayStart+t.p.Open); s < now {
				t.acc += (now - s) - t.win.Intersection(s, now)
			}
		}
		t.grace = false
	}
	t.q = q
	t.hasQ = true
	t.qual = qual
	t.quotes++
}

// OnWithdraw 清除报价，宽限作废。
func (t *Tracker) OnWithdraw(now int64) {
	t.Sync(now)
	t.hasQ = false
	t.qual = false
	t.grace = false
}

// OnFill 从一侧数量中扣减 qty。仅当状态由合格变为不合格时，
// 以 now 为 t0 开启宽限；宽限中再次 Fill 不改 t0。
func (t *Tracker) OnFill(now int64, side quote.Side, qty int64) {
	t.Sync(now)
	if side == quote.Bid {
		t.q.BidQty -= qty
	} else {
		t.q.AskQty -= qty
	}
	qual := Qualified(t.q, t.hasQ, t.p.Qmin, t.p.S)
	if t.qual && !qual {
		t.grace = true
		t.graceT0 = now
	}
	t.qual = qual
}

// Clear 在资格暂停时清除报价（不产生也不保留宽限）。
func (t *Tracker) Clear(now int64) {
	t.OnWithdraw(now)
}
