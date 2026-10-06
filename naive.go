package ontology

import (
	"context"
)

type naiveEvent struct {
	op      string
	time    Time
	vehicle VehicleInput
	order   OrderInput
	pos     Position
	id      string
}

type naiveOrder struct {
	in        OrderInput
	vehicle   string
	status    OrderStatus
	payable   Money
	cap       Money
	joinIndex int64
	onboard   bool
}

type naiveVehicle struct {
	in     VehicleInput
	at     Time
	orders map[string]struct{}
}

type NaiveModel struct {
	cfg    Config
	events []naiveEvent
}

func NewNaiveModel(cfg Config) *NaiveModel {
	return &NaiveModel{cfg: cfg}
}

func (m *NaiveModel) AddVehicle(_ context.Context, at Time, in VehicleInput) error {
	if !validConfig(m.cfg) || in.ID == "" || in.Seats < 0 || in.Position < 0 || in.CurrentPassengers < 0 || in.CurrentPassengers > in.Seats {
		return ErrInvalidArgument
	}
	if at < m.lastClock() {
		return ErrClockRollback
	}
	if m.hasVehicle(in.ID) {
		return ErrInvalidArgument
	}
	m.events = append(m.events, naiveEvent{op: "add_vehicle", time: at, vehicle: in})
	return nil
}

func (m *NaiveModel) SubmitOrder(_ context.Context, in OrderInput) (SubmitResult, error) {
	if !validOrderInput(in) {
		return SubmitResult{}, ErrInvalidArgument
	}
	if in.BookTime < m.lastClock() {
		return SubmitResult{}, ErrClockRollback
	}
	if m.hasOrder(in.ID) {
		return SubmitResult{}, ErrInvalidArgument
	}
	m.events = append(m.events, naiveEvent{op: "submit_order", time: in.BookTime, order: in})
	view, _, _, _ := m.replayUntil(len(m.events), true)
	ord := view[in.ID]
	result := SubmitResult{OrderID: in.ID, Status: ord.status, VehicleID: ord.vehicle, Payable: ord.payable, Cap: ord.cap, Solo: Money(in.Dropoff-in.Pickup) * m.cfg.PricePerUnit}
	if ord.status == StatusWaiting {
		return result, ErrNoVehicleAvailable
	}
	return result, nil
}

func (m *NaiveModel) CancelOrder(_ context.Context, id string, at Time) (CancelResult, error) {
	if id == "" {
		return CancelResult{}, ErrInvalidArgument
	}
	if at < m.lastClock() {
		return CancelResult{}, ErrClockRollback
	}
	ord := m.currentOrder(id)
	if ord == nil {
		return CancelResult{}, ErrOrderNotFound
	}
	if ord.status == StatusCompleted {
		return CancelResult{}, ErrOrderCompleted
	}
	if ord.status == StatusBoarded {
		return CancelResult{}, ErrBoardedCannotCancel
	}
	if ord.status == StatusCancelled || ord.status == StatusExpired {
		return CancelResult{}, ErrInvalidArgument
	}
	m.events = append(m.events, naiveEvent{op: "cancel_order", time: at, id: id})
	return CancelResult{OrderID: id, Status: StatusCancelled, Fee: m.cfg.CancellationFee}, nil
}

func (m *NaiveModel) UpdatePosition(_ context.Context, id string, at Time, pos Position) (PositionResult, error) {
	if id == "" || pos < 0 {
		return PositionResult{}, ErrInvalidArgument
	}
	if at < m.lastClock() {
		return PositionResult{}, ErrClockRollback
	}
	v := m.currentVehicle(id)
	if v == nil {
		return PositionResult{}, ErrVehicleNotFound
	}
	if pos < v.in.Position {
		return PositionResult{}, ErrPositionRollback
	}
	m.events = append(m.events, naiveEvent{op: "update_position", time: at, id: id, pos: pos})
	_, _, _, _ = m.replayUntil(len(m.events), true)
	return PositionResult{VehicleID: id, Position: pos}, nil
}

func (m *NaiveModel) GetOrder(_ context.Context, id string) (OrderView, error) {
	if id == "" {
		return OrderView{}, ErrInvalidArgument
	}
	views, _, _, _ := m.replayUntil(len(m.events), true)
	ord := views[id]
	if ord == nil {
		return OrderView{}, ErrOrderNotFound
	}
	return OrderView{ID: id, VehicleID: ord.vehicle, Status: ord.status, Payable: ord.payable, Cap: ord.cap, Solo: Money(ord.in.Dropoff-ord.in.Pickup) * m.cfg.PricePerUnit}, nil
}
