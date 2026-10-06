package matcher

func minInt(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func validateNew(req NewOrderRequest) *EngineError {
	if req.ID == "" {
		return errInvalid("order id must not be empty")
	}
	if req.Side != Buy && req.Side != Sell {
		return errInvalid("unknown side")
	}
	if req.Kind != Plain && req.Kind != Iceberg && req.Kind != Hidden {
		return errInvalid("unknown order kind")
	}
	if req.Price <= 0 {
		return errInvalid("price must be positive")
	}
	if req.Quantity <= 0 {
		return errInvalid("quantity must be positive")
	}
	if req.Kind == Iceberg {
		if req.DisplaySize <= 0 || req.DisplaySize > req.Quantity {
			return errInvalid("iceberg display size must be positive and not exceed total quantity")
		}
	} else if req.DisplaySize != 0 {
		return errInvalid("display size is only valid for iceberg orders")
	}
	return nil
}

// Submit validates and processes a new order. Rejected orders return an
// *EngineError without consuming a sequence number and without mutating any
// engine state.
func (e *Engine) Submit(req NewOrderRequest) (Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if verr := validateNew(req); verr != nil {
		return Result{}, verr
	}
	if _, exists := e.orders[req.ID]; exists {
		return Result{}, errDuplicate(req.ID)
	}

	taker := &bookOrder{
		id:          req.ID,
		seq:         e.nextSeq(),
		side:        req.Side,
		kind:        req.Kind,
		price:       req.Price,
		total:       req.Quantity,
		displaySize: req.DisplaySize,
		alive:       true,
	}
	e.orders[taker.id] = taker

	result := Result{Seq: taker.seq}
	result.Trades = e.match(taker)

	if taker.remaining() > 0 {
		e.rest(taker)
	}
	result.Order = taker.snapshot()
	return result, nil
}

func (e *Engine) opposite(side Side) *levelIndex {
	if side == Buy {
		return e.sells
	}
	return e.buys
}

func crosses(side Side, takerPrice, makerPrice int64) bool {
	if side == Buy {
		return takerPrice >= makerPrice
	}
	return takerPrice <= makerPrice
}

// match walks opposite-side price levels from best to worst until prices no
// longer cross or the taker is exhausted. Within each level visible batches
// have strict priority over hidden orders.
func (e *Engine) match(taker *bookOrder) []Trade {
	var trades []Trade
	idx := e.opposite(taker.side)

	for taker.remaining() > 0 {
		lvl := idx.best()
		if lvl == nil || !crosses(taker.side, taker.price, lvl.price) {
			break
		}
		e.levelsCrossed++
		e.matchVisible(taker, lvl, &trades)
		// Key invariant: an iceberg only holds reserve while it also has a
		// currently displayed batch (rest and replenishment always pair the
		// two). Hence an empty visible queue implies both "no displayed
		// batch" and "all iceberg reserve exhausted", which is exactly the
		// condition under which hidden orders may trade. No scan of orders
		// or aggregate reserve is needed.
		e.matchHidden(taker, lvl, &trades)
		if lvl.empty() {
			idx.remove(lvl.price)
		}
	}
	return trades
}

// fill produces one trade between taker and a resting maker and updates both
// orders' cumulative filled quantity. Quantity conservation is guaranteed
// because total never changes here and filled increases by exactly qty on
// both sides.
func (e *Engine) fill(taker, maker *bookOrder, price, qty int64, trades *[]Trade) {
	taker.filled += qty
	maker.filled += qty
	*trades = append(*trades, e.recordTrade(taker, maker, price, qty))
}

// matchVisible drains the visible FIFO queue one display batch per trade.
//
// Each trade is bounded by the head slot, so a trade never spans two
// batches. When an iceberg batch is fully consumed and reserve remains, a
// fresh batch of size min(displaySize, remaining) is appended at the tail of
// the same queue and remains reachable within this aggressor call, but it
// now queues behind every other batch (it loses time priority).
func (e *Engine) matchVisible(taker *bookOrder, lvl *priceLevel, trades *[]Trade) {
	for taker.remaining() > 0 && lvl.visibleHead != nil {
		head := lvl.visibleHead
		qty := minInt(taker.remaining(), head.size)
		maker := head.order
		e.fill(taker, maker, lvl.price, qty, trades)

		if qty < head.size {
			head.size -= qty
			lvl.visibleQty -= qty
			continue
		}

		lvl.popHead()
		maker.node = nil
		e.batchesConsumed++

		if maker.remaining() == 0 {
			maker.level = nil
			continue
		}
		if maker.kind == Iceberg {
			fresh := minInt(maker.displaySize, maker.remaining())
			node := &slotNode{order: maker, size: fresh}
			maker.node = node
			lvl.icebergReserve -= fresh
			lvl.appendVisible(node)
		} else {
			// A plain slot holds the order's entire remainder; consuming it
			// fully fills the order.
			maker.level = nil
		}
	}
}

// matchHidden drains hidden orders in sequence order. It is only entered
// after matchVisible returns with an empty visible queue, which (by the
// batch/reserve pairing invariant) also means all iceberg reserve is gone.
func (e *Engine) matchHidden(taker *bookOrder, lvl *priceLevel, trades *[]Trade) {
	for taker.remaining() > 0 && lvl.visibleHead == nil && lvl.hiddenHead != nil {
		head := lvl.hiddenHead
		qty := minInt(taker.remaining(), head.size)
		maker := head.order
		e.fill(taker, maker, lvl.price, qty, trades)

		if qty < head.size {
			head.size -= qty
			lvl.hiddenQty -= qty
			continue
		}
		lvl.removeHidden(head)
		maker.hnode = nil
		maker.level = nil
	}
}

func (e *Engine) ownIndex(side Side) *levelIndex {
	if side == Buy {
		return e.buys
	}
	return e.sells
}

// rest inserts the unfilled remainder of the taker into the book. An iceberg
// taker only displays a batch for the resting portion; the rest of its
// quantity becomes reserve.
func (e *Engine) rest(o *bookOrder) {
	idx := e.ownIndex(o.side)
	lvl := idx.get(o.price)
	isNew := lvl == nil
	if isNew {
		lvl = newPriceLevel(o.price)
	}
	o.level = lvl

	switch o.kind {
	case Plain:
		node := &slotNode{order: o, size: o.remaining()}
		o.node = node
		lvl.appendVisible(node)
	case Iceberg:
		fresh := minInt(o.displaySize, o.remaining())
		node := &slotNode{order: o, size: fresh}
		o.node = node
		lvl.icebergReserve += o.remaining() - fresh
		lvl.appendVisible(node)
	case Hidden:
		node := &hiddenNode{order: o, size: o.remaining()}
		o.hnode = node
		lvl.appendHidden(node)
	}

	if isNew {
		idx.add(lvl)
		e.heapPushes++
	}
}
