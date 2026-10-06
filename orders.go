package shophours

type orderRec struct {
	id           int64
	kind         OrderKind
	acceptedAt   int64
	promisedAt   int64
	status       OrderStatus
	cancelledBy  ResponsibleParty
	cancelledAt  int64
	cancelReason ErrorCode
	started      bool
	completed    bool
}

type orderBook struct {
	byID     map[int64]*orderRec
	nextID   int64
	openInst map[int64]*orderRec // 已接未开工即时单
}

func newOrderBook() *orderBook {
	return &orderBook{
		byID:     make(map[int64]*orderRec),
		nextID:   1,
		openInst: make(map[int64]*orderRec),
	}
}

func (b *orderBook) add(kind OrderKind, acceptedAt, promisedAt int64) *orderRec {
	rec := &orderRec{
		id:         b.nextID,
		kind:       kind,
		acceptedAt: acceptedAt,
		promisedAt: promisedAt,
		status:     OrderAccepted,
	}
	b.nextID++
	b.byID[rec.id] = rec
	if kind == KindInstant {
		b.openInst[rec.id] = rec
	}
	return rec
}

// cancelOpenInstant 取消全部已接未开工即时单，返回被取消订单。
func (b *orderBook) cancelOpenInstant(now int64, by ResponsibleParty, reason ErrorCode) []*orderRec {
	out := make([]*orderRec, 0, len(b.openInst))
	for id, rec := range b.openInst {
		rec.status = OrderCancelled
		rec.cancelledBy = by
		rec.cancelledAt = now
		rec.cancelReason = reason
		out = append(out, rec)
		delete(b.openInst, id)
	}
	return out
}

func (b *orderBook) snapshot(rec *orderRec) Order {
	return Order{
		ID:           rec.id,
		Kind:         rec.kind,
		AcceptedAt:   rec.acceptedAt,
		PromisedAt:   rec.promisedAt,
		Status:       rec.status,
		CancelledBy:  rec.cancelledBy,
		CancelledAt:  rec.cancelledAt,
		CancelReason: rec.cancelReason,
	}
}
