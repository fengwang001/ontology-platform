package transfer

import (
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"testing"
)

type naiveLine struct {
	product  Product
	quantity Quantity
	shipped  Quantity
	received Quantity
	shortage Quantity
	surplus  Quantity
}

type naiveOrder struct {
	id          OrderID
	source      Warehouse
	destination Warehouse
	status      OrderStatus
	shippedAt   Time
	lines       []naiveLine
}

type naiveSystem struct {
	clock   Time
	config  Config
	orders  map[OrderID]*naiveOrder
	stock   map[stockKey]StockView
	initial map[Product]Quantity
}

type naiveResult struct {
	kind   ErrorKind
	reason string
}

func newNaiveSystem(initial map[Warehouse]map[Product]Quantity, config Config) *naiveSystem {
	model := &naiveSystem{
		config:  config,
		orders:  map[OrderID]*naiveOrder{},
		stock:   map[stockKey]StockView{},
		initial: map[Product]Quantity{},
	}
	for warehouse, products := range initial {
		for product, quantity := range products {
			model.stock[stockKey{warehouse: warehouse, product: product}] = StockView{Available: quantity}
			model.initial[product] += quantity
		}
	}
	return model
}

func (m *naiveSystem) adjust(warehouse Warehouse, product Product, availableDelta int64, frozenDelta int64) {
	key := stockKey{warehouse: warehouse, product: product}
	stock := m.stock[key]
	stock.Available = Quantity(int64(stock.Available) + availableDelta)
	stock.Frozen = Quantity(int64(stock.Frozen) + frozenDelta)
	m.stock[key] = stock
}

func (m *naiveSystem) stateKind(status OrderStatus) ErrorKind {
	switch status {
	case StatusCreated:
		return KindNotShipped
	case StatusShipped:
		return KindAlreadyShipped
	case StatusClosed:
		return KindAlreadyClosed
	case StatusCanceled:
		return KindAlreadyCanceled
	default:
		return KindInvalidState
	}
}

func (m *naiveSystem) create(id OrderID, source, destination Warehouse, lines []Line, at Time) naiveResult {
	if id == "" || source == "" || destination == "" || source == destination || len(lines) == 0 {
		return naiveResult{kind: KindInvalidArgument, reason: "invalid identity or empty lines"}
	}
	seen := map[Product]struct{}{}
	for _, line := range lines {
		if line.Product == "" || line.Quantity == 0 {
			return naiveResult{kind: KindInvalidArgument, reason: "empty product or zero quantity"}
		}
		if _, duplicated := seen[line.Product]; duplicated {
			return naiveResult{kind: KindInvalidArgument, reason: "duplicate product"}
		}
		seen[line.Product] = struct{}{}
	}
	if at < m.clock {
		return naiveResult{kind: KindClockRollback, reason: "time precedes accepted clock"}
	}
	if _, exists := m.orders[id]; exists {
		return naiveResult{kind: KindInvalidArgument, reason: "duplicate order id"}
	}
	for index, line := range lines {
		if m.stock[stockKey{warehouse: source, product: line.Product}].Available < line.Quantity {
			return naiveResult{kind: KindInsufficientStock, reason: fmt.Sprintf("line %d has insufficient source availability", index)}
		}
	}

	modelLines := make([]naiveLine, len(lines))
	for index, line := range lines {
		modelLines[index] = naiveLine{product: line.Product, quantity: line.Quantity}
		m.adjust(source, line.Product, -int64(line.Quantity), int64(line.Quantity))
	}
	m.orders[id] = &naiveOrder{id: id, source: source, destination: destination, status: StatusCreated, lines: modelLines}
	m.clock = at
	return naiveResult{}
}

