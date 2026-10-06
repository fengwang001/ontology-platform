package ontology

type OrderKind string

const (
	OrderInstant     OrderKind = "instant"
	OrderReservation OrderKind = "reservation"
)

type OrderStatus string

const (
	OrderAccepted  OrderStatus = "accepted"
	OrderStarted   OrderStatus = "started"
	OrderCompleted OrderStatus = "completed"
	OrderCanceled  OrderStatus = "canceled"
)

type ResponsibleParty string

const (
	ResponsibilityMerchant ResponsibleParty = "merchant"
	ResponsibilityPlatform ResponsibleParty = "platform"
)

type Order struct {
	ID               string
	Kind             OrderKind
	AcceptedAt       int64
	PromisedPickupAt int64
	Preparation      int64
	Status           OrderStatus
	StartedAt        int64
	CompletedAt      int64
	CanceledAt       int64
	Responsibility   ResponsibleParty
}

type orderBook struct {
	orders map[string]*Order
}

func (b *orderBook) add(order *Order) error {
	if _, exists := b.orders[order.ID]; exists {
		return &CallError{Code: ErrInvalidParameter, Message: "order already exists"}
	}
	b.orders[order.ID] = order
	return nil
}

func (b *orderBook) get(id string) (*Order, error) {
	order, ok := b.orders[id]
	if !ok {
		return nil, &CallError{Code: ErrNotFound, Message: "order not found"}
	}
	return order, nil
}

func (b *orderBook) start(id string, at int64) error {
	order, err := b.get(id)
	if err != nil {
		return err
	}
	if order.Status == OrderStarted {
		return &CallError{Code: ErrInvalidState, Message: "order already started"}
	}
	if order.Status == OrderCompleted {
		return &CallError{Code: ErrInvalidState, Message: "order already completed"}
	}
	if order.Status == OrderCanceled {
		return &CallError{Code: ErrInvalidState, Message: "order already canceled"}
	}
	if at < order.AcceptedAt {
		return &CallError{Code: ErrInvalidState, Message: "order cannot start before acceptance"}
	}
	order.Status = OrderStarted
	order.StartedAt = at
	return nil
}

func (b *orderBook) complete(id string, at int64) error {
	order, err := b.get(id)
	if err != nil {
		return err
	}
	if order.Status == OrderAccepted {
		return &CallError{Code: ErrInvalidState, Message: "order has not started"}
	}
	if order.Status == OrderCompleted {
		return &CallError{Code: ErrInvalidState, Message: "order already completed"}
	}
	if order.Status == OrderCanceled {
		return &CallError{Code: ErrInvalidState, Message: "order already canceled"}
	}
	if at < order.StartedAt {
		return &CallError{Code: ErrInvalidState, Message: "order cannot complete before it starts"}
	}
	order.Status = OrderCompleted
	order.CompletedAt = at
	return nil
}

func (b *orderBook) cancelAcceptedInstant(at int64, responsibility ResponsibleParty) []string {
	canceled := make([]string, 0)
	for _, order := range b.orders {
		if order.Kind == OrderInstant && order.Status == OrderAccepted {
			order.Status = OrderCanceled
			order.CanceledAt = at
			order.Responsibility = responsibility
			canceled = append(canceled, order.ID)
		}
	}
	return canceled
}

func (b *orderBook) cancelAcceptedOrders(at int64, responsibility ResponsibleParty) []string {
	canceled := make([]string, 0)
	for _, order := range b.orders {
		if order.Status == OrderAccepted {
			order.Status = OrderCanceled
			order.CanceledAt = at
			order.Responsibility = responsibility
			canceled = append(canceled, order.ID)
		}
	}
	return canceled
}

func (b *orderBook) hasAcceptedInstant() bool {
	for _, order := range b.orders {
		if order.Kind == OrderInstant && order.Status == OrderAccepted {
			return true
		}
	}
	return false
}
