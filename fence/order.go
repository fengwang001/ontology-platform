package fence

type OrderStatus int

const (
	OrderPending OrderStatus = iota
	OrderAccepted
	OrderDelivered
	OrderCancelled
)

type CancelReason int

const (
	NotCancelled CancelReason = iota
	CancelledByShrink
)

type Order struct {
	ID           string
	MerchantID   string
	Cell         Cell
	Status       OrderStatus
	CancelReason CancelReason
	Rerouted     bool
	PlacedAt     int64
}

// canAccept guards the merchant-accept transition. Pending is the only state
// from which an accept attempt is made; the reachability check is performed by
// the coordinator afterwards.
func (o *Order) canAccept() error {
	switch o.Status {
	case OrderAccepted:
		return fail(ErrOrderAccepted, "order %s already accepted", o.ID)
	case OrderDelivered:
		return fail(ErrOrderDelivered, "order %s already delivered", o.ID)
	case OrderCancelled:
		return fail(ErrOrderCancelled, "order %s already cancelled", o.ID)
	}
	return nil
}

// canReroute guards the one-time address change. Terminal orders are rejected
// first (delivered, then cancelled), followed by the one-shot flag and the
// not-yet-accepted state.
func (o *Order) canReroute() error {
	switch {
	case o.Status == OrderDelivered:
		return fail(ErrOrderDelivered, "order %s already delivered", o.ID)
	case o.Status == OrderCancelled:
		return fail(ErrOrderCancelled, "order %s already cancelled", o.ID)
	case o.Rerouted:
		return fail(ErrOrderRerouted, "order %s already rerouted", o.ID)
	case o.Status != OrderAccepted:
		return fail(ErrOrderNotAccepted, "order %s not accepted yet", o.ID)
	}
	return nil
}

// canDeliver guards delivery, which requires a live accepted order.
func (o *Order) canDeliver() error {
	switch {
	case o.Status == OrderDelivered:
		return fail(ErrOrderDelivered, "order %s already delivered", o.ID)
	case o.Status == OrderCancelled:
		return fail(ErrOrderCancelled, "order %s already cancelled", o.ID)
	case o.Status != OrderAccepted:
		return fail(ErrOrderNotAccepted, "order %s not accepted", o.ID)
	}
	return nil
}

func (o *Order) cancelByShrink() {
	o.Status = OrderCancelled
	o.CancelReason = CancelledByShrink
}