func (m *naiveSystem) cancel(id OrderID, at Time) naiveResult {
	if id == "" {
		return naiveResult{kind: KindInvalidArgument, reason: "empty id"}
	}
	if at < m.clock {
		return naiveResult{kind: KindClockRollback, reason: "time precedes accepted clock"}
	}
	order := m.orders[id]
	if order == nil {
		return naiveResult{kind: KindOrderNotFound, reason: "no order"}
	}
	switch order.status {
	case StatusShipped:
		return naiveResult{kind: KindAlreadyShipped, reason: "shipped order cannot be canceled"}
	case StatusClosed:
		return naiveResult{kind: KindAlreadyClosed, reason: "closed order cannot be canceled"}
	case StatusCanceled:
		return naiveResult{kind: KindAlreadyCanceled, reason: "canceled order cannot be canceled again"}
	}
	for _, line := range order.lines {
		m.adjust(order.source, line.product, int64(line.quantity), -int64(line.quantity))
	}
	order.status = StatusCanceled
	m.clock = at
	return naiveResult{}
}

func (m *naiveSystem) ship(id OrderID, at Time) naiveResult {
	if id == "" {
		return naiveResult{kind: KindInvalidArgument, reason: "empty id"}
	}
	if at < m.clock {
		return naiveResult{kind: KindClockRollback, reason: "time precedes accepted clock"}
	}
	order := m.orders[id]
	if order == nil {
		return naiveResult{kind: KindOrderNotFound, reason: "no order"}
	}
	switch order.status {
	case StatusShipped:
		return naiveResult{kind: KindAlreadyShipped, reason: "already shipped"}
	case StatusClosed:
		return naiveResult{kind: KindAlreadyClosed, reason: "already closed"}
	case StatusCanceled:
		return naiveResult{kind: KindAlreadyCanceled, reason: "already canceled"}
	}
	for index := range order.lines {
		order.lines[index].shipped = order.lines[index].quantity
		m.adjust(order.source, order.lines[index].product, 0, -int64(order.lines[index].quantity))
	}
	order.status = StatusShipped
	order.shippedAt = at
	m.clock = at
	return naiveResult{}
}

func (m *naiveSystem) receive(id OrderID, lineIndex int, quantity Quantity, at Time) naiveResult {
	if id == "" || lineIndex < 0 || quantity == 0 {
		return naiveResult{kind: KindInvalidArgument, reason: "invalid id, index, or quantity"}
	}
	if at < m.clock {
		return naiveResult{kind: KindClockRollback, reason: "time precedes accepted clock"}
	}
	order := m.orders[id]
	if order == nil {
		return naiveResult{kind: KindOrderNotFound, reason: "no order"}
	}
	if lineIndex >= len(order.lines) {
		return naiveResult{kind: KindInvalidArgument, reason: "line out of range"}
	}
	if order.status != StatusShipped {
		return naiveResult{kind: m.stateKind(order.status), reason: "receive requires shipped and open"}
	}

	line := order.lines[lineIndex]
	allowed := new(big.Int).SetUint64(uint64(line.shipped))
	tolerance := new(big.Int).Mul(allowed, new(big.Int).SetUint64(m.config.TolerancePerMille))
	tolerance.Quo(tolerance, big.NewInt(1000))
	allowed.Add(allowed, tolerance)
	next := new(big.Int).Add(new(big.Int).SetUint64(uint64(line.received)), new(big.Int).SetUint64(uint64(quantity)))
	if next.Cmp(allowed) > 0 {
		return naiveResult{kind: KindOverReceived, reason: "received total exceeds shipped plus floor tolerance"}
	}

	line.received += quantity
	order.lines[lineIndex] = line
	m.adjust(order.destination, line.product, int64(quantity), 0)
	m.clock = at
	return naiveResult{}
}

