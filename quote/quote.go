// Package quote 维护单个做市关系的双边报价状态与合格判定。
// 本包不含任何时间概念，只回答"此刻报价是否合格"。
package quote

// Side 表示报价的一侧。
type Side int

const (
	// Buy 买侧（bid）。
	Buy Side = iota
	// Sell 卖侧（ask）。
	Sell
)

// State 是某一 (mm, sym) 的当前报价快照。
type State struct {
	Has    bool
	Bid    int64
	Ask    int64
	BidQty int64
	AskQty int64
}

// Set 整体替换报价。参数合法性由调用方保证。
func (s *State) Set(bid, bidQty, ask, askQty int64) {
	s.Has = true
	s.Bid, s.Ask = bid, ask
	s.BidQty, s.AskQty = bidQty, askQty
}

// Clear 清除报价；无报价时返回 false。
func (s *State) Clear() bool {
	if !s.Has {
		return false
	}
	*s = State{}
	return true
}

// Fill 从指定侧扣减数量；无报价或数量不足时返回 false 且不改状态。
func (s *State) Fill(side Side, qty int64) bool {
	if !s.Has {
		return false
	}
	switch side {
	case Buy:
		if qty > s.BidQty {
			return false
		}
		s.BidQty -= qty
	case Sell:
		if qty > s.AskQty {
			return false
		}
		s.AskQty -= qty
	default:
		return false
	}
	return true
}

// Qualified 判定当前报价是否满足义务：有报价、两侧数量均不小于 qmin，
// 且 (ask-bid)*20000 <= maxSpreadBps*(ask+bid)（取等合格）。
func (s *State) Qualified(qmin, maxSpreadBps int64) bool {
	if !s.Has || s.BidQty < qmin || s.AskQty < qmin {
		return false
	}
	return (s.Ask-s.Bid)*20000 <= maxSpreadBps*(s.Ask+s.Bid)
}
