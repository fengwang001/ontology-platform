package matcher

// Cancel removes an order from the book. Already-traded quantity is
// unaffected. Filled and cancelled orders cannot be cancelled again.
func (e *Engine) Cancel(id string) (Order, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	o, ok := e.orders[id]
	if !ok {
		return Order{}, errNotFound(id)
	}
	if !o.alive || o.remaining() == 0 {
		return Order{}, errFinished(id)
	}

	e.detach(o)
	o.alive = false
	return o.snapshot(), nil
}

// Modify sets a new positive remaining quantity for an order.
//
// When newRemaining is no greater than the current remaining quantity the
// order keeps its queue position; if the value cuts into the current display
// batch, the batch is truncated in place.
//
// When newRemaining is greater, the order loses time priority: it receives a
// fresh sequence number and re-enters at the tail of its price level. An
// iceberg restarts with a full display batch.
func (e *Engine) Modify(id string, newRemaining int64) (Order, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if newRemaining <= 0 {
		return Order{}, errInvalid("new remaining quantity must be positive")
	}
	o, ok := e.orders[id]
	if !ok {
		return Order{}, errNotFound(id)
	}
	if !o.alive || o.remaining() == 0 {
		return Order{}, errFinished(id)
	}

	current := o.remaining()
	switch {
	case newRemaining <= current:
		e.decrease(o, newRemaining)
	case newRemaining > current:
		e.increase(o, newRemaining)
	}
	return o.snapshot(), nil
}

// detach removes every book structure that references the order and drops
// the level from the active set when it becomes empty.
func (e *Engine) detach(o *bookOrder) {
	lvl := o.level
	switch {
	case o.node != nil:
		lvl.removeVisible(o.node)
		if o.kind == Iceberg {
			lvl.icebergReserve -= o.remaining() - o.node.size
		}
		o.node = nil
	case o.hnode != nil:
		lvl.removeHidden(o.hnode)
		o.hnode = nil
	}
	o.level = nil
	if lvl.empty() {
		e.ownIndex(o.side).remove(lvl.price)
	}
}

// decrease shrinks the order in place without moving its queue position.
func (e *Engine) decrease(o *bookOrder, newRemaining int64) {
	// The level reserve is a sum over all iceberg orders; adjust only this
	// order's contribution (old reserve minus new reserve), never overwrite
	// the aggregate, otherwise other iceberg orders' reserve is lost.
	oldReserve := int64(0)
	if o.kind == Iceberg && o.node != nil {
		oldReserve = o.remaining() - o.node.size
	}
	o.total = o.filled + newRemaining
	lvl := o.level
	switch {
	case o.node != nil:
		if newRemaining < o.node.size {
			// Truncate the current display batch in place; the shortened
			// batch then holds the entire new remainder, so reserve is zero.
			oldSize := o.node.size
			o.node.size = newRemaining
			lvl.visibleQty -= oldSize - newRemaining
		}
		if o.kind == Iceberg {
			newReserve := newRemaining - o.node.size
			lvl.icebergReserve -= oldReserve - newReserve
		}
	case o.hnode != nil:
		lvl.hiddenQty -= o.hnode.size - newRemaining
		o.hnode.size = newRemaining
	}
}

// increase appends additional quantity; the whole order loses priority and
// restarts at the tail of the level with a fresh sequence number.
func (e *Engine) increase(o *bookOrder, newRemaining int64) {
	lvl := o.level
	switch {
	case o.node != nil:
		lvl.removeVisible(o.node)
		if o.kind == Iceberg {
			lvl.icebergReserve -= o.remaining() - o.node.size
		}
		o.node = nil
	case o.hnode != nil:
		lvl.removeHidden(o.hnode)
		o.hnode = nil
	}

	o.seq = e.nextSeq()
	o.total = o.filled + newRemaining

	switch o.kind {
	case Plain:
		node := &slotNode{order: o, size: newRemaining}
		o.node = node
		lvl.appendVisible(node)
	case Iceberg:
		fresh := minInt(o.displaySize, newRemaining)
		node := &slotNode{order: o, size: fresh}
		o.node = node
		lvl.icebergReserve += newRemaining - fresh
		lvl.appendVisible(node)
	case Hidden:
		node := &hiddenNode{order: o, size: newRemaining}
		o.hnode = node
		lvl.appendHidden(node)
	}
}
