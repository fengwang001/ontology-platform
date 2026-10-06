package matching

import "sync"

// Engine 是单品种限价撮合引擎。所有方法可并发调用，
// 内部以互斥锁串行化，结果等价于某个确定的串行顺序。
type Engine struct {
	mu sync.Mutex

	nextSeq int64
	orders  map[int64]*order
	bids    *priceTree
	asks    *priceTree
	fills   []Fill
}

// New 创建一个空引擎。
func New() *Engine {
	return &Engine{
		orders: make(map[int64]*order),
		bids:   newPriceTree(),
		asks:   newPriceTree(),
	}
}

// Submit 接收一个新委托；被拒绝时不占序号、不改变任何状态。
func (e *Engine) Submit(p OrderParams) (seq int64, fills []Fill, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	// 错误优先级：参数非法 > 编号重复。
	if err = validateParams(p); err != nil {
		return 0, nil, err
	}
	if _, dup := e.orders[p.ClientID]; dup {
		return 0, nil, errf(ErrDuplicateClientID, "client id %d already exists", p.ClientID)
	}

	e.nextSeq++
	o := &order{
		clientID:  p.ClientID,
		seq:       e.nextSeq,
		side:      p.Side,
		price:     p.Price,
		typ:       p.Type,
		total:     p.TotalQty,
		remaining: p.TotalQty,
		showParam: p.IcebergVisibleQty,
		status:    StatusPending,
	}
	e.orders[o.clientID] = o

	fills = e.match(o)

	if o.remaining > 0 {
		// 主动冰山只有挂簿部分才有显示量：enqueue 按当前剩余量切批。
		e.enqueue(o)
	}
	return o.seq, fills, nil
}

// Cancel 撤销委托。
func (e *Engine) Cancel(clientID int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	o, ok := e.orders[clientID]
	if !ok {
		return errf(ErrOrderNotFound, "order %d not found", clientID)
	}
	switch o.status {
	case StatusCompleted:
		return errf(ErrOrderCompleted, "order %d already completed", clientID)
	case StatusCancelled:
		return errf(ErrOrderCancelled, "order %d already cancelled", clientID)
	}
	e.removeFromBook(o)
	o.status = StatusCancelled
	return nil
}

// ReplaceQty 改量：newRemaining 为新的剩余总量（必须为正）。
func (e *Engine) ReplaceQty(clientID int64, newRemaining int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if newRemaining <= 0 {
		return errf(ErrInvalidParam, "new remaining qty must be positive, got %d", newRemaining)
	}
	o, ok := e.orders[clientID]
	if !ok {
		return errf(ErrOrderNotFound, "order %d not found", clientID)
	}
	switch o.status {
	case StatusCompleted:
		return errf(ErrOrderCompleted, "order %d already completed", clientID)
	case StatusCancelled:
		return errf(ErrOrderCancelled, "order %d already cancelled", clientID)
	}

	oldRemaining := o.remaining
	// 守恒：total = filled + remaining，因此只重设 total。
	o.total = o.filled + newRemaining
	o.remaining = newRemaining

	e.doReplace(o, oldRemaining, newRemaining)
	return nil
}

// doReplace 依据增量/减量决定排队位置。
func (e *Engine) doReplace(o *order, oldRemaining, newRemaining int64) {
	if newRemaining > oldRemaining {
		// 加量：整个委托失去优先级，取新序号排到队尾。
		e.nextSeq++
		o.seq = e.nextSeq
		e.removeFromBook(o)
		e.enqueue(o)
	} else {
		// 减量：保留原排队位置；必要时同步截短显示批。
		if o.batch != nil && o.batch.left > newRemaining {
			diff := o.batch.left - newRemaining
			o.batch.left = newRemaining
			o.lev.visSize -= diff
		}
	}
}

// BestBid 返回最优买价；无买盘时 ok 为 false。
func (e *Engine) BestBid() (price int64, ok bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	lv := e.bids.max()
	if lv == nil {
		return 0, false
	}
	return lv.price, true
}

// BestAsk 返回最优卖价；无卖盘时 ok 为 false。
func (e *Engine) BestAsk() (price int64, ok bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	lv := e.asks.min()
	if lv == nil {
		return 0, false
	}
	return lv.price, true
}

// VisibleDepthAt 查询某价位显示总量（仅当前显示批）。
func (e *Engine) VisibleDepthAt(side Side, price int64) int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	if lv := e.tree(side).get(price); lv != nil {
		return lv.visSize
	}
	return 0
}

// SnapshotDepth 返回一侧全部价位的显示总量，价格按从优到劣排列。
func (e *Engine) SnapshotDepth(side Side) []DepthAtPrice {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.depthSnapshot(side)
}

// GetOrder 查询委托快照；未被接受过的编号返回 ok=false。
func (e *Engine) GetOrder(clientID int64) (Order, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	o, ok := e.orders[clientID]
	if !ok {
		return Order{}, false
	}
	return o.snapshot(), true
}

// Fills 返回全部成交的拷贝。
func (e *Engine) Fills() []Fill {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Fill, len(e.fills))
	copy(out, e.fills)
	return out
}
