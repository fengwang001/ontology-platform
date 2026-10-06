package transfer

import (
	"math/big"
	"sync"
)

type System struct {
	mu     sync.RWMutex
	clock  Time
	config Config
	orders map[OrderID]*transferOrder
	books  *ledger
}

func NewSystem(initial map[Warehouse]map[Product]Quantity, config Config) *System {
	return &System{
		config: config,
		orders: map[OrderID]*transferOrder{},
		books:  newLedger(initial),
	}
}

func (s *System) CreateOrder(id OrderID, source, destination Warehouse, lines []Line, at Time) error {
	if err := validateCreate(id, source, destination, lines); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if at < s.clock {
		return ErrClockRollback
	}
	if _, exists := s.orders[id]; exists {
		return invalidArgument("order id already exists")
	}

	for index, line := range lines {
		if s.books.available(source, line.Product) < line.Quantity {
			return failureWithLine(ErrInsufficientStock, index, "available stock is insufficient")
		}
	}

	orderLines := make([]orderLine, len(lines))
	for index, line := range lines {
		orderLines[index] = orderLine{product: line.Product, quantity: line.Quantity}
		s.books.freeze(source, line.Product, line.Quantity)
	}
	s.orders[id] = &transferOrder{
		id:          id,
		source:      source,
		destination: destination,
		status:      StatusCreated,
		lines:       orderLines,
	}
	s.clock = at
	return nil
}

