// Package payout 实现创作者分成结算账本的结算层，并作为装配根：
// Ledger 组合 revenue.Book（收入与份额）与 hold.Registry（冻结），
// 提供成熟期判定、欠款抵扣与起付结算，以及整笔退款。
package payout

import (
	"errors"

	"ontology/hold"
	"ontology/revenue"
)

// 结算层的哨兵错误，调用方用 errors.Is 区分。
var (
	ErrEventNotFound   = errors.New("payout: event not found")
	ErrAlreadyRefunded = errors.New("payout: event already refunded")
	ErrCreatorNotFound = errors.New("payout: creator not found")
)

// Balance 是创作者未退款份额的 Remaining 分类与欠款快照。
type Balance struct {
	Held      int64 // 被任一冻结覆盖（无论是否成熟）
	Pending   int64 // 未被覆盖且未成熟
	Available int64 // 未被覆盖且已成熟
	Debt      int64 // 欠款
}

// Ledger 是完整账本：收入层 + 冻结层 + 结算参数。
type Ledger struct {
	Book  *revenue.Book
	Holds *hold.Registry

	minPay  int64
	touched int // 非导出：最近一次 Settle 消耗循环触碰的份额记录数
}

// New 创建账本；wd 为成熟期（0 到 1e9 秒），min 为起付额（1 到 1e12 分）。
func New(wd, min int64) (*Ledger, error) {
	if min < 1 || min > 1_000_000_000_000 {
		return nil, revenue.ErrInvalidParam
	}
	book, err := revenue.NewBook(wd)
	if err != nil {
		return nil, err
	}
	return &Ledger{Book: book, Holds: hold.NewRegistry(book), minPay: min}, nil
}

// SetSplit 设定内容的分成表（见 revenue.Book.SetSplit）。
func (l *Ledger) SetSplit(now int64, content string, parts []revenue.Part) error {
	return l.Book.SetSplit(now, content, parts)
}

// Earn 记录收入事件并返回各份额（见 revenue.Book.Earn）。
func (l *Ledger) Earn(now int64, eventID, content string, amount int64) ([]int64, error) {
	return l.Book.Earn(now, eventID, content, amount)
}

// Hold 冻结区间内份额（见 hold.Registry.Hold）。
func (l *Ledger) Hold(now int64, holdID, content string, from, to int64) error {
	return l.Holds.Hold(now, holdID, content, from, to)
}

// Release 解除一条冻结（见 hold.Registry.Release）。
func (l *Ledger) Release(now int64, holdID string) error {
	return l.Holds.Release(now, holdID)
}

// Balance 把创作者未退款份额的 Remaining 分成 held/pending/available 三类，
// 并返回欠款。Balance 是只读查询：不校验时钟、不推进时钟；
// 创作者不存在时返回零值。
func (l *Ledger) Balance(creator string, now int64) Balance {
	var bal Balance
	_ = l.Book.Do(func() error {
		c, ok := l.Book.GetCreator(creator)
		if !ok {
			return nil
		}
		c.Promote(now)
		bal = Balance{Held: c.Held(), Pending: c.Pending(), Available: c.Available(), Debt: c.Debt()}
		return nil
	})
	return bal
}

// Settle 结算一位创作者：令 A 为 available，按入账先后（FIFO）消耗可结算份额。
// A 不大于 debt 时全部可结算份额清零抵债，出款 0；否则先消耗 debt 抵债，
// 净额 n = A − debt 不小于 Min 时其余全部消耗并出款 n，小于 Min 则不出款、
// 剩余部分原样结转（抵债照常发生）。返回出款额与抵债额。
// 拒绝次序：参数非法 > 时钟回退 > 创作者不存在。
func (l *Ledger) Settle(now int64, creator string) (int64, int64, error) {
	if creator == "" || now < 0 || now > 1_000_000_000_000 {
		return 0, 0, revenue.ErrInvalidParam
	}
	var paidOut, offset int64
	err := l.Book.Do(func() error {
		if err := l.Book.CheckClock(now); err != nil {
			return err
		}
		c, ok := l.Book.GetCreator(creator)
		if !ok {
			return ErrCreatorNotFound
		}
		l.touched = 0
		c.Promote(now)
		avail := c.Available()
		debt := c.Debt()
		var consume int64
		switch {
		case avail <= debt:
			consume = avail
			offset = avail
		default:
			offset = debt
			if net := avail - debt; net >= l.minPay {
				consume = avail
				paidOut = net
			} else {
				consume = debt // 未达起付：抵债照常，剩余原样结转
			}
		}
		c.AddDebt(-offset)
		l.consume(c, now, consume)
		l.Book.AcceptClock(now)
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	return paidOut, offset, nil
}

// consume 按 FIFO 消耗至多 limit 分的可结算份额。
// 事件时刻单调不减，遇到首个未成熟份额即停；被冻结份额跳过；
// 付清的份额当场出列，故触碰数与已付清历史无关。
func (l *Ledger) consume(c *revenue.Creator, now int64, limit int64) {
	left := limit
	wd := l.Book.Wd()
	for s := c.Front(); s != nil && left > 0; {
		next := s.Next()
		l.touched++
		switch {
		case s.Holds > 0:
			// 被冻结的 Remaining 不会被结算消耗
		case s.Event.Time+wd > now:
			return // 之后的份额均未成熟
		default:
			take := min(s.Remaining, left)
			c.Consume(s, take)
			left -= take
		}
		s = next
	}
}

// Refund 整笔退款：每个份额的 Remaining 直接移除（无论 pending/available/held），
// 已付出或已抵债的部分 Share − Remaining 计入该创作者的欠款。
// 拒绝次序：参数非法 > 时钟回退 > 事件不存在 > 已退款。
func (l *Ledger) Refund(now int64, eventID string) error {
	if eventID == "" || now < 0 || now > 1_000_000_000_000 {
		return revenue.ErrInvalidParam
	}
	return l.Book.Do(func() error {
		if err := l.Book.CheckClock(now); err != nil {
			return err
		}
		ev, ok := l.Book.GetEvent(eventID)
		if !ok {
			return ErrEventNotFound
		}
		if ev.Refunded {
			return ErrAlreadyRefunded
		}
		promoted := make(map[*revenue.Creator]bool)
		for _, s := range ev.Shares {
			if !promoted[s.Owner] {
				s.Owner.Promote(now)
				promoted[s.Owner] = true
			}
		}
		for _, s := range ev.Shares {
			c := s.Owner
			r := s.Remaining
			if r > 0 {
				c.RemoveRemaining(s, now)
			}
			if paid := s.Share - r; paid > 0 {
				c.AddDebt(paid)
			}
			s.Remaining = 0
		}
		ev.Refunded = true
		l.Book.AcceptClock(now)
		return nil
	})
}
