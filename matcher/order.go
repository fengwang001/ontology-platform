package matcher

// slotNode is one displayed batch linked-list entry at a price level.
//
// Every plain order owns exactly one slot for its whole lifetime. An iceberg
// order owns one slot for its current display batch; whenever that batch is
// fully filled and reserve quantity remains, a brand-new slot is appended at
// the tail of the level's visible queue, which models the loss of time
// priority on replenishment.
type slotNode struct {
	order *bookOrder
	size  int64
	prev  *slotNode
	next  *slotNode
}

// hiddenNode links one hidden order inside a price level's hidden queue,
// ordered by the order's sequence number.
type hiddenNode struct {
	order *bookOrder
	size  int64
	prev  *hiddenNode
	next  *hiddenNode
}

// bookOrder is the mutable engine-internal representation of an order.
// The identity invariant is:
//
//	total == filled + remaining
//
// where for an iceberg resting in the book
//
//	remaining == node.size + reserve(held in its price level share)
//
// holds across the single current slot plus the level's aggregate reserve.
type bookOrder struct {
	id          string
	seq         int64
	side        Side
	kind        OrderKind
	price       int64
	total       int64
	displaySize int64
	filled      int64

	// node is the current visible slot; nil for hidden orders and for an
	// iceberg whose slot was just consumed and not yet replenished.
	node *slotNode
	// hnode is the hidden queue entry; nil unless the order is hidden.
	hnode *hiddenNode
	// level is the price level that currently contains the order.
	level *priceLevel
	// alive is false once the order is cancelled. Filled orders are simply
	// absent from the book (level == nil, nodes detached).
	alive bool
}

func (o *bookOrder) remaining() int64 { return o.total - o.filled }

func (o *bookOrder) status() OrderStatus {
	switch {
	case !o.alive:
		return Cancelled
	case o.filled == 0:
		return Resting
	case o.filled < o.total:
		return PartiallyFilled
	default:
		return Filled
	}
}

func (o *bookOrder) visible() int64 {
	if o.node != nil {
		return o.node.size
	}
	return 0
}

func (o *bookOrder) snapshot() Order {
	return Order{
		ID:          o.id,
		Seq:         o.seq,
		Side:        o.side,
		Kind:        o.kind,
		Price:       o.price,
		Total:       o.total,
		DisplaySize: o.displaySize,
		Filled:      o.filled,
		Remaining:   o.remaining(),
		Visible:     o.visible(),
		Status:      o.status(),
	}
}
