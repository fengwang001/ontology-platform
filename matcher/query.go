package matcher

// BestBid returns the highest price with visible resting quantity, or 0 and
// false when the visible bid side is empty. Hidden-only levels are not shown.
func (e *Engine) BestBid() (int64, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	lvl := e.bestVisible(e.buys)
	if lvl == nil {
		return 0, false
	}
	return lvl.price, true
}

// BestAsk returns the lowest price with visible resting quantity, or 0 and
// false when the visible ask side is empty. Hidden-only levels are not shown.
func (e *Engine) BestAsk() (int64, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	lvl := e.bestVisible(e.sells)
	if lvl == nil {
		return 0, false
	}
	return lvl.price, true
}

// bestVisible skips levels that hold only hidden quantity. Such levels are
// still in the active index because they can trade, but they are not public
// top-of-book.
func (e *Engine) bestVisible(idx *levelIndex) *priceLevel {
	for {
		lvl := idx.best()
		if lvl == nil {
			return nil
		}
		if lvl.visibleQty > 0 {
			return lvl
		}
		// Hidden-only level at the best price: no better visible level can
		// exist behind it, so the visible book reports empty.
		return nil
	}
}

// VisibleQtyAt returns the displayed quantity at a price summed across both
// sides' entries at that exact price. In practice a price holds resting
// quantity on at most one side after matching; passing the side explicitly
// via VisibleQtyOn is preferred.
func (e *Engine) VisibleQtyAt(side Side, price int64) int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	lvl := e.ownIndex(side).get(price)
	if lvl == nil {
		return 0
	}
	return lvl.visibleQty
}

// BidLevels returns visible bid quantities from best (highest) to worst.
func (e *Engine) BidLevels() []PriceLevelView {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.visibleLevels(e.buys)
}

// AskLevels returns visible ask quantities from best (lowest) to worst.
func (e *Engine) AskLevels() []PriceLevelView {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.visibleLevels(e.sells)
}

func (e *Engine) visibleLevels(idx *levelIndex) []PriceLevelView {
	out := make([]PriceLevelView, 0, len(idx.active))
	for price, lvl := range idx.active {
		if lvl.visibleQty > 0 {
			out = append(out, PriceLevelView{Price: price, VisibleQty: lvl.visibleQty})
		}
	}
	if idx.negate {
		sortViewsDesc(out)
	} else {
		sortViewsAsc(out)
	}
	return out
}

// GetOrder returns a point-in-time immutable snapshot of one order.
func (e *Engine) GetOrder(id string) (Order, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	o, ok := e.orders[id]
	if !ok {
		return Order{}, false
	}
	return o.snapshot(), true
}

// Trades returns a copy of the complete trade history in execution order.
func (e *Engine) Trades() []Trade {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]Trade, len(e.trades))
	copy(out, e.trades)
	return out
}

// Stats returns a snapshot of internal work counters for complexity tests.
func (e *Engine) Stats() Stats {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return Stats{
		BatchesConsumed: e.batchesConsumed,
		LevelsCrossed:   e.levelsCrossed,
		HeapPushes:      e.heapPushes,
		HeapStalePops:   e.heapStalePops,
	}
}

func sortViewsAsc(v []PriceLevelView) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j-1].Price > v[j].Price; j-- {
			v[j-1], v[j] = v[j], v[j-1]
		}
	}
}

func sortViewsDesc(v []PriceLevelView) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j-1].Price < v[j].Price; j-- {
			v[j-1], v[j] = v[j], v[j-1]
		}
	}
}
