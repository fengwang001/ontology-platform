package ontology

// BidderState 是竞价者当前状态的只读快照。
type BidderState struct {
	ID     string
	Bid    int64
	Q      int64
	Budget int64
	H      int64
	F      int64
	Seq    int64
}

// Available 返回可用预算 budget - h。
func (s BidderState) Available() int64 { return s.Budget - s.H }

// AuctionWinner 是拍卖赢家的只读快照（含结算信息）。
type AuctionWinner struct {
	ID      string
	P       int64
	Qe      int64
	Clicked bool
}

// AuctionState 是一次拍卖的只读快照。
type AuctionState struct {
	ID       int64
	Resolved bool
	Winners  []AuctionWinner
}

// GetBidder 返回竞价者状态快照，不存在时 ok 为 false。
func (e *Engine) GetBidder(id string) (BidderState, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	b, ok := e.bidders[id]
	if !ok {
		return BidderState{}, false
	}
	return BidderState{
		ID: b.id, Bid: b.bid, Q: b.q, Budget: b.budget,
		H: b.h, F: b.f, Seq: b.seq,
	}, true
}

// GetAuction 返回拍卖快照，不存在时 ok 为 false。
func (e *Engine) GetAuction(id int64) (AuctionState, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	a, ok := e.auctions[id]
	if !ok {
		return AuctionState{}, false
	}
	ws := make([]AuctionWinner, 0, len(a.winners))
	for _, w := range a.winners {
		ws = append(ws, AuctionWinner{ID: w.id, P: w.p, Qe: w.qe, Clicked: w.clicked})
	}
	return AuctionState{ID: a.id, Resolved: a.resolved, Winners: ws}, true
}

// NumBidders 返回登记竞价者数。
func (e *Engine) NumBidders() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.bidders)
}

// NumAuctions 返回成功产生的拍卖数（被 NoParticipants 拒绝不计数）。
func (e *Engine) NumAuctions() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.auctions)
}

// SortCompares 返回最近一次 Auction 排序产生的比较次数（非导出计数器的只读视图）。
func (e *Engine) SortCompares() int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.sortCompares
}
