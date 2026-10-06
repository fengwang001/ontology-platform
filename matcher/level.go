package matcher

// priceLevel owns everything resting at one price on one side.
//
// Visible batches (plain orders plus current iceberg display batches) form a
// FIFO doubly linked list. Hidden orders form a second FIFO list, ordered by
// sequence number. icebergReserve stores the sum of not-yet-displayed
// iceberg quantity; it is only used to decide whether hidden orders may
// trade (they may not while any reserve exists) and is never reported in
// visible-quantity statistics.
type priceLevel struct {
	price int64

	visibleHead *slotNode
	visibleTail *slotNode

	hiddenHead *hiddenNode
	hiddenTail *hiddenNode

	visibleQty     int64
	icebergReserve int64
	hiddenQty      int64
}

func newPriceLevel(price int64) *priceLevel {
	return &priceLevel{price: price}
}

func (l *priceLevel) empty() bool {
	return l.visibleQty == 0 && l.icebergReserve == 0 && l.hiddenQty == 0
}

// appendVisible adds a fresh display batch at the tail of the visible queue.
func (l *priceLevel) appendVisible(n *slotNode) {
	n.prev = l.visibleTail
	n.next = nil
	if l.visibleTail != nil {
		l.visibleTail.next = n
	} else {
		l.visibleHead = n
	}
	l.visibleTail = n
	l.visibleQty += n.size
}

// removeVisible detaches an arbitrary visible slot (used by cancel and by
// decrease modifications that keep time priority).
func (l *priceLevel) removeVisible(n *slotNode) {
	if n.prev != nil {
		n.prev.next = n.next
	} else {
		l.visibleHead = n.next
	}
	if n.next != nil {
		n.next.prev = n.prev
	} else {
		l.visibleTail = n.prev
	}
	n.prev = nil
	n.next = nil
	l.visibleQty -= n.size
}

// popHead removes and returns the fully-consumed head slot.
func (l *priceLevel) popHead() *slotNode {
	n := l.visibleHead
	l.removeVisible(n)
	return n
}

// appendHidden adds a hidden order at the tail (sequence order).
func (l *priceLevel) appendHidden(n *hiddenNode) {
	n.prev = l.hiddenTail
	n.next = nil
	if l.hiddenTail != nil {
		l.hiddenTail.next = n
	} else {
		l.hiddenHead = n
	}
	l.hiddenTail = n
	l.hiddenQty += n.size
}

// removeHidden detaches an arbitrary hidden queue entry.
func (l *priceLevel) removeHidden(n *hiddenNode) {
	if n.prev != nil {
		n.prev.next = n.next
	} else {
		l.hiddenHead = n.next
	}
	if n.next != nil {
		n.next.prev = n.prev
	} else {
		l.hiddenTail = n.prev
	}
	n.prev = nil
	n.next = nil
	l.hiddenQty -= n.size
}