func (s *System) CancelOrder(id OrderID, at Time) error {
	if id == "" {
		return invalidArgument("order id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if at < s.clock {
		return ErrClockRollback
	}
	order, err := s.requireOrder(id)
	if err != nil {
		return err
	}
	switch order.status {
	case StatusShipped:
		return ErrAlreadyShipped
	case StatusClosed:
		return ErrAlreadyClosed
	case StatusCanceled:
		return ErrAlreadyCanceled
	}

	for _, line := range order.lines {
		s.books.release(order.source, line.product, line.quantity)
	}
	order.status = StatusCanceled
	s.clock = at
	return nil
}

func (s *System) ShipOrder(id OrderID, at Time) error {
	if id == "" {
		return invalidArgument("order id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if at < s.clock {
		return ErrClockRollback
	}
	order, err := s.requireOrder(id)
	if err != nil {
		return err
	}
	switch order.status {
	case StatusShipped:
		return ErrAlreadyShipped
	case StatusClosed:
		return ErrAlreadyClosed
	case StatusCanceled:
		return ErrAlreadyCanceled
	}

	for index, line := range order.lines {
		s.books.ship(order.id, index, order.source, line.product, line.quantity)
		line.shipped = line.quantity
		order.lines[index] = line
	}
	order.status = StatusShipped
	order.shippedAt = at
	s.clock = at
	return nil
}

func (s *System) ReceiveLine(id OrderID, lineIndex int, quantity Quantity, at Time) error {
	if id == "" {
		return invalidArgument("order id is required")
	}
	if lineIndex < 0 || quantity == 0 {
		return invalidArgument("line index must be non-negative and quantity must be positive")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if at < s.clock {
		return ErrClockRollback
	}
	order, err := s.requireOrder(id)
	if err != nil {
		return err
	}
	if lineIndex >= len(order.lines) {
		return invalidArgument("line index is out of range")
	}
	if order.status != StatusShipped {
		return s.stateError(order.status)
	}

	line := order.lines[lineIndex]
	limit := new(big.Int).SetUint64(uint64(line.shipped))
	tolerance := new(big.Int).Mul(limit, new(big.Int).SetUint64(s.config.TolerancePerMille))
	tolerance.Quo(tolerance, big.NewInt(1000))
	allowed := new(big.Int).Add(limit, tolerance)
	next := new(big.Int).Add(new(big.Int).SetUint64(uint64(line.received)), new(big.Int).SetUint64(uint64(quantity)))
	if next.Cmp(allowed) > 0 {
		return ErrOverReceived
	}
	if !s.books.canAddAvailable(order.destination, line.product, quantity) {
		return invalidArgument("destination stock quantity overflow")
	}

	s.books.receive(order.id, lineIndex, order.destination, line.product, quantity)
	line.received += quantity
	order.lines[lineIndex] = line
	s.clock = at
	return nil
}

func (s *System) CloseOrder(id OrderID, at Time) error {
	if id == "" {
		return invalidArgument("order id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if at < s.clock {
		return ErrClockRollback
	}
	order, err := s.requireOrder(id)
	if err != nil {
		return err
	}
	if order.status != StatusShipped {
		return s.stateError(order.status)
	}

	allComplete := true
	for _, line := range order.lines {
		if line.received < line.shipped {
			allComplete = false
			break
		}
	}
	if !allComplete && at-order.shippedAt < Time(s.config.WaitDuration) {
		return ErrCloseTooEarly
	}

	for index, line := range order.lines {
		s.books.closeLine(order.id, index, line.product, line.shipped, line.received)
		if line.shipped > line.received {
			line.shortage = line.shipped - line.received
		}
		if line.received > line.shipped {
			line.surplus = line.received - line.shipped
		}
		order.lines[index] = line
	}
	order.status = StatusClosed
	s.clock = at
	return nil
}

func (s *System) RecoverShortage(id OrderID, lineIndex int, quantity Quantity, at Time) error {
	if id == "" {
		return invalidArgument("order id is required")
	}
	if lineIndex < 0 || quantity == 0 {
		return invalidArgument("line index must be non-negative and quantity must be positive")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if at < s.clock {
		return ErrClockRollback
	}
	order, err := s.requireOrder(id)
	if err != nil {
		return err
	}
	if lineIndex >= len(order.lines) {
		return invalidArgument("line index is out of range")
	}
	if order.status != StatusClosed {
		return s.stateError(order.status)
	}

	line := order.lines[lineIndex]
	if line.shortage == 0 {
		return ErrNoShortage
	}
	if quantity > line.shortage {
		return ErrRecoveryTooMuch
	}
	if !s.books.canAddAvailable(order.destination, line.product, quantity) {
		return invalidArgument("destination stock quantity overflow")
	}

	s.books.recover(line.product, quantity)
	line.shortage -= quantity
	order.lines[lineIndex] = line
	key := stockKey{warehouse: order.destination, product: line.product}
	stock := s.books.stock[key]
	stock.Available += quantity
	s.books.stock[key] = stock
	s.clock = at
	return nil
}

func (s *System) Stock(warehouse Warehouse, product Product) StockView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.books.stockAt(warehouse, product)
}

func (s *System) Order(id OrderID) (OrderView, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	order, exists := s.orders[id]
	if !exists {
		return OrderView{}, false
	}

	view := OrderView{
		ID:          order.id,
		Source:      order.source,
		Destination: order.destination,
		Status:      order.status,
		ShippedAt:   order.shippedAt,
		Lines:       make([]LineView, len(order.lines)),
	}
	for index, line := range order.lines {
		view.Lines[index] = LineView{
			Product:  line.product,
			Quantity: line.quantity,
			Shipped:  line.shipped,
			Received: line.received,
			Shortage: line.shortage,
			Surplus:  line.surplus,
		}
	}
	return view, true
}

func (s *System) VerifyConservation() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.books.verify()
}

func (s *System) requireOrder(id OrderID) (*transferOrder, error) {
	order := s.orders[id]
	if order == nil {
		return nil, ErrOrderNotFound
	}
	return order, nil
}

func (s *System) stateError(status OrderStatus) error {
	switch status {
	case StatusCreated:
		return ErrNotShipped
	case StatusShipped:
		return ErrAlreadyShipped
	case StatusClosed:
		return ErrAlreadyClosed
	case StatusCanceled:
		return ErrAlreadyCanceled
	default:
		return ErrInvalidState
	}
}

func validateCreate(id OrderID, source, destination Warehouse, lines []Line) error {
	if id == "" || source == "" || destination == "" {
		return invalidArgument("id, source and destination are required")
	}
	if source == destination {
		return invalidArgument("source and destination must differ")
	}
	if len(lines) == 0 {
		return invalidArgument("at least one line is required")
	}
	seen := make(map[Product]struct{}, len(lines))
	for _, line := range lines {
		if line.Product == "" {
			return invalidArgument("product is required")
		}
		if line.Quantity == 0 {
			return invalidArgument("line quantity must be positive")
		}
		if _, duplicated := seen[line.Product]; duplicated {
			return invalidArgument("duplicated product in order")
		}
		seen[line.Product] = struct{}{}
	}
	return nil
}

func invalidArgument(detail string) error {
	return Failure{Kind: KindInvalidArgument, Detail: detail}
}

func failureWithLine(base Failure, lineIndex int, detail string) error {
	base.LineIndex = lineIndex
	base.Detail = detail
	return base
}