func (m *naiveSystem) close(id OrderID, at Time) naiveResult {
	if id == "" {
		return naiveResult{kind: KindInvalidArgument, reason: "empty id"}
	}
	if at < m.clock {
		return naiveResult{kind: KindClockRollback, reason: "time precedes accepted clock"}
	}
	order := m.orders[id]
	if order == nil {
		return naiveResult{kind: KindOrderNotFound, reason: "no order"}
	}
	if order.status != StatusShipped {
		return naiveResult{kind: m.stateKind(order.status), reason: "close requires shipped order"}
	}

	complete := true
	for _, line := range order.lines {
		if line.received < line.shipped {
			complete = false
		}
	}
	if !complete && at-order.shippedAt < Time(m.config.WaitDuration) {
		return naiveResult{kind: KindCloseTooEarly, reason: "waiting duration has not elapsed"}
	}
	for index := range order.lines {
		line := order.lines[index]
		if line.received < line.shipped {
			line.shortage = line.shipped - line.received
		}
		if line.received > line.shipped {
			line.surplus = line.received - line.shipped
		}
		order.lines[index] = line
	}
	order.status = StatusClosed
	m.clock = at
	return naiveResult{}
}

func (m *naiveSystem) recover(id OrderID, lineIndex int, quantity Quantity, at Time) naiveResult {
	if id == "" || lineIndex < 0 || quantity == 0 {
		return naiveResult{kind: KindInvalidArgument, reason: "invalid id, index, or quantity"}
	}
	if at < m.clock {
		return naiveResult{kind: KindClockRollback, reason: "time precedes accepted clock"}
	}
	order := m.orders[id]
	if order == nil {
		return naiveResult{kind: KindOrderNotFound, reason: "no order"}
	}
	if lineIndex >= len(order.lines) {
		return naiveResult{kind: KindInvalidArgument, reason: "line out of range"}
	}
	if order.status != StatusClosed {
		return naiveResult{kind: m.stateKind(order.status), reason: "recovery requires closed order"}
	}

	line := order.lines[lineIndex]
	if line.shortage == 0 {
		return naiveResult{kind: KindNoShortage, reason: "line has no shortage"}
	}
	if quantity > line.shortage {
		return naiveResult{kind: KindRecoveryTooMuch, reason: "recovery exceeds current shortage"}
	}
	line.shortage -= quantity
	order.lines[lineIndex] = line
	m.adjust(order.destination, line.product, int64(quantity), 0)
	m.clock = at
	return naiveResult{}
}

func (m *naiveSystem) verify() bool {
	products := map[Product]struct{}{}
	for product := range m.initial {
		products[product] = struct{}{}
	}
	for key := range m.stock {
		products[key.product] = struct{}{}
	}

	inTransit := map[Product]Quantity{}
	surplus := map[Product]Quantity{}
	shortage := map[Product]Quantity{}
	for _, order := range m.orders {
		for _, line := range order.lines {
			products[line.product] = struct{}{}
			if order.status == StatusShipped && line.received < line.shipped {
				inTransit[line.product] += line.shipped - line.received
			}
			if line.received > line.shipped {
				surplus[line.product] += line.received - line.shipped
			}
			if order.status == StatusClosed {
				shortage[line.product] += line.shortage
			}
		}
	}

	for product := range products {
		left := new(big.Int)
		for key, stock := range m.stock {
			if key.product != product {
				continue
			}
			if int64(stock.Available) < 0 || int64(stock.Frozen) < 0 {
				return false
			}
			left.Add(left, new(big.Int).SetUint64(uint64(stock.Available)))
			left.Add(left, new(big.Int).SetUint64(uint64(stock.Frozen)))
		}
		left.Add(left, new(big.Int).SetUint64(uint64(inTransit[product])))
		left.Add(left, new(big.Int).SetUint64(uint64(shortage[product])))

		right := new(big.Int).SetUint64(uint64(m.initial[product]))
		right.Add(right, new(big.Int).SetUint64(uint64(surplus[product])))
		if left.Cmp(right) != 0 {
			return false
		}
	}
	return true
}

var _ = fmt.Sprint
var _ = rand.New
var _ = errors.As

func TestNaiveModelSkeleton(t *testing.T) {
	model := newNaiveSystem(nil, Config{})
	if !model.verify() {
		t.Fatal("empty naive model should verify")
	}
}
