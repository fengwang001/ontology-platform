package naive

// OrderState is the queryable state of one order.
type OrderState struct {
	Seq         int64
	Side        Side
	Kind        Kind
	Price       int64
	Total       int64
	DisplaySize int64
	Filled      int64
	Remaining   int64
	Visible     int64
	Cancelled   bool
}

// State returns the full state of one order.
func (e *Engine) State(id string) (OrderState, bool) {
	o, ok := e.orders[id]
	if !ok {
		return OrderState{}, false
	}
	s := OrderState{
		Seq: o.seq, Side: o.side, Kind: o.kind, Price: o.price, Total: o.total,
		DisplaySize: o.displaySize, Filled: o.filled, Remaining: e.remOf(o),
		Cancelled: o.cancelled,
	}
	if l := e.levels[o.side][o.price]; l != nil && o.kind != Hidden {
		for _, sl := range l.visible {
			if sl.ID == o.id {
				s.Visible = sl.Size
			}
		}
	}
	return s, true
}

// VisibleQty reports displayed quantity at one side/price.
func (e *Engine) VisibleQty(s Side, price int64) int64 {
	l := e.levels[s][price]
	if l == nil {
		return 0
	}
	var q int64
	for _, sl := range l.visible {
		q += sl.Size
	}
	return q
}

// Trades returns the full trade history.
func (e *Engine) Trades() []Trade {
	out := make([]Trade, len(e.trades))
	copy(out, e.trades)
	return out
}

// Seq is exposed for differential assertions.
func (e *Engine) Seq() int64 { return e.seq }

// HasOrder reports whether an id is known.
func (e *Engine) HasOrder(id string) bool {
	_, ok := e.orders[id]
	return ok
}
